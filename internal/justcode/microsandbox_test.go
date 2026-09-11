package justcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestMicrosandbox(t *testing.T, runner Runner) *MicrosandboxRuntime {
	t.Helper()
	m := NewMicrosandboxRuntime(Config{
		APIKey:     "key",
		ProjectDir: "/tmp/proj",
		Username:   "opencode",
		Password:   "pw",
	})
	m.Runner = runner
	m.AssetsDir = t.TempDir()
	return m
}

func TestMicrosandboxStartRequiresAPIKey(t *testing.T) {
	m := NewMicrosandboxRuntime(Config{ProjectDir: t.TempDir()})
	if err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
}

func TestMicrosandboxStartNew(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		switch strings.Join(args, " ") {
		case "ls --running -q":
			return ExecResult{ExitCode: 0, Stdout: "other-sandbox\n"}
		case "inspect albert-opencode-sandbox":
			return ExecResult{ExitCode: 1} // does not exist yet
		default:
			return ExecResult{ExitCode: 0}
		}
	}}
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
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Join(args, " ") == "inspect albert-opencode-sandbox" {
			return ExecResult{ExitCode: 1}
		}
		return ExecResult{ExitCode: 0}
	}}
	m := newTestMicrosandbox(t, r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.AssetsDir, "microsandbox.yaml")); err != nil {
		t.Fatalf("microsandbox.yaml not materialized: %v", err)
	}
}

func TestMicrosandboxStartAlreadyRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Join(args, " ") == "ls --running -q" {
			return ExecResult{ExitCode: 0, Stdout: "albert-opencode-sandbox\n"}
		}
		return ExecResult{ExitCode: 0}
	}}
	m := newTestMicrosandbox(t, r)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, c := range r.calls {
		if strings.HasPrefix(c, "msb run ") || strings.HasPrefix(c, "msb start ") {
			t.Fatalf("unexpected start call %q", c)
		}
	}
}

func TestMicrosandboxIsRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 0, Stdout: "albert-opencode-sandbox\n"}
	}}
	m := newTestMicrosandbox(t, r)
	on, err := m.IsRunning(context.Background())
	if err != nil || !on {
		t.Fatalf("IsRunning = %v, %v; want true", on, err)
	}
}
