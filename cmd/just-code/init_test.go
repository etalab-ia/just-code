package main

import (
	"bufio"
	"charm.land/huh/v2"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/etalab-ia/just-code/internal/justcode"
)

// stubCatalogueCheck replaces the catalogue validator for one test. The real
// one needs an Albert credential and a reachable (or cached) catalogue, so a
// test that exercises it fails on a machine that has those — which is exactly
// how this seam came to exist.
func stubCatalogueCheck(t *testing.T, fn func(root, model string) (string, error)) {
	t.Helper()
	orig := catalogueModelWarningFn
	catalogueModelWarningFn = fn
	t.Cleanup(func() { catalogueModelWarningFn = orig })
}

func stubProjectSkills(t *testing.T) {
	t.Helper()
	original := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return []justcode.ProjectSkill{{ID: "official/example", Name: "example", Description: "fixture"}}, nil
	}
	t.Cleanup(func() { projectSkillCatalogueFn = original })
}

func stubInitSkillResolver(t *testing.T) {
	t.Helper()
	originalResolve, originalVerify := resolveInitProjectSkillsFn, verifyInitProjectSkillsFn
	resolveInitProjectSkillsFn = func(_ context.Context, ids []string) ([]justcode.ProjectSkill, map[string]justcode.SkillLock, error) {
		entries := make([]justcode.ProjectSkill, 0, len(ids))
		locks := make(map[string]justcode.SkillLock, len(ids))
		for _, id := range ids {
			entries = append(entries, justcode.ProjectSkill{ID: id, Name: strings.TrimPrefix(id, "official/")})
			locks[id] = justcode.SkillLock{
				Repository: "https://github.com/etalab-ia/skills.git",
				Revision:   strings.Repeat("a", 40),
				SHA256:     strings.Repeat("b", 64),
			}
		}
		return entries, locks, nil
	}
	verifyInitProjectSkillsFn = func([]string, map[string]justcode.SkillLock) error { return nil }
	t.Cleanup(func() {
		resolveInitProjectSkillsFn, verifyInitProjectSkillsFn = originalResolve, originalVerify
	})
}

func TestPrintInitIntroShowsProjectAndSteps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "qa-just-code-scratch")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if !printInitIntro(root, bufio.NewReader(strings.NewReader("\n"))) {
			t.Fatal("empty Enter line should start init")
		}
	})
	for _, want := range []string{
		"Configuration du projet : qa-just-code-scratch",
		"Dossier : " + root,
		"Par défaut, Microsandbox travaille sur une copie filtrée",
		"vos fichiers .env",
		"Avec Tart et agent-vm, le dossier du projet est monté dans la machine",
		"l'agent peut lire et modifier les fichiers qui s'y trouvent.",
		"1. Skills (facultatif)",
		"2. MCP (facultatif)",
		"3. GitHub (facultatif)",
		"Rien n'est enregistré avant le récapitulatif final.",
		"Appuyez sur Entrée pour commencer, ou sur Ctrl+C pour quitter.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("intro is missing %q; output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "les fichiers ignorés par git n'y sont jamais copiés") {
		t.Fatalf("intro overstates workspace filtering for mounted runtimes: %s", out)
	}
}

func TestInitInteractiveUsesDisplayedDirectoryWithoutPrompting(t *testing.T) {
	got := initOptionsWithDisplayedRoot(initOptions{Set: map[string]bool{}})
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != cwd || !got.Set["root"] {
		t.Fatalf("interactive root = %q, root supplied = %v; want %q and true", got.Root, got.Set["root"], cwd)
	}

	root := t.TempDir()
	got = initOptionsWithDisplayedRoot(initOptions{Root: root, Set: map[string]bool{}})
	if got.Root != root || !got.Set["root"] {
		t.Fatalf("explicit root = %q, root supplied = %v; want %q and true", got.Root, got.Set["root"], root)
	}
}

func TestPrintInitIntroEOFStopsBeforeInit(t *testing.T) {
	out := captureStdout(t, func() {
		if printInitIntro(t.TempDir(), bufio.NewReader(strings.NewReader(""))) {
			t.Fatal("EOF must cancel init")
		}
	})
	if !strings.Contains(out, "Rien n'est enregistré avant le récapitulatif final.") {
		t.Fatalf("intro was not shown before cancellation: %q", out)
	}
}

func TestInitSetupOfferRunsSetupBeforeContinuing(t *testing.T) {
	originalCheck, originalSetup := initAlbertCredentialCheckFn, runInitSetupFn
	initAlbertCredentialCheckFn = func() error { return justcode.ErrCredentialNotFound }
	setupCalled := false
	runInitSetupFn = func(bool) (int, error) {
		setupCalled = true
		return 0, nil
	}
	t.Cleanup(func() {
		initAlbertCredentialCheckFn, runInitSetupFn = originalCheck, originalSetup
	})

	out := captureStdout(t, func() {
		proceed, code, err := ensureInitSetup(bufio.NewReader(strings.NewReader("\n")))
		if !proceed || code != 0 || err != nil {
			t.Fatalf("ensureInitSetup = %v, %d, %v", proceed, code, err)
		}
	})
	if !setupCalled {
		t.Fatal("missing global configuration must run setup before the project wizard")
	}
	if !strings.Contains(out, "Lancer 'just-code setup' d'abord ?") {
		t.Fatalf("setup offer missing from output: %q", out)
	}
}

func TestInitSetupOfferCanBeDeclinedWithoutError(t *testing.T) {
	originalCheck, originalSetup := initAlbertCredentialCheckFn, runInitSetupFn
	initAlbertCredentialCheckFn = func() error { return justcode.ErrCredentialNotFound }
	runInitSetupFn = func(bool) (int, error) {
		t.Fatal("declining must not launch setup")
		return 1, nil
	}
	t.Cleanup(func() {
		initAlbertCredentialCheckFn, runInitSetupFn = originalCheck, originalSetup
	})

	out := captureStdout(t, func() {
		proceed, code, err := ensureInitSetup(bufio.NewReader(strings.NewReader("n\n")))
		if proceed || code != 0 || err != nil {
			t.Fatalf("declined setup = %v, %d, %v; want clean stop", proceed, code, err)
		}
	})
	if !strings.Contains(out, "Vous pourrez lancer 'just-code setup'") {
		t.Fatalf("decline guidance missing: %q", out)
	}
}

func TestInitSetupCheckUsesDefaultAlbertCredentialNotNamedReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(justcode.ProjectStateDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root), justcode.ProjectManifest{CredentialRef: "missing-project-ref"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	settingsPath, err := justcode.UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := justcode.WriteUserSettings(justcode.DefaultFS, settingsPath, justcode.UserSettings{CredentialRef: "missing-user-ref"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALBERT_API_KEY", "test-albert-key")
	t.Chdir(root)

	for _, tc := range []struct {
		name string
		ref  string
	}{
		{name: "environment named reference", ref: "missing-env-ref"},
		{name: "user settings named reference", ref: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JUST_CODE_CREDENTIAL_REF", tc.ref)
			needed, err := initSetupNeeded()
			if err != nil || needed {
				t.Fatalf("setup needed = %v, err = %v; usable default Albert key should mean setup is complete", needed, err)
			}
		})
	}
}

func TestInitCmdRejectsInvalidOptionsBeforeOfferingSetup(t *testing.T) {
	originalTTY, originalStdoutTTY := initTTYFn, initStdoutTTYFn
	originalCredentialCheck := initAlbertCredentialCheckFn
	initTTYFn = func() bool { return true }
	initStdoutTTYFn = func() bool { return true }
	setupCheckCalled := false
	initAlbertCredentialCheckFn = func() error {
		setupCheckCalled = true
		return justcode.ErrCredentialNotFound
	}
	t.Cleanup(func() {
		initTTYFn, initStdoutTTYFn = originalTTY, originalStdoutTTY
		initAlbertCredentialCheckFn = originalCredentialCheck
	})

	code, err := initCmd([]string{"--runtime", "unknown"})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "--microsandbox") {
		t.Fatalf("initCmd invalid runtime = %d, %v; want validation error before interaction", code, err)
	}
	if setupCheckCalled {
		t.Fatal("invalid options must be rejected before the global setup offer")
	}
}

// stubWizardHuh forces the line-based wizard path. The scripted tests drive
// initRun with tty=true and a string reader; the seam's production probe
// would be false there (stdout is a pipe), but pinning it keeps the tests
// independent of what the test binary's streams happen to be.
func stubWizardHuh(t *testing.T) {
	orig := wizardHuhFn
	wizardHuhFn = func() bool { return false }
	t.Cleanup(func() { wizardHuhFn = orig })
}

func initTestProject(t *testing.T) string {
	t.Helper()
	stubWizardHuh(t)
	originalInstallHint := runtimeInstallHintFn
	runtimeInstallHintFn = func(justcode.Runtime) (bool, string) { return true, "" }
	t.Cleanup(func() { runtimeInstallHintFn = originalInstallHint })
	// The wizard resolves the GitHub origin and stored credential before
	// offering the approval question (#155). Stub the seam so the scripted
	// tests stay hermetic: the real one reads the host keychain and a real
	// git remote. Tests that exercise GitHub behavior install their own stub.
	originalPreflight := githubInitPreflightFn
	githubInitPreflightFn = func(string) (justcode.GitHubRemote, error) {
		return justcode.GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}, nil
	}
	t.Cleanup(func() { githubInitPreflightFn = originalPreflight })
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 32, MemoryMB: 262144}, nil
	}
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func stubMCPBindingStateDir(t *testing.T) string {
	t.Helper()
	stateDir := t.TempDir()
	original := mcpBindingStateDirFn
	mcpBindingStateDirFn = func() string { return stateDir }
	t.Cleanup(func() { mcpBindingStateDirFn = original })
	return stateDir
}

// TestParseInitArgs pins the flag surface, including the two numeric options.
func TestParseInitArgs(t *testing.T) {
	opts, err := parseInitArgs([]string{
		"--root", "/tmp/p", "--runtime", "tart", "--isolation", "backend",
		"--model", "albert/x", "--cpus", "4", "--memory-mb", "2048",
		"--credential-ref", "work", "--github", "--skill", "official/rgaa",
		"--skill", "experimental/rag-parse", "--local-only-skills", "--replace", "--yes",
	})
	if err != nil {
		t.Fatalf("parseInitArgs: %v", err)
	}
	if opts.Root != "/tmp/p" || opts.Runtime != "tart" || opts.Isolation != "backend" ||
		opts.Model != "albert/x" || opts.CPUs != 4 || opts.MemoryMB != 2048 ||
		opts.CredentialRef != "work" || !opts.GitHub || !opts.Replace || !opts.Yes ||
		len(opts.Skills) != 2 || !opts.SkillsLocalOnly {
		t.Fatalf("options = %+v", opts)
	}
	if !opts.Set["root"] || !opts.Set["cpus"] || !opts.Set["memory-mb"] || !opts.Set["github"] ||
		!opts.Set["skills"] || !opts.Set["skills-storage"] {
		t.Fatalf("every supplied field must be recorded as set: %v", opts.Set)
	}
	readableMemory, err := parseInitArgs([]string{"--memory-mb", "2.5G"})
	if err != nil || readableMemory.MemoryMB != 2560 {
		t.Fatalf("--memory-mb 2.5G parsed as %d MiB, err=%v; want 2560", readableMemory.MemoryMB, err)
	}
	duplicate, err := parseInitArgs([]string{"--skill", "official/rgaa", "--skill", "official/rgaa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateInitOptions(duplicate); err == nil || !strings.Contains(err.Error(), "selected more than once") {
		t.Fatalf("duplicate skill selection error = %v", err)
	}
	for _, args := range [][]string{{"--cpus", "many"}, {"--memory-mb", "x"}, {"--root"}, {"--bogus"}} {
		if _, err := parseInitArgs(args); err == nil {
			t.Fatalf("%v must be rejected", args)
		}
	}
}

func TestInitFlagsCannotExceedHostResourceCap(t *testing.T) {
	root := initTestProject(t)
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 8, MemoryMB: 16384}, nil
	}
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources })
	for _, tc := range []struct {
		name string
		opts initOptions
	}{
		{"CPU", initOptions{Root: root, CPUs: 9, Set: map[string]bool{"root": true, "cpus": true}}},
		{"memory", initOptions{Root: root, MemoryMB: 16385, Set: map[string]bool{"root": true, "memory-mb": true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, err := initRun(tc.opts, bufio.NewReader(strings.NewReader("")), false); code == 0 || err == nil || !strings.Contains(err.Error(), "exceeds detected host capacity") {
				t.Fatalf("initRun = %d, %v; want host capacity rejection", code, err)
			}
		})
	}
}

func TestNonInteractiveHighResourceAllocationRequiresYes(t *testing.T) {
	root := initTestProject(t)
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 8, MemoryMB: 16384}, nil
	}
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources })
	options := initOptions{
		Root: root, CPUs: 8, MemoryMB: 4096,
		Set: map[string]bool{"root": true, "cpus": true, "memory-mb": true},
	}
	code, err := initRun(options, bufio.NewReader(strings.NewReader("")), false)
	if code == 0 || err == nil || !strings.Contains(err.Error(), "requires explicit confirmation") || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-interactive full-CPU setup = %d, %v; want explicit confirmation error", code, err)
	}
	if _, statErr := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(statErr) {
		t.Fatalf("configuration was written without confirmation (stat error: %v)", statErr)
	}
}

func TestInitKeepsImplicitDefaultsOnSingleCPUHost(t *testing.T) {
	root := initTestProject(t)
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 1, MemoryMB: 6000}, nil
	}
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources })
	opts := initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}}
	if code, err := initRun(opts, bufio.NewReader(strings.NewReader("")), false); code != 0 || err != nil {
		t.Fatalf("initRun = %d, %v", code, err)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CPUs != 0 || manifest.MemoryMB != 0 {
		t.Fatalf("single-choice defaults = %d CPUs / %d MiB, want both implicit", manifest.CPUs, manifest.MemoryMB)
	}
}

func TestInitAutoSelectsSingleCPUWithoutPrompting(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	writeManifest(t, root, justcode.ProjectManifest{Runtime: string(justcode.RuntimeMicrosandbox), Isolation: string(justcode.IsolationFull), CPUs: 2, MemoryMB: 4096})
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 1, MemoryMB: 6000}, nil
	}
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources })
	// GitHub, skills, no-skill confirmation, storage, MCPs, no-MCP
	// confirmation, apply. Resource questions are now advanced-only.
	input := strings.Join([]string{"n", "", "", "", "", "", "y"}, "\n") + "\n"
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Root: root, Replace: true, Set: map[string]bool{"root": true}}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "selected 1 CPU automatically") || strings.Contains(out, "cpus (`default` resets)") {
		t.Fatalf("the sole CPU value must be stated without prompting: %q", out)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CPUs != 0 || manifest.MemoryMB != 4096 {
		t.Fatalf("single CPU repair should reset only the CPU to its implicit default, got %d CPUs / %d MiB", manifest.CPUs, manifest.MemoryMB)
	}
}

func TestInitGitHubApprovalStaysOutOfProjectManifest(t *testing.T) {
	root := initTestProject(t)
	originalPreflight, originalApprove := githubInitPreflightFn, approveGitHubForProjectFn
	var preflightRoot, approvedRoot string
	githubInitPreflightFn = func(gotRoot string) (justcode.GitHubRemote, error) {
		preflightRoot = gotRoot
		return justcode.GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}, nil
	}
	approveGitHubForProjectFn = func(gotRoot string) error {
		approvedRoot = gotRoot
		return nil
	}
	t.Cleanup(func() {
		githubInitPreflightFn, approveGitHubForProjectFn = originalPreflight, originalApprove
	})
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{
			Root: root, GitHub: true, Yes: true,
			Set: map[string]bool{"root": true, "github": true},
		}, bufio.NewReader(strings.NewReader("")), false)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if preflightRoot != root || approvedRoot != canonicalRoot {
		t.Fatalf("preflight root = %q, approval root = %q, want %q and canonical %q", preflightRoot, approvedRoot, root, canonicalRoot)
	}
	if !strings.Contains(out, "guest workflow for owner/repo") || !strings.Contains(out, "approval is not stored in project files") {
		t.Fatalf("review must identify the repo and host-local approval: %q", out)
	}
	manifest, err := os.ReadFile(justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(manifest)), "github") {
		t.Fatalf("GitHub approval or origin leaked into project manifest: %s", manifest)
	}
}

func TestInitGitHubWorkflowRejectsNonMicrosandboxRuntime(t *testing.T) {
	root := initTestProject(t)
	originalPreflight, originalApprove := githubInitPreflightFn, approveGitHubForProjectFn
	githubInitPreflightFn = func(string) (justcode.GitHubRemote, error) {
		return justcode.GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}, nil
	}
	approved := false
	approveGitHubForProjectFn = func(string) error { approved = true; return nil }
	t.Cleanup(func() {
		githubInitPreflightFn, approveGitHubForProjectFn = originalPreflight, originalApprove
	})
	code, err := initRun(initOptions{
		Root: root, Runtime: "tart", GitHub: true, Yes: true,
		Set: map[string]bool{"root": true, "runtime": true, "github": true},
	}, bufio.NewReader(strings.NewReader("")), false)
	want := "requires the microsandbox runtime"
	if runtime.GOOS == "windows" {
		want = "tart is macOS only"
	}
	if code == 0 || err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("non-Microsandbox GitHub workflow: code=%d err=%v", code, err)
	}
	if approved {
		t.Fatal("GitHub approval must not be persisted after runtime validation fails")
	}
}

// TestInitSkipsGitHubQuestionWhenOriginUnusable pins #155: a project whose
// origin or credential cannot support the guest workflow is never offered
// the approval question, so the run can no longer fail after the last screen
// and throw the session away. The reason is stated on screen.
func TestInitSkipsGitHubQuestionWhenOriginUnusable(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	originalPreflight, originalApprove := githubInitPreflightFn, approveGitHubForProjectFn
	githubInitPreflightFn = func(string) (justcode.GitHubRemote, error) {
		return justcode.GitHubRemote{}, fmt.Errorf("the project has no readable origin remote; configure a GitHub.com origin to enable the guest GitHub workflow")
	}
	approved := false
	approveGitHubForProjectFn = func(string) error { approved = true; return nil }
	t.Cleanup(func() { githubInitPreflightFn, approveGitHubForProjectFn = originalPreflight, originalApprove })
	// runtime, isolation, model, cpus, memory, credential-ref, skills, MCPs,
	// skill storage, apply — the GitHub question is absent by design.
	input := strings.Join([]string{"", "", "", "", "", "", "", "", "", "", ""}, "\n") + "\n"
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Root: root, Replace: true, Set: map[string]bool{"root": true}}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if strings.Contains(out, "Approve the GitHub workflow") {
		t.Fatalf("the approval question must not be asked when the origin is unusable: %q", out)
	}
	if !strings.Contains(out, "GitHub guest workflow (optional) — unavailable for this project:") || !strings.Contains(out, "no readable origin remote") {
		t.Fatalf("the skip must state the reason on screen: %q", out)
	}
	if approved {
		t.Fatal("no approval may be recorded when the question is skipped")
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); err != nil {
		t.Fatalf("the project must still be configured: %v", err)
	}
}

// TestInitGitHubQuestionResolvesOriginUpfront pins the other half of #155:
// with a usable origin the question is offered, names the repo, and a Yes
// completes the run instead of failing after the review with the answers lost.
func TestInitGitHubQuestionResolvesOriginUpfront(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	originalPreflight, originalApprove := githubInitPreflightFn, approveGitHubForProjectFn
	var preflightRoots []string
	githubInitPreflightFn = func(gotRoot string) (justcode.GitHubRemote, error) {
		preflightRoots = append(preflightRoots, gotRoot)
		return justcode.GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}, nil
	}
	approveGitHubForProjectFn = func(string) error { return nil }
	t.Cleanup(func() { githubInitPreflightFn, approveGitHubForProjectFn = originalPreflight, originalApprove })
	// github=y, skills, no-skill confirmation, skill storage, MCPs, apply.
	// The expert settings
	// (runtime, isolation, model, resources, credential) left the default
	// path with #170: nothing answers them here.
	input := strings.Join([]string{"y", "", "", "", "", ""}, "\n") + "\n"
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Root: root, Replace: true, Set: map[string]bool{"root": true}}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("a Yes with a usable origin must complete the run: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Approve the GitHub workflow for this project? [y/N]: ") {
		t.Fatalf("the question must be asked: %q", out)
	}
	if !strings.Contains(out, "Origin: owner/repo") {
		t.Fatalf("the question must name the resolved origin: %q", out)
	}
	if !strings.Contains(out, "guest workflow for owner/repo") {
		t.Fatalf("the review must carry the resolved origin: %q", out)
	}
	if len(preflightRoots) < 2 {
		t.Fatalf("the origin must be resolved at question time and re-checked before writing, got %d calls", len(preflightRoots))
	}
}

// TestInitGitHubFlagFailsLoudlyWithoutOrigin pins the flag contract: an
// explicit --github is an error, never a silent skip.
func TestInitGitHubFlagFailsLoudlyWithoutOrigin(t *testing.T) {
	root := initTestProject(t)
	originalPreflight, originalApprove := githubInitPreflightFn, approveGitHubForProjectFn
	githubInitPreflightFn = func(string) (justcode.GitHubRemote, error) {
		return justcode.GitHubRemote{}, fmt.Errorf("the project has no readable origin remote; configure a GitHub.com origin to enable the guest GitHub workflow")
	}
	approveGitHubForProjectFn = func(string) error { return nil }
	t.Cleanup(func() { githubInitPreflightFn, approveGitHubForProjectFn = originalPreflight, originalApprove })
	code, err := initRun(initOptions{
		Root: root, GitHub: true, Yes: true,
		Set: map[string]bool{"root": true, "github": true},
	}, bufio.NewReader(strings.NewReader("")), false)
	if code == 0 || err == nil || !strings.Contains(err.Error(), "no readable origin remote") {
		t.Fatalf("--github without a usable origin must fail with the preflight error: code=%d err=%v", code, err)
	}
	if _, statErr := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(statErr) {
		t.Fatalf("a refused --github must leave nothing on disk (stat error: %v)", statErr)
	}
}

// TestAskInitQuestionsHuhOffersGitHubOnlyWhenUsable pins the huh form's half
// of #155: the approval field is built only when the preflight resolves, and
// a failed preflight forces the workflow off instead of letting the post-form
// check destroy the session. huhRun is stubbed, so this exercises the
// construction logic without a terminal.
func TestAskInitQuestionsHuhOffersGitHubOnlyWhenUsable(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	originalRun := huhRun
	huhRun = func(*huh.Form) error { return nil }
	t.Cleanup(func() { huhRun = originalRun })
	originalPreflight := githubInitPreflightFn
	t.Cleanup(func() { githubInitPreflightFn = originalPreflight })
	opts := initOptions{
		Root: root, Set: map[string]bool{"root": true},
		hostCPUs: 8, hostMemoryMB: 16384, maxCPUs: 8, maxMemoryMB: 16384,
	}

	githubInitPreflightFn = func(string) (justcode.GitHubRemote, error) {
		return justcode.GitHubRemote{}, fmt.Errorf("the project has no readable origin remote; configure a GitHub.com origin to enable the guest GitHub workflow")
	}
	answers, err := askInitQuestionsHuh(opts, justcode.InitAnswers{Root: root})
	if err != nil {
		t.Fatalf("askInitQuestionsHuh: %v", err)
	}
	if answers.GitHubWorkflow || answers.GitHubRemote.Repo != "" {
		t.Fatalf("an unusable origin must leave the workflow off with no stashed remote: %+v", answers)
	}

	githubInitPreflightFn = func(string) (justcode.GitHubRemote, error) {
		return justcode.GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}, nil
	}
	answers, err = askInitQuestionsHuh(opts, justcode.InitAnswers{Root: root})
	if err != nil {
		t.Fatalf("askInitQuestionsHuh: %v", err)
	}
	if answers.GitHubRemote.Repo != "owner/repo" {
		t.Fatalf("a usable origin must be stashed for the review, got %+v", answers)
	}
}

// huhRun is stubbed, so this pins the construction and write-back logic the
// form runs: a fresh initialization (no manifest) preselects the curated
// default exactly like the line wizard, and a manifest holding withdrawn ids
// (context7, playwright) survives the form instead of being silently wiped —
// huh rebuilds a MultiSelect's value from its options on submit, so the
// withdrawn ids must come back as retained options (#242 review).
func TestAskInitQuestionsHuhMCPSeedingAndWithdrawnRetention(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	originalRun := huhRun
	huhRun = func(*huh.Form) error { return nil }
	t.Cleanup(func() { huhRun = originalRun })
	opts := initOptions{
		Root: root, Set: map[string]bool{"root": true},
		hostCPUs: 8, hostMemoryMB: 16384, maxCPUs: 8, maxMemoryMB: 16384,
	}

	// Fresh project: the default selection is the seeded baseline, so an
	// accepted form submission keeps it implicit (MCPsSet false).
	answers, err := askInitQuestionsHuh(opts, justcode.InitAnswers{Root: root})
	if err != nil {
		t.Fatalf("askInitQuestionsHuh: %v", err)
	}
	if !equalStringSlices(answers.MCPConnectors, justcode.DefaultFreshMCPIDs) {
		t.Fatalf("fresh init must preselect %v, got %v", justcode.DefaultFreshMCPIDs, answers.MCPConnectors)
	}
	if answers.MCPsSet {
		t.Fatal("an accepted default must stay implicit (MCPsSet false)")
	}

	// Existing manifest with withdrawn ids: the selection survives the form
	// unchanged, and stays implicit so --replace preserves the manifest.
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root), justcode.ProjectManifest{
		MCPConnectors:   []string{"context7", "playwright"},
		SkillsLocalOnly: true,
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	answers, err = askInitQuestionsHuh(opts, justcode.InitAnswers{Root: root})
	if err != nil {
		t.Fatalf("askInitQuestionsHuh with manifest: %v", err)
	}
	if !equalStringSlices(answers.MCPConnectors, []string{"context7", "playwright"}) {
		t.Fatalf("withdrawn manifest ids must be retained through the form, got %v", answers.MCPConnectors)
	}
	if answers.MCPsSet {
		t.Fatal("an unchanged legacy selection must not be flagged as a new selection (MCPsSet false)")
	}

	// A manifest deliberately written with zero MCPs triggers the
	// zero-selection confirmation before any write-back. The main form's
	// stub must succeed once so the write-back runs, then the confirmation
	// form's stub fails: the wizard asks instead of silently keeping or
	// re-defaulting the empty set.
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root), justcode.ProjectManifest{
		MCPConnectors:   []string{},
		SkillsLocalOnly: true,
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	formCalls := 0
	huhRun = func(*huh.Form) error {
		formCalls++
		if formCalls > 1 {
			return huh.ErrUserAborted
		}
		return nil
	}
	if _, err = askInitQuestionsHuh(opts, justcode.InitAnswers{Root: root}); !errors.Is(err, huh.ErrUserAborted) {
		t.Fatalf("a zero-MCP manifest must trigger the confirmation and an abort must surface, got %v", err)
	}
}

// The zero-selection confirmation must read through the wizard's buffered
// reader: wrapping it in a fresh bufio.Reader prefetches the queued answers
// and the init aborted with "no answer" right after `none` (#242 review).
func TestPromptNoMCPConfirmationSharesWizardReader(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("none\ny\nroot\n"))
	if _, ok := promptLine(input, "mcps: "); !ok {
		t.Fatal("expected the mcps answer to be consumed first")
	}
	if !promptNoMCPConfirmation(input) {
		t.Fatal("expected the confirmation to accept the queued y")
	}
	next, ok := promptLine(input, "next: ")
	if !ok || strings.TrimSpace(next) != "root" {
		t.Fatalf("the answer queued behind the confirmation must survive, got %q (ok=%v)", next, ok)
	}
}

// TestInitNonTTYNamesWhatIsMissing pins the P12 acceptance item: with no
// terminal the command must never wait. It fails and says which input is
// missing, so a script can supply it next time.
func TestInitNonTTYNamesWhatIsMissing(t *testing.T) {
	root := initTestProject(t)
	// Run FROM the project so the "nothing was written" assertion checks the
	// directory a regressed run would actually write to, instead of the test
	// package directory inside the real checkout.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	// No --root: the one answer that cannot be defaulted from nothing.
	code, err := initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader("")), false)
	if code != 1 || err == nil {
		t.Fatalf("a non-TTY run without --root must fail: code=%d err=%v", code, err)
	}
	for _, want := range []string{"no terminal available", "--root", "--yes", "--isolation"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure must mention %q: %v", want, err)
		}
	}
	// And nothing was written.
	if _, err := os.Stat(filepath.Join(root, ".just-code")); !os.IsNotExist(err) {
		t.Fatalf("a failed run must not write anything (stat err = %v)", err)
	}
}

func TestSeedInitAnswersFromMissingManifestResetsPreviousProjectValues(t *testing.T) {
	root := initTestProject(t)
	previous := justcode.InitAnswers{
		Root: root, Runtime: justcode.RuntimeTart, Isolation: justcode.IsolationBackend,
		Model: "previous/model", CPUs: 4, MemoryMB: 8192, CredentialRef: "previous",
		GitHubWorkflow: true, GitHubRemote: justcode.GitHubRemote{Repo: "previous/repo"},
		MCPConnectors: []string{"context7"}, MCPsSet: true,
		Skills: []string{"official/rgaa"}, SkillsSet: true,
		SkillsLocalOnly: true, SkillsLocalOnlySet: true,
		BrowserResourceGuidance: "previous host sizing",
	}
	got := seedInitAnswersFromManifest(initOptions{Set: map[string]bool{}}, previous)
	if got.Runtime != "" || got.Isolation != "" || got.Model != "" || got.CPUs != 0 || got.MemoryMB != 0 || got.CredentialRef != "" ||
		got.GitHubWorkflow || got.GitHubRemote.Repo != "" || len(got.MCPConnectors) != 0 || got.MCPsSet ||
		len(got.Skills) != 0 || got.SkillsSet || got.SkillsLocalOnly || got.SkillsLocalOnlySet || got.BrowserResourceGuidance != "" {
		t.Fatalf("answers from previous project leaked into unconfigured root: %+v", got)
	}
}

// TestInitNonTTYWithAllInputsWritesTheManifest pins the scriptable path: every
// answer on the command line, no terminal, no prompt.
func TestInitNonTTYWithAllInputsWritesTheManifest(t *testing.T) {
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	root := initTestProject(t)
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{
			Root: root, Isolation: "backend", CPUs: 4, MemoryMB: 2048,
			Replace: true, Yes: true,
			MCPConnectors: []string{"chrome-devtools", "data-gouv"},
			Set:           map[string]bool{"root": true, "isolation": true, "cpus": true, "memory-mb": true, "mcps": true},
		}, bufio.NewReader(strings.NewReader("")), false)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Review the project setup") {
		t.Fatalf("the review must be printed even without a terminal: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatalf("the manifest must be written and readable: %v", err)
	}
	if pm.CPUs != 4 || pm.MemoryMB != 2048 || pm.Isolation != string(justcode.IsolationBackend) {
		t.Fatalf("manifest = %+v", pm)
	}
	if strings.Join(pm.MCPConnectors, ",") != "chrome-devtools,data-gouv" {
		t.Fatalf("MCP connectors = %v", pm.MCPConnectors)
	}
}

func TestParseInitMCPFlags(t *testing.T) {
	opts, err := parseInitArgs([]string{"--root", ".", "--mcp", "context7", "--mcp", "data-gouv"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Set["mcps"] || strings.Join(opts.MCPConnectors, ",") != "context7,data-gouv" {
		t.Fatalf("options = %+v", opts)
	}
	if _, err := parseInitArgs([]string{"--clear-mcps", "--mcp", "context7"}); err == nil {
		t.Fatal("--clear-mcps and --mcp must be mutually exclusive")
	}
}

func TestClearingContext7SelectionRevokesItsHostApproval(t *testing.T) {
	originalRevoke := revokeMCPBindingFn
	guestRevoked := false
	revokeMCPBindingFn = func(context.Context, string, justcode.CredentialKind) (bool, bool, error) {
		guestRevoked = true
		return false, false, nil
	}
	t.Cleanup(func() { revokeMCPBindingFn = originalRevoke })
	root := initTestProject(t)
	stateDir := stubMCPBindingStateDir(t)
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root), justcode.ProjectManifest{MCPConnectors: []string{"context7"}}); err != nil {
		t.Fatal(err)
	}
	project, err := justcode.DiscoverProject(root)
	if err != nil {
		t.Fatal(err)
	}
	instance := project.InstanceName()
	approvalPath := justcode.BindingApprovalsPath(stateDir, instance)
	if err := justcode.ApproveBinding(justcode.DefaultFS, approvalPath, justcode.CredentialContext7); err != nil {
		t.Fatal(err)
	}
	code, err := initRun(initOptions{
		Root: root, Replace: true, Yes: true,
		Set: map[string]bool{"root": true, "mcps": true, "clear-mcps": true},
	}, bufio.NewReader(strings.NewReader("")), false)
	if code != 0 || err != nil {
		t.Fatalf("initRun: code=%d err=%v", code, err)
	}
	approvals, err := justcode.ReadBindingApprovals(justcode.DefaultFS, approvalPath)
	if err != nil {
		t.Fatal(err)
	}
	if approvals.Approves(justcode.CredentialContext7) {
		t.Fatal("Context7 approval remained after the selected connector was removed")
	}
	if !guestRevoked {
		t.Fatal("guest Context7 binding was not revoked")
	}
}

func TestContext7RevocationFailureKeepsSelectionForRetry(t *testing.T) {
	originalRevoke := revokeMCPBindingFn
	callCount := 0
	revokeMCPBindingFn = func(context.Context, string, justcode.CredentialKind) (bool, bool, error) {
		callCount++
		if callCount == 1 {
			return false, false, fmt.Errorf("simulated guest revoke failure")
		}
		return true, true, nil
	}
	t.Cleanup(func() { revokeMCPBindingFn = originalRevoke })
	root := initTestProject(t)
	stateDir := stubMCPBindingStateDir(t)
	manifestPath := justcode.ProjectManifestPath(root)
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, manifestPath, justcode.ProjectManifest{MCPConnectors: []string{"context7"}}); err != nil {
		t.Fatal(err)
	}
	project, err := justcode.DiscoverProject(root)
	if err != nil {
		t.Fatal(err)
	}
	approvalPath := justcode.BindingApprovalsPath(stateDir, project.InstanceName())
	if err := justcode.ApproveBinding(justcode.DefaultFS, approvalPath, justcode.CredentialContext7); err != nil {
		t.Fatal(err)
	}
	clear := initOptions{
		Root: root, Replace: true, Yes: true,
		Set: map[string]bool{"root": true, "mcps": true, "clear-mcps": true},
	}
	if code, err := initRun(clear, bufio.NewReader(strings.NewReader("")), false); code == 0 || err == nil {
		t.Fatalf("failed revocation should fail init, got code=%d err=%v", code, err)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, manifestPath)
	if err != nil || !containsConnector(manifest.MCPConnectors, "context7") {
		t.Fatalf("selection after failed revocation = %v, %v; want context7 retained for retry", manifest.MCPConnectors, err)
	}
	if code, err := initRun(clear, bufio.NewReader(strings.NewReader("")), false); code != 0 || err != nil {
		t.Fatalf("retry init: code=%d err=%v", code, err)
	}
	manifest, err = justcode.ReadProjectManifest(justcode.DefaultFS, manifestPath)
	if err != nil || containsConnector(manifest.MCPConnectors, "context7") || callCount != 2 {
		t.Fatalf("selection after successful retry = %v, calls=%d, err=%v", manifest.MCPConnectors, callCount, err)
	}
}

// TestClearingContext7OnNonMicrosandboxRuntimeStillAttemptsGuestRevocation
// pins that deselection attempts the guest revoke even when the recorded
// runtime is not Microsandbox: the recorded runtime can lag a runtime switch,
// and a preserved sandbox from the previous runtime would otherwise keep its
// stale proxy registration. The revoke itself reports revoked=false for a
// nonexistent instance, so the attempt is safe.
func TestClearingContext7OnNonMicrosandboxRuntimeStillAttemptsGuestRevocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Tart is not supported on Windows")
	}
	originalRevoke := revokeMCPBindingFn
	revokeCalls := 0
	revokeMCPBindingFn = func(context.Context, string, justcode.CredentialKind) (bool, bool, error) {
		revokeCalls++
		return false, false, nil
	}
	t.Cleanup(func() { revokeMCPBindingFn = originalRevoke })
	root := initTestProject(t)
	stateDir := stubMCPBindingStateDir(t)
	manifestPath := justcode.ProjectManifestPath(root)
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, manifestPath, justcode.ProjectManifest{
		Runtime: string(justcode.RuntimeTart), MCPConnectors: []string{"context7"},
	}); err != nil {
		t.Fatal(err)
	}
	project, err := justcode.DiscoverProject(root)
	if err != nil {
		t.Fatal(err)
	}
	approvalPath := justcode.BindingApprovalsPath(stateDir, project.InstanceName())
	if err := justcode.ApproveBinding(justcode.DefaultFS, approvalPath, justcode.CredentialContext7); err != nil {
		t.Fatal(err)
	}
	code, err := initRun(initOptions{
		Root: root, Replace: true, Yes: true,
		Set: map[string]bool{"root": true, "mcps": true, "clear-mcps": true},
	}, bufio.NewReader(strings.NewReader("")), false)
	if code != 0 || err != nil || revokeCalls != 1 {
		t.Fatalf("non-Microsandbox deselection: code=%d err=%v revoke calls=%d", code, err, revokeCalls)
	}
	approvals, err := justcode.ReadBindingApprovals(justcode.DefaultFS, approvalPath)
	if err != nil || approvals.Approves(justcode.CredentialContext7) {
		t.Fatalf("Context7 approval after deselection = %+v, %v", approvals, err)
	}
}

// TestClearingContext7WhenControlPlaneUnreachableProceedsWithWarning pins
// the availability contract: a tart-only user with no reachable Microsandbox
// control plane must still be able to deselect the connector. The host
// approval revocation is the primary authorization, and the stale guest
// registration is inert without it, so an unverifiable lookup must warn and
// proceed rather than block the deselection forever.
func TestClearingContext7WhenControlPlaneUnreachableProceedsWithWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Tart is not supported on Windows")
	}
	originalRevoke := revokeMCPBindingFn
	revokeMCPBindingFn = func(context.Context, string, justcode.CredentialKind) (bool, bool, error) {
		return false, false, fmt.Errorf("%w: Microsandbox control plane unavailable", justcode.ErrLookupFailed)
	}
	t.Cleanup(func() { revokeMCPBindingFn = originalRevoke })
	root := initTestProject(t)
	stateDir := stubMCPBindingStateDir(t)
	manifestPath := justcode.ProjectManifestPath(root)
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, manifestPath, justcode.ProjectManifest{
		Runtime: string(justcode.RuntimeTart), MCPConnectors: []string{"context7"},
	}); err != nil {
		t.Fatal(err)
	}
	project, err := justcode.DiscoverProject(root)
	if err != nil {
		t.Fatal(err)
	}
	approvalPath := justcode.BindingApprovalsPath(stateDir, project.InstanceName())
	if err := justcode.ApproveBinding(justcode.DefaultFS, approvalPath, justcode.CredentialContext7); err != nil {
		t.Fatal(err)
	}
	code, err := initRun(initOptions{
		Root: root, Replace: true, Yes: true,
		Set: map[string]bool{"root": true, "mcps": true, "clear-mcps": true},
	}, bufio.NewReader(strings.NewReader("")), false)
	if code != 0 || err != nil {
		t.Fatalf("unreachable control plane must not block deselection: code=%d err=%v", code, err)
	}
	approvals, err := justcode.ReadBindingApprovals(justcode.DefaultFS, approvalPath)
	if err != nil || approvals.Approves(justcode.CredentialContext7) {
		t.Fatalf("Context7 approval after deselection = %+v, %v", approvals, err)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, manifestPath)
	if err != nil || containsConnector(manifest.MCPConnectors, "context7") {
		t.Fatalf("selection after deselection = %v, %v; want context7 removed", manifest.MCPConnectors, err)
	}
}

// TestInitInteractiveUsesDefaultsOnEmptyAnswers pins that pressing enter
// through the questions is a valid path: every question has a default, and the
// engine's defaults are what get written. Since #170 the expert settings
// (runtime, isolation, resources) are not part of this path at all: they live
// behind the review's "Réglages avancés" entry.
func TestInitInteractiveUsesDefaultsOnEmptyAnswers(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	input := root + "\n\n\n\n\n\n\n" // root, GitHub, skills, no-skill confirmation, storage, MCPs, apply
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if strings.Contains(out, "Sharing mode") || strings.Contains(out, "Guest resources") || strings.Contains(out, "Credential") {
		t.Fatalf("the expert settings must not be asked in the default path (#170): %q", out)
	}
	if !strings.Contains(out, "Apply? [Y/n/edit/advanced]: ") {
		t.Fatalf("the review must offer the advanced settings entry: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if pm.Isolation != string(justcode.IsolationFull) {
		t.Fatalf("an empty answer must take the default, got %q", pm.Isolation)
	}
	if pm.CPUs != 0 || pm.MemoryMB != 0 {
		t.Fatalf("the default sizing must be left unset in the manifest, got %d/%d", pm.CPUs, pm.MemoryMB)
	}
	if strings.Join(pm.MCPConnectors, ",") != "data-gouv" {
		t.Fatalf("a fresh project must default to the data-gouv MCP, got %v", pm.MCPConnectors)
	}
}

// TestInitReviewAdvancedSettingsApply pins the line path of #170: the review's
// "advanced" entry opens the expert settings (runtime, isolation, resources),
// the edited values land in the manifest, and the flow returns to the review.
func TestInitReviewAdvancedSettingsApply(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	stubInitSkillResolver(t)
	originalCatalogue := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return initSkillFixtureCatalogue(), nil
	}
	t.Cleanup(func() { projectSkillCatalogueFn = originalCatalogue })
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 8, MemoryMB: 16384}, nil
	}
	originalInstallHint := runtimeInstallHintFn
	runtimeInstallHintFn = func(justcode.Runtime) (bool, string) { return true, "" }
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources; runtimeInstallHintFn = originalInstallHint })
	input := strings.Join([]string{
		"n", "", "versioned", "chrome-devtools", // GitHub, skills, storage, MCPs
		"advanced",
		"microsandbox", "backend", "3", "2048", // runtime, isolation, cpus, memory
		"qwen3-coder", // main-model override
		"",            // apply
	}, "\n") + "\n"
	code, err := initRun(initOptions{Root: root, Set: map[string]bool{"root": true}}, bufio.NewReader(strings.NewReader(input)), true)
	if code != 0 || err != nil {
		t.Fatalf("initRun: code=%d err=%v", code, err)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime != "microsandbox" || manifest.Isolation != "backend" || manifest.CPUs != 3 || manifest.MemoryMB != 2048 || strings.Join(manifest.MCPConnectors, ",") != "chrome-devtools" {
		t.Fatalf("the advanced settings must reach the manifest: %+v", manifest)
	}
	if manifest.Model != "albert/qwen3-coder" || manifest.CredentialRef != "" {
		t.Fatalf("main model must be configurable; credential reference remains CLI-only: %+v", manifest)
	}
}

// TestInitAdvancedLineWarnsOnSharedFolderRuntime pins two #170 acceptance
// points on the line path: picking Tart or agent-vm displays the shared-folder
// consequence, and a runtime that is not installed is flagged with the command
// that installs it.
func TestInitAdvancedLineWarnsOnSharedFolderRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Tart is not a supported Windows runtime")
	}
	root := initTestProject(t)
	stubProjectSkills(t)
	originalInstallHint := runtimeInstallHintFn
	runtimeInstallHintFn = func(rt justcode.Runtime) (bool, string) {
		if rt == justcode.RuntimeTart {
			return false, "brew install openai/tools/tart"
		}
		return true, ""
	}
	t.Cleanup(func() { runtimeInstallHintFn = originalInstallHint })
	// root, GitHub, skills, MCPs, storage, review=advanced, runtime=tart,
	// isolation, cpus, memory, review=apply.
	input := root + "\n\n\n\n\n\nadvanced\ntart\n\n\n\n\n\n"
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "l'agent voit tous les fichiers du projet, y compris .env") {
		t.Fatalf("the shared-folder consequence must be displayed: %q", out)
	}
	if !strings.Contains(out, "n'est pas installé") || !strings.Contains(out, "brew install openai/tools/tart") {
		t.Fatalf("an uninstalled runtime must name its install command: %q", out)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime != "tart" {
		t.Fatalf("runtime = %q, want tart", manifest.Runtime)
	}
}

// TestInitNonTTYAcceptsFreshDefaultMCPWithoutPrompt pins that supplying --mcp
// data-gouv keeps the scriptable path at the original answer count: the MCP
// question is skipped, so the remaining prompts line up without an extra
// newline.
func TestInitNonTTYAcceptsFreshDefaultMCPWithoutPrompt(t *testing.T) {
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	root := initTestProject(t)
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{
			Root: root, Isolation: "backend", CPUs: 4, MemoryMB: 2048,
			Replace: true, Yes: true,
			MCPConnectors: []string{"data-gouv"},
			Set:           map[string]bool{"root": true, "isolation": true, "cpus": true, "memory-mb": true, "mcps": true},
		}, bufio.NewReader(strings.NewReader("")), false)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Review the project setup") {
		t.Fatalf("the review must be printed even without a terminal: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatalf("the manifest must be written and readable: %v", err)
	}
	if strings.Join(pm.MCPConnectors, ",") != "data-gouv" {
		t.Fatalf("MCP connectors = %v", pm.MCPConnectors)
	}
}

// TestInitRefusesWithdrawnMCPFlagValue pins that a withdrawn connector passed
// on the command line is rejected before any question runs.
func TestInitRefusesWithdrawnMCPFlagValue(t *testing.T) {
	root := initTestProject(t)
	code, err := initRun(initOptions{
		Root:          root,
		MCPConnectors: []string{"context7"},
		Set:           map[string]bool{"root": true, "mcps": true},
	}, bufio.NewReader(strings.NewReader("")), false)
	if code != 2 || err == nil {
		t.Fatalf("initRun with a withdrawn --mcp value: code=%d err=%v, want refusal before the first prompt", code, err)
	}
}

func TestBrowserResourceGuidanceRefreshesAfterResourceChanges(t *testing.T) {
	originalHostResources := detectHostResourcesFn
	detectHostResourcesFn = func() (justcode.HostResources, error) {
		return justcode.HostResources{CPUs: 8, MemoryMB: 16384}, nil
	}
	t.Cleanup(func() { detectHostResourcesFn = originalHostResources })

	answers := justcode.InitAnswers{
		MCPConnectors:           []string{"chrome-devtools"},
		CPUs:                    2,
		MemoryMB:                4096,
		BrowserResourceGuidance: "stale guidance for 2 vCPU and 4096 MiB RAM",
	}
	updated := withBrowserResourceGuidance(answers)
	if strings.Contains(updated.BrowserResourceGuidance, "stale guidance") {
		t.Fatalf("stale guidance retained: %q", updated.BrowserResourceGuidance)
	}
	if !strings.Contains(updated.BrowserResourceGuidance, "2 vCPU") || !strings.Contains(updated.BrowserResourceGuidance, "4096 MiB RAM") {
		t.Fatalf("guidance does not match current resources: %q", updated.BrowserResourceGuidance)
	}

	answers.CPUs = 4
	answers.MemoryMB = 8192
	updated = withBrowserResourceGuidance(answers)
	if !strings.Contains(updated.BrowserResourceGuidance, "4 vCPU") || !strings.Contains(updated.BrowserResourceGuidance, "8192 MiB RAM") {
		t.Fatalf("guidance was not refreshed for edited resources: %q", updated.BrowserResourceGuidance)
	}
}

func TestInitSkillPromptShowsSixChoicesAndConfirmsEmptySelection(t *testing.T) {
	root := initTestProject(t)
	originalCatalogue := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return []justcode.ProjectSkill{
			{ID: "official/securite-developpement"},
			{ID: "official/anssi-guides"},
			{ID: "official/rgaa"},
			{ID: "official/react-dsfr"},
			{ID: "official/lasuite-ui-kit"},
			{ID: "official/datagouv-apis"},
			{ID: "experimental/example", Name: "example", Description: "fixture", Experimental: true},
			{ID: "experimental/un-identifiant-vraiment-tres-long", Name: "long", Experimental: true,
				Description: "Rechercher sémantiquement dans une base de connaissances indexée — description volontairement très longue et accentuée pour dépasser quatre-vingts colonnes."},
		}, nil
	}
	t.Cleanup(func() { projectSkillCatalogueFn = originalCatalogue })
	answers := justcode.InitAnswers{Root: root}
	set := map[string]bool{
		"root": true, "runtime": true, "isolation": true, "model": true,
		"cpus": true, "memory-mb": true, "credential-ref": true, "github": true,
		"mcps": true,
	}
	input := "none\nn\nrgaa\nversioned\n"
	var got justcode.InitAnswers
	out := captureStdout(t, func() {
		var err error
		got, err = askInitQuestions(bufio.NewReader(strings.NewReader(input)), initOptions{Set: set}, answers)
		if err != nil {
			t.Fatalf("askInitQuestions: %v", err)
		}
	})
	if !got.SkillsSet || len(got.Skills) != 1 || got.Skills[0] != "official/rgaa" {
		t.Fatalf("selected skills = %+v", got)
	}
	for _, want := range []string{
		"Skills (facultatif)", "[x] securite-developpement", "[ ] anssi-guides",
		"[ ] rgaa", "[ ] react-dsfr", "[ ] lasuite-ui-kit", "[ ] datagouv-apis",
		"Aucune skill sélectionnée. Continuer sans skill ? (O/n)",
		"Où enregistrer les skills ?", "règles de sécurité de l'État, issues des guides ANSSI",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("skills flow missing %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"experimental/presentation-docs", "experimental/example", "usage-ia-agents-etat"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("skills screen must not offer %q: %s", unwanted, out)
		}
	}
}

func TestInitLineWizardDefaultsSecuritySkillForNewProject(t *testing.T) {
	root := initTestProject(t)
	originalCatalogue := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return initSkillFixtureCatalogue(), nil
	}
	t.Cleanup(func() { projectSkillCatalogueFn = originalCatalogue })
	set := map[string]bool{
		"root": true, "runtime": true, "isolation": true, "model": true,
		"cpus": true, "memory-mb": true, "credential-ref": true, "github": true,
		"mcps": true, "skills-storage": true,
	}
	var got justcode.InitAnswers
	out := captureStdout(t, func() {
		var err error
		got, err = askInitQuestions(bufio.NewReader(strings.NewReader("\n")), initOptions{Set: set}, justcode.InitAnswers{Root: root})
		if err != nil {
			t.Fatalf("askInitQuestions: %v", err)
		}
	})
	if !got.SkillsSet || !equalStringSlices(got.Skills, []string{"official/securite-developpement"}) {
		t.Fatalf("new-project default selection = %+v", got)
	}
	if !strings.Contains(out, "[x] securite-developpement") || !strings.Contains(out, "1 skill sélectionnée.") {
		t.Fatalf("default skill and count not shown: %s", out)
	}
}

func TestInitLineSkillPromptRetriesTyposAndRetainsOlderPins(t *testing.T) {
	root := initTestProject(t)
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root),
		justcode.ProjectManifest{Skills: []string{"experimental/old-tool"}}); err != nil {
		t.Fatal(err)
	}
	originalCatalogue := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return initSkillFixtureCatalogue(), nil
	}
	t.Cleanup(func() { projectSkillCatalogueFn = originalCatalogue })
	set := map[string]bool{
		"root": true, "runtime": true, "isolation": true, "model": true,
		"cpus": true, "memory-mb": true, "credential-ref": true, "github": true,
		"mcps": true, "skills-storage": true,
	}
	var got justcode.InitAnswers
	out := captureStdout(t, func() {
		var err error
		got, err = askInitQuestions(bufio.NewReader(strings.NewReader("not-a-skill\nrgaa\n")), initOptions{Set: set}, justcode.InitAnswers{Root: root})
		if err != nil {
			t.Fatalf("askInitQuestions: %v", err)
		}
	})
	if !equalStringSlices(got.Skills, []string{"official/rgaa", "experimental/old-tool"}) {
		t.Fatalf("replacement selection = %v", got.Skills)
	}
	if !equalStringSlices(got.RetainedSkills, []string{"experimental/old-tool"}) {
		t.Fatalf("retained skills = %v, want the out-of-catalogue pin", got.RetainedSkills)
	}
	for _, want := range []string{"unknown skill \"not-a-skill\"", "experimental/old-tool", "conservées si vous saisissez de nouveaux noms"} {
		if !strings.Contains(out, want) {
			t.Errorf("retry or retention message missing %q: %s", want, out)
		}
	}
}

func TestInitLineSkillPromptSkipsUnavailableCatalogue(t *testing.T) {
	root := initTestProject(t)
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root),
		justcode.ProjectManifest{Skills: []string{"official/rgaa"}}); err != nil {
		t.Fatal(err)
	}
	originalCatalogue := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return nil, errors.New("offline")
	}
	t.Cleanup(func() { projectSkillCatalogueFn = originalCatalogue })
	set := map[string]bool{
		"root": true, "runtime": true, "isolation": true, "model": true,
		"cpus": true, "memory-mb": true, "credential-ref": true, "github": true,
		"mcps": true,
	}
	var got justcode.InitAnswers
	out := captureStdout(t, func() {
		var err error
		got, err = askInitQuestions(bufio.NewReader(strings.NewReader("versioned\n")), initOptions{Set: set}, justcode.InitAnswers{Root: root})
		if err != nil {
			t.Fatalf("askInitQuestions: %v", err)
		}
	})
	if got.SkillsSet || !equalStringSlices(got.Skills, []string{"official/rgaa"}) {
		t.Fatalf("unavailable catalogue changed the current skills: %+v", got)
	}
	if !strings.Contains(out, "La sélection actuelle est conservée") || strings.Contains(out, "[ ] securite-developpement") {
		t.Fatalf("unavailable catalogue should skip skill choices: %s", out)
	}
}

func TestConfirmNoInitSkillsRequiresAnAnswer(t *testing.T) {
	if proceed, err := confirmNoInitSkills(bufio.NewReader(strings.NewReader("\n"))); err != nil || !proceed {
		t.Fatalf("empty answer should accept the default yes: proceed=%v err=%v", proceed, err)
	}
	if proceed, err := confirmNoInitSkills(bufio.NewReader(strings.NewReader("n\n"))); err != nil || proceed {
		t.Fatalf("no answer should return to selection: proceed=%v err=%v", proceed, err)
	}
	if proceed, err := confirmNoInitSkills(bufio.NewReader(strings.NewReader(""))); err == nil || proceed {
		t.Fatalf("EOF must not count as confirmation: proceed=%v err=%v", proceed, err)
	}
}

func initSkillFixtureCatalogue() []justcode.ProjectSkill {
	catalogue := make([]justcode.ProjectSkill, 0, len(initSkillOrder))
	for _, id := range initSkillOrder {
		catalogue = append(catalogue, justcode.ProjectSkill{ID: id})
	}
	return catalogue
}

func TestPrintProjectSkillsFitsEightyColumns(t *testing.T) {
	entries := []justcode.ProjectSkill{
		{ID: "official/short", Description: "courte"},
		{ID: "experimental/un-identifiant-vraiment-tres-long", Experimental: true,
			Description: "Rechercher sémantiquement dans une base de connaissances indexée\net même sur plusieurs lignes — description volontairement très longue et accentuée pour dépasser quatre-vingts colonnes."},
		{ID: "wide/cjk", Description: strings.Repeat("é", 60)},
		{ID: "wide/cjk", Description: strings.Repeat("宽", 60)},
	}
	out := captureStdout(t, func() { printProjectSkills(entries) })
	if !utf8.ValidString(out) {
		t.Fatalf("truncated catalogue output must stay valid UTF-8: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(entries) {
		t.Fatalf("one line per entry, got %d for %d entries: %q", len(lines), len(entries), out)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 78 {
			t.Fatalf("catalogue line exceeds the 80-column terminal: %q", line)
		}
	}
}

func TestInitSeedsUnspecifiedAnswersFromExistingManifest(t *testing.T) {
	root := initTestProject(t)
	wizard := justcode.InitWizard{}
	plan, err := wizard.Plan(justcode.InitAnswers{
		Root: root, Runtime: justcode.RuntimeMicrosandbox, Isolation: justcode.IsolationBackend,
		Model: "test-model", CPUs: 3, MemoryMB: 2048, CredentialRef: "work",
		MCPConnectors: []string{"data-gouv"}, MCPsSet: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := wizard.Apply(plan, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := seedInitAnswersFromManifest(initOptions{Set: map[string]bool{}}, justcode.InitAnswers{Root: root})
	if got.Runtime != justcode.RuntimeMicrosandbox || got.Isolation != justcode.IsolationBackend || got.Model != "test-model" || got.CPUs != 3 || got.MemoryMB != 2048 || got.CredentialRef != "work" || strings.Join(got.MCPConnectors, ",") != "data-gouv" {
		t.Fatalf("seeded answers = %+v", got)
	}
}

// TestInitRefusesToReplaceWithoutTheFlag pins the guard at the CLI level: the
// engine refuses too, but the message has to reach the user before anything is
// written.
func TestInitRefusesToReplaceWithoutTheFlag(t *testing.T) {
	root := initTestProject(t)
	if _, err := (justcode.InitWizard{}).Plan(justcode.InitAnswers{Root: root}); err != nil {
		t.Fatal(err)
	}
	// First run creates the manifest.
	if _, err := initRun(initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}},
		bufio.NewReader(strings.NewReader("")), false); err != nil {
		t.Fatalf("first init: %v", err)
	}
	// Second run must refuse without --replace.
	var err error
	var code int
	_ = captureStdout(t, func() {
		code, err = initRun(initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}},
			bufio.NewReader(strings.NewReader("")), false)
	})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("a second run must refuse without --replace: code=%d err=%v", code, err)
	}
	// With --replace it goes through.
	if _, err := initRun(initOptions{Root: root, Yes: true, Replace: true, Set: map[string]bool{"root": true}},
		bufio.NewReader(strings.NewReader("")), false); err != nil {
		t.Fatalf("init --replace: %v", err)
	}
}

// TestInitHelpNeedsNoTerminal pins that the help path is answered without a
// prompt, since it is the one thing a user reaches for when the questions are
// unclear.
func TestInitHelpNeedsNoTerminal(t *testing.T) {
	out := captureStdout(t, func() {
		if code, err := initCmd([]string{"--help"}); code != 0 || err != nil {
			t.Fatalf("init --help: code=%d err=%v", code, err)
		}
	})
	for _, want := range []string{"--root", "--isolation", "--replace", "no terminal"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help must document %q: %q", want, out)
		}
	}
}

// TestIsTTYTreatsDevNullAsNonInteractive pins the script idiom: /dev/null is a
// character device, so a naive check calls it a terminal, and
// `init --yes < /dev/null` would then silently configure whatever directory
// the script happened to run from.
func TestIsTTYTreatsDevNullAsNonInteractive(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()
	if isTTY() {
		t.Fatalf("%s must not be reported as a terminal", os.DevNull)
	}
}

// TestInitSkipsQuestionsAnsweredByFlags pins that the flag surface and the
// prompts are one coherent path: a supplied flag is not re-asked, and the
// displayed default is the value that will actually be used.
func TestInitSkipsQuestionsAnsweredByFlags(t *testing.T) {
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	root := initTestProject(t)
	input := "\n\n\n" // GitHub defaults to off, then MCPs and apply confirmation
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{
			Root: root, Runtime: "microsandbox", Isolation: "backend", Model: "albert/x",
			CPUs: 4, MemoryMB: 2048, CredentialRef: "work",
			Set: map[string]bool{"root": true, "runtime": true, "isolation": true, "model": true, "cpus": true, "memory-mb": true, "credential-ref": true, "skills": true, "skills-storage": true},
		}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	for _, asked := range []string{"root:", "runtime [", "isolation [", "model:", "cpus:", "memory MiB or G:", "reference:"} {
		if strings.Contains(out, asked) {
			t.Fatalf("a question already answered by a flag must not be asked (%q): %q", asked, out)
		}
	}
	if !strings.Contains(out, "Apply?") {
		t.Fatalf("the confirmation must still be asked: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	// The flag values are what got written: the prompt must not have been able
	// to override them, and no engine default may have leaked in.
	if pm.Isolation != "backend" || pm.CPUs != 4 || pm.MemoryMB != 2048 || pm.CredentialRef != "work" {
		t.Fatalf("manifest = %+v", pm)
	}
}

// TestInitValidatesFlagsBeforeAsking pins that a bad flag value is reported
// immediately: making someone answer six prompts to learn about a typo is a
// needless cost.
func TestInitValidatesFlagsBeforeAsking(t *testing.T) {
	root := initTestProject(t)
	for _, tc := range []struct {
		name string
		opts initOptions
		want string
	}{
		{"bad runtime", initOptions{Root: root, Runtime: "podman", Set: map[string]bool{"root": true, "runtime": true}}, "microsandbox"},
		{"bad isolation", initOptions{Root: root, Isolation: "sometimes", Set: map[string]bool{"root": true, "isolation": true}}, "isolation"},
		{"zero cpus", initOptions{Root: root, CPUs: 0, Set: map[string]bool{"root": true, "cpus": true}}, "--cpus must be 1"},
		{"huge memory", initOptions{Root: root, MemoryMB: justcode.MaxSandboxMemoryMB + 1, Set: map[string]bool{"root": true, "memory-mb": true}}, "--memory-mb must be 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			_ = captureStdout(t, func() {
				_, err = initRun(tc.opts, bufio.NewReader(strings.NewReader("")), true)
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestParseInitArgsRejectsAFlagAsValue pins that `--root --yes` is a missing
// value, not a directory named "--yes".
func TestParseInitArgsRejectsAFlagAsValue(t *testing.T) {
	_, err := parseInitArgs([]string{"--root", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Fatalf("error = %v, want a missing-value error", err)
	}
}

// TestInitStopsOnAModelTheCatalogueDoesNotList pins both halves of the model
// rule at the CLI level: a listing that does not contain the model stops the
// setup (the answer must change), while an unreachable catalogue only warns.
func TestInitStopsOnAModelTheCatalogueDoesNotList(t *testing.T) {
	root := initTestProject(t)
	stubCatalogueCheck(t, func(string, string) (string, error) {
		return "", fmt.Errorf("the model %q is not in the Albert catalogue", "albert/gone")
	})
	var err error
	_ = captureStdout(t, func() {
		_, err = initRun(initOptions{Root: root, Model: "albert/gone", Yes: true, Set: map[string]bool{"root": true, "model": true}},
			bufio.NewReader(strings.NewReader("")), false)
	})
	if err == nil || !strings.Contains(err.Error(), "catalogue") {
		t.Fatalf("a model the catalogue does not list must stop the setup: %v", err)
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("a refused model must leave nothing on disk (stat err = %v)", err)
	}

	// Unreachable catalogue: a warning, and the setup proceeds.
	stubCatalogueCheck(t, func(string, string) (string, error) {
		return "the model was recorded unverified: the catalogue could not be reached", nil
	})
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Root: root, Model: "albert/deepseek-v4-flash", Yes: true, Set: map[string]bool{"root": true, "model": true}},
			bufio.NewReader(strings.NewReader("")), false)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "unverified") {
		t.Fatalf("the warning must reach the user: %q", out)
	}
}

// TestInitRejectsATypedZero pins parity with the flag in the advanced
// settings screen (#170): an empty answer means "use the default", so a typed
// 0 is an out-of-range value — and the screen re-asks instead of aborting,
// so a typo never loses the answers already given.
func TestInitRejectsATypedZero(t *testing.T) {
	root := initTestProject(t)
	stubProjectSkills(t)
	originalInstallHint := runtimeInstallHintFn
	runtimeInstallHintFn = func(justcode.Runtime) (bool, string) { return true, "" }
	t.Cleanup(func() { runtimeInstallHintFn = originalInstallHint })
	// root, GitHub, skills, MCPs, storage, review=advanced, runtime,
	// isolation, cpus(0) then a kept default, memory, review=cancel.
	input := root + "\n\n\n\n\n\nadvanced\n\n\n0\n\n\n\nn\n"
	out := captureStdout(t, func() {
		_, _ = initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader(input)), true)
	})
	if !strings.Contains(out, "Au moins 1 processeur.") {
		t.Fatalf("a typed 0 must be rejected in the advanced screen without leaving it: %q", out)
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("the cancelled run must leave nothing on disk (stat err = %v)", err)
	}
}

// TestOfferProjectInitSkipsWhenConfigured pins the first rule: a project that
// already has a configuration is never asked about it, so a normal launch
// takes no new step.
func TestOfferProjectInitSkipsWhenConfigured(t *testing.T) {
	root := initTestProject(t)
	if _, err := (justcode.InitWizard{}).Plan(justcode.InitAnswers{Root: root}); err != nil {
		t.Fatal(err)
	}
	if code, err := initRun(initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}},
		bufio.NewReader(strings.NewReader("")), false); err != nil || code != 0 {
		t.Fatalf("precondition: init must write a manifest: code=%d err=%v", code, err)
	}
	configured, code, err := offerProjectInit(root, parsedArgs{action: "attach"})
	if !configured || code != 0 || err != nil {
		t.Fatalf("a configured project must proceed: configured=%v code=%d err=%v", configured, code, err)
	}
}

func clearLaunchChoiceEnv(t *testing.T) {
	t.Helper()
	// The launch offer correctly treats exported runtime choices as supplied
	// answers. These tests exercise the no-env path, so isolate them from a
	// developer shell that may export RUNTIME or ISOLATION.
	t.Setenv("RUNTIME", "")
	t.Setenv("ISOLATION", "")
}

// TestOfferProjectInitWithNoTerminalNamesTheCommand pins the non-TTY contract
// on the launch path: nothing waits, and the failure says exactly what to run.
func TestOfferProjectInitWithNoTerminalNamesTheCommand(t *testing.T) {
	clearLaunchChoiceEnv(t)
	root := initTestProject(t)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()

	configured, code, err := offerProjectInit(root, parsedArgs{action: "attach"})
	if configured {
		t.Fatal("an unconfigured project must not proceed silently without a terminal")
	}
	if code != 1 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	for _, want := range []string{"--root", "--yes", justcode.ProjectManifestPath(root)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure must mention %q: %v", want, err)
		}
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written without an answer (stat err = %v)", err)
	}
}

// TestOfferProjectInitRespectsExplicitChoices pins that a launch already
// carrying its own runtime or isolation flags is not asked to configure a
// project: those flags are the answer the offer would collect.
func TestOfferProjectInitRespectsExplicitChoices(t *testing.T) {
	clearLaunchChoiceEnv(t)
	root := initTestProject(t)
	for _, parsed := range []parsedArgs{{action: "start", runtime: "--tart"}, {action: "start", isolation: "backend"}} {
		configured, _, err := offerProjectInit(root, parsed)
		if !configured || err != nil {
			t.Fatalf("an explicit choice must skip the offer: configured=%v err=%v", configured, err)
		}
	}
}

// TestOfferProjectInitAppliesWhatItWrote pins that accepting the offer is not
// cosmetic: the manifest written by the offer is what the launch then applies.
func TestOfferProjectInitAppliesWhatItWrote(t *testing.T) {
	clearLaunchChoiceEnv(t)
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	stubProjectSkills(t)
	root := initTestProject(t)
	// confirm, GitHub, skills, no-skill confirmation, storage, MCPs, review=advanced, runtime,
	// isolation(backend), cpus, memory, review=apply. The expert settings
	// live behind the review's "advanced" entry since #170.
	input := "\n\n\n\n\n\nadvanced\n\nbackend\n\n\n\n\n"
	devNullLike := bufio.NewReader(strings.NewReader(input))

	// Drive the offer with the same answers a user would give.
	var configured bool
	var err error
	_ = captureStdout(t, func() {
		configured, _, err = offerProjectInitWithReader(root, parsedArgs{action: "attach"}, devNullLike, true)
	})
	if err != nil || !configured {
		t.Fatalf("the offer must go through: configured=%v err=%v", configured, err)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatalf("the offer must write the manifest: %v", err)
	}
	if pm.Isolation != "backend" {
		t.Fatalf("isolation = %q, want the answer given to the offer", pm.Isolation)
	}
	runtimeChoice, isolationChoice := projectRuntimeIsolation(root)
	if runtimeChoice != justcode.RuntimeMicrosandbox || isolationChoice != justcode.IsolationBackend {
		t.Fatalf("the launch must resolve what the offer wrote: %q / %q", runtimeChoice, isolationChoice)
	}
}

// TestOfferProjectInitSkipsWhenTheEnvironmentDecides pins that a choice made
// through the documented environment variables counts as an answer: a CI run
// exporting RUNTIME or ISOLATION has no terminal to be asked with, and asking
// anyway would turn a working launch into a failure.
func TestOfferProjectInitSkipsWhenTheEnvironmentDecides(t *testing.T) {
	for _, env := range []string{"RUNTIME", "ISOLATION"} {
		t.Run(env, func(t *testing.T) {
			root := initTestProject(t)
			t.Setenv(env, "microsandbox")
			if env == "ISOLATION" {
				t.Setenv(env, "backend")
			}
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Skipf("cannot open %s: %v", os.DevNull, err)
			}
			defer func() { _ = devNull.Close() }()
			orig := os.Stdin
			os.Stdin = devNull
			defer func() { os.Stdin = orig }()

			configured, code, err := offerProjectInit(root, parsedArgs{action: "start"})
			if !configured || code != 0 || err != nil {
				t.Fatalf("an environment-carried choice must skip the offer: configured=%v code=%d err=%v", configured, code, err)
			}
		})
	}
	// An empty exported value selects nothing, so it is not a choice.
	root := initTestProject(t)
	t.Setenv("RUNTIME", "")
	t.Setenv("ISOLATION", "")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()
	if configured, _, _ := offerProjectInit(root, parsedArgs{action: "start"}); configured {
		t.Fatal("an empty exported value must not silently stand in for a configuration")
	}
}

// TestOfferProjectInitWarnsOnABrokenManifest pins that a manifest the launch
// cannot read is not passed over in silence: the launch readers tolerate the
// error, so this is the only place holding it.
func TestOfferProjectInitWarnsOnABrokenManifest(t *testing.T) {
	root := initTestProject(t)
	if err := os.MkdirAll(filepath.Dir(justcode.ProjectManifestPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	// A secret-looking field is rejected by the reader, like a broken one.
	if err := os.WriteFile(justcode.ProjectManifestPath(root), []byte(`{"schemaVersion":1,"apiKey":"leak"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var configured bool
	var err error
	stderr := captureStderr(t, func() {
		configured, _, err = offerProjectInit(root, parsedArgs{action: "attach"})
	})
	if err != nil || !configured {
		t.Fatalf("a broken manifest must not block the launch: configured=%v err=%v", configured, err)
	}
	if !strings.Contains(stderr, "cannot be read") || !strings.Contains(stderr, "--replace") {
		t.Fatalf("the user must be told the manifest is ignored and how to fix it: %q", stderr)
	}
	// And the broken file is left alone: the offer must not replace it.
	raw, readErr := os.ReadFile(justcode.ProjectManifestPath(root))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(raw), "leak") {
		t.Fatalf("the offer must not rewrite the manifest: %s", raw)
	}
}

// TestIsLaunchActionPinsTheGuestBuildingSet keeps the one action set the launch
// flow shares from drifting: the offer, the malformed-config gate and the guest
// sizing gate all key off it, and a read-only action must never be gated on a
// configuration the user may have come to inspect because it is broken.
func TestIsLaunchActionPinsTheGuestBuildingSet(t *testing.T) {
	for _, action := range []string{"attach", "start", "restart", "recreate"} {
		if !isLaunchAction(action) {
			t.Fatalf("%q builds a guest and must be a launch action", action)
		}
	}
	for _, action := range []string{"stop", "logs", "shell", "check", "doctor", "clean", "help", "version", "config", "auth", "bindings", "workspace", "init", "trust", "models"} {
		if isLaunchAction(action) {
			t.Fatalf("%q does not build a guest and must not be gated on the project configuration", action)
		}
	}
}
