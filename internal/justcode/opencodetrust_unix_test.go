//go:build !windows

package justcode

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Non-regular inputs must be refused before the open: a FIFO would hang the
// hash and a device file would stream unbounded data into it.
func TestHashFileContentRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo-input.js")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	if _, err := hashFileContent(fifo); err == nil {
		t.Fatal("hash must refuse a FIFO")
	}
	if _, err := os.Lstat(fifo); err != nil {
		t.Fatalf("the FIFO must be left in place, not consumed: %v", err)
	}
	if _, err := os.Lstat("/dev/zero"); err != nil {
		t.Skip("no /dev/zero on this system")
	}
	if _, err := hashFileContent("/dev/zero"); err == nil {
		t.Fatal("hash must refuse a device file instead of hashing an endless stream")
	}
}
