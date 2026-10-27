//go:build !windows

package justcode

import "syscall"

// hostOpenNoFollow is the open(2) flag that refuses a symlink as the final
// path component, so the writer can never open (or create) a file through a
// link someone swapped in. It is passed through to the anchored open the
// os.Root performs.
const hostOpenNoFollow = syscall.O_NOFOLLOW
