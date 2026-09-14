// Package justcode implements the just-code CLI as a tested Go library — a
// single binary replacing the original justfile across the Docker,
// Microsandbox, and Tart runtimes.
package justcode

import (
	"context"
	"fmt"
	"runtime"
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

// currentGOOS is the platform indirection for tests: the fake-backends in
// dispatcher_test need to steer which runtimes get probed without rebuilding
// for Windows. It is not a knob; production callers read runtime.GOOS.
var currentGOOS = runtime.GOOS

// supportedRuntimes is the subset of allRuntimes that exists on this platform.
// Windows runs only the microsandbox runtime (via WHP): Docker Desktop is not a
// target there and Tart is macOS-only, so probing them would only surface raw
// "executable file not found" errors from `stop` and `check`.
func supportedRuntimes() []Runtime {
	return supportedRuntimesOn(currentGOOS)
}

func supportedRuntimesOn(goos string) []Runtime {
	if goos == "windows" {
		return []Runtime{RuntimeMicrosandbox}
	}
	return allRuntimes
}

// Backend is the lifecycle of a single sandbox backend. Docker, Microsandbox,
// and Tart each implement it.
type Backend interface {
	ID() Runtime
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
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
// justfile contract. On Windows the microsandbox runtime is the default and the
// only supported choice; elsewhere there is deliberately no built-in default.
func ResolveRuntime(flag, preference string) (Runtime, error) {
	return resolveRuntimeOn(flag, preference, runtime.GOOS)
}

// resolveRuntimeOn is ResolveRuntime with the platform as an argument so the
// Windows contract can be exercised from any OS.
func resolveRuntimeOn(flag, preference, goos string) (Runtime, error) {
	if flag != "" {
		return parseRuntimeFlagOn(flag, goos)
	}
	if preference != "" {
		return parseRuntimeNameOn(preference, goos)
	}
	if goos == "windows" {
		return RuntimeMicrosandbox, nil
	}
	return "", fmt.Errorf("select --docker, --microsandbox, or --tart, or set RUNTIME in .env")
}

// parseRuntimeFlag accepts --docker, --microsandbox, or --tart.
func parseRuntimeFlag(flag string) (Runtime, error) {
	return parseRuntimeFlagOn(flag, runtime.GOOS)
}

func parseRuntimeFlagOn(flag, goos string) (Runtime, error) {
	return parseRuntimeNameOn(strings.TrimPrefix(flag, "--"), goos)
}

// parseRuntimeName accepts docker, microsandbox, or tart (the RUNTIME env
// spelling) and rejects anything else with a message matching the accepted
// flag surface. Docker and Tart are macOS/Linux only; on Windows they are
// rejected with a pointer at the one runtime that does work there.
func parseRuntimeName(name string) (Runtime, error) {
	return parseRuntimeNameOn(name, runtime.GOOS)
}

func parseRuntimeNameOn(name, goos string) (Runtime, error) {
	switch name {
	case string(RuntimeDocker):
		if goos == "windows" {
			return "", fmt.Errorf("docker is not supported on Windows; use --microsandbox")
		}
		return RuntimeDocker, nil
	case string(RuntimeMicrosandbox):
		return RuntimeMicrosandbox, nil
	case string(RuntimeTart):
		if goos == "windows" {
			return "", fmt.Errorf("tart is macOS only; use --microsandbox")
		}
		return RuntimeTart, nil
	default:
		return "", fmt.Errorf("expected --docker, --microsandbox, or --tart (RUNTIME must be docker, microsandbox, or tart)")
	}
}
