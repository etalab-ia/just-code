package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

func updateCmd(args []string, projectRoot string) (int, error) {
	var selected []string
	approve := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			fmt.Println("Usage: just-code update [--skill <catalogue/id>]... [--yes]\n\nReview selected versioned project skills against the current catalogue.\nWithout --yes, apply requires an interactive confirmation. A non-TTY run\nwithout --yes is preview-only.")
			return 0, nil
		case "--skill":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return 2, fmt.Errorf("--skill requires a catalogue ID such as official/rgaa")
			}
			i++
			selected = append(selected, args[i])
		case "--yes":
			approve = true
		default:
			return 2, fmt.Errorf("unknown update option %q", args[i])
		}
	}
	if err := justcode.RecoverProjectUpdate(justcode.DefaultFS, projectRoot); err != nil {
		return 1, fmt.Errorf("recover project update: %w", err)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(projectRoot))
	if err != nil {
		return 1, err
	}
	if manifest.SkillsLocalOnly {
		return 1, fmt.Errorf("project skill updates currently require versioned skills; local-only selections were not changed")
	}
	if len(selected) == 0 && len(manifest.Skills) == 0 {
		plan, err := justcode.PlanProjectUpdate(context.Background(), justcode.DefaultFS, projectRoot, nil, "", nil)
		if err != nil {
			return 1, err
		}
		if !plan.NoOp {
			return 1, fmt.Errorf("unexpected update plan for a project without versioned skills")
		}
		fmt.Println("No versioned project skills are selected; nothing to update.")
		return 0, nil
	}
	selectedIDs := make(map[string]bool, len(manifest.Skills))
	for _, id := range manifest.Skills {
		selectedIDs[id] = true
	}
	seen := map[string]bool{}
	for _, id := range selected {
		if !selectedIDs[id] {
			return 2, fmt.Errorf("skill %q is not selected in this project", id)
		}
		if seen[id] {
			return 2, fmt.Errorf("skill %q was selected for update more than once", id)
		}
		seen[id] = true
	}
	revision, err := justcode.LatestProjectSkillsRevision(context.Background())
	if err != nil {
		return 1, err
	}
	plan, err := justcode.PlanProjectUpdate(context.Background(), justcode.DefaultFS, projectRoot, selected, revision, nil)
	if err != nil {
		return 1, err
	}
	if plan.NoOp {
		fmt.Println("Project skills are already current; nothing changed.")
		return 0, nil
	}
	fmt.Printf("Project skill update available at catalogue commit %s:\n", revision)
	for _, change := range plan.Changes {
		from := change.FromRevision
		if from == "" {
			from = "unlocked"
		}
		fromDigest := change.FromSHA256
		if len(fromDigest) > 12 {
			fromDigest = fromDigest[:12]
		}
		toDigest := change.ToSHA256
		if len(toDigest) > 12 {
			toDigest = toDigest[:12]
		}
		fmt.Printf("  %s  %s (%s) -> %s (%s)\n", change.ID, from, fromDigest, change.ToRevision, toDigest)
	}
	managedDiff, err := plan.ManagedInstructionsDiff()
	if err != nil {
		return 1, fmt.Errorf("prepare managed-instructions preview: %w", err)
	}
	if managedDiff != "" {
		fmt.Print(managedDiff)
	}
	if !approve {
		if !isTTY() {
			fmt.Println("Preview only; no project files changed. Re-run with --yes to approve this update.")
			return 0, nil
		}
		fmt.Print("Apply these selected project updates? [y/N] ")
		reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(reply), "y") && !strings.EqualFold(strings.TrimSpace(reply), "yes") {
			fmt.Println("No changes applied.")
			return 0, nil
		}
	}
	if err := plan.Apply(justcode.DefaultFS); err != nil {
		return 1, err
	}
	fmt.Printf("Updated %d selected project skill(s) and their managed instructions.\n", len(plan.Changes))
	fmt.Println("Guest packages and runtime images are not changed or recreated by this command.")
	return 0, nil
}
