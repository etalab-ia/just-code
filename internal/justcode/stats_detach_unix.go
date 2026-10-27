//go:build !windows

package justcode

import (
	"os/exec"
	"syscall"
)

// newDetachedCommand starts the flusher in its own session, so the finishing
// wizard is not its parent in the terminal sense and cannot wait for it.
func newDetachedCommand(exe string, args ...string) *exec.Cmd {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
