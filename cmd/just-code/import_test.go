package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

func TestParseAlbertCodeImportArgs(t *testing.T) {
	opts, err := parseAlbertCodeImportArgs([]string{"albert-code", "--root", "/tmp/project", "--apply", "--import-albert-key"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.root != "/tmp/project" || !opts.apply || !opts.importAlbertKey {
		t.Fatalf("parsed options = %+v", opts)
	}
	for _, args := range [][]string{
		{}, {"opencode"}, {"albert-code", "--import-albert-key"}, {"albert-code", "--apply", "--yes"}, {"albert-code", "--root"},
	} {
		if _, err := parseAlbertCodeImportArgs(args); err == nil {
			t.Errorf("parseAlbertCodeImportArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestParseArgsRoutesImportCommandBeforeConfigLoading(t *testing.T) {
	parsed, err := parseArgs([]string{"import", "albert-code", "--root", "/tmp/project", "--apply"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.action != "import" || !sameStrings(parsed.importArgs, []string{"albert-code", "--root", "/tmp/project", "--apply"}) {
		t.Fatalf("parsed command = %+v", parsed)
	}
}

func TestStoreImportedAlbertCredentialDoesNotOverwrite(t *testing.T) {
	stubCredentialGenerationBump(t)
	store := &importTestCredentialStore{}
	created, err := storeImportedAlbertCredential(context.Background(), store, "key-one")
	if err != nil || !created || store.value != "key-one" {
		t.Fatalf("first store: created=%v value=%q err=%v", created, store.value, err)
	}
	created, err = storeImportedAlbertCredential(context.Background(), store, "key-one")
	if err != nil || created {
		t.Fatalf("idempotent store: created=%v err=%v", created, err)
	}
	if _, err := storeImportedAlbertCredential(context.Background(), store, "key-two"); err == nil {
		t.Fatal("a different existing Albert credential was overwritten")
	}
	if store.value != "key-one" {
		t.Fatal("refused credential import modified the existing store value")
	}
}

func TestRollbackImportedCredentialPreservesReplacement(t *testing.T) {
	store := &importTestCredentialStore{value: "replacement-key", set: true}
	err := rollbackImportedCredential(context.Background(), store, true, "imported-key")
	if err == nil || !strings.Contains(err.Error(), "changed during import") {
		t.Fatalf("rollback of a replaced credential returned %v", err)
	}
	if store.value != "replacement-key" || !store.set {
		t.Fatal("rollback removed a credential that replaced the imported value")
	}
}

func TestAlbertCodeImportCredentialRollbackAfterApplyFailure(t *testing.T) {
	for _, failRemove := range []bool{false, true} {
		name := "remove-succeeds"
		if failRemove {
			name = "remove-fails"
		}
		t.Run(name, func(t *testing.T) {
			stubCredentialGenerationBump(t)
			root := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ALBERT_API_KEY=rollback-test-key\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			store := &importTestCredentialStore{}
			removeErr := errors.New("injected remove failure")
			if failRemove {
				store.removeErr = removeErr
			}
			originalStore, originalApply := importCredentialStoreFn, applyImportedProjectFn
			importCredentialStoreFn = func() justcode.CredentialStore { return store }
			applyImportedProjectFn = func(justcode.InitWizard, justcode.InitPlan) error { return errors.New("injected apply failure") }
			t.Cleanup(func() {
				importCredentialStoreFn, applyImportedProjectFn = originalStore, originalApply
			})
			code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply", "--import-albert-key"})
			if code != 1 || err == nil || !strings.Contains(err.Error(), "injected apply failure") {
				t.Fatalf("apply failure result: code=%d err=%v", code, err)
			}
			if failRemove {
				if !strings.Contains(err.Error(), removeErr.Error()) || !store.set {
					t.Fatalf("failed cleanup was not reported or credential state changed: err=%v set=%v", err, store.set)
				}
			} else if store.set {
				t.Fatal("new credential remained after successful rollback")
			}
			if strings.Contains(err.Error(), "rollback-test-key") {
				t.Fatal("credential value leaked through rollback error")
			}
		})
	}
}

func TestAlbertCodeImportRefusesSymlinkedProjectStateDirectory(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, ".albert-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".albert-code", "skills.txt"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".just-code")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "project state directory") {
		t.Fatalf("import through symlinked project state directory: code=%d err=%v", code, err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("import wrote through .just-code symlink: %v", entries)
	}
}

func TestAlbertCodeImportPreviewAndApplyAreSafeAndIdempotent(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	excludePath := filepath.Join(root, ".git", "info", "exclude")
	excludeContents := "# user-local rules\n*.local\n.env\n"
	if err := os.WriteFile(excludePath, []byte(excludeContents), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `{
  "provider": {"albert": {"npm": "@ai-sdk/openai-compatible", "name": "Albert API", "options": {"baseURL": "https://albert.api.etalab.gouv.fr/v1", "apiKey": "{env:ALBERT_API_KEY}"}, "models": {"deepseek-v4-flash": {"name": "DeepSeek V4 Flash"}}}},
  "model": "albert/deepseek-v4-flash",
  "mcp": {"data-gouv": {"type": "remote", "url": "https://mcp.data.gouv.fr/mcp", "enabled": true}},
  "permission": {"bash": {".*": "allow"}}
}`
	configPath := filepath.Join(root, "opencode.json")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "-f", "opencode.json").CombinedOutput(); err != nil {
		t.Fatalf("stage legacy OpenCode config fixture: %v: %s", err, out)
	}
	secret := "ALBERT_API_KEY=do-not-print-this-value\n"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	var modelValidationErr error
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", modelValidationErr })

	var code int
	var err error
	preview := captureStdout(t, func() {
		code, err = albertCodeImportCmd([]string{"albert-code", "--root", root})
	})
	if err != nil || code != 0 {
		t.Fatalf("preview: code=%d err=%v", code, err)
	}
	if strings.Contains(preview, "do-not-print-this-value") || !strings.Contains(preview, "Preview only") || !strings.Contains(preview, "sealed guest clone") {
		t.Fatalf("unsafe or incomplete preview: %s", preview)
	}
	manifestPath := justcode.ProjectManifestPath(root)
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("preview wrote the manifest: %v", err)
	}
	if got, err := os.ReadFile(configPath); err != nil || string(got) != config {
		t.Fatal("preview modified the legacy OpenCode config")
	}

	out := captureStdout(t, func() {
		code, err = albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
	})
	if err != nil || code != 0 {
		t.Fatalf("apply: code=%d err=%v, output=%s", code, err, out)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Runtime != string(justcode.RuntimeMicrosandbox) || manifest.Isolation != string(justcode.IsolationFull) || manifest.Model != "albert/deepseek-v4-flash" || !sameStrings(manifest.MCPConnectors, []string{"data-gouv"}) {
		t.Fatalf("imported manifest = %+v", manifest)
	}
	if manifest.CredentialRef != "" {
		t.Fatalf("preview/apply imported a credential without the explicit flag: %q", manifest.CredentialRef)
	}
	if _, err := os.Stat(justcode.ProjectLockPath(root)); err != nil {
		t.Fatalf("import did not write a lockfile: %v", err)
	}
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := justcode.ProjectLockPath(root)
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	modelValidationErr = errors.New("model was delisted after import")
	second := captureStdout(t, func() {
		code, err = albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
	})
	if err != nil || code != 0 || !strings.Contains(second, "already been imported") {
		t.Fatalf("second import was not an idempotent no-op: code=%d err=%v output=%s", code, err, second)
	}
	if got, err := os.ReadFile(manifestPath); err != nil || string(got) != string(manifestBefore) {
		t.Fatal("idempotent re-import changed manifest bytes")
	}
	if got, err := os.ReadFile(lockPath); err != nil || string(got) != string(lockBefore) {
		t.Fatal("idempotent re-import changed lockfile bytes")
	}
	if got, err := os.ReadFile(filepath.Join(root, ".env")); err != nil || string(got) != secret {
		t.Fatal("import modified the original .env")
	}
	if got, err := os.ReadFile(excludePath); err != nil || string(got) != excludeContents {
		t.Fatal("import modified unrelated .git/info/exclude entries")
	}
}

func TestAlbertCodeImportRefusesMCPsAbsentFromLegacySources(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, ".albert-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".albert-code", "skills.txt"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "opencode.json")
	config := `{"mcp":{"data-gouv":{"type":"remote","url":"https://mcp.data.gouv.fr/mcp","enabled":true}}}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"}); code != 0 || err != nil {
		t.Fatalf("seed import: code=%d err=%v", code, err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"}); code != 1 || err == nil || !strings.Contains(err.Error(), "differs from the import") {
		t.Fatalf("re-import accepted an MCP absent from legacy sources: code=%d err=%v", code, err)
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil || !sameStrings(manifest.MCPConnectors, []string{"data-gouv"}) {
		t.Fatalf("refusal changed the existing MCPs: %+v, %v", manifest.MCPConnectors, err)
	}
}

func TestAlbertCodeImportSuccessDoesNotPrintImportedCredential(t *testing.T) {
	stubCredentialGenerationBump(t)
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	const secret = "success-path-test-secret"
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("ALBERT_API_KEY="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &importTestCredentialStore{}
	originalStore := importCredentialStoreFn
	importCredentialStoreFn = func() justcode.CredentialStore { return store }
	t.Cleanup(func() { importCredentialStoreFn = originalStore })
	var code int
	var commandErr error
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code, commandErr = albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply", "--import-albert-key"})
		})
	})
	if commandErr != nil || code != 0 || store.value != secret {
		t.Fatalf("credential import: code=%d err=%v stored=%v", code, commandErr, store.set)
	}
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Fatal("successful credential import printed the key")
	}
}

func TestAlbertCodeImportAppliesSkillsAndPreservesAlbertManagedInstructions(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, ".albert-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".albert-code", "skills.txt"), []byte("example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const agents = "before\n<!-- albert-code:agents:start -->\nlegacy rule\n<!-- albert-code:agents:end -->\nafter\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(agents), 0o600); err != nil {
		t.Fatal(err)
	}
	stubProjectSkills(t)
	stubImportSkillResolver(t)
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	output := captureStdout(t, func() {
		code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
		if err != nil || code != 0 {
			t.Fatalf("apply with skills: code=%d err=%v", code, err)
		}
	})
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil || !sameStrings(manifest.Skills, []string{"official/example"}) {
		t.Fatalf("imported skills = %v, err=%v", manifest.Skills, err)
	}
	lock, err := justcode.ReadLockfile(justcode.DefaultFS, justcode.ProjectLockPath(root))
	if err != nil {
		t.Fatal(err)
	}
	expectedSkillLock := lock.Skills["official/example"]
	lock.Skills["official/example"] = justcode.SkillLock{Repository: "https://github.com/etalab-ia/skills.git", Revision: strings.Repeat("a", 40), SHA256: strings.Repeat("c", 64)}
	if err := justcode.WriteLockfile(justcode.DefaultFS, justcode.ProjectLockPath(root), lock); err != nil {
		t.Fatal(err)
	}
	if code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"}); code != 1 || err == nil || !strings.Contains(err.Error(), "differs from the import") {
		t.Fatalf("re-import accepted a changed skill pin: code=%d err=%v", code, err)
	}
	lock.Skills["official/example"] = expectedSkillLock
	if err := justcode.WriteLockfile(justcode.DefaultFS, justcode.ProjectLockPath(root), lock); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"before", "legacy rule", "after", "<!-- albert-code:agents:start -->", "<!-- BEGIN JUST-CODE MANAGED SKILLS -->"} {
		if !strings.Contains(string(updated), want) {
			t.Fatalf("AGENTS.md lost %q after import: %s", want, updated)
		}
	}
	if !strings.Contains(output, "AGENTS.md was updated only through the just-code managed skills section") {
		t.Fatalf("apply output omitted managed-instructions result: %s", output)
	}
	second := captureStdout(t, func() {
		code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
		if err != nil || code != 0 {
			t.Fatalf("second apply with skills: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(second, "already been imported") {
		t.Fatalf("skill import was not idempotent: %s", second)
	}
}

func TestAlbertCodeImportRepairsMissingManagedSkillInstructionsOnReimport(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, ".albert-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".albert-code", "skills.txt"), []byte("example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const userInstructions = "custom user instructions\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(userInstructions), 0o600); err != nil {
		t.Fatal(err)
	}
	stubProjectSkills(t)
	stubImportSkillResolver(t)
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	if code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"}); code != 0 || err != nil {
		t.Fatalf("initial import: code=%d err=%v", code, err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(userInstructions), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	var commandErr error
	output := captureStdout(t, func() {
		code, commandErr = albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
	})
	if commandErr != nil || code != 0 || !strings.Contains(output, "Reconciled the existing import") {
		t.Fatalf("re-import did not repair managed instructions: code=%d err=%v output=%s", code, commandErr, output)
	}
	updated, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || !strings.Contains(string(updated), userInstructions) || !strings.Contains(string(updated), "BEGIN JUST-CODE MANAGED SKILLS") {
		t.Fatalf("re-import failed to restore managed instructions or preserve user text: %q, %v", updated, err)
	}
}

func TestAlbertCodeImportRefusesToWriteThroughSymlinkedAgentsFile(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, ".albert-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".albert-code", "skills.txt"), []byte("example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "instructions.md")
	const targetContents = "external instructions\n"
	if err := os.WriteFile(target, []byte(targetContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	stubProjectSkills(t)
	stubImportSkillResolver(t)
	if _, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("import through symlinked AGENTS.md was not refused: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != targetContents {
		t.Fatalf("symlink target changed: contents=%q err=%v", got, err)
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("manifest was written despite unsafe AGENTS.md: %v", err)
	}
}

func stubImportSkillResolver(t *testing.T) {
	t.Helper()
	// This isolates importer-to-wizard wiring and managed-instruction writes;
	// the real cached-artifact verifier is covered separately by internal/justcode.
	originalResolve, originalVerify := resolveImportSkillsFn, verifyImportSkillsFn
	lock := justcode.SkillLock{
		Repository: "https://github.com/etalab-ia/skills.git",
		Revision:   strings.Repeat("a", 40),
		SHA256:     strings.Repeat("b", 64),
	}
	resolveImportSkillsFn = func(_ context.Context, ids []string) ([]justcode.ProjectSkill, map[string]justcode.SkillLock, error) {
		if !sameStrings(ids, []string{"official/example"}) {
			return nil, nil, errors.New("unexpected selected skill IDs")
		}
		return []justcode.ProjectSkill{{ID: "official/example", Name: "example", Description: "fixture"}}, map[string]justcode.SkillLock{"official/example": lock}, nil
	}
	verifyImportSkillsFn = func(ids []string, locks map[string]justcode.SkillLock) error {
		if !sameStrings(ids, []string{"official/example"}) || locks["official/example"] != lock {
			return errors.New("unexpected skill verification inputs")
		}
		return nil
	}
	t.Cleanup(func() {
		resolveImportSkillsFn, verifyImportSkillsFn = originalResolve, originalVerify
	})
}

func TestAlbertCodeImportRefusesExistingManagedFieldsAbsentFromLegacySource(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	runtimeScript := filepath.Join(root, ".agent-vm.runtime.sh")
	const script = "do-not-source-this\n"
	if err := os.WriteFile(runtimeScript, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	dependencySetID := strings.Repeat("a", 32)
	manifest := justcode.ProjectManifest{
		DependencySetID: dependencySetID,
		Runtime:         string(justcode.RuntimeMicrosandbox),
		Isolation:       string(justcode.IsolationFull),
		Model:           "albert/custom-model",
	}
	lock := justcode.Lockfile{
		DependencySetID: dependencySetID,
		Entries:         map[string]string{},
		Skills:          map[string]justcode.SkillLock{},
	}
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root), manifest); err != nil {
		t.Fatal(err)
	}
	if err := justcode.WriteLockfile(justcode.DefaultFS, justcode.ProjectLockPath(root), lock); err != nil {
		t.Fatal(err)
	}
	code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "differs from the import") {
		t.Fatalf("import with unrelated existing model = (%d, %v); want refusal", code, err)
	}
	if got, err := os.ReadFile(runtimeScript); err != nil || string(got) != script {
		t.Fatal("import read, executed, or modified the legacy runtime script")
	}
	stored, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil || stored.Model != "albert/custom-model" {
		t.Fatalf("import changed existing manifest: %+v, %v", stored, err)
	}
}

func TestAlbertCodeImportRefusesUnexplainedManifestFields(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{"custom-cpus", `{"schemaVersion":4,"runtime":"microsandbox","isolation":"full","cpus":8}`},
		{"custom-memory", `{"schemaVersion":4,"runtime":"microsandbox","isolation":"full","memoryMB":8192}`},
		{"project-label", `{"schemaVersion":4,"runtime":"microsandbox","isolation":"full","project":"custom"}`},
		{"legacy-storage", `{"schemaVersion":4,"runtime":"microsandbox","isolation":"full","storage":"custom"}`},
		{"future-field", `{"schemaVersion":4,"runtime":"microsandbox","isolation":"full","futureWorkflow":{"remote":"owner/repo"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(root, ".agent-vm.runtime.sh"), []byte("legacy\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Dir(justcode.ProjectManifestPath(root))
			if err := os.MkdirAll(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(justcode.ProjectManifestPath(root), []byte(tc.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			code, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"})
			if code != 1 || err == nil || !strings.Contains(err.Error(), "differs from the import") {
				t.Fatalf("manifest with unaccounted %s field: code=%d err=%v", tc.name, code, err)
			}
		})
	}
}

func TestAlbertCodeImportPreviewShowsUnresolvedSkillsWhenCatalogueIsOffline(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.Mkdir(filepath.Join(root, ".albert-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".albert-code", "skills.txt"), []byte("rgaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalCatalogue := projectSkillCatalogueFn
	projectSkillCatalogueFn = func(context.Context) ([]justcode.ProjectSkill, error) {
		return nil, errors.New("injected offline catalogue")
	}
	t.Cleanup(func() { projectSkillCatalogueFn = originalCatalogue })
	preview := captureStdout(t, func() {
		code, err := albertCodeImportCmd([]string{"albert-code", "--root", root})
		if err != nil || code != 0 {
			t.Fatalf("offline preview: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(preview, "Legacy skills not resolved (catalogue unavailable): rgaa") {
		t.Fatalf("offline preview omitted legacy skill name: %s", preview)
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("offline preview wrote project configuration: %v", err)
	}
	if _, err := albertCodeImportCmd([]string{"albert-code", "--root", root, "--apply"}); err == nil {
		t.Fatal("apply proceeded without resolving legacy skills")
	}
}

type importTestCredentialStore struct {
	value     string
	set       bool
	removeErr error
}

func (s *importTestCredentialStore) Kind() string { return "test" }
func (s *importTestCredentialStore) Put(_ context.Context, _ justcode.CredentialKind, value string) error {
	s.value, s.set = value, true
	return nil
}
func (s *importTestCredentialStore) Get(context.Context, justcode.CredentialKind) (string, error) {
	if !s.set {
		return "", justcode.ErrCredentialNotFound
	}
	return s.value, nil
}
func (s *importTestCredentialStore) Remove(context.Context, justcode.CredentialKind) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	s.value, s.set = "", false
	return nil
}
func (*importTestCredentialStore) Verify(context.Context) error { return nil }
func (*importTestCredentialStore) Generation(context.Context, justcode.CredentialKind) (string, error) {
	return "", nil
}

func stubCredentialGenerationBump(t *testing.T) {
	t.Helper()
	original := bumpImportedCredentialGenerationFn
	bumpImportedCredentialGenerationFn = func(justcode.CredentialKind) {}
	t.Cleanup(func() { bumpImportedCredentialGenerationFn = original })
}
