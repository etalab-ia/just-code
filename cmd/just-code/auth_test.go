package main

import (
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// CLI auth tests (P08): argument parsing and dispatch. Store behavior is
// covered by the internal package's tests; these pin the CLI surface.

func TestParseArgsAuthCapturesSubcommand(t *testing.T) {
	p, err := parseArgs([]string{"auth", "add", "--stdin"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if p.action != "auth" {
		t.Fatalf("action = %q, want auth", p.action)
	}
	if strings.Join(p.authArgs, " ") != "add --stdin" {
		t.Fatalf("authArgs = %v", p.authArgs)
	}
}

func TestParseArgsAuthStandalone(t *testing.T) {
	p, err := parseArgs([]string{"auth"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if p.action != "auth" || len(p.authArgs) != 0 {
		t.Fatalf("parsed = %+v", p)
	}
}

func TestParseAuthArgsDefaultsAndRejects(t *testing.T) {
	kind, stdin, fallback, err := parseAuthArgs(nil)
	if err != nil || kind != justcode.CredentialAlbert || stdin || fallback {
		t.Fatalf("defaults = %q, %v, %v, %v", kind, stdin, fallback, err)
	}
	if _, _, _, err := parseAuthArgs([]string{"github"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, _, _, err := parseAuthArgs([]string{"albert", "--stdin", "--fallback"}); err != nil {
		t.Fatalf("valid flags: %v", err)
	}
}

func TestAuthCmdUnknownSubcommand(t *testing.T) {
	code, err := authCmd([]string{"list"})
	if code != 2 || err == nil {
		t.Fatalf("code = %d, err = %v; want 2, error", code, err)
	}
}

func TestAuthCmdUsage(t *testing.T) {
	code, err := authCmd(nil)
	if code != 2 || err != nil {
		t.Fatalf("code = %d, err = %v; want 2, nil", code, err)
	}
}
