package justcode

import (
	"os"
	"path/filepath"
	"testing"
)

func trustProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// A project with an auto-discovered plugin and a config declaring an MCP
	// command.
	plugin := filepath.Join(dir, ".opencode", "plugin", "hook.js")
	if err := os.MkdirAll(filepath.Dir(plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, []byte("// v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(cfg, []byte(`{"mcp":{"docs":{"command":"npx"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTrustLifecycle(t *testing.T) {
	root := trustProject(t)
	fs, stateDir := DefaultFS, t.TempDir()

	// Before approval: both inputs are unapproved.
	unapproved, err := DiscoverUnapprovedInputs(fs, stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(unapproved) != 2 {
		t.Fatalf("both inputs must be unapproved: %+v", unapproved)
	}

	// Approval records the current content.
	if err := ApproveExecutionInputs(fs, stateDir, root); err != nil {
		t.Fatal(err)
	}
	unapproved, err = DiscoverUnapprovedInputs(fs, stateDir, root)
	if err != nil || len(unapproved) != 0 {
		t.Fatalf("after approval nothing may remain: %+v, %v", unapproved, err)
	}

	// A changed file is unapproved again: approval is tied to content.
	plugin := filepath.Join(root, ".opencode", "plugin", "hook.js")
	if err := os.WriteFile(plugin, []byte("// v2 malicious"), 0o644); err != nil {
		t.Fatal(err)
	}
	unapproved, err = DiscoverUnapprovedInputs(fs, stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(unapproved) != 1 || unapproved[0].Reason != "changed since approval" {
		t.Fatalf("a changed plugin must be unapproved as changed: %+v", unapproved)
	}

	// A new auto-discovered plugin appears: unapproved.
	if err := os.WriteFile(filepath.Join(root, ".opencode", "plugin", "second.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	unapproved, err = DiscoverUnapprovedInputs(fs, stateDir, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(unapproved) != 2 {
		t.Fatalf("changed + new plugin must both be unapproved: %+v", unapproved)
	}
}

func TestTrustRecordNeverInRepository(t *testing.T) {
	root := trustProject(t)
	fs, stateDir := DefaultFS, t.TempDir()
	if err := ApproveExecutionInputs(fs, stateDir, root); err != nil {
		t.Fatal(err)
	}
	// The record exists in host state...
	path := OpenCodeTrustPath(stateDir, root)
	if _, err := fs.ReadFile(path); err != nil {
		t.Fatalf("the record must exist in host state: %v", err)
	}
	// ...and nowhere under the project root.
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Base(p) == "opencode-trust.json" {
			t.Fatalf("trust record leaked into the repository: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTrustRecordRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, ".opencode", "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(pluginDir, "link.js")); err != nil {
		t.Skip("symlinks unavailable on this platform")
	}
	fs, stateDir := DefaultFS, t.TempDir()
	err := ApproveExecutionInputs(fs, stateDir, root)
	if err == nil {
		t.Fatal("a symlinked plugin must not be approvable: the path could be repointed after approval")
	}
}
