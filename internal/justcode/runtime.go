// Package justcode implements the just-code CLI as a tested Go library. This
// first port covers the Tart runtime lifecycle — the source of the shell
// fragility in the original justfile — plus the runtime-selection flag surface.
package justcode

import (
	"fmt"
	"strings"
)

// Runtime identifies a supported sandbox backend.
type Runtime string

const (
	RuntimeDocker       Runtime = "docker"
	RuntimeMicrosandbox Runtime = "microsandbox"
	RuntimeTart         Runtime = "tart"
)

// ResolveRuntime picks a runtime from an explicit --<runtime> flag and the
// RUNTIME environment preference. The explicit flag always wins, mirroring the
// justfile contract. There is deliberately no built-in default.
func ResolveRuntime(flag, preference string) (Runtime, error) {
	if flag != "" {
		return parseRuntimeFlag(flag)
	}
	if preference != "" {
		return parseRuntimeName(preference)
	}
	return "", fmt.Errorf("select --docker, --microsandbox, or --tart, or set RUNTIME in .env")
}

// parseRuntimeFlag accepts --docker, --microsandbox, or --tart.
func parseRuntimeFlag(flag string) (Runtime, error) {
	return parseRuntimeName(strings.TrimPrefix(flag, "--"))
}

// parseRuntimeName accepts docker, microsandbox, or tart (the RUNTIME env
// spelling) and rejects anything else with a message matching the accepted
// flag surface.
func parseRuntimeName(name string) (Runtime, error) {
	switch name {
	case string(RuntimeDocker):
		return RuntimeDocker, nil
	case string(RuntimeMicrosandbox):
		return RuntimeMicrosandbox, nil
	case string(RuntimeTart):
		return RuntimeTart, nil
	default:
		return "", fmt.Errorf("expected --docker, --microsandbox, or --tart (RUNTIME must be docker, microsandbox, or tart)")
	}
}
