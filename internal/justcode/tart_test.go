package justcode

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner scripts host-command results via an optional callback.
type fakeRunner struct {
	onRun   func(name string, args []string) ExecResult
	calls   []string
	lastEnv []string
}

func (f *fakeRunner) run(name string, args ...string) (ExecResult, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.onRun != nil {
		return f.onRun(name, args), nil
	}
	return ExecResult{ExitCode: 0}, nil
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (ExecResult, error) {
	return f.run(name, args...)
}

func (f *fakeRunner) RunEnv(_ context.Context, env []string, name string, args ...string) (ExecResult, error) {
	f.lastEnv = env
	return f.run(name, args...)
}

// hasCall reports whether any recorded call contains the given substring.
func (f *fakeRunner) hasCall(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// fakeStarter records the last started command and its stdin.
type fakeStarter struct {
	lastStdin []byte
	lastName  string
	lastArgs  []string
}

func (f *fakeStarter) Start(stdin io.Reader, _ string, name string, args ...string) error {
	if stdin != nil {
		f.lastStdin, _ = io.ReadAll(stdin)
	}
	f.lastName = name
	f.lastArgs = args
	return nil
}

func newTestTart(t *testing.T, runner Runner) *Tart {
	t.Helper()
	return &Tart{
		Config: Config{
			TartVM:       "opencode-tahoe-base-latest",
			TartImage:    DefaultTartImage,
			Username:     "opencode",
			Password:     "pw",
			PasswordSet:  true,
			TartMTU:      "1280",
			WorkspaceDir: t.TempDir(),
			APIKey:       "key",
		},
		Runner:           runner,
		Starter:          &fakeStarter{},
		StateDir:         t.TempDir(),
		SelfBinary:       writeFakeSelf(t),
		KillPollInterval: time.Millisecond,
		KillMaxPolls:     3,
	}
}

// writeFakeSelf stands in for os.Executable in tests, so staging does not copy
// the (large) test binary.
func writeFakeSelf(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "self")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho just-code\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStageGuestBinaryCopiesSelf(t *testing.T) {
	tt := newTestTart(t, &fakeRunner{})
	// Stand in for os.Executable with a known file.
	src := filepath.Join(t.TempDir(), "self")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho just-code\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tt.SelfBinary = src

	if err := tt.stageGuestBinary(); err != nil {
		t.Fatalf("stageGuestBinary: %v", err)
	}
	staged := filepath.Join(TartStageDir(tt.StateDir), guestBinaryName)
	data, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("staged binary missing: %v", err)
	}
	if string(data) != "#!/bin/sh\necho just-code\n" {
		t.Fatalf("staged binary content = %q", data)
	}
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("staged binary is not executable: %o", info.Mode().Perm())
	}
}

func TestParseTartList(t *testing.T) {
	out := "local opencode-tahoe-base-latest 192.168.64.2 50.0G stopped\n" +
		"local opencode-sonoma-base-latest 192.168.64.3 50.0G running\n" +
		"local other-vm 192.168.64.4 10.0G running\n" +
		"remote something 1.2.3.4 10G running\n"
	got := ParseTartList(out, "opencode-")
	want := []string{"opencode-sonoma-base-latest"}
	if len(got) != len(want) || (len(got) > 0 && got[0] != want[0]) {
		t.Fatalf("ParseTartList = %v, want %v", got, want)
	}
}

func TestStopBackendAlreadyStopped(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Contains(strings.Join(args, " "), "pgrep") {
			return ExecResult{ExitCode: 1} // not running
		}
		return ExecResult{ExitCode: 0}
	}}
	tt := newTestTart(t, r)
	if err := tt.StopBackend(context.Background()); err != nil {
		t.Fatalf("StopBackend: %v", err)
	}
	if len(r.calls) == 0 || !strings.HasPrefix(r.calls[0], "tart exec opencode-tahoe-base-latest pkill -x opencode") {
		t.Fatalf("expected SIGTERM pkill first, got %v", r.calls)
	}
}

func TestStopBackendForceKill(t *testing.T) {
	var forceKilled bool
	var pgrepCount int
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "pkill -9"):
			forceKilled = true
			return ExecResult{ExitCode: 0}
		case strings.Contains(joined, "pgrep"):
			pgrepCount++
			if forceKilled {
				return ExecResult{ExitCode: 1} // gone after SIGKILL
			}
			return ExecResult{ExitCode: 0} // still running
		default:
			return ExecResult{ExitCode: 0}
		}
	}}
	tt := newTestTart(t, r)
	if err := tt.StopBackend(context.Background()); err != nil {
		t.Fatalf("StopBackend: %v", err)
	}
	if !forceKilled {
		t.Fatal("expected pkill -9 (SIGKILL) to be issued")
	}
	if pgrepCount != 4 { // three loop polls + one final confirmation
		t.Fatalf("pgrep called %d times, want 4", pgrepCount)
	}
}

func TestStopBackendRefusesWhenStillRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Contains(strings.Join(args, " "), "pgrep") {
			return ExecResult{ExitCode: 0} // never dies
		}
		return ExecResult{ExitCode: 0}
	}}
	tt := newTestTart(t, r)
	tt.KillMaxPolls = 2
	err := tt.StopBackend(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to relaunch") {
		t.Fatalf("expected refuse error, got %v", err)
	}
}

func TestBackendArgsExcludeSecrets(t *testing.T) {
	args := BackendArgs("opencode-tahoe-base-latest",
		"/tmp/just-code-guest", "4096", "opencode", "1280")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "pw") || strings.Contains(joined, "key") {
		t.Fatalf("secrets leaked into argv: %v", args)
	}
	want := []string{"exec", "-i", "opencode-tahoe-base-latest",
		"/tmp/just-code-guest", GuestBootstrapCommand, "4096", "opencode", "1280"}
	if len(args) != len(want) {
		t.Fatalf("BackendArgs = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("BackendArgs[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestSecretsReader(t *testing.T) {
	data, err := io.ReadAll(SecretsReader("pw", "key"))
	if err != nil {
		t.Fatalf("SecretsReader: %v", err)
	}
	if string(data) != "pw\nkey\n" {
		t.Fatalf("SecretsReader = %q, want password then key on stdin", data)
	}
}

func TestLaunchBackendCopiesGuestBinaryAndStreamsSecrets(t *testing.T) {
	r := &fakeRunner{}
	tt := newTestTart(t, r)
	if err := tt.launchBackend(context.Background()); err != nil {
		t.Fatalf("launchBackend: %v", err)
	}

	// The payload must be copied to guest-local disk and made executable before
	// it is run: the share itself is not reliably executable.
	if !r.hasCall("/bin/cp " + guestShareBinary + " " + guestLocalBinary) {
		t.Fatalf("expected a guest cp from the share to local disk; calls: %v", r.calls)
	}
	if !r.hasCall("/bin/chmod 755 " + guestLocalBinary) {
		t.Fatalf("expected a guest chmod 755; calls: %v", r.calls)
	}
	staged := filepath.Join(TartStageDir(tt.StateDir), guestBinaryName)
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("binary was not staged into the share: %v", err)
	}

	fs := tt.Starter.(*fakeStarter)
	if got := string(fs.lastStdin); got != "pw\nkey\n" {
		t.Fatalf("stdin = %q, want password and key on stdin", got)
	}
	if !strings.HasPrefix(strings.Join(fs.lastArgs, " "), "exec -i opencode-tahoe-base-latest "+guestLocalBinary+" "+GuestBootstrapCommand) {
		t.Fatalf("unexpected bootstrap argv: %v", fs.lastArgs)
	}
	if strings.Contains(strings.Join(fs.lastArgs, " "), "pw") ||
		strings.Contains(strings.Join(fs.lastArgs, " "), "key") {
		t.Fatalf("secrets leaked into argv: %v", fs.lastArgs)
	}
}
