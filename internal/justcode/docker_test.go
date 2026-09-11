package justcode

import (
	"context"
	"strings"
	"testing"
)

func TestDockerStartRequiresAPIKey(t *testing.T) {
	d := NewDockerRuntime(Config{ProjectDir: t.TempDir()})
	if err := d.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("expected API key error, got %v", err)
	}
}

func TestDockerStartCommand(t *testing.T) {
	r := &fakeRunner{}
	d := NewDockerRuntime(Config{APIKey: "key", ProjectDir: t.TempDir()})
	d.Runner = r
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(r.calls) != 1 || r.calls[0] != "docker compose up -d --quiet-pull" {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestDockerIsRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if strings.Join(args, " ") == "container top albert-opencode-sandbox" {
			return ExecResult{ExitCode: 0}
		}
		return ExecResult{ExitCode: 1}
	}}
	d := NewDockerRuntime(Config{})
	d.Runner = r
	on, err := d.IsRunning(context.Background())
	if err != nil || !on {
		t.Fatalf("IsRunning = %v, %v; want true", on, err)
	}
}

func TestDockerIsNotRunning(t *testing.T) {
	r := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1}
	}}
	d := NewDockerRuntime(Config{})
	d.Runner = r
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
