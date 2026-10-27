//go:build windows

package justcode

import (
	"os/exec"
	"syscall"
)

// DETACHED_PROCESS keeps the flusher independent of the finishing wizard's
// console.
const detachedProcess = 0x00000008

func newDetachedCommand(exe string, args ...string) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	return cmd
}
