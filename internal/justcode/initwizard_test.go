package justcode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRoot is a Git-less project directory: the wizard's review notes that
// the manifest will not be versioned, which is useful for one test and
// harmless for the others.
func initTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInitPlanResolvesDefaults(t *testing.T) {
	root := initTestRoot(t)
	plan, err := InitWizard{}.Plan(InitAnswers{Root: root})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Answers.Runtime != RuntimeMicrosandbox {
		t.Fatalf("runtime = %q, want the built-in default", plan.Answers.Runtime)
	}
	if plan.Answers.Isolation != IsolationFull {
		t.Fatalf("isolation = %q, want the built-in default", plan.Answers.Isolation)
	}
	if plan.ManifestPath != ProjectManifestPath(plan.Answers.Root) || plan.LockPath != ProjectLockPath(plan.Answers.Root) {
		t.Fatalf("paths = %q / %q", plan.ManifestPath, plan.LockPath)
	}
	if plan.ExistingManifest != nil {
		t.Fatal("a fresh project must not report an existing manifest")
	}
	// Zero means "use the built-in default", not "zero CPUs".
	if plan.Answers.CPUs != 0 || plan.Answers.MemoryMB != 0 {
		t.Fatalf("sizing = %d / %d, want the unset representation", plan.Answers.CPUs, plan.Answers.MemoryMB)
	}
	review := FormatInitReview(plan)
	if !strings.Contains(review, "2 CPUs, 4096 MiB") {
		t.Fatalf("the review must show the effective sizing: %q", review)
	}
}

func TestInitPlanRejectsBadAnswers(t *testing.T) {
	root := initTestRoot(t)
	file := filepath.Join(root, "main.go")
	cases := []struct {
		name    string
		answers InitAnswers
		want    string
	}{
		{"missing root", InitAnswers{}, "root is required"},
		// The message is the OS's, so assert on the part this code owns: the
		// path it could not use. "no such file" is Unix wording; Windows says
		// "The system cannot find the file specified".
		{"nonexistent root", InitAnswers{Root: filepath.Join(root, "nope")}, "nope"},
		{"root is a file", InitAnswers{Root: file}, "not a directory"},
		{"unknown runtime", InitAnswers{Root: root, Runtime: "podman"}, "microsandbox"},
		{"unknown isolation", InitAnswers{Root: root, Isolation: "sometimes"}, "isolation"},
		{"cpus above the SDK range", InitAnswers{Root: root, CPUs: MaxSandboxCPUs + 1}, "CPUs must be"},
		{"negative memory", InitAnswers{Root: root, MemoryMB: -1}, "memory must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := (InitWizard{}).Plan(tc.answers); err == nil {
				t.Fatal("expected an error")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestInitPlanValidatesTheModel(t *testing.T) {
	root := initTestRoot(t)
	rejected := InitWizard{ValidateModel: func(string) (string, error) {
		return "", errors.New("the model is not in the catalogue")
	}}
	if _, err := rejected.Plan(InitAnswers{Root: root, Model: "albert/gone"}); err == nil {
		t.Fatal("a rejected model must stop the setup: the answer has to change")
	}

	// An unreachable catalogue is a warning, not a refusal: the model is still
	// recorded and the user can correct it later.
	warned := InitWizard{ValidateModel: func(string) (string, error) {
		return "the catalogue could not be reached; the model was recorded unverified", nil
	}}
	plan, err := warned.Plan(InitAnswers{Root: root, Model: "albert/deepseek-v4-flash"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// The non-Git warning is expected too (the fixture is not a repository), so
	// assert the catalogue warning is present rather than alone.
	var found bool
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "unverified") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want the unverified-model note", plan.Warnings)
	}
	if !strings.Contains(FormatInitReview(plan), "unverified") {
		t.Fatal("the review must carry the warning the user has to weigh")
	}
}

func TestInitApplyWritesManifestAndLock(t *testing.T) {
	root := initTestRoot(t)
	plan, err := (InitWizard{}).Plan(InitAnswers{
		Root: root, Model: "albert/deepseek-v4-flash",
		CPUs: 4, MemoryMB: 8192, CredentialRef: "work",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := (InitWizard{}).Apply(plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	pm, err := ReadProjectManifest(DefaultFS, plan.ManifestPath)
	if err != nil {
		t.Fatalf("the manifest must be readable by the normal reader: %v", err)
	}
	if pm.Runtime != string(RuntimeMicrosandbox) || pm.Isolation != string(IsolationFull) ||
		pm.Model != "albert/deepseek-v4-flash" || pm.CPUs != 4 || pm.MemoryMB != 8192 ||
		pm.CredentialRef != "work" {
		t.Fatalf("manifest = %+v", pm)
	}
	// Only settings the launch path actually applies are recorded: a field
	// nothing reads would be a decision the setup pretends to have taken.
	if pm.Project != "" || pm.Storage != "" {
		t.Fatalf("the manifest must not record settings no reader consumes: %+v", pm)
	}
	if pm.SchemaVersion != projectManifestSchemaVersion {
		t.Fatalf("schemaVersion = %d", pm.SchemaVersion)
	}
	lf, err := ReadLockfile(DefaultFS, plan.LockPath)
	if err != nil {
		t.Fatalf("the lockfile must exist and be readable: %v", err)
	}
	if len(lf.Entries) != 0 {
		t.Fatalf("nothing is pinned yet, entries = %v", lf.Entries)
	}
	// The manifest must never carry a credential value.
	raw, err := os.ReadFile(plan.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "API_KEY") {
		t.Fatalf("the manifest must reference the credential by name only: %s", raw)
	}
}

func TestInitApplyRefusesToReplaceWithoutConsent(t *testing.T) {
	root := initTestRoot(t)
	if err := os.MkdirAll(filepath.Dir(ProjectManifestPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	original := ProjectManifest{SchemaVersion: 1, Project: "kept", Model: "albert/original"}
	if err := WriteProjectManifest(DefaultFS, ProjectManifestPath(root), original); err != nil {
		t.Fatal(err)
	}

	plan, err := (InitWizard{}).Plan(InitAnswers{Root: root, Model: "albert/new"})
	if err != nil {
		t.Fatalf("Plan on an existing manifest must still work: %v", err)
	}
	if plan.ExistingManifest == nil {
		t.Fatal("the plan must report the existing manifest")
	}
	// The review shows what would be replaced, not just that something exists.
	review := FormatInitReview(plan)
	if !strings.Contains(review, "REPLACED") || !strings.Contains(review, "albert/original") {
		t.Fatalf("the review must show what would be lost: %q", review)
	}

	if err := (InitWizard{}).Apply(plan, false); err == nil {
		t.Fatal("Apply must refuse to replace an existing manifest without consent")
	}
	kept, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if kept.Project != "kept" || kept.Model != "albert/original" {
		t.Fatalf("the existing manifest must be untouched: %+v", kept)
	}

	if err := (InitWizard{}).Apply(plan, true); err != nil {
		t.Fatalf("Apply with consent: %v", err)
	}
	replaced, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Model != "albert/new" {
		t.Fatalf("manifest = %+v", replaced)
	}
}

// TestInitPlanCanonicalizesToTheWorktreeRoot pins the rule that makes the
// written manifest readable: the launch path resolves a project to its Git
// worktree root, so initializing a subdirectory of one must write the root's
// manifest, not a nested file nothing would ever read.
func TestInitPlanCanonicalizesToTheWorktreeRoot(t *testing.T) {
	root := initTestRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "T"}, {"add", "-A"}, {"commit", "-q", "-m", "i", "--allow-empty"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v (%s)", args, err, out)
		}
	}
	sub := filepath.Join(root, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := (InitWizard{}).Plan(InitAnswers{Root: sub})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// Both sides are compared in their resolved form: t.TempDir() is the
	// unresolved spelling on macOS (/var -> /private/var) and Windows (8.3
	// short names), while discovery resolves it.
	wantRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		wantRoot = resolved
	}
	if plan.Answers.Root != wantRoot {
		t.Fatalf("root = %q, want the worktree root %q", plan.Answers.Root, wantRoot)
	}
	if plan.ManifestPath != ProjectManifestPath(wantRoot) {
		t.Fatalf("manifest path = %q, want the root's manifest", plan.ManifestPath)
	}
	// The user is told the manifest covers more than the directory they named.
	var told bool
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "worktree root") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the review must say the manifest covers the worktree: %v", plan.Warnings)
	}
	// And no nested manifest is written for the subdirectory.
	if err := (InitWizard{}).Apply(plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, ".just-code")); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written below the root (stat err = %v)", err)
	}
}

// TestInitApplyKeepsExistingPins pins that replacing a manifest is not a
// reason to discard a lockfile someone else committed.
func TestInitApplyKeepsExistingPins(t *testing.T) {
	root := initTestRoot(t)
	if err := os.MkdirAll(filepath.Dir(ProjectLockPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(DefaultFS, ProjectLockPath(root), Lockfile{Entries: map[string]string{"skill": "rev-1"}}); err != nil {
		t.Fatal(err)
	}
	plan, err := (InitWizard{}).Plan(InitAnswers{Root: root, Model: "albert/x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (InitWizard{}).Apply(plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	lf, err := ReadLockfile(DefaultFS, ProjectLockPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if lf.Entries["skill"] != "rev-1" {
		t.Fatalf("an existing pin must survive a setup run: %v", lf.Entries)
	}
}

func TestInitReviewWarnsAboutAnUnversionedManifest(t *testing.T) {
	root := initTestRoot(t)
	plan, err := (InitWizard{}).Plan(InitAnswers{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Warnings) == 0 || !strings.Contains(plan.Warnings[0], "not a Git repository") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
	// The sealed-runtime review must state what the guest receives, since that
	// is the difference the user is choosing between.
	if !strings.Contains(FormatInitReview(plan), "filtered copy") {
		t.Fatal("a Microsandbox project must be told the guest gets a filtered copy")
	}
	// The mounting-runtime wording only applies where such a runtime is
	// selectable: Tart is macOS-only, so this is skipped on Windows rather
	// than asserting a choice the platform refuses.
	mounting := RuntimeTart
	if _, err := parseRuntimeName(string(mounting)); err != nil {
		t.Skipf("%s is not selectable on this platform (%v)", mounting, err)
	}
	mounted, err := (InitWizard{}).Plan(InitAnswers{Root: root, Runtime: mounting})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(FormatInitReview(mounted), "MOUNTS") {
		t.Fatal("a mounting runtime must be told the project is exposed in the guest")
	}
}

// TestInitPlanDoesNotWarnOnADifferentSpellingOfTheSameDirectory pins that the
// worktree warning is about a genuinely wider root, not about a path that
// merely resolves differently: t.TempDir() is the unresolved spelling on macOS
// (/var -> /private/var) and on Windows (8.3 short names), and warning there
// would fire on an ordinary launch.
func TestInitPlanDoesNotWarnOnADifferentSpellingOfTheSameDirectory(t *testing.T) {
	real := t.TempDir()
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == real {
		// The spelling does not differ here; exercise it through a symlink so
		// the property is tested on every platform.
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		real = link
	}
	plan, err := (InitWizard{}).Plan(InitAnswers{Root: real})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "worktree root") {
			t.Fatalf("a differently-spelled path to the same directory must not warn: %v", plan.Warnings)
		}
	}
	if plan.Answers.Root != resolved {
		t.Fatalf("root = %q, want %q", plan.Answers.Root, resolved)
	}
}
