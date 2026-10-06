package justcode

import (
	"bytes"
	"context"
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
	if !strings.Contains(review, "2 CPUs, 4G") {
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

func TestInitPlanEnforcesHostResourceCeiling(t *testing.T) {
	root := initTestRoot(t)
	host := HostResources{CPUs: 8, MemoryMB: 16384}
	for _, tc := range []struct {
		name   string
		cpus   int
		memory int
	}{
		{name: "CPU above host capacity", cpus: 9, memory: 4096},
		{name: "memory above host capacity", cpus: 4, memory: 16385},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wizard := InitWizard{HostResources: &host}
			_, err := wizard.Plan(InitAnswers{Root: root, CPUs: tc.cpus, MemoryMB: tc.memory})
			if err == nil || !strings.Contains(err.Error(), "exceeds detected host capacity") {
				t.Fatalf("Plan error = %v, want host capacity rejection", err)
			}
		})
	}
}

func TestInitPlanAddsResourceWarningsToReview(t *testing.T) {
	root := initTestRoot(t)
	host := HostResources{CPUs: 8, MemoryMB: 16384}
	plan, err := (InitWizard{HostResources: &host}).Plan(InitAnswers{Root: root, CPUs: 8, MemoryMB: 13108})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	review := FormatInitReview(plan)
	for _, warning := range []string{"all 8 logical host CPUs", "80.0% of detected host memory"} {
		if !strings.Contains(review, warning) {
			t.Errorf("review lacks warning %q: %s", warning, review)
		}
	}
}

func TestInitPlanGitHubWorkflowRequiresCanonicalRemote(t *testing.T) {
	root := initTestRoot(t)
	answers := InitAnswers{
		Root:           root,
		GitHubWorkflow: true,
		GitHubRemote:   GitHubRemote{URL: "https://user:token@github.com/owner/repo.git", Repo: "owner/repo"},
	}
	if _, err := (InitWizard{}).Plan(answers); err == nil {
		t.Fatal("a remote carrying credentials must not be accepted by the init plan")
	}
	answers.GitHubRemote = GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}
	plan, err := (InitWizard{}).Plan(answers)
	if err != nil {
		t.Fatalf("canonical GitHub remote: %v", err)
	}
	if !strings.Contains(FormatInitReview(plan), "guest workflow for owner/repo") {
		t.Fatal("the review must identify the approved repository")
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

// TestInitApplyRefusesToDiscardAnUnreadableLock pins the other half of the
// preservation rule: a lockfile that cannot be read must stop the run, not be
// replaced with an empty one. Replacing it would discard whatever pins it
// held, silently — the same refusal the manifest already gets.
func TestInitApplyRefusesToDiscardAnUnreadableLock(t *testing.T) {
	root := initTestRoot(t)
	if err := os.MkdirAll(filepath.Dir(ProjectLockPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProjectLockPath(root), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := (InitWizard{}).Plan(InitAnswers{Root: root, Model: "albert/x"})
	if err != nil {
		t.Fatalf("Plan (a corrupt lock is not the manifest's problem): %v", err)
	}
	if err := (InitWizard{}).Apply(plan, false); err == nil {
		t.Fatal("Apply must refuse to overwrite a lockfile it cannot read")
	}
	raw, err := os.ReadFile(ProjectLockPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{not json" {
		t.Fatalf("the unreadable lock must be left untouched, got %q", raw)
	}
}

func TestInitPinsVersionedSkillsAndManagedInstructionsIdempotently(t *testing.T) {
	root := initTestRoot(t)
	cache, state := t.TempDir(), t.TempDir()
	oldCacheDir := userCacheDirFn
	userCacheDirFn = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDirFn = oldCacheDir })
	const userText = "User-authored rules stay byte-for-byte.\n\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(userText), 0o644); err != nil {
		t.Fatal(err)
	}
	wizard := InitWizard{StateDir: state, ResolveSkills: fixtureSkillResolver(t)}
	plan, err := wizard.Plan(InitAnswers{Root: root, Skills: []string{"official/rgaa"}, SkillsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(FormatInitReview(plan), "AGENTS.md managed-zone diff") || !plan.InstructionsChanged {
		t.Fatalf("review omitted the generated instruction diff: %+v", plan)
	}
	if err := wizard.Apply(plan, false); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil || len(manifest.Skills) != 1 || manifest.Skills[0] != "official/rgaa" || manifest.SkillsLocalOnly {
		t.Fatalf("manifest = %+v, err = %v", manifest, err)
	}
	lock, err := ReadLockfile(DefaultFS, ProjectLockPath(root))
	if err != nil || lock.Skills["official/rgaa"].Revision != projectSkillsRevision || !isSHA256(lock.Skills["official/rgaa"].SHA256) {
		t.Fatalf("lock = %+v, err = %v", lock, err)
	}
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || !strings.HasPrefix(string(agents), userText) || !strings.Contains(string(agents), projectSkillsRevision) {
		t.Fatalf("managed AGENTS.md = %q, err = %v", agents, err)
	}
	beforeManifest, beforeLock, beforeAgents := readFileForTest(t, ProjectManifestPath(root)), readFileForTest(t, ProjectLockPath(root)), append([]byte(nil), agents...)
	// A replacement with no skill flags keeps the pinned selection, lock and
	// managed zone exactly unchanged and needs no network access.
	wizard.ResolveSkills = nil
	repeat, err := wizard.Plan(InitAnswers{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.InstructionsChanged {
		t.Fatalf("identical pinned selection produced an AGENTS.md diff: before=%q after=%q", repeat.InstructionsBefore, repeat.InstructionsAfter)
	}
	if err := wizard.Apply(repeat, true); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeManifest, readFileForTest(t, ProjectManifestPath(root))) ||
		!bytes.Equal(beforeLock, readFileForTest(t, ProjectLockPath(root))) ||
		!bytes.Equal(beforeAgents, readFileForTest(t, filepath.Join(root, "AGENTS.md"))) {
		t.Fatal("reapplying an unchanged skill selection changed project files")
	}
	if _, err := wizard.Plan(InitAnswers{Root: root, Runtime: RuntimeAgentVM}); err == nil {
		t.Fatal("preserved project skills must be rejected with a non-Microsandbox runtime")
	}
	clear, err := wizard.Plan(InitAnswers{Root: root, Skills: []string{}, SkillsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if !clear.InstructionsChanged || strings.Contains(clear.InstructionsAfter, "official/rgaa") {
		t.Fatalf("clearing skills must remove the bounded block: before=%q after=%q", clear.InstructionsBefore, clear.InstructionsAfter)
	}
	if err := wizard.Apply(clear, true); err != nil {
		t.Fatal(err)
	}
	cleared, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil || len(cleared.Skills) != 0 || cleared.SkillsLocalOnly {
		t.Fatalf("cleared manifest = %+v, err = %v", cleared, err)
	}
	clearedAgents := readFileForTest(t, filepath.Join(root, "AGENTS.md"))
	if !bytes.HasPrefix(clearedAgents, []byte(userText)) || bytes.Contains(clearedAgents, []byte("official/rgaa")) {
		t.Fatalf("clearing skills damaged user instructions or kept a managed skill: %q", clearedAgents)
	}
}

func TestInitApplyMergesAGENTSChangesMadeAfterPlan(t *testing.T) {
	root := initTestRoot(t)
	cache := t.TempDir()
	oldCacheDir := userCacheDirFn
	userCacheDirFn = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDirFn = oldCacheDir })
	wizard := InitWizard{ResolveSkills: fixtureSkillResolver(t)}
	plan, err := wizard.Plan(InitAnswers{Root: root, Skills: []string{"official/rgaa"}, SkillsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	const concurrentEdit = "User edit made while the review was open.\n"
	current := plan.InstructionsAfter + "\n" + concurrentEdit
	if err := os.WriteFile(plan.InstructionsPath, []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wizard.Apply(plan, false); err != nil {
		t.Fatal(err)
	}
	got := readFileForTest(t, plan.InstructionsPath)
	if !bytes.Contains(got, []byte("official/rgaa")) || !bytes.Contains(got, []byte(concurrentEdit)) {
		t.Fatalf("Apply lost concurrent user text or managed skill: %q", got)
	}
}

func TestInitApplyRepairsManagedInstructionsAfterNoDiffReview(t *testing.T) {
	root := initTestRoot(t)
	cache := t.TempDir()
	oldCacheDir := userCacheDirFn
	userCacheDirFn = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDirFn = oldCacheDir })
	wizard := InitWizard{ResolveSkills: fixtureSkillResolver(t)}
	answers := InitAnswers{Root: root, Skills: []string{"official/rgaa"}, SkillsSet: true}
	first, err := wizard.Plan(answers)
	if err != nil {
		t.Fatal(err)
	}
	if err := wizard.Apply(first, false); err != nil {
		t.Fatal(err)
	}
	plan, err := wizard.Plan(answers)
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstructionsChanged {
		t.Fatal("unchanged versioned skills should have an unchanged managed-zone preview")
	}
	const concurrentEdit = "User edit made after an unchanged preview.\n"
	if err := os.WriteFile(plan.InstructionsPath, []byte(concurrentEdit), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wizard.Apply(plan, true); err != nil {
		t.Fatal(err)
	}
	got := readFileForTest(t, plan.InstructionsPath)
	if !bytes.Contains(got, []byte("official/rgaa")) || !bytes.Contains(got, []byte(concurrentEdit)) {
		t.Fatalf("Apply did not restore the managed skill zone or lost concurrent user text: %q", got)
	}
}

func TestInitApplyRemovesStaleInstructionsForEmptyVersionedSelection(t *testing.T) {
	root := initTestRoot(t)
	manifestPath, lockPath := ProjectManifestPath(root), ProjectLockPath(root)
	if err := WriteProjectManifest(DefaultFS, manifestPath, ProjectManifest{
		Runtime: string(RuntimeMicrosandbox), Isolation: string(IsolationFull),
	}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(DefaultFS, lockPath, Lockfile{Entries: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	const userInstructions = "User policy.\n"
	instructionsPath := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(instructionsPath, []byte(userInstructions), 0o644); err != nil {
		t.Fatal(err)
	}
	wizard := InitWizard{}
	plan, err := wizard.Plan(InitAnswers{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstructionsPath == "" || plan.InstructionsChanged {
		t.Fatalf("empty schema-v2 selection must own the managed zone without a preview diff: %+v", plan)
	}
	pins := map[string]SkillLock{"official/rgaa": {Revision: projectSkillsRevision}}
	stale, err := MergeManagedInstructions(userInstructions, []string{"official/rgaa"}, pins)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.InstructionsPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wizard.Apply(plan, true); err != nil {
		t.Fatal(err)
	}
	got := readFileForTest(t, instructionsPath)
	if !bytes.Contains(got, []byte(userInstructions)) || bytes.Contains(got, []byte("BEGIN JUST-CODE MANAGED SKILLS")) {
		t.Fatalf("empty versioned selection left stale managed instructions or changed user text: %q", got)
	}
}

func TestInitLocalOnlySkillsStayOutsideTheCheckout(t *testing.T) {
	root := initTestRoot(t)
	cache, state := t.TempDir(), t.TempDir()
	oldCacheDir := userCacheDirFn
	userCacheDirFn = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDirFn = oldCacheDir })
	wizard := InitWizard{StateDir: state, ResolveSkills: fixtureSkillResolver(t)}
	plan, err := wizard.Plan(InitAnswers{
		Root: root, Skills: []string{"official/rgaa"}, SkillsSet: true,
		SkillsLocalOnly: true, SkillsLocalOnlySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstructionsChanged || strings.Contains(FormatInitReview(plan), "AGENTS.md managed-zone diff") {
		t.Fatal("local-only selection must not preview a project instruction edit")
	}
	if err := wizard.Apply(plan, false); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadProjectManifest(DefaultFS, ProjectManifestPath(root))
	if err != nil || !manifest.SkillsLocalOnly || len(manifest.Skills) != 0 {
		t.Fatalf("manifest = %+v, err = %v", manifest, err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("local-only mode wrote AGENTS.md (stat err = %v)", err)
	}
	path, err := LocalSkillSelectionsPath(state, DiscoverInstanceForTest(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, root+string(filepath.Separator)) {
		t.Fatalf("host-local skill state is inside the checkout: %s", path)
	}
	local, err := ReadLocalSkillSelections(DefaultFS, path)
	if err != nil || len(local.Skills) != 1 || local.Pins["official/rgaa"].Revision != projectSkillsRevision {
		t.Fatalf("local selections = %+v, err = %v", local, err)
	}
	packages, err := LoadProjectSkillPackages(DefaultFS, root, state, DiscoverInstanceForTest(t, root))
	if err != nil || len(packages) != 1 {
		t.Fatalf("local packages = %d, err = %v", len(packages), err)
	}
}

type failRenameForPathFS struct {
	FS
	Path string
}

func (f failRenameForPathFS) RenameTmp(oldPath, newPath string) error {
	if newPath == f.Path {
		return os.ErrPermission
	}
	return f.FS.RenameTmp(oldPath, newPath)
}

func TestInitLocalOnlyStateWriteFailurePreservesVersionedFiles(t *testing.T) {
	root := initTestRoot(t)
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	cache, state := t.TempDir(), t.TempDir()
	oldCacheDir := userCacheDirFn
	userCacheDirFn = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDirFn = oldCacheDir })
	ids := []string{"official/rgaa"}
	_, pins, err := fixtureSkillResolver(t)(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMapFS()
	agentsPath := filepath.Join(root, "AGENTS.md")
	agents, err := MergeManagedInstructions("User rules.\n", ids, pins)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentsPath, []byte(agents), 0o644); err != nil {
		t.Fatal(err)
	}
	fs.files[agentsPath] = []byte(agents)
	manifestPath, lockPath := ProjectManifestPath(root), ProjectLockPath(root)
	if err := WriteProjectManifest(fs, manifestPath, ProjectManifest{Skills: ids, Runtime: string(RuntimeMicrosandbox), Isolation: string(IsolationFull)}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(fs, lockPath, Lockfile{Skills: pins}); err != nil {
		t.Fatal(err)
	}
	beforeManifest := append([]byte(nil), fs.files[manifestPath]...)
	beforeLock := append([]byte(nil), fs.files[lockPath]...)
	beforeAgents := append([]byte(nil), fs.files[agentsPath]...)
	wizard := InitWizard{FS: fs, StateDir: state}
	plan, err := wizard.Plan(InitAnswers{Root: root, SkillsLocalOnly: true, SkillsLocalOnlySet: true})
	if err != nil {
		t.Fatal(err)
	}
	wizard.FS = failRenameForPathFS{FS: fs, Path: plan.ManifestPath}
	if err := wizard.Apply(plan, true); err == nil {
		t.Fatal("manifest failure after staging local pins must be reported")
	}
	if !bytes.Equal(fs.files[manifestPath], beforeManifest) || !bytes.Equal(fs.files[lockPath], beforeLock) || !bytes.Equal(fs.files[agentsPath], beforeAgents) {
		t.Fatal("failed local-only state persistence changed versioned project files")
	}
	localPath, err := LocalSkillSelectionsPath(state, DiscoverInstanceForTest(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := fs.files[localPath]; exists {
		t.Fatal("failed local-only persistence left a partial selection file")
	}
}

func TestMergeManagedInstructionsRefusesSymlink(t *testing.T) {
	root := initTestRoot(t)
	outside := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(outside, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "AGENTS.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	wizard := InitWizard{ResolveSkills: fixtureSkillResolver(t)}
	if _, err := wizard.Plan(InitAnswers{Root: root, Skills: []string{"official/rgaa"}, SkillsSet: true}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked AGENTS.md error = %v", err)
	}
	if got := readFileForTest(t, outside); string(got) != "keep me\n" {
		t.Fatalf("external file changed: %q", got)
	}
}

func readFileForTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func DiscoverInstanceForTest(t *testing.T, root string) string {
	t.Helper()
	project, err := DiscoverProject(root)
	if err != nil {
		t.Fatal(err)
	}
	return project.InstanceName()
}

// TestInitWarnsForARelativeNestedRoot pins that the "covers the whole
// worktree" notice survives a relative invocation: `just-code init nested/dir`
// names a directory inside the worktree just as much as an absolute path does.
func TestInitWarnsForARelativeNestedRoot(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	plan, err := (InitWizard{}).Plan(InitAnswers{Root: filepath.Join("nested")})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var told bool
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "worktree root") {
			told = true
		}
	}
	if !told {
		t.Fatalf("a relative nested root must still be reported: %v", plan.Warnings)
	}
}
