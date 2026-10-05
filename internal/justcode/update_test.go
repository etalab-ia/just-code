package justcode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func updateFixture(t *testing.T, ids ...string) (string, ProjectManifest, Lockfile) {
	t.Helper()
	root := t.TempDir()
	setID := strings.Repeat("a", 32)
	manifest := ProjectManifest{DependencySetID: setID, Skills: append([]string(nil), ids...)}
	lock := Lockfile{DependencySetID: setID, Entries: map[string]string{}}
	lock.Skills = make(map[string]SkillLock, len(ids))
	for i, id := range ids {
		lock.Skills[id] = SkillLock{Repository: projectSkillsRepository, Revision: projectSkillsRevision, SHA256: strings.Repeat(string(rune('b'+i)), 64)}
	}
	if err := WriteProjectManifest(DefaultFS, ProjectManifestPath(root), manifest); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(DefaultFS, ProjectLockPath(root), lock); err != nil {
		t.Fatal(err)
	}
	instructions, err := MergeManagedInstructions("User-authored policy.\n", ids, lock.Skills)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(instructions), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, manifest, lock
}

func updateResolver(locks map[string]SkillLock) ProjectSkillResolver {
	return func(_ context.Context, ids []string, revision string) ([]ProjectSkill, map[string]SkillLock, error) {
		resolved := make(map[string]SkillLock, len(ids))
		for i, id := range ids {
			pin := SkillLock{Repository: projectSkillsRepository, Revision: revision, SHA256: strings.Repeat(string(rune('d'+i)), 64)}
			if existing, ok := locks[id]; ok && existing.Revision == revision {
				pin = existing
			}
			resolved[id] = pin
		}
		return nil, resolved, nil
	}
}

func TestPlanProjectUpdateNoOpDoesNotChangeProjectFiles(t *testing.T) {
	root, _, lock := updateFixture(t, "official/rgaa")
	beforeManifest := readFileForTest(t, ProjectManifestPath(root))
	beforeLock := readFileForTest(t, ProjectLockPath(root))
	beforeAgents := readFileForTest(t, filepath.Join(root, "AGENTS.md"))
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, projectSkillsRevision, updateResolver(lock.Skills))
	if err != nil || !plan.NoOp {
		t.Fatalf("plan = %+v, err = %v; want no-op", plan, err)
	}
	if err := plan.Apply(DefaultFS); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(beforeManifest, readFileForTest(t, ProjectManifestPath(root))) ||
		!equalBytes(beforeLock, readFileForTest(t, ProjectLockPath(root))) ||
		!equalBytes(beforeAgents, readFileForTest(t, filepath.Join(root, "AGENTS.md"))) {
		t.Fatal("a no-op update changed project files")
	}
	if _, err := os.Stat(ProjectUpdateJournalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("no-op update left a journal: %v", err)
	}
}

func TestPlanProjectUpdateIsSelectiveAndPreservesUserInstructions(t *testing.T) {
	root, _, oldLock := updateFixture(t, "official/rgaa", "official/security")
	target := strings.Repeat("c", 40)
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, []string{"official/rgaa"}, target, updateResolver(oldLock.Skills))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].ID != "official/rgaa" {
		t.Fatalf("changes = %+v, want only official/rgaa", plan.Changes)
	}
	if !strings.Contains(string(plan.newInstructions), "User-authored policy.") || !strings.Contains(string(plan.newInstructions), "official/security` at `"+projectSkillsRevision) {
		t.Fatalf("managed update altered user content or unselected pin: %q", plan.newInstructions)
	}
	diff, err := plan.ManagedInstructionsDiff()
	if err != nil || !strings.Contains(diff, target) || strings.Contains(diff, "User-authored policy") {
		t.Fatalf("managed instructions preview = %q, err = %v", diff, err)
	}
	if err := plan.Apply(DefaultFS); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLockfile(DefaultFS, ProjectLockPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDependencySet(manifest, lock); err != nil {
		t.Fatal(err)
	}
	if lock.Skills["official/rgaa"].Revision != target || lock.Skills["official/security"] != oldLock.Skills["official/security"] {
		t.Fatalf("selective lock update = %+v", lock.Skills)
	}
	if _, err := os.Stat(ProjectUpdateJournalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("successful update left a journal: %v", err)
	}
}

func TestPlanProjectUpdateNetworkFailureLeavesKnownGoodFiles(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	beforeManifest := readFileForTest(t, ProjectManifestPath(root))
	beforeLock := readFileForTest(t, ProjectLockPath(root))
	_, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), func(context.Context, []string, string) ([]ProjectSkill, map[string]SkillLock, error) {
		return nil, nil, errors.New("network unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "network unavailable") {
		t.Fatalf("plan error = %v, want catalogue/network error", err)
	}
	if !equalBytes(beforeManifest, readFileForTest(t, ProjectManifestPath(root))) || !equalBytes(beforeLock, readFileForTest(t, ProjectLockPath(root))) {
		t.Fatal("catalogue refresh failure changed the known-good project")
	}
}

func TestPlanProjectUpdateRejectsIntegrityMismatch(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	_, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), func(_ context.Context, ids []string, revision string) ([]ProjectSkill, map[string]SkillLock, error) {
		return nil, map[string]SkillLock{ids[0]: {Repository: projectSkillsRepository, Revision: revision, SHA256: "tampered"}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 validation") {
		t.Fatalf("plan error = %v, want integrity mismatch", err)
	}
}

func TestPlanProjectUpdatePreservesUnknownFields(t *testing.T) {
	root, _, oldLock := updateFixture(t, "official/rgaa")
	for _, item := range []struct {
		path  string
		field string
	}{
		{ProjectManifestPath(root), "futureManifestField"},
		{ProjectLockPath(root), "futureLockField"},
	} {
		data := readFileForTest(t, item.path)
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		fields[item.field] = "preserve-me"
		data, err := json.MarshalIndent(fields, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(item.path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(oldLock.Skills))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		data  []byte
		field string
	}{
		{plan.newManifest, "futureManifestField"},
		{plan.newLock, "futureLockField"},
	} {
		var fields map[string]any
		if err := json.Unmarshal(item.data, &fields); err != nil {
			t.Fatal(err)
		}
		if fields[item.field] != "preserve-me" {
			t.Errorf("update lost unknown field %s: %v", item.field, fields[item.field])
		}
	}
}

func TestRecoverProjectUpdateRollsBackSplitPair(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	oldManifest := readFileForTest(t, ProjectManifestPath(root))
	oldLock := readFileForTest(t, ProjectLockPath(root))
	oldAgents := readFileForTest(t, filepath.Join(root, "AGENTS.md"))
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	writeUpdateJournalForTest(t, root, plan)
	if err := atomicWrite(DefaultFS, ProjectManifestPath(root), plan.newManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecoverProjectUpdate(DefaultFS, root); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(oldManifest, readFileForTest(t, ProjectManifestPath(root))) || !equalBytes(oldLock, readFileForTest(t, ProjectLockPath(root))) || !equalBytes(oldAgents, readFileForTest(t, filepath.Join(root, "AGENTS.md"))) {
		t.Fatal("recovery did not restore the old manifest/lock/rules pair")
	}
	if _, err := os.Stat(ProjectUpdateJournalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("rollback left a journal: %v", err)
	}
}

func TestRecoverProjectUpdateRefusesUnknownManifestOrLockEdits(t *testing.T) {
	for _, name := range []string{"manifest", "lockfile"} {
		t.Run(name, func(t *testing.T) {
			root, _, _ := updateFixture(t, "official/rgaa")
			plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(nil))
			if err != nil {
				t.Fatal(err)
			}
			writeUpdateJournalForTest(t, root, plan)
			path := ProjectManifestPath(root)
			if name == "lockfile" {
				path = ProjectLockPath(root)
			}
			changed := append(readFileForTest(t, path), []byte(" \n")...)
			if err := atomicWrite(DefaultFS, path, changed, 0o644); err != nil {
				t.Fatal(err)
			}
			manifestBefore := readFileForTest(t, ProjectManifestPath(root))
			lockBefore := readFileForTest(t, ProjectLockPath(root))
			if err := RecoverProjectUpdate(DefaultFS, root); err == nil || !strings.Contains(err.Error(), "changed outside the pending update journal") {
				t.Fatalf("recovery error = %v, want refusal for unknown %s bytes", err, name)
			}
			if !equalBytes(manifestBefore, readFileForTest(t, ProjectManifestPath(root))) || !equalBytes(lockBefore, readFileForTest(t, ProjectLockPath(root))) {
				t.Fatalf("recovery overwrote user-edited %s", name)
			}
			if _, err := os.Stat(ProjectUpdateJournalPath(root)); err != nil {
				t.Fatalf("recovery removed the journal after refusing unknown bytes: %v", err)
			}
		})
	}
}

func TestRecoverProjectUpdateWithoutJournalDoesNotMutateCheckout(t *testing.T) {
	root := t.TempDir()
	if err := RecoverProjectUpdate(DefaultFS, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ProjectStateDir(root)); !os.IsNotExist(err) {
		t.Fatalf("recovery created project state without a journal: %v", err)
	}
}

func TestRecoverProjectUpdateFinishesCommittedPairWithoutStoringUserInstructions(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	writeUpdateJournalForTest(t, root, plan)
	if err := atomicWrite(DefaultFS, ProjectManifestPath(root), plan.newManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(DefaultFS, ProjectLockPath(root), plan.newLock, 0o644); err != nil {
		t.Fatal(err)
	}
	journal := readFileForTest(t, ProjectUpdateJournalPath(root))
	if strings.Contains(string(journal), "User-authored policy") {
		t.Fatal("journal copied user-authored AGENTS.md bytes")
	}
	if err := RecoverProjectUpdate(DefaultFS, root); err != nil {
		t.Fatal(err)
	}
	agents := string(readFileForTest(t, filepath.Join(root, "AGENTS.md")))
	if !strings.Contains(agents, "User-authored policy.") || !strings.Contains(agents, strings.Repeat("c", 40)) {
		t.Fatalf("recovery did not finish the managed rules update: %q", agents)
	}
}

func TestRecoverRollbackPreservesAGENTSCreatedByUserAfterCrash(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	if plan.instructionsExisted {
		t.Fatal("fixture unexpectedly had AGENTS.md")
	}
	writeUpdateJournalForTest(t, root, plan)
	if err := atomicWrite(DefaultFS, ProjectManifestPath(root), plan.newManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	const userText = "New user policy written while recovery was pending.\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(userText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecoverProjectUpdate(DefaultFS, root); err != nil {
		t.Fatal(err)
	}
	got := string(readFileForTest(t, filepath.Join(root, "AGENTS.md")))
	if !strings.Contains(got, userText) || !strings.Contains(got, projectSkillsRevision) {
		t.Fatalf("recovery lost user content or old managed rules: %q", got)
	}
}

func TestProjectUpdateApplyRollsBackOnSecondWriteFailure(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	oldManifest := readFileForTest(t, ProjectManifestPath(root))
	oldLock := readFileForTest(t, ProjectLockPath(root))
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	fs := &failUpdateRenameOnceFS{FS: DefaultFS, Path: ProjectLockPath(root)}
	if err := plan.Apply(fs); err == nil {
		t.Fatal("Apply succeeded despite lockfile rename failure")
	}
	if !equalBytes(oldManifest, readFileForTest(t, ProjectManifestPath(root))) || !equalBytes(oldLock, readFileForTest(t, ProjectLockPath(root))) {
		t.Fatal("failed update did not roll back the manifest/lock pair")
	}
	if _, err := os.Stat(ProjectUpdateJournalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("failed update left a recoverable journal unexpectedly: %v", err)
	}
}

func TestProjectUpdateApplyReturnsSuccessWhenRecoveryRollsForward(t *testing.T) {
	root, _, _ := updateFixture(t, "official/rgaa")
	target := strings.Repeat("c", 40)
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, target, updateResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	fs := &failUpdateRenameOnceFS{FS: DefaultFS, Path: filepath.Join(root, "AGENTS.md")}
	if err := plan.Apply(fs); err != nil {
		t.Fatalf("Apply returned error although recovery completed the update: %v", err)
	}
	manifest, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLockfile(DefaultFS, ProjectLockPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDependencySet(manifest, lock); err != nil {
		t.Fatal(err)
	}
	if lock.Skills["official/rgaa"].Revision != target || !strings.Contains(string(readFileForTest(t, filepath.Join(root, "AGENTS.md"))), target) {
		t.Fatal("recovery did not complete the proposed skill update")
	}
	if _, err := os.Stat(ProjectUpdateJournalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("successful roll-forward left a journal: %v", err)
	}
}

func TestProjectUpdateRejectsStalePlan(t *testing.T) {
	root, _, oldLock := updateFixture(t, "official/rgaa")
	plan, err := PlanProjectUpdate(context.Background(), DefaultFS, root, nil, strings.Repeat("c", 40), updateResolver(oldLock.Skills))
	if err != nil {
		t.Fatal(err)
	}
	oldLock.Entries["unrelated"] = "preserve-me"
	if err := WriteLockfile(DefaultFS, ProjectLockPath(root), oldLock); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(DefaultFS); err == nil || !strings.Contains(err.Error(), "changed after the update plan") {
		t.Fatalf("Apply error = %v, want stale-plan refusal", err)
	}
	lock, err := ReadLockfile(DefaultFS, ProjectLockPath(root))
	if err != nil || lock.Entries["unrelated"] != "preserve-me" {
		t.Fatalf("concurrent lock edit was lost: lock=%+v, err=%v", lock, err)
	}
	if _, err := os.Stat(ProjectUpdateJournalPath(root)); !os.IsNotExist(err) {
		t.Fatalf("stale plan created a journal: %v", err)
	}
}

func TestProjectUpdateLockUsesHostStateAndSerializesActions(t *testing.T) {
	root := t.TempDir()
	lock := &ProjectLock{Path: projectUpdateLockPath(root), FS: DefaultFS}
	release, err := lock.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	started := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- withProjectUpdateLock(DefaultFS, root, func() error {
			started <- struct{}{}
			return nil
		})
	}()
	select {
	case <-started:
		t.Fatal("second project update entered while the first held the host-state lock")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(ProjectStateDir(root)); !os.IsNotExist(err) {
		t.Fatalf("lock created a directory inside the project checkout: %v", err)
	}
	release()
	released = true
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("second update failed after the lock was released: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second update did not proceed after the host-state lock was released")
	}
	select {
	case <-started:
	default:
		t.Fatal("second update callback never ran")
	}
	if err := RecoverProjectUpdate(DefaultFS, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ProjectStateDir(root)); !os.IsNotExist(err) {
		t.Fatalf("journal-free recovery created project state: %v", err)
	}
}

func TestLoadProjectSkillStateRejectsMismatchedDependencySet(t *testing.T) {
	root := t.TempDir()
	manifest := ProjectManifest{DependencySetID: strings.Repeat("a", 32)}
	lock := Lockfile{DependencySetID: strings.Repeat("b", 32)}
	if err := WriteProjectManifest(DefaultFS, ProjectManifestPath(root), manifest); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(DefaultFS, ProjectLockPath(root), lock); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := LoadProjectSkillState(DefaultFS, root, t.TempDir(), "jc-update-test"); err == nil || !strings.Contains(err.Error(), "different dependency sets") {
		t.Fatalf("LoadProjectSkillState error = %v, want mismatched-pair rejection", err)
	}
}

func TestUpdateJournalPairRejectsSecretFields(t *testing.T) {
	id := strings.Repeat("a", 32)
	manifest, err := json.Marshal(ProjectManifest{SchemaVersion: projectManifestSchemaVersion, DependencySetID: id})
	if err != nil {
		t.Fatal(err)
	}
	lock := []byte(`{"schemaVersion":3,"dependencySetId":"` + id + `","entries":{},"token":"ghp_literal"}`)
	if _, _, err := validateUpdateJournalPair(manifest, lock); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("validateUpdateJournalPair error = %v, want secret-field rejection", err)
	}
}

func writeUpdateJournalForTest(t *testing.T, root string, plan ProjectUpdatePlan) {
	t.Helper()
	data, err := json.Marshal(projectUpdateJournal{
		SchemaVersion: 1, OldManifest: plan.oldManifest, OldLock: plan.oldLock,
		NewManifest: plan.newManifest, NewLock: plan.newLock,
		NewInstructionsHash: fmt.Sprintf("%x", sha256.Sum256(plan.newInstructions)),
		InstructionsExisted: plan.instructionsExisted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(DefaultFS, ProjectUpdateJournalPath(root), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type failUpdateRenameOnceFS struct {
	FS
	Path string
	Done bool
}

func (f *failUpdateRenameOnceFS) RenameTmp(oldPath, newPath string) error {
	if newPath == f.Path && !f.Done {
		f.Done = true
		return errors.New("injected lockfile rename failure")
	}
	return f.FS.RenameTmp(oldPath, newPath)
}
