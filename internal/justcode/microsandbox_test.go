package justcode

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures copied from real `msb ls` output: name, image, status, created.
const (
	msbLsRunning = `NAME                       IMAGE                                STATUS     CREATED
albert-opencode-sandbox    ghcr.io/anomalyco/opencode:latest    running    2026-09-11 10:27:36
`
	msbLsStopped = `NAME                       IMAGE                                STATUS     CREATED
albert-opencode-sandbox    ghcr.io/anomalyco/opencode:latest    stopped    2026-09-11 10:27:36
`
	msbLsAbsent = `NAME                       IMAGE                                STATUS     CREATED
other-sandbox              ghcr.io/anomalyco/opencode:latest    running    2026-09-11 10:27:36
`
)

func newTestMicrosandbox(t *testing.T, runner Runner) *MicrosandboxRuntime {
	t.Helper()
	m := NewMicrosandboxRuntime(Config{
		APIKey:       "key",
		WorkspaceDir: t.TempDir(),
		Username:     "opencode",
		Password:     "pw",
	})
	m.Runner = runner
	m.AssetsDir = t.TempDir()
	// Default to "backend not healthy" so tests exercise the relaunch path
	// unless they opt into a healthy backend.
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{}
	}
	return m
}

// msbRunner responds to `msb ls` with the given listing and succeeds otherwise.
func msbRunner(ls string) *fakeRunner {
	return &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Join(args, " ") == "ls" {
			return ExecResult{ExitCode: 0, Stdout: ls}
		}
		return ExecResult{ExitCode: 0}
	}}
}

func (r *fakeRunner) hasPrefixCall(prefix string) bool {
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	defer func() { os.Stderr = original }()

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(read)
		done <- string(data)
	}()

	fn()
	write.Close()
	out := <-done
	read.Close()
	return out
}

func TestMicrosandboxStartRequiresAPIKey(t *testing.T) {
	m := NewMicrosandboxRuntime(Config{WorkspaceDir: t.TempDir()})
	if err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
}

func TestMicrosandboxStartNew(t *testing.T) {
	r := msbRunner(msbLsAbsent)
	m := newTestMicrosandbox(t, r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	last := r.calls[len(r.calls)-1]
	if !strings.HasPrefix(last, "msb run ") {
		t.Fatalf("expected msb run, got %q", last)
	}
	if !strings.Contains(last, "--conf "+filepath.Join(m.AssetsDir, "microsandbox.yaml")) {
		t.Fatalf("--conf does not point at the materialized config: %q", last)
	}
	if !strings.Contains(last, "--env OPENCODE_SERVER_PASSWORD=pw") {
		t.Fatalf("password env missing from run: %q", last)
	}
	if strings.Contains(last, "key") {
		t.Fatalf("API key leaked into msb args: %q", last)
	}
}

func TestMicrosandboxMaterializesConfig(t *testing.T) {
	m := newTestMicrosandbox(t, msbRunner(msbLsAbsent))
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.AssetsDir, "microsandbox.yaml")); err != nil {
		t.Fatalf("microsandbox.yaml not materialized: %v", err)
	}
}

// TestMicrosandboxStartRunningHealthy is the branch that must NOT touch the VM:
// a running sandbox with a live backend needs no relaunch.
func TestMicrosandboxStartRunningHealthy(t *testing.T) {
	r := msbRunner(msbLsRunning)
	m := newTestMicrosandbox(t, r)
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Status: 200, Healthy: true, Body: `{"healthy":true}`}
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, prefix := range []string{"msb run ", "msb start ", "msb modify ", "msb exec "} {
		if r.hasPrefixCall(prefix) {
			t.Fatalf("%s must not run when the backend is healthy; calls: %v", prefix, r.calls)
		}
	}
}

// TestMicrosandboxStartRunningDeadBackend is the headline regression: the VM
// persists but its entrypoint only ran at creation, so a running VM with no
// opencode process must be healed by relaunching the entrypoint.
func TestMicrosandboxStartRunningDeadBackend(t *testing.T) {
	r := msbRunner(msbLsRunning)
	m := newTestMicrosandbox(t, r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !r.hasPrefixCall("msb exec albert-opencode-sandbox -- sh -c nohup /.msb/scripts/start") {
		t.Fatalf("expected the entrypoint to be relaunched; calls: %v", r.calls)
	}
	if r.hasPrefixCall("msb run ") || r.hasPrefixCall("msb start ") {
		t.Fatalf("a running VM must not be recreated or restarted; calls: %v", r.calls)
	}
}

// TestMicrosandboxStartStoppedRelaunchesBackend covers the second half of the
// same bug: booting a stopped VM does not re-run the entrypoint either.
func TestMicrosandboxStartStoppedRelaunchesBackend(t *testing.T) {
	r := msbRunner(msbLsStopped)
	m := newTestMicrosandbox(t, r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, prefix := range []string{
		"msb modify albert-opencode-sandbox",
		"msb start albert-opencode-sandbox",
		"msb exec albert-opencode-sandbox -- sh -c nohup /.msb/scripts/start",
	} {
		if !r.hasPrefixCall(prefix) {
			t.Fatalf("expected %q; calls: %v", prefix, r.calls)
		}
	}
	if r.hasPrefixCall("msb run ") {
		t.Fatalf("a stopped sandbox must be started, not recreated; calls: %v", r.calls)
	}
}

// TestMicrosandboxLaunchBackendRetries covers the bounded retry while the guest
// agent catches up with a freshly booted VM.
func TestMicrosandboxLaunchBackendRetries(t *testing.T) {
	attempts := 0
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Contains(strings.Join(args, " "), "nohup") {
			attempts++
			if attempts < 3 {
				return ExecResult{ExitCode: 1, Stderr: "agent not ready"}
			}
		}
		return ExecResult{ExitCode: 0}
	}}
	m := newTestMicrosandbox(t, r)
	if err := m.launchBackend(context.Background()); err != nil {
		t.Fatalf("launchBackend: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestMicrosandboxIsRunning(t *testing.T) {
	for _, tc := range []struct {
		name string
		ls   string
		want bool
	}{
		{"running", msbLsRunning, true},
		{"stopped", msbLsStopped, false},
		{"absent", msbLsAbsent, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMicrosandbox(t, msbRunner(tc.ls))
			on, err := m.IsRunning(context.Background())
			if err != nil || on != tc.want {
				t.Fatalf("IsRunning = %v, %v; want %v", on, err, tc.want)
			}
		})
	}
}

func TestParseMsbLs(t *testing.T) {
	got, ok := parseMsbLs(msbLsRunning)
	if !ok {
		t.Fatal("managed sandbox not found")
	}
	if got.Name != msbSandbox || got.Status != "running" {
		t.Fatalf("parsed %+v", got)
	}
	if _, ok := parseMsbLs("NAME IMAGE STATUS CREATED\n"); ok {
		t.Fatal("header-only output must not match")
	}
}

func TestParseMsbWorkspaceMount(t *testing.T) {
	output := "Mounts:\n  /workspace -> /Users/luis/Code/proj\n  /other -> /tmp\n"
	if got := parseMsbWorkspaceMount(output); got != "/Users/luis/Code/proj" {
		t.Fatalf("mount = %q", got)
	}
	if got := parseMsbWorkspaceMount("nothing here"); got != "" {
		t.Fatalf("mount = %q, want empty", got)
	}
}

// TestMicrosandboxWarnsOnStaleWorkspaceMount covers the "mounts are fixed at
// creation" trap Luis hit: a changed workspace silently leaves /workspace empty.
func TestMicrosandboxWarnsOnStaleWorkspaceMount(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		joined := strings.Join(args, " ")
		switch {
		case joined == "ls":
			return ExecResult{ExitCode: 0, Stdout: msbLsRunning}
		case strings.HasPrefix(joined, "inspect"):
			return ExecResult{ExitCode: 0, Stdout: "/workspace -> /somewhere/else\n"}
		}
		return ExecResult{ExitCode: 0}
	}}
	m := newTestMicrosandbox(t, r)

	stderr := captureStderr(t, func() {
		if err := m.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
	})
	if !strings.Contains(stderr, "/somewhere/else") || !strings.Contains(stderr, "just-code restart --microsandbox") {
		t.Fatalf("expected a stale-mount warning, got %q", stderr)
	}
}

// TestMicrosandboxPassesAPIKeyToMsb covers the secret resolution gap: `msb`
// resolves ALBERT_API_KEY from the host environment, so it must be passed
// explicitly rather than relying on inheritance.
func TestMicrosandboxPassesAPIKeyToMsb(t *testing.T) {
	r := msbRunner(msbLsAbsent)
	m := newTestMicrosandbox(t, r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(r.lastEnv) == 0 {
		t.Fatal("no environment was passed to msb")
	}
	found := false
	for _, e := range r.lastEnv {
		if e == "ALBERT_API_KEY=key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ALBERT_API_KEY missing from the child env: %v", r.lastEnv)
	}
}
