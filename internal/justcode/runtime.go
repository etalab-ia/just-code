// Package justcode implements the just-code CLI as a tested Go library — a
// single binary replacing the original justfile across the Docker,
// Microsandbox, and Tart runtimes.
package justcode

import (
	"context"
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

// allRuntimes is the deterministic ordering used by `stop` and `check`, matching
// the justfile's _running-runtimes.
var allRuntimes = []Runtime{RuntimeDocker, RuntimeMicrosandbox, RuntimeTart}

// Backend is the lifecycle of a single sandbox backend. Docker, Microsandbox,
// and Tart each implement it.
type Backend interface {
	ID() Runtime
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Build(ctx context.Context) error
	Restart(ctx context.Context) error
	Clean(ctx context.Context) error
	Doctor(ctx context.Context) error
	// Logs and Shell are interactive and run attached to the terminal.
	Logs() error
	Shell() error
	IsRunning(ctx context.Context) (bool, error)
	// Endpoint returns the host-reachable backend URL when running.
	Endpoint(ctx context.Context) (string, error)
}

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
