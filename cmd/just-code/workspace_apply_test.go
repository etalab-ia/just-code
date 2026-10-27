package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// Tests for the `just-code workspace apply` CLI flow. The runtime is a fake
// carrying a hand-built plan, so the tests drive the flow (list, menu,
// detail walk, outcome) without a guest; the writes themselves go through
// the real ApplyGuestFiles into the plan's root.

// fakeWorkspaceApplyRuntime is the seam stub.
type fakeWorkspaceApplyRuntime struct {
	plan      *justcode.WorkspaceApplyPlan
	planErr   error
	saveErr   error
	saved     [][]justcode.WorkspaceApplyFile
	saveCalls int
}

func (f *fakeWorkspaceApplyRuntime) PlanGuestApply(context.Context) (*justcode.WorkspaceApplyPlan, error) {
	return f.plan, f.planErr
}

func (f *fakeWorkspaceApplyRuntime) SaveUnappliedPatch(_ context.Context, _ string, files []justcode.WorkspaceApplyFile) (string, error) {
	f.saveCalls++
	f.saved = append(f.saved, files)
	if f.saveErr != nil {
		return "", f.saveErr
	}
	return "/state/unapplied-test.patch", nil
}

// withLinePrompts forces the numbered-prompt mode: the huh surface needs a
// real terminal, and the numbered prompt is the deterministic, reviewable
// path.
func withLinePrompts(t *testing.T) {
	t.Helper()
	origHuh := wizardHuhFn
	origAccessible := huhAccessibleModeFn
	wizardHuhFn = func() bool { return false }
	huhAccessibleModeFn = func() bool { return false }
	t.Cleanup(func() {
		wizardHuhFn = origHuh
		huhAccessibleModeFn = origAccessible
	})
}

// applyTestChange builds a plan file with a plausible diff section.
func applyTestChange(rel string, status justcode.GuestChangeStatus, add, del int) justcode.GuestChange {
	return justcode.GuestChange{
		Rel:      rel,
		Status:   status,
		AddCount: add,
		DelCount: del,
		Diff:     "diff --git a/" + rel + " b/" + rel + "\n@@ -1 +1 @@\n-old\n+new\n",
	}
}

// applyTestOK builds an applicable file whose content will be written.
func applyTestOK(rel string, status justcode.GuestChangeStatus, content string, preimage ...string) justcode.WorkspaceApplyFile {
	change := applyTestChange(rel, status, 2, 1)
	if len(preimage) > 0 {
		change.PreHash = testGitBlobHash([]byte(preimage[0]))
	}
	return justcode.WorkspaceApplyFile{
		Change:  change,
		Verdict: justcode.WorkspaceApplyOK,
		Content: []byte(content),
		Mode:    0o644,
	}
}

// hostFile seeds a file in the plan's root, the preimage the writer expects.
func hostFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readTestFile reads root/rel for an assertion.
func readTestFile(t *testing.T, root, rel string) (string, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(data), err
}

// testGitBlobHash is the git blob hash, computed here so the cmd package's
// plans carry the same preimage identifier the writer verifies. It doubles
// as a cross-package pin of the format.
func testGitBlobHash(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d", len(data))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// TestWorkspaceApplyRejectsUnknownOption pins the usage error: a typo'd option
// must not run an apply.
func TestWorkspaceApplyRejectsUnknownOption(t *testing.T) {
	code, err := workspaceCmd([]string{"apply", "--bogus"}, justcode.Config{}, "test-instance", t.TempDir())
	if code != 2 || err == nil {
		t.Fatalf("code=%d err=%v, want 2 and an error", code, err)
	}
	if !strings.Contains(err.Error(), "Unknown workspace apply option") {
		t.Fatalf("the error must name the option: %v", err)
	}
	code, err = workspaceCmd([]string{"apply", "extra"}, justcode.Config{}, "test-instance", t.TempDir())
	if code != 2 || err == nil {
		t.Fatalf("code=%d err=%v, want 2 and an error", code, err)
	}
}

// TestWorkspaceApplyCmdAppliesAll drives the default flow end to end: the
// list, the menu, the writes, and the full French outcome per the issue's
// mockups.
func TestWorkspaceApplyCmdAppliesAll(t *testing.T) {
	withLinePrompts(t)
	root := t.TempDir()
	hostFile(t, root, "src/app.js", "app\n")
	hostFile(t, root, "gone.js", "bye\n")
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:           root,
			SnapshotCommit: "abc123",
			Files: []justcode.WorkspaceApplyFile{
				{Change: applyTestChange(".env", justcode.GuestChangeAdded, 2, 0), Verdict: justcode.WorkspaceApplyExcluded, Exclusion: justcode.TransferExclusionDotenv},
				{Change: applyTestChange("conflict.js", justcode.GuestChangeModified, 2, 3), Verdict: justcode.WorkspaceApplyConflict, Conflict: justcode.WorkspaceConflictHostModified},
				{Change: justcode.GuestChange{Rel: "gone.js", Status: justcode.GuestChangeDeleted, DelCount: 1, PreHash: testGitBlobHash([]byte("bye\n"))}, Verdict: justcode.WorkspaceApplyOK},
				applyTestOK("new.js", justcode.GuestChangeAdded, "brand new\n"),
				applyTestOK("src/app.js", justcode.GuestChangeModified, "app v2\n", "app\n"),
			},
		},
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		code, err = workspaceApplyCmd(rt, strings.NewReader("1\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	// The list is shown before any question, with the mockup's wording.
	// The paths are padded to the longest one (conflict.js, 11 runes) so
	// the counts align in a column.
	for _, want := range []string{
		"Modifications de l'agent dans la machine isolée : 5 fichiers",
		"  ~ src/app.js   +2 −1",
		"  + new.js       +2",
		"  − gone.js      −1",
		"  ~ conflict.js  +2 −3  (a aussi été modifié dans votre dossier)",
		"  + .env         +2  (exclu de la copie)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the list must show %q, got:\n%s", want, stdout)
		}
	}
	// The menu offers the three mockup actions.
	for _, want := range []string{
		"1. Appliquer tout à votre dossier",
		"2. Voir le détail (fichier par fichier, avec choix)",
		"3. Ne rien appliquer",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the menu must offer %q, got:\n%s", want, stdout)
		}
	}
	// The outcome, in the mockup's words.
	if !strings.Contains(stdout, "3 fichiers appliqués à votre dossier. Relisez-les puis commitez quand vous voulez.") {
		t.Errorf("the outcome line is wrong, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "conflict.js a aussi été modifié dans votre dossier : il n'est pas appliqué.") {
		t.Errorf("the conflict line is wrong, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, ".env est exclu de la copie : il n'est pas appliqué.") {
		t.Errorf("the exclusion line is wrong, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Le patch est conservé dans /state/unapplied-test.patch pour une application manuelle.") {
		t.Errorf("the patch line is wrong, got:\n%s", stdout)
	}
	// The patch covers exactly the conflicted file.
	if rt.saveCalls != 1 || len(rt.saved[0]) != 1 || rt.saved[0][0].Change.Rel != "conflict.js" {
		t.Fatalf("the patch must cover the conflict only: calls=%d saved=%v", rt.saveCalls, rt.saved)
	}
	// The writes landed in the plan's root, not anywhere else.
	if got, _ := readTestFile(t, root, "src/app.js"); got != "app v2\n" {
		t.Errorf("src/app.js = %q", got)
	}
	if got, _ := readTestFile(t, root, "new.js"); got != "brand new\n" {
		t.Errorf("new.js = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "gone.js")); !os.IsNotExist(err) {
		t.Error("gone.js must be deleted")
	}
	if _, err := os.Lstat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Error("the excluded .env must not appear")
	}
}

// TestWorkspaceApplyCmdDetailWalk pins the file-by-file choice: each
// applicable file is asked, non-applicable ones are only reported, and the
// singular outcome is worded for one file.
func TestWorkspaceApplyCmdDetailWalk(t *testing.T) {
	withLinePrompts(t)
	root := t.TempDir()
	hostFile(t, root, "a.js", "a\n")
	hostFile(t, root, "b.js", "b\n")
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:           root,
			SnapshotCommit: "abc123",
			Files: []justcode.WorkspaceApplyFile{
				{Change: applyTestChange("conflict.js", justcode.GuestChangeModified, 1, 1), Verdict: justcode.WorkspaceApplyConflict, Conflict: justcode.WorkspaceConflictHostModified},
				applyTestOK("a.js", justcode.GuestChangeModified, "a2\n", "a\n"),
				applyTestOK("b.js", justcode.GuestChangeModified, "b2\n", "b\n"),
			},
		},
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		// detail, then skip a.js, then apply b.js.
		code, err = workspaceApplyCmd(rt, strings.NewReader("2\n2\n1\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout)
	}
	if !strings.Contains(stdout, "conflict.js — a aussi été modifié dans votre dossier : non applicable") {
		t.Errorf("the detail walk must report the conflict without asking, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 fichier appliqué à votre dossier. Relisez-les puis commitez quand vous voulez.") {
		t.Errorf("the singular outcome is wrong, got:\n%s", stdout)
	}
	if got, _ := readTestFile(t, root, "a.js"); got != "a\n" {
		t.Errorf("a.js was written despite being skipped: %q", got)
	}
	if got, _ := readTestFile(t, root, "b.js"); got != "b2\n" {
		t.Errorf("b.js = %q", got)
	}
}

// TestWorkspaceApplyCmdDetailApplyRestAndStop pins the two shortcuts of the
// detail walk: "apply the rest" stops asking, and "stop" keeps what was
// already chosen without writing anything more.
func TestWorkspaceApplyCmdDetailApplyRestAndStop(t *testing.T) {
	t.Run("apply the rest", func(t *testing.T) {
		withLinePrompts(t)
		root := t.TempDir()
		hostFile(t, root, "a.js", "a\n")
		hostFile(t, root, "b.js", "b\n")
		rt := &fakeWorkspaceApplyRuntime{
			plan: &justcode.WorkspaceApplyPlan{
				Root:           root,
				SnapshotCommit: "abc123",
				Files: []justcode.WorkspaceApplyFile{
					applyTestOK("a.js", justcode.GuestChangeModified, "a2\n", "a\n"),
					applyTestOK("b.js", justcode.GuestChangeModified, "b2\n", "b\n"),
				},
			},
		}
		var code int
		var err error
		stdout := captureStdout(t, func() {
			// detail, then "apply the rest" at the first file.
			code, err = workspaceApplyCmd(rt, strings.NewReader("2\n3\n"))
		})
		if err != nil || code != 0 {
			t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout)
		}
		if !strings.Contains(stdout, "2 fichiers appliqués à votre dossier.") {
			t.Errorf("the rest must be applied without further prompts, got:\n%s", stdout)
		}
	})
	t.Run("stop", func(t *testing.T) {
		withLinePrompts(t)
		root := t.TempDir()
		hostFile(t, root, "a.js", "a\n")
		hostFile(t, root, "b.js", "b\n")
		rt := &fakeWorkspaceApplyRuntime{
			plan: &justcode.WorkspaceApplyPlan{
				Root:           root,
				SnapshotCommit: "abc123",
				Files: []justcode.WorkspaceApplyFile{
					applyTestOK("a.js", justcode.GuestChangeModified, "a2\n", "a\n"),
					applyTestOK("b.js", justcode.GuestChangeModified, "b2\n", "b\n"),
				},
			},
		}
		var code int
		var err error
		stdout := captureStdout(t, func() {
			// detail, apply a.js, stop at b.js.
			code, err = workspaceApplyCmd(rt, strings.NewReader("2\n1\n4\n"))
		})
		if err != nil || code != 0 {
			t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout)
		}
		if !strings.Contains(stdout, "1 fichier appliqué à votre dossier.") {
			t.Errorf("stopping must keep the earlier choice only, got:\n%s", stdout)
		}
		if got, _ := readTestFile(t, root, "b.js"); got != "b\n" {
			t.Errorf("b.js was written after a stop: %q", got)
		}
	})
}

// TestWorkspaceApplyCmdNone pins the explicit refusal: nothing is written,
// nothing is reported as applied, and no patch is kept.
func TestWorkspaceApplyCmdNone(t *testing.T) {
	withLinePrompts(t)
	root := t.TempDir()
	hostFile(t, root, "a.js", "a\n")
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:           root,
			SnapshotCommit: "abc123",
			Files: []justcode.WorkspaceApplyFile{
				{Change: applyTestChange("conflict.js", justcode.GuestChangeModified, 1, 1), Verdict: justcode.WorkspaceApplyConflict, Conflict: justcode.WorkspaceConflictHostModified},
				applyTestOK("a.js", justcode.GuestChangeModified, "a2\n"),
			},
		},
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		code, err = workspaceApplyCmd(rt, strings.NewReader("3\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if strings.Contains(stdout, "appliqué") && !strings.Contains(stdout, "Ne rien appliquer") {
		t.Errorf("nothing may be reported as applied, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "Le patch est conservé") {
		t.Errorf("an explicit refusal must not keep a patch, got:\n%s", stdout)
	}
	if rt.saveCalls != 0 {
		t.Errorf("SaveUnappliedPatch was called %d times", rt.saveCalls)
	}
	if got, _ := readTestFile(t, root, "a.js"); got != "a\n" {
		t.Errorf("a.js was written despite the refusal: %q", got)
	}
}

// TestWorkspaceApplyCmdEOFAborts pins the abort: a closed stdin (or Ctrl-C at
// the huh menu) writes nothing and exits non-zero.
func TestWorkspaceApplyCmdEOFAborts(t *testing.T) {
	withLinePrompts(t)
	root := t.TempDir()
	hostFile(t, root, "a.js", "a\n")
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:           root,
			SnapshotCommit: "abc123",
			Files: []justcode.WorkspaceApplyFile{
				applyTestOK("a.js", justcode.GuestChangeModified, "a2\n"),
			},
		},
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		code, err = workspaceApplyCmd(rt, strings.NewReader(""))
	})
	if err != nil {
		t.Fatalf("an abort is a message, not an error: %v", err)
	}
	if code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	stderr := captureStderr(t, func() {
		code, err = workspaceApplyCmd(&fakeWorkspaceApplyRuntime{plan: rt.plan}, strings.NewReader(""))
	})
	if !strings.Contains(stderr, "Application annulée : aucun fichier n'a été écrit.") {
		t.Errorf("the abort message is wrong, got:\n%s", stderr)
	}
	if got, _ := readTestFile(t, root, "a.js"); got != "a\n" {
		t.Errorf("a file was written after an abort: %q", got)
	}
	if rt.saveCalls != 0 {
		t.Errorf("SaveUnappliedPatch was called %d times after an abort", rt.saveCalls)
	}
	_ = stdout
}

// TestWorkspaceApplyCmdEmptyPlan pins the no-changes path.
func TestWorkspaceApplyCmdEmptyPlan(t *testing.T) {
	withLinePrompts(t)
	rt := &fakeWorkspaceApplyRuntime{plan: &justcode.WorkspaceApplyPlan{Root: t.TempDir()}}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		code, err = workspaceApplyCmd(rt, strings.NewReader("1\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(stdout, "Aucune modification à récupérer depuis la machine isolée.") {
		t.Errorf("the empty-plan message is wrong, got:\n%s", stdout)
	}
}

// TestWorkspaceApplyCmdNoMenuWhenNothingApplicable pins the degenerate menu:
// when no file can be written there is nothing to choose, so the conflicts
// are reported (and kept in a patch) without reading stdin at all.
func TestWorkspaceApplyCmdNoMenuWhenNothingApplicable(t *testing.T) {
	withLinePrompts(t)
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:           t.TempDir(),
			SnapshotCommit: "abc123",
			Files: []justcode.WorkspaceApplyFile{
				{Change: applyTestChange("gone.js", justcode.GuestChangeModified, 1, 1), Verdict: justcode.WorkspaceApplyConflict, Conflict: justcode.WorkspaceConflictHostMissing},
				{Change: applyTestChange("conflict.js", justcode.GuestChangeModified, 1, 1), Verdict: justcode.WorkspaceApplyConflict, Conflict: justcode.WorkspaceConflictHostModified},
			},
		},
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		// Empty stdin: any prompt would abort, which must not happen.
		code, err = workspaceApplyCmd(rt, strings.NewReader(""))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout)
	}
	if strings.Contains(stdout, "Appliquer tout") {
		t.Errorf("no menu may be shown when nothing is applicable, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "gone.js n'existe plus dans votre dossier : il n'est pas appliqué.") {
		t.Errorf("the host-missing conflict line is wrong, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Le patch est conservé dans /state/unapplied-test.patch") {
		t.Errorf("the conflicts must still be kept in a patch, got:\n%s", stdout)
	}
	if rt.saveCalls != 1 || len(rt.saved[0]) != 2 {
		t.Fatalf("the patch must cover both conflicts: calls=%d saved=%v", rt.saveCalls, rt.saved)
	}
}

// TestWorkspaceApplyCmdReportsWriteTimeConflicts pins the TOCTOU report: a
// file that was applicable when planned but whose preimage broke before the
// write is reported and patched like any other conflict.
func TestWorkspaceApplyCmdReportsWriteTimeConflicts(t *testing.T) {
	withLinePrompts(t)
	root := t.TempDir()
	// The plan was built against "old\n"; the host changed since.
	hostFile(t, root, "a.js", "changed on the host\n")
	preimage := justcode.GuestChange{
		Rel:      "a.js",
		Status:   justcode.GuestChangeModified,
		PreHash:  testGitBlobHash([]byte("old\n")),
		AddCount: 1,
		DelCount: 1,
		Diff:     "diff --git a/a.js b/a.js\n@@ -1 +1 @@\n-old\n+new\n",
	}
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:           root,
			SnapshotCommit: "abc123",
			Files: []justcode.WorkspaceApplyFile{
				{Change: preimage, Verdict: justcode.WorkspaceApplyOK, Content: []byte("new\n"), Mode: 0o644},
			},
		},
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		code, err = workspaceApplyCmd(rt, strings.NewReader("1\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout)
	}
	if !strings.Contains(stdout, "a.js a aussi été modifié dans votre dossier : il n'est pas appliqué.") {
		t.Errorf("the write-time conflict must be reported, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Le patch est conservé dans /state/unapplied-test.patch") {
		t.Errorf("the write-time conflict must be kept in a patch, got:\n%s", stdout)
	}
	if got, _ := readTestFile(t, root, "a.js"); got != "changed on the host\n" {
		t.Errorf("the host file was overwritten: %q", got)
	}
	if rt.saveCalls != 1 || len(rt.saved[0]) != 1 || rt.saved[0][0].Change.Rel != "a.js" {
		t.Fatalf("the patch must cover the demoted file: calls=%d saved=%v", rt.saveCalls, rt.saved)
	}
}

// TestWorkspaceApplyCmdWarnsWhenSecretsUnscanned pins the honest degradation:
// when the incoming content could not be scanned, the user is told.
func TestWorkspaceApplyCmdWarnsWhenSecretsUnscanned(t *testing.T) {
	withLinePrompts(t)
	root := t.TempDir()
	rt := &fakeWorkspaceApplyRuntime{
		plan: &justcode.WorkspaceApplyPlan{
			Root:             root,
			SnapshotCommit:   "abc123",
			SecretsUnscanned: true,
			Files: []justcode.WorkspaceApplyFile{
				applyTestOK("a.js", justcode.GuestChangeAdded, "a\n"),
			},
		},
	}
	var code int
	var err error
	stderr := captureStderr(t, func() {
		stdout := captureStdout(t, func() {
			code, err = workspaceApplyCmd(rt, strings.NewReader("3\n"))
		})
		_ = stdout
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(stderr, "la détection de secrets n'est pas disponible") {
		t.Errorf("the unscanned warning is missing, got:\n%s", stderr)
	}
}

// TestWorkspaceApplyCmdListsEveryFile pins the review contract: the list
// shows every changed file, however many there are — a cap would hide a write
// from the review that authorizes it.
func TestWorkspaceApplyCmdListsEveryFile(t *testing.T) {
	withLinePrompts(t)
	rt := &fakeWorkspaceApplyRuntime{plan: &justcode.WorkspaceApplyPlan{Root: t.TempDir(), SnapshotCommit: "abc123"}}
	for i := 0; i < 205; i++ {
		rt.plan.Files = append(rt.plan.Files, applyTestOK(fmt.Sprintf("f%03d.js", i), justcode.GuestChangeModified, "x\n"))
	}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		// Nothing is applied: the list is the point of the test.
		code, err = workspaceApplyCmd(rt, strings.NewReader("3\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(stdout, "Modifications de l'agent dans la machine isolée : 205 fichiers") {
		t.Errorf("the header must count every file, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "… et") {
		t.Errorf("no cap line may hide files, got:\n%s", stdout)
	}
	for i := 0; i < 205; i++ {
		if !strings.Contains(stdout, fmt.Sprintf("f%03d.js", i)) {
			t.Errorf("f%03d.js is missing from the list", i)
		}
	}
}

// TestWorkspaceApplyCmdShowsModeOnlyChanges pins the list line for a
// mode-only change: the mode transition is shown instead of empty counts.
func TestWorkspaceApplyCmdShowsModeOnlyChanges(t *testing.T) {
	withLinePrompts(t)
	rt := &fakeWorkspaceApplyRuntime{plan: &justcode.WorkspaceApplyPlan{
		Root:           t.TempDir(),
		SnapshotCommit: "abc123",
		Files: []justcode.WorkspaceApplyFile{
			{
				Change: justcode.GuestChange{
					Rel:     "run.sh",
					Status:  justcode.GuestChangeModified,
					OldMode: "100644",
					NewMode: "100755",
					Diff:    "diff --git a/run.sh b/run.sh\nold mode 100644\nnew mode 100755\n",
				},
				Verdict: justcode.WorkspaceApplyOK,
				Content: []byte("echo hi\n"),
				Mode:    0o755,
			},
		},
	}}
	var code int
	var err error
	stdout := captureStdout(t, func() {
		code, err = workspaceApplyCmd(rt, strings.NewReader("3\n"))
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stdout=%s", code, err, stdout)
	}
	if !strings.Contains(stdout, "mode 100644 → 100755") {
		t.Errorf("a mode-only change must show its mode transition, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "+0 −0") {
		t.Errorf("empty counts must not be shown for a mode-only change, got:\n%s", stdout)
	}
}
