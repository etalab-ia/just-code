// Package assets holds the runtime resources embedded into the just-code
// binary: the Dockerfile, the Compose and Microsandbox configs, and the Tart
// guest bootstrap. The CLI materializes them under its state directory so a
// built binary is self-contained and does not depend on the checkout.
package assets

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed Dockerfile docker-compose.yml microsandbox.yaml tart-bootstrap.sh
var FS embed.FS

// Files lists every embedded asset, in a stable order.
var Files = []string{
	"Dockerfile",
	"docker-compose.yml",
	"microsandbox.yaml",
	"tart-bootstrap.sh",
}

// Read returns an embedded asset by name.
func Read(name string) ([]byte, error) {
	return FS.ReadFile(name)
}

// Materialize writes every embedded asset into dir and returns dir. Files are
// rewritten on each call so a binary upgrade refreshes stale copies, and each
// write goes through a temp file + rename so a concurrent reader never sees a
// torn file.
func Materialize(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, name := range Files {
		data, err := FS.ReadFile(name)
		if err != nil {
			return "", err
		}
		target := filepath.Join(dir, name)
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, target); err != nil {
			return "", err
		}
	}
	return dir, nil
}
