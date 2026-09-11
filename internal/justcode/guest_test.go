package justcode

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeExecer struct {
	called bool
	path   string
	argv   []string
	env    []string
}

func (f *fakeExecer) Exec(path string, argv []string, env []string) error {
	f.called = true
	f.path = path
	f.argv = argv
	f.env = env
	return nil
}

// guestTest builds a GuestConfig with hermetic paths. bins are executable stubs
// placed on the guest PATH.
func guestTest(t *testing.T, runner Runner, stdin string, bins ...string) (GuestConfig, *fakeExecer) {
	t.Helper()
	cfg, execer, _ := guestTestDir(t, runner, stdin, bins...)
	return cfg, execer
}

// guestTestDir is guestTest plus the bin directory, for tests that simulate an
// installer dropping a binary on PATH.
func guestTestDir(t *testing.T, runner Runner, stdin string, bins ...string) (GuestConfig, *fakeExecer, string) {
	t.Helper()
	home := t.TempDir()
	binDir := t.TempDir()
	for _, b := range bins {
		if err := os.WriteFile(filepath.Join(binDir, b), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	execer := &fakeExecer{}
	cfg := GuestConfig{
		Port:     "4096",
		Username: "opencode",
		MTU:      "auto",
		Stdin:    strings.NewReader(stdin),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		Runner:   runner,
		Execer:   execer,
		Chdir:    func(string) error { return nil },
		Home:     home,
		Environ:  []string{"PATH=" + binDir},
		HTTPGet:  func(string) ([]byte, error) { return []byte("echo installed\n"), nil },
	}
	return cfg, execer, binDir
}

func TestGuestPathIncludesAdminDirsAndOpencodeBin(t *testing.T) {
	got := guestPath([]string{"PATH=/usr/bin:/bin"}, "/Users/x")
	for _, want := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin", filepath.Join("/Users/x", ".opencode", "bin")} {
		if !strings.Contains(got, want) {
			t.Errorf("guestPath = %q, missing %q", got, want)
		}
	}
}

func TestParseDefaultInterface(t *testing.T) {
	out := "   route to: default\ndestination: default\n       mask: default\n    gateway: 192.168.64.1\n  interface: en0\n      flags: <UP,GATEWAY>\n"
	if got := parseDefaultInterface(out); got != "en0" {
		t.Fatalf("parseDefaultInterface = %q, want en0", got)
	}
	if got := parseDefaultInterface("nothing here"); got != "" {
		t.Fatalf("parseDefaultInterface = %q, want empty", got)
	}
}

func TestGuestMTUApplied(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "route" {
			return ExecResult{ExitCode: 0, Stdout: "  interface: en7\n"}
		}
		return ExecResult{ExitCode: 0}
	}}
	cfg, execer := guestTest(t, r, "pw\nkey\n", "opencode")
	cfg.MTU = "1400"

	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	if !r.hasCall("sudo -n ifconfig en7 mtu 1400") {
		t.Fatalf("MTU not applied; calls: %v", r.calls)
	}
	if !execer.called {
		t.Fatal("expected the process to be replaced by opencode serve")
	}
}

func TestGuestMTUAutoSkipsNetworkChanges(t *testing.T) {
	// A route failure would surface if the MTU logic ran; "auto" must skip it.
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "route" {
			return ExecResult{ExitCode: 1}
		}
		return ExecResult{ExitCode: 0}
	}}
	cfg, _ := guestTest(t, r, "pw\nkey\n", "opencode")
	cfg.MTU = MTUAuto

	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	if r.hasCall("route") || r.hasCall("sudo") {
		t.Fatalf("auto must not touch the network; calls: %v", r.calls)
	}
}

func TestGuestMTUInvalidFailsBeforeExec(t *testing.T) {
	for _, value := range []string{"1279", "1501", "9000", "01280", "abc", "1280;id"} {
		t.Run(value, func(t *testing.T) {
			r := &fakeRunner{}
			cfg, execer := guestTest(t, r, "pw\nkey\n", "opencode")
			cfg.MTU = value
			err := RunGuestBootstrap(context.Background(), cfg)
			if err == nil || !strings.Contains(err.Error(), "TART_MTU must be") {
				t.Fatalf("expected MTU validation error, got %v", err)
			}
			if execer.called {
				t.Fatal("opencode must not start with an invalid MTU")
			}
		})
	}
}

func TestGuestMissingInterface(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "route" {
			return ExecResult{ExitCode: 0, Stdout: ""}
		}
		return ExecResult{ExitCode: 0}
	}}
	cfg, execer := guestTest(t, r, "pw\nkey\n", "opencode")
	cfg.MTU = "1280"

	err := RunGuestBootstrap(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "cannot determine") {
		t.Fatalf("expected interface error, got %v", err)
	}
	if execer.called {
		t.Fatal("opencode must not start without a resolved interface")
	}
}

func TestGuestSudoFailure(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "route" {
			return ExecResult{ExitCode: 0, Stdout: "  interface: en0\n"}
		}
		if name == "sudo" {
			return ExecResult{ExitCode: 1}
		}
		return ExecResult{ExitCode: 0}
	}}
	cfg, execer := guestTest(t, r, "pw\nkey\n", "opencode")
	cfg.MTU = "1280"

	err := RunGuestBootstrap(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "cannot apply TART_MTU") {
		t.Fatalf("expected sudo error, got %v", err)
	}
	if execer.called {
		t.Fatal("opencode must not start when the MTU could not be applied")
	}
}

func TestGuestEmptyPasswordPreserved(t *testing.T) {
	cfg, execer := guestTest(t, &fakeRunner{}, "\nkey\n", "opencode")
	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	if !envHas(execer.env, "OPENCODE_SERVER_PASSWORD=") {
		t.Fatalf("empty password not preserved (no-auth mode); env: %v", execer.env)
	}
	if !envHas(execer.env, "ALBERT_API_KEY=key") {
		t.Fatalf("API key not propagated; env: %v", execer.env)
	}
}

func TestGuestDefaultPasswordWhenStdinEmpty(t *testing.T) {
	cfg, execer := guestTest(t, &fakeRunner{}, "", "opencode")
	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	if !envHas(execer.env, "OPENCODE_SERVER_PASSWORD="+DefaultPassword) {
		t.Fatalf("default password not applied; env: %v", execer.env)
	}
}

func TestGuestExecArgvAndEnv(t *testing.T) {
	cfg, execer := guestTest(t, &fakeRunner{}, "pw\nkey\n", "opencode")
	cfg.Port = "5000"
	cfg.Username = "albert"

	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	want := []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", "5000"}
	if strings.Join(execer.argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v, want %v", execer.argv, want)
	}
	if !strings.HasSuffix(execer.path, "opencode") {
		t.Fatalf("exec path = %q, want the resolved opencode binary", execer.path)
	}
	if !envHas(execer.env, "OPENCODE_SERVER_USERNAME=albert") {
		t.Fatalf("username not propagated; env: %v", execer.env)
	}
	if !envHas(execer.env, "OPENCODE_CONFIG_CONTENT="+opencodeConfigContent) {
		t.Fatal("opencode config content not propagated")
	}
}

func TestGuestInstallsOpencodeViaBrew(t *testing.T) {
	// brew present, opencode absent; a successful brew install drops it on PATH.
	var binDir string
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "brew" {
			_ = os.WriteFile(filepath.Join(binDir, "opencode"), []byte("#!/bin/sh\n"), 0o755)
		}
		return ExecResult{ExitCode: 0}
	}}
	cfg, execer, dir := guestTestDir(t, r, "pw\nkey\n", "brew")
	binDir = dir

	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	if !r.hasCall("brew install anomalyco/tap/opencode") {
		t.Fatalf("brew install not attempted; calls: %v", r.calls)
	}
	if !execer.called {
		t.Fatal("expected opencode to be exec'd after install")
	}
}

func TestGuestInstallFallsBackToInstallerScript(t *testing.T) {
	var binDir string
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "brew" || name == "npm" {
			return ExecResult{ExitCode: 1} // both package managers fail
		}
		if name == "bash" {
			// The vendor installer succeeds and provides the binary.
			_ = os.WriteFile(filepath.Join(binDir, "opencode"), []byte("#!/bin/sh\n"), 0o755)
			return ExecResult{ExitCode: 0}
		}
		return ExecResult{ExitCode: 0}
	}}
	cfg, execer, dir := guestTestDir(t, r, "pw\nkey\n", "brew", "npm")
	binDir = dir

	if err := RunGuestBootstrap(context.Background(), cfg); err != nil {
		t.Fatalf("RunGuestBootstrap: %v", err)
	}
	if !r.hasCall("npm install -g opencode-ai") {
		t.Fatalf("npm fallback not attempted; calls: %v", r.calls)
	}
	if !r.hasCall("bash ") {
		t.Fatalf("vendor installer not run; calls: %v", r.calls)
	}
	if !execer.called {
		t.Fatal("expected opencode to be exec'd after the installer ran")
	}
}

func TestGuestFailsWhenOpencodeUnavailable(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1} // every install path fails
	}}
	cfg, execer := guestTest(t, r, "pw\nkey\n", "brew", "npm")
	err := RunGuestBootstrap(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "installer failed") {
		t.Fatalf("expected installer failure, got %v", err)
	}
	if execer.called {
		t.Fatal("opencode must not start when it could not be installed")
	}
}

func TestWithEnvReplacesRatherThanDuplicates(t *testing.T) {
	got := withEnv([]string{"PATH=/old", "HOME=/h"}, "PATH=/new")
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "PATH=/old") {
		t.Fatalf("stale PATH retained: %v", got)
	}
	if !envHas(got, "PATH=/new") || !envHas(got, "HOME=/h") {
		t.Fatalf("withEnv = %v", got)
	}
	if strings.Count(joined, "PATH=") != 1 {
		t.Fatalf("duplicate PATH entries: %v", got)
	}
}

func envHas(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
