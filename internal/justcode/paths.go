package justcode

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultStateDir returns the host state directory shared by the backends:
// ~/.local/state/just-code.
func DefaultStateDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "just-code")
	}
	return filepath.Join(".local", "state", "just-code")
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

// AgentVMStageDir holds the agent-vm backend's staged files (the secrets env
// pushed into the guest). It never contains the checkout or its .env.
func AgentVMStageDir(stateDir string) string {
	return filepath.Join(stateDir, "agent-vm")
}

// AgentVMLogPath is the host log for the agent-vm backend.
func AgentVMLogPath(stateDir string) string {
	return filepath.Join(stateDir, "agent-vm.log")
}

// AgentVMTemplateMarkerPath is the completion marker for a just-code-built
// base template. It is written on the host only after provisioning and the
// final stop succeed, so its presence distinguishes a finished template from
// one left behind by an interrupted build. A marker is needed in addition to
// the in-guest checks because a killed process cannot run its own rollback.
func AgentVMTemplateMarkerPath(stateDir, template string) string {
	return filepath.Join(stateDir, "agent-vm-template-"+safeFileName(template)+".ready")
}

// safeFileName reduces a name to characters safe in a filename, so an unusual
// AGENT_VM_TEMPLATE value cannot escape the state directory or collide with
// another marker.
func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "template"
	}
	return b.String()
}
