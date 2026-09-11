package justcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestDocker(t *testing.T, runner Runner) *DockerRuntime {
	t.Helper()
	d := NewDockerRuntime(Config{
		APIKey:     "key",
		ProjectDir: t.TempDir(),
		Username:   "opencode",
		Password:   "pw",
	})
	d.Runner = runner
	d.AssetsDir = t.TempDir()
	return d
}

func TestDockerStartRequiresAPIKey(t *testing.T) {
	d := NewDockerRuntime(Config{ProjectDir: t.TempDir()})
	if err := d.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
}

func TestDockerStartCommand(t *testing.T) {
	r := &fakeRunner{}
	d := newTestDocker(t, r)
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected one call, got %v", r.calls)
	}
	call := r.calls[0]
	if !strings.HasPrefix(call, "docker compose ") {
		t.Fatalf("call = %q, want a docker compose invocation", call)
	}
	if !strings.Contains(call, "-f "+filepath.Join(d.AssetsDir, "docker-compose.yml")) {
		t.Fatalf("call = %q, want -f pointing at the materialized compose file", call)
	}
	if !strings.Contains(call, "--project-directory "+d.AssetsDir) {
		t.Fatalf("call = %q, want the assets dir as project directory", call)
	}
	if !strings.HasSuffix(call, " up -d --quiet-pull") {
		t.Fatalf("call = %q, want the up subcommand", call)
	}
}

func TestDockerComposeEnv(t *testing.T) {
	r := &fakeRunner{}
	d := newTestDocker(t, r)
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	env := strings.Join(r.lastEnv, "\n")
	for _, want := range []string{
		"PROJECT_DIR=" + d.cfg.ProjectDir,
		"ALBERT_API_KEY=key",
		"OPENCODE_SERVER_PASSWORD=pw",
		"OPENCODE_SERVER_USERNAME=opencode",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("compose env missing %q; got:\n%s", want, env)
		}
	}
}

func TestDockerComposePreservesEmptyPassword(t *testing.T) {
	r := &fakeRunner{}
	d := NewDockerRuntime(Config{
		APIKey:      "key",
		ProjectDir:  t.TempDir(),
		Username:    "opencode",
		Password:    "",
		PasswordSet: true,
	})
	d.Runner = r
	d.AssetsDir = t.TempDir()
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	env := strings.Join(r.lastEnv, "\n")
	if !strings.Contains(env, "OPENCODE_SERVER_PASSWORD=\n") && !strings.HasSuffix(env, "OPENCODE_SERVER_PASSWORD=") {
		t.Fatalf("empty password not preserved in compose env:\n%s", env)
	}
}

func TestDockerMaterializesAssets(t *testing.T) {
	d := newTestDocker(t, &fakeRunner{})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, name := range []string{"Dockerfile", "docker-compose.yml"} {
		if _, err := os.Stat(filepath.Join(d.AssetsDir, name)); err != nil {
			t.Fatalf("asset %s not materialized: %v", name, err)
		}
	}
}

func TestDockerIsRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Join(args, " ") == "container top albert-opencode-sandbox" {
			return ExecResult{ExitCode: 0}
		}
		return ExecResult{ExitCode: 1}
	}}
	d := newTestDocker(t, r)
	on, err := d.IsRunning(context.Background())
	if err != nil || !on {
		t.Fatalf("IsRunning = %v, %v; want true", on, err)
	}
}

func TestDockerIsNotRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1}
	}}
	d := newTestDocker(t, r)
	on, err := d.IsRunning(context.Background())
	if err != nil || on {
		t.Fatalf("IsRunning = %v, %v; want false", on, err)
	}
}

func TestDockerEndpoint(t *testing.T) {
	d := NewDockerRuntime(Config{})
	ep, err := d.Endpoint(context.Background())
	if err != nil || ep != "http://localhost:4096" {
		t.Fatalf("Endpoint = %q, %v", ep, err)
	}
}
