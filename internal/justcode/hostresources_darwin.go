//go:build darwin

package justcode

import "golang.org/x/sys/unix"

func hostMemoryBytes() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}
