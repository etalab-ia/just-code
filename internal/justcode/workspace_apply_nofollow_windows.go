//go:build windows

package justcode

// hostOpenNoFollow is the open(2) flag that refuses a symlink as the final
// path component. Windows has no O_NOFOLLOW; there the anchored os.Root
// still refuses any path that would escape the workspace, and symlink
// components are refused by the per-component check.
const hostOpenNoFollow = 0
