package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		action  string
		runtime string
		version bool
	}{
		{"bare attaches", nil, "attach", "", false},
		{"leading runtime flag attaches", []string{"--tart"}, "attach", "--tart", false},
		{"agent-vm leading runtime flag", []string{"--agent-vm"}, "attach", "--agent-vm", false},
		{"runtime flag then command", []string{"--microsandbox", "start"}, "start", "--microsandbox", false},
		{"agent-vm command then runtime flag", []string{"start", "--agent-vm"}, "start", "--agent-vm", false},
		{"command then runtime flag", []string{"start", "--microsandbox"}, "start", "--microsandbox", false},
		{"explicit start", []string{"start", "--microsandbox"}, "start", "--microsandbox", false},
		{"stop", []string{"stop"}, "stop", "", false},
		{"check", []string{"check"}, "check", "", false},
		{"logs", []string{"logs", "--tart"}, "logs", "--tart", false},
		{"help", []string{"help"}, "help", "", false},
		{"help flag", []string{"--help"}, "help", "", false},
		{"version subcommand", []string{"version"}, "version", "", false},
		{"version long flag", []string{"--version"}, "attach", "", true},
		{"version short flag", []string{"-V"}, "attach", "", true},
		{"version lowercase alias", []string{"-v"}, "attach", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseArgs(c.args)
			if err != nil {
				t.Fatalf("parseArgs(%v): %v", c.args, err)
			}
			if got.action != c.action {
				t.Errorf("action = %q, want %q", got.action, c.action)
			}
			if got.runtime != c.runtime {
				t.Errorf("runtime = %q, want %q", got.runtime, c.runtime)
			}
			if got.version != c.version {
				t.Errorf("version = %v, want %v", got.version, c.version)
			}
		})
	}
}

// TestParseArgsRejectsCode covers the deliberate break with the old CLI: the
// `code` command is gone and the error explains the replacement.
func TestParseArgsRejectsCode(t *testing.T) {
	_, err := parseArgs([]string{"code"})
	if err == nil {
		t.Fatal("expected an error for the removed `code` command")
	}
	if !strings.Contains(err.Error(), "'code' command was removed") {
		t.Fatalf("error = %q, want the migration message", err)
	}
}

func TestParseArgsRejectsUnknown(t *testing.T) {
	for _, args := range [][]string{{"foo"}, {"--podman"}, {"start", "stop"}, {"--docker", "--tart"}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v): expected an error", args)
		}
	}
}

// TestParseArgsRejectsDocker covers the removal of the Docker runtime: --docker
// is no longer a known flag and must be reported as such.
func TestParseArgsRejectsDocker(t *testing.T) {
	_, err := parseArgs([]string{"--docker"})
	if err == nil || !strings.Contains(err.Error(), "Unknown argument: --docker") {
		t.Fatalf("error = %v, want an unknown-argument error for --docker", err)
	}
}

func TestParseArgsConflictingRuntimes(t *testing.T) {
	for _, args := range [][]string{{"--microsandbox", "--tart"}, {"--tart", "--agent-vm"}} {
		_, err := parseArgs(args)
		if err == nil || !strings.Contains(err.Error(), "Select exactly one runtime") {
			t.Fatalf("parseArgs(%v) = %v, want the runtime conflict message", args, err)
		}
	}
}

// TestRunNeedsNoRuntimeForLocalActions guards a regression where the dispatch
// resolved a runtime before handling actions that do not need one: `just-code
// version` used to fail with "select --microsandbox or --tart, ...".
func TestRunNeedsNoRuntimeForLocalActions(t *testing.T) {
	t.Setenv("RUNTIME", "")
	for _, args := range [][]string{{"version"}, {"help"}, {"-V"}} {
		if _, err := run(args); err != nil {
			t.Errorf("run(%v) = %v, want no error", args, err)
		}
	}
}

// TestApplyIsolationPrecedence pins the flag-over-environment contract at the
// entry point: an explicit --isolation value wins even when ISOLATION in .env
// is invalid, so a typo is always recoverable from the command line. The
// resolved value is applied to the returned config, which is what the backends
// are constructed from.
func TestApplyIsolationPrecedence(t *testing.T) {
	brokenEnv := justcode.Config{
		Isolation:    justcode.IsolationBackend,
		IsolationErr: fmt.Errorf("ISOLATION must be backend or full, got %q", "sometimes"),
	}

	// No flag, broken env: the error surfaces and the config is untouched.
	got, err := applyIsolation(brokenEnv, "")
	if err == nil {
		t.Fatal("an invalid ISOLATION must be surfaced when no flag is given")
	}
	if got.Isolation != justcode.IsolationBackend {
		t.Errorf("failed resolution must not change the level, got %q", got.Isolation)
	}

	// Explicit flag, broken env: the flag wins and the level is applied.
	got, err = applyIsolation(brokenEnv, "full")
	if err != nil {
		t.Fatalf("an explicit flag must override an invalid ISOLATION: %v", err)
	}
	if got.Isolation != justcode.IsolationFull {
		t.Errorf("Isolation = %q, want full (the resolved flag value)", got.Isolation)
	}
	if got.IsolationErr != nil {
		t.Errorf("IsolationErr must be cleared once the flag resolves the level: %v", got.IsolationErr)
	}

	// Explicit flag, valid env: the flag still wins.
	got, err = applyIsolation(justcode.Config{Isolation: justcode.IsolationBackend}, "full")
	if err != nil || got.Isolation != justcode.IsolationFull {
		t.Errorf("applyIsolation(valid env, full) = (%q, %v)", got.Isolation, err)
	}
}

// TestParseArgsStopAll pins the --all flag: it parses on the stop command and
// is rejected as a duplicate.
func TestParseArgsStopAll(t *testing.T) {
	p, err := parseArgs([]string{"stop", "--all"})
	if err != nil {
		t.Fatalf("parseArgs(stop --all): %v", err)
	}
	if p.action != "stop" || !p.stopAll {
		t.Fatalf("parseArgs(stop --all) = %+v, want stop with stopAll", p)
	}
	if _, err := parseArgs([]string{"stop", "--all", "--all"}); err == nil {
		t.Fatal("duplicate --all must be rejected")
	}
}

// TestParseArgsAllAcceptedAnywhere pins that --all, like the runtime flags,
// may appear in any position.
func TestParseArgsAllAcceptedAnywhere(t *testing.T) {
	p, err := parseArgs([]string{"--all", "stop"})
	if err != nil {
		t.Fatalf("parseArgs(--all stop): %v", err)
	}
	if p.action != "stop" || !p.stopAll {
		t.Fatalf("parseArgs(--all stop) = %+v", p)
	}
}

// TestWorkspaceSourceDefaultsToProjectRoot pins the P12 sealed-source rule: a
// zero-flag launch transfers the project the user is in, while an explicit
// WORKSPACE_DIR is never overridden.
func TestWorkspaceSourceDefaultsToProjectRoot(t *testing.T) {
	root := t.TempDir()
	pc := justcode.ProjectContext{Root: root, Name: "proj"}

	got := withProjectRootAsWorkspaceSource(justcode.Config{WorkspaceDir: "/configured"}, pc, nil)
	if got.WorkspaceDir != root {
		t.Fatalf("a zero-flag launch must use the project root, got %q", got.WorkspaceDir)
	}

	explicit := justcode.Config{WorkspaceDir: "/configured", WorkspaceDirSet: true}
	if got := withProjectRootAsWorkspaceSource(explicit, pc, nil); got.WorkspaceDir != "/configured" {
		t.Fatalf("an explicit source must be honoured, got %q", got.WorkspaceDir)
	}

	// Discovery failure leaves the value alone rather than guessing. This is
	// the case with NO explicit configuration, which is the one that would
	// otherwise be replaced by an empty root.
	unset := justcode.Config{WorkspaceDir: "/default-ish"}
	if got := withProjectRootAsWorkspaceSource(unset, pc, errDiscovery); got.WorkspaceDir != "/default-ish" {
		t.Fatalf("a discovery failure must not change the source, got %q", got.WorkspaceDir)
	}
	if got := withProjectRootAsWorkspaceSource(explicit, pc, errDiscovery); got.WorkspaceDir != "/configured" {
		t.Fatalf("a discovery failure must not change an explicit source, got %q", got.WorkspaceDir)
	}
}

var errDiscovery = errors.New("not a project")
