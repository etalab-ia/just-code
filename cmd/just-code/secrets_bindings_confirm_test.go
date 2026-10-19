package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withApproveConfirmSeams installs a TTY plus a confirm function so approval
// can be exercised in both directions. The returned pointer records how many
// times the prompt was consulted, which is what distinguishes "no prompt was
// declared" from "a prompt was declined".
func withApproveConfirmSeams(t *testing.T, tty, answer bool) (root string, promptCalls *int) {
	t.Helper()
	var calls int
	root, _, _ = withSecretsCLISeams(t)
	origTTY, origConfirm := projectSecretsTTYFn, projectSecretsConfirmFn
	projectSecretsTTYFn = func() bool { return tty }
	projectSecretsConfirmFn = func(string, string, bool) (bool, error) {
		calls++
		return answer, nil
	}
	t.Cleanup(func() {
		projectSecretsTTYFn, projectSecretsConfirmFn = origTTY, origConfirm
	})
	return root, &calls
}

// TestBindingsApproveRequiresYesWhenNonInteractive pins the head of #209:
// --yes was declared and parsed but never read, so no prompt existed at all.
// The fix makes a non-interactive approval require it explicitly, matching the
// rule `secrets import --apply` already follows.
func TestBindingsApproveRequiresYesWhenNonInteractive(t *testing.T) {
	root, calls := withApproveConfirmSeams(t, false, true)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("API_KEY=value-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, err := secretsBindingsCmd([]string{"approve", "development", ".env", "API_KEY", "--destination", "api.example.com", "--root", root}); code != 2 || err == nil {
		t.Fatalf("non-interactive approval without --yes must be refused: code=%d err=%v", code, err)
	}
	if *calls != 0 {
		t.Fatalf("no prompt may be attempted without a TTY, got %d", *calls)
	}
}

// TestBindingsApprovePromptsOnTTY pins the body of #209: on a terminal the
// approval asks first, and a declined prompt records nothing.
func TestBindingsApprovePromptsOnTTY(t *testing.T) {
	root, calls := withApproveConfirmSeams(t, true, false)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("API_KEY=value-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Import first so the binding would otherwise resolve.
	if code, err := projectSecretsImportCmd([]string{"--root", root, "--profile", "development", "--file", ".env", "--apply", "--yes"}); code != 0 || err != nil {
		t.Fatalf("import: code=%d err=%v", code, err)
	}

	out := captureSecretsStdout(t, func() {
		if code, err := secretsBindingsCmd([]string{"approve", "development", ".env", "API_KEY", "--destination", "api.example.com", "--root", root}); code != 0 || err != nil {
			t.Fatalf("a declined prompt is a normal outcome: code=%d err=%v", code, err)
		}
	})
	if *calls != 1 {
		t.Fatalf("a TTY approval must ask exactly once, got %d", *calls)
	}
	if !strings.Contains(out, "Cancelled") {
		t.Fatalf("a declined approval must say so: %q", out)
	}
	// Nothing may be recorded when the prompt is declined.
	list := captureSecretsStdout(t, func() {
		if code, err := secretsBindingsCmd([]string{"list", "--root", root}); code != 0 || err != nil {
			t.Fatalf("list: code=%d err=%v", code, err)
		}
	})
	if strings.Contains(list, "api.example.com") {
		t.Fatalf("a declined approval must not be recorded: %q", list)
	}
}
