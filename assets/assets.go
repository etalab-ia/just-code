// Package assets holds guest resources embedded into the just-code binary.
package assets

import "embed"

//go:embed opencode-config.json start-script.sh
var FS embed.FS

// Files lists every embedded asset, in a stable order.
var Files = []string{
	"opencode-config.json",
	"start-script.sh",
}

// Read returns an embedded asset by name.
func Read(name string) ([]byte, error) {
	return FS.ReadFile(name)
}
