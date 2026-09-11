package justcode

import (
	"os"
	"path/filepath"
)

// DefaultStateDir returns the host state directory shared by the backends:
// ~/.local/state/just-code.
func DefaultStateDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "just-code")
	}
	return filepath.Join(".local", "state", "just-code")
}

// DefaultAssetsDir is where the embedded runtime assets are materialized.
func DefaultAssetsDir() string {
	return filepath.Join(DefaultStateDir(), "assets")
}

// TartStageDir is the read-only share handed to the Tart guest. It holds only
// the bootstrap script, never the checkout or its .env.
func TartStageDir(stateDir string) string {
	return filepath.Join(stateDir, "tart")
}

// TartLogPath is the host log for the Tart backend.
func TartLogPath(stateDir string) string {
	return filepath.Join(stateDir, "tart.log")
}
