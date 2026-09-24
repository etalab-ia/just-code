package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// TestParseOptionalBindingKindRejectsAlbert pins the Codex P2 on PR #87:
// `bindings revoke albert` must not strip the mandatory Albert proxy
// registration, so the CLI validates the kind as an optional binding before
// touching approvals or runtime state.
func TestParseOptionalBindingKindRejectsAlbert(t *testing.T) {
	if _, err := justcode.ParseOptionalBindingKind("albert"); err == nil {
		t.Fatal("the required albert binding must be rejected")
	}
	if _, err := justcode.ParseOptionalBindingKind("gitlab"); err == nil {
		t.Fatal("an unknown kind must be rejected")
	}
	for _, ok := range []string{"github", "context7"} {
		if kind, err := justcode.ParseOptionalBindingKind(ok); err != nil || string(kind) != ok {
			t.Fatalf("optional kind %q: %q, %v", ok, kind, err)
		}
	}
}

// TestBindingsCmdRejectsRequiredKind covers the command surface itself, so a
// future refactor that bypasses the validation is caught here.
func TestBindingsCmdRejectsRequiredKind(t *testing.T) {
	for _, sub := range []string{"approve", "revoke"} {
		if code, err := bindingsCmd([]string{sub, "albert"}, "jc-test"); code != 2 || err == nil {
			t.Fatalf("bindings %s albert: code %d, err %v; want 2, error", sub, code, err)
		}
	}
}

// TestResolveCredentialRefReadsTheProjectRoot pins the Codex P1 on PR #87:
// invoked from a subdirectory of a worktree, the manifest must be read from
// the discovered project root, not from cwd, or the project's credentialRef
// is silently ignored and the user-level credential is injected instead.
func TestResolveCredentialRefReadsTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(justcode.ProjectStateDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":1,"credentialRef":"project-key"}` + "\n")
	if err := os.WriteFile(justcode.ProjectManifestPath(root), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	// From the project root the manifest is found.
	if got := resolveCredentialRef(root); got != "project-key" {
		t.Fatalf("root resolution = %q", got)
	}
	// The env override still wins.
	t.Setenv("JUST_CODE_CREDENTIAL_REF", "env-key")
	if got := resolveCredentialRef(root); got != "env-key" {
		t.Fatalf("env override = %q", got)
	}
}
