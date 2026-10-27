//go:build !windows

package justcode

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestApplyGuestFilesWriteFailurePreservesHostFile pins the failure contract
// of the atomic write: when the content cannot be written to the end — here a
// file-size limit cuts the write mid-stream — the host file is exactly as it
// was. The write goes through a same-directory temporary, and only a fully
// written, synced temporary is ever swapped in; a truncated or failed
// temporary is discarded, never renamed over the host file.
func TestApplyGuestFilesWriteFailurePreservesHostFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.js", "old content\n")

	var orig syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &orig); err != nil {
		t.Skipf("cannot read RLIMIT_FSIZE: %v", err)
	}
	// RLIM_INFINITY is the all-ones rlim_t; comparing against ^uint64(0)
	// avoids the platform constant's typing quirks.
	if orig.Max != ^uint64(0) && orig.Max < 16 {
		t.Skipf("RLIMIT_FSIZE max (%d) is too low to test", orig.Max)
	}
	// Writes are capped at 8 bytes; the incoming content is far longer.
	lim := syscall.Rlimit{Cur: 8, Max: orig.Max}
	// SIGXFSZ's default action would kill the process; the write must be
	// an error instead, so the failure path can be observed.
	signal.Ignore(syscall.SIGXFSZ)
	t.Cleanup(func() { signal.Reset(syscall.SIGXFSZ) })
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &lim); err != nil {
		t.Skipf("cannot set RLIMIT_FSIZE: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &orig); err != nil {
			t.Fatalf("cannot restore RLIMIT_FSIZE: %v", err)
		}
	})

	files := []WorkspaceApplyFile{
		{
			Change:  GuestChange{Rel: "a.js", Status: GuestChangeModified, PreHash: gitBlobHash([]byte("old content\n"))},
			Verdict: WorkspaceApplyOK,
			Content: []byte(strings.Repeat("x", 4096)),
			Mode:    0o644,
		},
	}
	applied, failed, conflicts := ApplyGuestFiles(root, files)
	if len(applied) != 0 || len(failed) != 1 || len(conflicts) != 0 {
		t.Fatalf("the capped write must fail the file: applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.js")); string(got) != "old content\n" {
		t.Fatalf("a failed write must leave the host file intact, got %q", got)
	}
	// The failed temporary must not linger in the user's tree.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".just-code-apply-") {
			t.Fatalf("a write temporary was left behind: %s", e.Name())
		}
	}
}
