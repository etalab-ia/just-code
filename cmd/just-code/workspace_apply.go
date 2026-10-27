package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/huh/v2"
	"github.com/etalab-ia/just-code/internal/justcode"
)

// `just-code workspace apply` (issue #173): retrieve the agent's work from the
// sealed microVM into the host folder in one command. The flow is deliberately
// linear and everything is shown before the first byte is written:
//
//  1. the plan (every changed file, its verdict, its +/− counts) is printed;
//  2. the user picks apply-all, file-by-file detail, or nothing;
//  3. only the chosen files are written, and a file the host changed since the
//     transfer is never overwritten — its diff is kept as a patch instead.
//
// Nothing is staged and nothing is committed: the user reviews and commits.

// workspaceApplyRuntime is the seam between this flow and the runtime. Tests
// stub it to drive the flow without a guest.
type workspaceApplyRuntime interface {
	PlanGuestApply(ctx context.Context) (*justcode.WorkspaceApplyPlan, error)
	SaveUnappliedPatch(ctx context.Context, baseline string, files []justcode.WorkspaceApplyFile) (string, error)
}

// workspaceApplyMenuChoice is the answer to the main menu.
type workspaceApplyMenuChoice string

const (
	workspaceApplyAll    workspaceApplyMenuChoice = "all"
	workspaceApplyDetail workspaceApplyMenuChoice = "detail"
	workspaceApplyNone   workspaceApplyMenuChoice = "none"
)

// The main menu options, in display order; the first is the default.
var workspaceApplyMenuOptions = []string{
	"Appliquer tout à votre dossier",
	"Voir le détail (fichier par fichier, avec choix)",
	"Ne rien appliquer",
}

// The per-file options of the detail walk, in display order.
var workspaceApplyFileOptions = []string{
	"Appliquer",
	"Ne pas appliquer",
	"Appliquer le reste sans me redemander",
	"Arrêter (rien d'autre ne sera écrit)",
}

// maxDetailDiffLines caps the diff shown in the detail walk; the full diff is
// always available through 'just-code workspace export'.
const maxDetailDiffLines = 80

// workspaceApplyCmd runs the whole flow. in is the prompt input (os.Stdin in
// production, a strings.Reader in tests).
func workspaceApplyCmd(rt workspaceApplyRuntime, in io.Reader) (int, error) {
	plan, err := rt.PlanGuestApply(context.Background())
	if err != nil {
		return 1, err
	}
	if len(plan.Files) == 0 {
		fmt.Println("Aucune modification à récupérer depuis la machine isolée.")
		return 0, nil
	}
	if plan.SecretsUnscanned {
		fmt.Fprintln(os.Stderr, "Attention : la détection de secrets n'est pas disponible sur cette machine ; le contenu renvoyé par l'agent n'a pas pu être vérifié.")
	}
	// The list comes before any question: the acceptance rule is that the
	// user sees what would change before the first write.
	printWorkspaceApplyList(plan.Files)
	applicable := plan.Applicable()
	if len(applicable) == 0 {
		// Nothing can be written, so there is no menu to answer; the
		// conflicts and exclusions below are the whole outcome.
		return workspaceApplyOutcome(rt, plan, nil, "")
	}
	// One buffered reader for the whole flow: the menu and the detail walk
	// share it, so answers queued on a piped stdin are consumed in order
	// instead of the menu's buffer swallowing the walk's answers.
	reader := bufio.NewReader(in)
	choice, err := askWorkspaceApplyMain(reader)
	if err != nil {
		// An aborted menu (Ctrl-C, closed stdin) must not write anything.
		fmt.Fprintln(os.Stderr, "Application annulée : aucun fichier n'a été écrit.")
		return 1, nil
	}
	var chosen []justcode.WorkspaceApplyFile
	switch choice {
	case workspaceApplyAll:
		chosen = applicable
	case workspaceApplyDetail:
		chosen, err = workspaceApplyDetailWalk(plan, reader)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Application annulée : aucun fichier n'a été écrit.")
			return 1, nil
		}
	}
	return workspaceApplyOutcome(rt, plan, chosen, choice)
}

// askWorkspaceApplyMain asks what to do with the plan.
func askWorkspaceApplyMain(reader *bufio.Reader) (workspaceApplyMenuChoice, error) {
	if wizardHuhFn() && !huhAccessibleModeFn() {
		choice := string(workspaceApplyAll)
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Options(
						huh.NewOption(workspaceApplyMenuOptions[0], string(workspaceApplyAll)),
						huh.NewOption(workspaceApplyMenuOptions[1], string(workspaceApplyDetail)),
						huh.NewOption(workspaceApplyMenuOptions[2], string(workspaceApplyNone)),
					).
					Value(&choice),
			),
		)
		if err := huhRun(form); err != nil {
			return "", err
		}
		return workspaceApplyMenuChoice(choice), nil
	}
	n, err := askNumberedChoice(reader, workspaceApplyMenuOptions, 1)
	if err != nil {
		return "", err
	}
	return []workspaceApplyMenuChoice{workspaceApplyAll, workspaceApplyDetail, workspaceApplyNone}[n-1], nil
}

// askNumberedChoice prints a numbered menu and reads a line. An empty line
// picks the default; a closed input aborts (nothing is guessed).
func askNumberedChoice(reader *bufio.Reader, options []string, def int) (int, error) {
	for i, option := range options {
		fmt.Printf("  %d. %s\n", i+1, option)
	}
	for {
		fmt.Printf("Choix [%d] : ", def)
		line, err := reader.ReadString('\n')
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return 0, err
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			if eof {
				return 0, huh.ErrUserAborted
			}
			return def, nil
		}
		if n, perr := strconv.Atoi(answer); perr == nil && n >= 1 && n <= len(options) {
			return n, nil
		}
		if eof {
			return 0, huh.ErrUserAborted
		}
		fmt.Printf("Réponse non reconnue : entrez un nombre entre 1 et %d.\n", len(options))
	}
}

// workspaceApplyDetailWalk shows each file (its diff capped) and takes a
// per-file decision, honouring "apply the rest" and "stop".
func workspaceApplyDetailWalk(plan *justcode.WorkspaceApplyPlan, reader *bufio.Reader) ([]justcode.WorkspaceApplyFile, error) {
	var chosen []justcode.WorkspaceApplyFile
	applyRest := false
	for i := range plan.Files {
		f := plan.Files[i]
		if f.Verdict != justcode.WorkspaceApplyOK {
			fmt.Printf("  %s %s — %s : non applicable\n", applyStatusGlyph(f.Change.Status), f.Change.Rel, applyVerdictLabel(f))
			continue
		}
		if applyRest {
			chosen = append(chosen, f)
			continue
		}
		printWorkspaceApplyFileDetail(f)
		choice, err := askWorkspaceApplyFileChoice(f, reader)
		if err != nil {
			return nil, err
		}
		switch choice {
		case 1:
			chosen = append(chosen, f)
		case 2:
			// Skip this file.
		case 3:
			applyRest = true
			chosen = append(chosen, f)
		case 4:
			// Stop: what was already chosen stands, nothing else is
			// written.
			return chosen, nil
		}
	}
	return chosen, nil
}

// askWorkspaceApplyFileChoice asks the per-file question, on huh when the
// terminal supports it and through the numbered prompt otherwise. The diff is
// printed to normal scrollback first, so a short terminal never hides the
// choices behind it.
func askWorkspaceApplyFileChoice(f justcode.WorkspaceApplyFile, reader *bufio.Reader) (int, error) {
	if wizardHuhFn() && !huhAccessibleModeFn() {
		choice := 1
		options := make([]huh.Option[int], 0, len(workspaceApplyFileOptions))
		for i, option := range workspaceApplyFileOptions {
			options = append(options, huh.NewOption(option, i+1))
		}
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[int]().Options(options...).Value(&choice),
			),
		)
		if err := huhRun(form); err != nil {
			return 0, err
		}
		return choice, nil
	}
	return askNumberedChoice(reader, workspaceApplyFileOptions, 1)
}

// printWorkspaceApplyFileDetail prints one file's header and capped diff.
func printWorkspaceApplyFileDetail(f justcode.WorkspaceApplyFile) {
	fmt.Printf("\n%s %s %s\n", applyStatusGlyph(f.Change.Status), f.Change.Rel, applyChangeCounts(f.Change))
	if f.Change.Binary {
		fmt.Println("  (fichier binaire : le contenu n'est pas affiché)")
		return
	}
	if f.Change.Diff == "" {
		fmt.Println("  (aucun diff)")
		return
	}
	lines := strings.Split(strings.TrimSuffix(f.Change.Diff, "\n"), "\n")
	if len(lines) > maxDetailDiffLines {
		fmt.Println(strings.Join(lines[:maxDetailDiffLines], "\n"))
		fmt.Printf("  … (diff tronqué : %d lignes au total ; le patch complet reste disponible via 'just-code workspace export')\n", len(lines))
		return
	}
	fmt.Println(f.Change.Diff)
}

// workspaceApplyOutcome writes the chosen files and reports what happened.
//
// choice is "" when no menu was shown (nothing was applicable), and the
// explicit "Ne rien appliquer" is the only path that skips the conflict
// report and the saved patch.
func workspaceApplyOutcome(rt workspaceApplyRuntime, plan *justcode.WorkspaceApplyPlan, chosen []justcode.WorkspaceApplyFile, choice workspaceApplyMenuChoice) (int, error) {
	applied, failed, writeConflicts := justcode.ApplyGuestFiles(plan.Root, chosen)
	if len(applied) > 0 {
		fmt.Printf("%d fichier%s appliqué%s à votre dossier. Relisez-les puis commitez quand vous voulez.\n",
			len(applied), frenchPlural(len(applied)), frenchPlural(len(applied)))
	} else if choice != "" && choice != workspaceApplyNone {
		fmt.Println("Aucun fichier n'a été appliqué.")
	}
	// Every conflicted file, whether detected while planning or while
	// writing, is reported and kept in the patch. The explicit "Ne rien
	// appliquer" is the only path that skips the report and the patch.
	if choice != workspaceApplyNone {
		conflicts := append(planConflicts(plan), writeConflicts...)
		for _, f := range conflicts {
			fmt.Printf("%s %s : il n'est pas appliqué.\n", f.Change.Rel, applyConflictLabel(f))
		}
		for _, f := range plan.Files {
			if f.Verdict == justcode.WorkspaceApplyExcluded {
				fmt.Printf("%s est exclu de la copie : il n'est pas appliqué.\n", f.Change.Rel)
			}
		}
		if len(conflicts) > 0 {
			if path, err := rt.SaveUnappliedPatch(context.Background(), plan.SnapshotCommit, conflicts); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: the conflicted patch could not be saved: %v\n", err)
			} else if path != "" {
				fmt.Printf("Le patch est conservé dans %s pour une application manuelle.\n", path)
			}
		}
	}
	exit := 0
	if len(failed) > 0 {
		exit = 1
		for _, rel := range failed {
			fmt.Fprintf(os.Stderr, "%s n'a pas pu être écrit dans votre dossier.\n", rel)
		}
	}
	return exit, nil
}

// planConflicts returns the plan's conflicted files in order.
func planConflicts(plan *justcode.WorkspaceApplyPlan) []justcode.WorkspaceApplyFile {
	var out []justcode.WorkspaceApplyFile
	for _, f := range plan.Files {
		if f.Verdict == justcode.WorkspaceApplyConflict {
			out = append(out, f)
		}
	}
	return out
}

// printWorkspaceApplyList prints the header and one line per changed file.
// Every file is listed — the list is what the user reviews before anything is
// written, so hiding entries behind a cap would hide a write.
func printWorkspaceApplyList(files []justcode.WorkspaceApplyFile) {
	fmt.Printf("Modifications de l'agent dans la machine isolée : %d fichier%s\n", len(files), frenchPlural(len(files)))
	width := 0
	for _, f := range files {
		if n := utf8.RuneCountInString(f.Change.Rel); n > width && n <= 48 {
			width = n
		}
	}
	for _, f := range files {
		line := fmt.Sprintf("  %s %s  %s", applyStatusGlyph(f.Change.Status), padRight(f.Change.Rel, width), applyChangeCounts(f.Change))
		if note := applyFileNote(f); note != "" {
			line += "  " + note
		}
		fmt.Println(line)
	}
}

// applyStatusGlyph is the list marker for a change kind: ~ modified, + added,
// − deleted.
func applyStatusGlyph(status justcode.GuestChangeStatus) string {
	switch status {
	case justcode.GuestChangeAdded:
		return "+"
	case justcode.GuestChangeDeleted:
		return "−"
	default:
		return "~"
	}
}

// applyChangeCounts renders the +/− line counts (U+2212 minus, like the
// mockups); a binary file has no readable counts, and a mode-only change
// shows the mode transition instead of empty counts.
func applyChangeCounts(c justcode.GuestChange) string {
	if c.Binary {
		return "(binaire)"
	}
	if c.AddCount == 0 && c.DelCount == 0 && c.OldMode != "" && c.NewMode != "" && c.OldMode != c.NewMode {
		return fmt.Sprintf("mode %s → %s", c.OldMode, c.NewMode)
	}
	switch c.Status {
	case justcode.GuestChangeAdded:
		return fmt.Sprintf("+%d", c.AddCount)
	case justcode.GuestChangeDeleted:
		return fmt.Sprintf("−%d", c.DelCount)
	default:
		return fmt.Sprintf("+%d −%d", c.AddCount, c.DelCount)
	}
}

// applyFileNote annotates a file that will not be applied as-is.
func applyFileNote(f justcode.WorkspaceApplyFile) string {
	switch f.Verdict {
	case justcode.WorkspaceApplyConflict:
		return "(" + applyConflictLabel(f) + ")"
	case justcode.WorkspaceApplyExcluded:
		return "(exclu de la copie)"
	default:
		return ""
	}
}

// applyConflictLabel is the French sentence explaining a conflict.
func applyConflictLabel(f justcode.WorkspaceApplyFile) string {
	switch f.Conflict {
	case justcode.WorkspaceConflictHostMissing:
		return "n'existe plus dans votre dossier"
	case justcode.WorkspaceConflictHostExists:
		return "existe déjà dans votre dossier"
	default:
		return "a aussi été modifié dans votre dossier"
	}
}

// applyVerdictLabel is the short label used in the detail walk for a file
// that cannot be applied.
func applyVerdictLabel(f justcode.WorkspaceApplyFile) string {
	if f.Verdict == justcode.WorkspaceApplyExcluded {
		return "exclu de la copie"
	}
	return applyConflictLabel(f)
}

// frenchPlural returns "s" for counts other than 1.
func frenchPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// padRight pads s with spaces to n display runes.
func padRight(s string, n int) string {
	width := utf8.RuneCountInString(s)
	if width >= n {
		return s
	}
	return s + strings.Repeat(" ", n-width)
}
