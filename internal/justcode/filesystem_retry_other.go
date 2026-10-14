//go:build !windows

package justcode

func isTransientFilesystemError(error) bool { return false }
