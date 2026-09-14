package main

import (
	"strings"
	"testing"
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
		{"runtime flag then command", []string{"--docker", "start"}, "start", "--docker", false},
		{"command then runtime flag", []string{"start", "--docker"}, "start", "--docker", false},
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

func TestParseArgsConflictingRuntimes(t *testing.T) {
	_, err := parseArgs([]string{"--docker", "--tart"})
	if err == nil || !strings.Contains(err.Error(), "Select exactly one runtime") {
		t.Fatalf("error = %v, want the runtime conflict message", err)
	}
}

// TestRunNeedsNoRuntimeForLocalActions guards a regression where the dispatch
// resolved a runtime before handling actions that do not need one: `just-code
// version` used to fail with "select --docker, ...".
func TestRunNeedsNoRuntimeForLocalActions(t *testing.T) {
	t.Setenv("RUNTIME", "")
	for _, args := range [][]string{{"version"}, {"help"}, {"-V"}} {
		if _, err := run(args); err != nil {
			t.Errorf("run(%v) = %v, want no error", args, err)
		}
	}
}
