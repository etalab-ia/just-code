package justcode

import (
	"errors"
	"os"
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
	if plan.Answers.Storage != StorageVersioned {
		t.Fatalf("storage = %q", plan.Answers.Storage)
	}
	if plan.Answers.Name != filepath.Base(root) {
		t.Fatalf("name = %q, want the root's base name", plan.Answers.Name)
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
		{"nonexistent root", InitAnswers{Root: filepath.Join(root, "nope")}, "no such file"},
		{"root is a file", InitAnswers{Root: file}, "not a directory"},
		{"unknown runtime", InitAnswers{Root: root, Runtime: "podman"}, "microsandbox"},
		{"unknown isolation", InitAnswers{Root: root, Isolation: "sometimes"}, "isolation"},
		{"cpus above the SDK range", InitAnswers{Root: root, CPUs: MaxSandboxCPUs + 1}, "CPUs must be"},
		{"negative memory", InitAnswers{Root: root, MemoryMB: -1}, "memory must be"},
		{"unknown storage", InitAnswers{Root: root, Storage: "elsewhere"}, "storage must be"},
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
		Root: root, Name: "demo", Model: "albert/deepseek-v4-flash",
		CPUs: 4, MemoryMB: 8192, CredentialRef: "work", Storage: StorageVersioned,
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
	if pm.Project != "demo" || pm.Runtime != string(RuntimeMicrosandbox) || pm.Isolation != string(IsolationFull) ||
		pm.Model != "albert/deepseek-v4-flash" || pm.CPUs != 4 || pm.MemoryMB != 8192 ||
		pm.CredentialRef != "work" || pm.Storage != StorageVersioned {
		t.Fatalf("manifest = %+v", pm)
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

	plan, err := (InitWizard{}).Plan(InitAnswers{Root: root, Name: "replacement", Model: "albert/new"})
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
	if replaced.Project != "replacement" || replaced.Model != "albert/new" {
		t.Fatalf("manifest = %+v", replaced)
	}
}

func TestInitLocalStorageKeepsTheCheckoutClean(t *testing.T) {
	isolateHostState(t)
	root := initTestRoot(t)
	plan, err := (InitWizard{}).Plan(InitAnswers{Root: root, Storage: StorageLocal})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := (InitWizard{}).Apply(plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".just-code")); !os.IsNotExist(err) {
		t.Fatalf("local storage must not add anything to the checkout (stat err = %v)", err)
	}
	if _, err := ReadProjectManifest(DefaultFS, plan.ManifestPath); err != nil {
		t.Fatalf("the manifest must be readable from host state: %v", err)
	}
	if !strings.Contains(FormatInitReview(plan), "host state only") {
		t.Fatalf("the review must state where the state lives: %q", FormatInitReview(plan))
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
	mounted, err := (InitWizard{}).Plan(InitAnswers{Root: root, Runtime: RuntimeTart})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(FormatInitReview(mounted), "MOUNTS") {
		t.Fatal("a mounting runtime must be told the project is exposed in the guest")
	}
}
