package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// captureStdout captures what fn writes to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestParseArgsWorkspaceCapturesSubcommand pins the dispatch contract: the
// words after `workspace` belong to the command.
func TestParseArgsWorkspaceCapturesSubcommand(t *testing.T) {
	p, err := parseArgs([]string{"workspace", "sync", "--force"})
	if err != nil {
		t.Fatal(err)
	}
	if p.action != "workspace" {
		t.Fatalf("action = %q", p.action)
	}
	if len(p.workspaceArgs) != 2 || p.workspaceArgs[0] != "sync" || p.workspaceArgs[1] != "--force" {
		t.Fatalf("workspaceArgs = %v", p.workspaceArgs)
	}
}

// TestWorkspaceCmdRejectsUnknownSubcommand keeps a typo from silently doing
// nothing.
func TestWorkspaceCmdRejectsUnknownSubcommand(t *testing.T) {
	code, err := workspaceCmd([]string{"frobnicate"}, justcode.Config{}, "jc-x", t.TempDir())
	if code != 2 || err == nil {
		t.Fatalf("unknown subcommand must be a usage error: code=%d err=%v", code, err)
	}
}

// TestWorkspaceAllowRejectsPathOutsideTheProject pins the guard on recorded
// re-inclusions: a decision can only name a file inside the project.
func TestWorkspaceAllowRejectsPathOutsideTheProject(t *testing.T) {
	store := justcode.TransferOptInStore{Path: filepath.Join(t.TempDir(), "optin.json"), FS: justcode.DefaultFS}
	code, err := workspaceAllowCmd(store, t.TempDir(), "../outside")
	if code != 2 || err == nil {
		t.Fatalf("an escaping path must be rejected: code=%d err=%v", code, err)
	}
}

// TestWorkspaceStatusReportsFilteredSet pins the read-only review screen: it
// shows what crosses and what the filter refuses, without writing anything.
func TestWorkspaceStatusReportsFilteredSet(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=canary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("git"); err == nil {
		for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@e.com"}, {"config", "user.name", "T"}, {"add", "-A"}, {"commit", "-q", "-m", "i", "--allow-empty"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
	}

	out := captureStdout(t, func() {
		if code, err := workspaceStatusCmd(dir, "jc-x", nil); code != 0 || err != nil {
			t.Fatalf("workspaceStatusCmd: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "src/main.go") || !strings.Contains(out, ".env") {
		t.Fatalf("status must report both the crossing file and the exclusion: %q", out)
	}
	if !strings.Contains(out, "dotenv") {
		t.Fatalf("the exclusion reason must be shown: %q", out)
	}
	if !strings.Contains(out, "not mounted") {
		t.Fatalf("the status must state that the checkout is not mounted: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "transfer-optin.json")); err == nil {
		t.Fatal("a read-only status must not write state")
	}
}
