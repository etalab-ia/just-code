//go:build !windows

package justcode

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A FIFO named like a config must stay in discovery (so the trust flow can
// block on it) while the read refuses it instead of hanging on the open.
func TestParseProjectOpenCodeConfigRefusesFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	if got := FindProjectOpenCodeConfig(dir); got != path {
		t.Fatalf("discovery must keep reporting the FIFO, got %q", got)
	}
	if _, err := ParseProjectOpenCodeConfig(path); err == nil {
		t.Fatal("parse must refuse a FIFO instead of hanging on it")
	}
	if _, err := DiscoverProjectExecutionInputs(dir); err == nil {
		t.Fatal("input discovery must fail closed on a FIFO config")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the FIFO must be left in place, not consumed: %v", err)
	}
}
