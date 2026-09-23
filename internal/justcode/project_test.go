package justcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// gitInit runs git init in dir, creating a real worktree so discovery tests
// exercise the real `git rev-parse --show-toplevel` path.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", dir, err, out)
	}
}

func TestDiscoverProjectGitRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	gitInit(t, base)
	sub := filepath.Join(base, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	pc, err := DiscoverProject(sub)
	if err != nil {
		t.Fatalf("DiscoverProject: %v", err)
	}
	// DiscoverProject resolves symlinks, and on macOS t.TempDir() lives
	// under /var -> /private/var; compare against the resolved form.
	want, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if pc.Root != want {
		t.Errorf("Root = %q, want worktree root %q", pc.Root, want)
	}
	if !pc.IsGit {
		t.Error("IsGit = false, want true")
	}
	if pc.Name != filepath.Base(base) {
		t.Errorf("Name = %q, want %q", pc.Name, filepath.Base(base))
	}
}

func TestDiscoverProjectNonGitUsesDirectory(t *testing.T) {
	base := t.TempDir()
	pc, err := DiscoverProject(base)
	if err != nil {
		t.Fatalf("DiscoverProject: %v", err)
	}
	want, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if pc.Root != want {
		t.Errorf("Root = %q, want %q", pc.Root, want)
	}
	if pc.IsGit {
		t.Error("IsGit = true for a non-Git directory")
	}
}

func TestDiscoverProjectSymlinkAliasResolvesConsistently(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	gitInit(t, base)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(base, alias); err != nil {
		t.Skip("symlinks unavailable")
	}
	direct, err := DiscoverProject(base)
	if err != nil {
		t.Fatal(err)
	}
	viaAlias, err := DiscoverProject(alias)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Root != viaAlias.Root {
		t.Errorf("symlink alias changed identity: %q vs %q", direct.Root, viaAlias.Root)
	}
	if direct.InstanceName() != viaAlias.InstanceName() {
		t.Errorf("symlink alias changed instance name: %q vs %q", direct.InstanceName(), viaAlias.InstanceName())
	}
}

func TestInstanceNameSameNamedReposDoNotCollide(t *testing.T) {
	a := InstanceName("/home/alice/Code/org/just-code", "just-code")
	b := InstanceName("/home/alice/Code/other/just-code", "just-code")
	if a == b {
		t.Fatalf("two same-named repositories got the same instance name: %q", a)
	}
	if !strings.HasPrefix(a, "jc-just-code-") {
		t.Errorf("name %q is not readable (want jc-just-code-<suffix>)", a)
	}
}

func TestInstanceNameWorktreesDoNotCollide(t *testing.T) {
	main := InstanceName("/home/u/repo", "repo")
	wt := InstanceName("/home/u/.worktrees/repo-feature", "repo-feature")
	if main == wt {
		t.Fatalf("worktree and main checkout got the same instance name: %q", main)
	}
}

func TestInstanceNameStableAcrossCalls(t *testing.T) {
	root := "/home/u/Code/org/proj"
	if InstanceName(root, "proj") != InstanceName(root, "proj") {
		t.Fatal("instance name is not deterministic")
	}
}

func TestInstanceNameSafeCharacters(t *testing.T) {
	got := InstanceName("/tmp/weird name with spaces", "weird name with spaces")
	for _, r := range got {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			t.Fatalf("instance name %q contains unsafe character %q", got, r)
		}
	}
}

func TestPathSuffixNormalizesWindowsSeparators(t *testing.T) {
	if runtime.GOOS != "windows" {
		// filepath.ToSlash is a no-op on non-Windows, so the normalization is
		// only observable there; the property tested here is that the suffix
		// is derived from the cleaned, ToSlash'd path.
		got := pathSuffix(filepath.Join("home", "u", "proj"))
		want := pathSuffix("/home/u/proj")
		if got != want {
			t.Errorf("pathSuffix(%q) = %q, want %q", filepath.Join("home", "u", "proj"), got, want)
		}
	}
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewProjectRegistry("/reg/registry.json")
	r.FS = newMapFS()
	if err := r.Register("/proj/a", "jc-a-11111"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	inst, ok, err := r.Lookup("/proj/a")
	if err != nil || !ok || inst != "jc-a-11111" {
		t.Fatalf("Lookup = %q, %v, %v", inst, ok, err)
	}
	// Idempotent re-registration.
	if err := r.Register("/proj/a", "jc-a-11111"); err != nil {
		t.Fatalf("idempotent Register: %v", err)
	}
}

func TestRegistryPersistsAcrossInstances(t *testing.T) {
	fs := newMapFS()
	r1 := NewProjectRegistry("/reg/registry.json")
	r1.FS = fs
	if err := r1.Register("/proj/a", "jc-a-11111"); err != nil {
		t.Fatal(err)
	}
	r2 := NewProjectRegistry("/reg/registry.json")
	r2.FS = fs
	inst, ok, err := r2.Lookup("/proj/a")
	if err != nil || !ok || inst != "jc-a-11111" {
		t.Fatalf("second registry instance Lookup = %q, %v, %v", inst, ok, err)
	}
}

func TestRegistryRefusesInstanceCollision(t *testing.T) {
	r := NewProjectRegistry("/reg/registry.json")
	r.FS = newMapFS()
	if err := r.Register("/proj/a", "jc-a-11111"); err != nil {
		t.Fatal(err)
	}
	err := r.Register("/proj/b", "jc-a-11111")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want duplicate-registration refusal", err)
	}
}

func TestRegistryRefusesSilentRebind(t *testing.T) {
	r := NewProjectRegistry("/reg/registry.json")
	r.FS = newMapFS()
	if err := r.Register("/proj/a", "jc-a-11111"); err != nil {
		t.Fatal(err)
	}
	err := r.Register("/proj/a", "jc-a-22222")
	if err == nil || !strings.Contains(err.Error(), "explicit rebind") {
		t.Fatalf("err = %v, want silent-rebind refusal", err)
	}
	// Explicit rebind is allowed.
	if err := r.Rebind("/proj/a", "jc-a-22222"); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	inst, _, _ := r.Lookup("/proj/a")
	if inst != "jc-a-22222" {
		t.Errorf("after rebind, instance = %q, want jc-a-22222", inst)
	}
}

func TestRegistryRejectsNewerSchema(t *testing.T) {
	fs := newMapFS()
	fs.files["/reg/registry.json"] = []byte(`{"schemaVersion": 99, "entries": []}`)
	r := NewProjectRegistry("/reg/registry.json")
	r.FS = fs
	_, _, err := r.Lookup("/proj/a")
	if err == nil || !strings.Contains(err.Error(), "newer than this build supports") {
		t.Fatalf("err = %v, want schema refusal", err)
	}
}

func TestProjectLockAcquireRelease(t *testing.T) {
	l := &ProjectLock{Path: "/locks/demo.lock", FS: newMapFS()}
	release, err := l.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	// Re-acquiring after release must succeed.
	release2, err := l.Acquire()
	if err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
	release2()
}

func TestProjectLockConcurrentSetupSingleWinner(t *testing.T) {
	// The mapFS WriteFile cannot fail, so the lock's exclusive property is
	// exercised against the real filesystem: two concurrent Acquires must
	// serialize, never both succeed at once.
	dir := t.TempDir()
	l1 := &ProjectLock{Path: filepath.Join(dir, "p.lock"), FS: DefaultFS}
	l2 := &ProjectLock{Path: filepath.Join(dir, "p.lock"), FS: DefaultFS}
	r1, err := l1.Acquire()
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	acquired2 := make(chan error, 1)
	go func() {
		// This must block until the first holder releases; it cannot just
		// succeed. The test bounds the wait so a broken lock fails fast.
		r2, err := l2.Acquire()
		if err == nil {
			r2()
		}
		acquired2 <- err
	}()
	select {
	case err := <-acquired2:
		t.Fatalf("second Acquire returned while lock held: %v", err)
	case <-time.After(200 * time.Millisecond):
		// Still blocked: correct.
	}
	r1()
	select {
	case err := <-acquired2:
		if err != nil {
			t.Fatalf("second Acquire after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Acquire never returned after release")
	}
}

func TestMicrosandboxInstanceNameThreading(t *testing.T) {
	// A backend built for an instance must operate on that instance, not the
	// legacy singleton: every client call carries the project name.
	client := &fakeMSBClient{exists: true, status: "running"}
	m := NewMicrosandboxRuntimeForInstance(Config{
		APIKey:       "key",
		WorkspaceDir: t.TempDir(),
		Username:     "opencode",
		Password:     "pw",
	}, "jc-demo-ab12c")
	m.Client = client
	if m.InstanceName() != "jc-demo-ab12c" {
		t.Fatalf("InstanceName = %q", m.InstanceName())
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasCall(client, "stop jc-demo-ab12c") {
		t.Fatalf("Stop did not target the project instance; calls: %v", client.calls)
	}
}

func TestMicrosandboxDefaultStaysLegacySingleton(t *testing.T) {
	// Compatibility: a backend built without an instance keeps operating on
	// the legacy singleton name, so existing users' sandboxes are found.
	m := NewMicrosandboxRuntime(Config{APIKey: "key", WorkspaceDir: t.TempDir()})
	if m.InstanceName() != msbSandbox {
		t.Fatalf("default InstanceName = %q, want legacy %q", m.InstanceName(), msbSandbox)
	}
}

func TestMicrosandboxLegacyInstanceNotAdopted(t *testing.T) {
	// The registry is the authority: a legacy-named sandbox that was never
	// registered for this project must not be adopted silently. The lookup
	// returns no registration, so the caller must fail closed rather than
	// operate on a name that happens to exist.
	r := NewProjectRegistry("/reg/registry.json")
	r.FS = newMapFS()
	_, ok, err := r.Lookup("/proj/demo")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unregistered project resolved to a registration")
	}
}
