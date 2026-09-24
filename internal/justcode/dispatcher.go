package justcode

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// Dispatcher wires the runtimes together and implements the cross-runtime
// commands: stop (all), check (single), and runtime-conflict resolution.
type Dispatcher struct {
	cfg      Config
	backends map[Runtime]Backend
	// Runner is used only by the migration cleanup for the removed Docker
	// runtime; each backend carries its own.
	Runner Runner
}

// NewDispatcher builds a dispatcher over the runtimes for a config. Without
// a project context the backends operate on the legacy singleton instances,
// so existing behavior is unchanged (P06).
func NewDispatcher(cfg Config) *Dispatcher {
	return NewDispatcherWith(cfg, map[Runtime]Backend{
		RuntimeMicrosandbox: NewMicrosandboxRuntime(cfg),
		RuntimeTart:         NewTart(cfg),
		RuntimeAgentVM:      NewAgentVM(cfg),
	})
}

// NewDispatcherForInstance builds a dispatcher whose backends are all bound
// to one project-derived instance name (P06): every lifecycle operation the
// dispatcher drives targets that project's instances only.
func NewDispatcherForInstance(cfg Config, instance string) *Dispatcher {
	return NewDispatcherWith(cfg, map[Runtime]Backend{
		RuntimeMicrosandbox: NewMicrosandboxRuntimeForInstance(cfg, instance),
		RuntimeTart:         NewTartForInstance(cfg, instance),
		RuntimeAgentVM:      NewAgentVMForInstance(cfg, instance),
	})
}

// NewDispatcherWith builds a dispatcher over an explicit set of backends,
// primarily for tests.
func NewDispatcherWith(cfg Config, backends map[Runtime]Backend) *Dispatcher {
	return &Dispatcher{cfg: cfg, backends: backends, Runner: OSRunner{}}
}

// Backend returns the backend for a runtime.
func (d *Dispatcher) Backend(rt Runtime) (Backend, error) {
	b, ok := d.backends[rt]
	if !ok {
		return nil, fmt.Errorf("unknown runtime %q", rt)
	}
	return b, nil
}

// Running returns the active runtimes in deterministic order. Only runtimes
// supported on this platform are probed (see supportedRuntimes).
func (d *Dispatcher) Running(ctx context.Context) ([]Runtime, error) {
	var out []Runtime
	for _, rt := range supportedRuntimes() {
		on, err := d.backends[rt].IsRunning(ctx)
		if err != nil {
			return nil, err
		}
		if on {
			out = append(out, rt)
		}
	}
	return out, nil
}

// SingleRunning returns the sole active runtime, or an error if none or more
// than one is running.
func (d *Dispatcher) SingleRunning(ctx context.Context) (Runtime, error) {
	running, err := d.Running(ctx)
	if err != nil {
		return "", err
	}
	switch len(running) {
	case 0:
		return "", fmt.Errorf("no just-code runtime is running")
	case 1:
		return running[0], nil
	default:
		return "", fmt.Errorf("multiple just-code runtimes are running; run `just-code stop` first")
	}
}

// instanceBackend is the per-instance surface a backend exposes for the
// global sweeps (StopAll, Prepare conflict detection). It is implemented by
// the tart, agent-vm, and microsandbox backends.
type instanceBackend interface {
	RunningInstances(ctx context.Context) ([]string, error)
	StopInstance(ctx context.Context, name string) error
}

// StopAll stops every active managed instance on the host, across runtimes
// and projects, mirroring `just stop --all`. The dispatcher's backends are
// bound to the current project (P06), so their Stop is project-scoped and
// would leave other projects' instances running. The sweep therefore
// enumerates instances globally per backend (RunningInstances) and stops
// each by name (StopInstance), not the dispatcher's own instance.
func (d *Dispatcher) StopAll(ctx context.Context) error {
	if err := d.clearLegacyDocker(ctx); err != nil {
		return err
	}
	var firstErr error
	stoppedAny := false
	for _, rt := range supportedRuntimes() {
		b := d.backends[rt]
		ib, ok := b.(instanceBackend)
		if !ok {
			// Backend without per-instance enumeration (none today outside
			// tests): fall back to its project-scoped Stop when it runs.
			running, err := b.IsRunning(ctx)
			if err != nil {
				return err
			}
			if !running {
				continue
			}
			stoppedAny = true
			if err := b.Stop(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		instances, err := ib.RunningInstances(ctx)
		if err != nil {
			if commandNotFound(err) {
				continue
			}
			return err
		}
		for _, name := range instances {
			stoppedAny = true
			if err := ib.StopInstance(ctx, name); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if !stoppedAny && firstErr == nil {
		fmt.Println("No just-code runtime is running.")
	}
	return firstErr
}

// Stop stops the current project's instance on every runtime, the new
// project-scoped `stop` contract (P06). Stopping project A never touches
// project B: each backend stops only the instance it was constructed with.
// An explicit all-instance stop is StopAll (`stop --all`).
func (d *Dispatcher) Stop(ctx context.Context) error {
	if err := d.clearLegacyDocker(ctx); err != nil {
		return err
	}
	stopped := false
	for _, rt := range supportedRuntimes() {
		b, ok := d.backends[rt]
		if !ok {
			continue
		}
		running, err := b.IsRunning(ctx)
		if err != nil {
			return err
		}
		if !running {
			continue
		}
		if err := b.Stop(ctx); err != nil {
			return err
		}
		stopped = true
	}
	if !stopped {
		fmt.Println("No just-code runtime is running for this project.")
	}
	return nil
}

// Prepare resolves conflicts before starting a runtime: it migrates a host
// still carrying the removed Docker runtime's container, then, if a different
// runtime is already active, prompts to stop it (interactive) or refuses.
// Prepare detects conflicts before starting the requested runtime. Since
// P06 the backends are project-scoped: d.Running reports only the current
// project's instances, so a conflict in another project would be invisible
// and two backends could run simultaneously. Conflicts are therefore
// enumerated globally: every managed instance on the host, across runtimes
// and projects. The user can still confirm and switch runtimes; the stop
// applies to the conflicting instances by name.
func (d *Dispatcher) Prepare(ctx context.Context, requested Runtime) error {
	if err := d.clearLegacyDocker(ctx); err != nil {
		return err
	}
	conflicts, err := d.otherRunningInstances(ctx, requested)
	if err != nil {
		return err
	}
	if len(conflicts) == 0 {
		return nil
	}

	sort.Strings(conflicts)
	names := strings.Join(conflicts, " ")
	if !isTerminal(os.Stdin) {
		return fmt.Errorf("%s is already running. Run `just-code stop` before starting %s.", names, requested)
	}
	fmt.Printf("%s is already running. Stop it and start %s? [y/N] ", names, requested)
	reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "y", "yes":
		for _, name := range conflicts {
			ib, ok := d.instanceBackendFor(name)
			if !ok {
				continue
			}
			if err := ib.StopInstance(ctx, name); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("keeping %s running", names)
	}
}

// otherRunningInstances enumerates every managed instance running on the
// host, across runtimes and projects, except the current project's instance
// of the requested runtime (that is the instance Prepare is about to start;
// reusing it is legitimate). Only the current project's own instance is
// ignored: another project's instance of the SAME runtime is a conflict,
// both for the one-active-instance policy and because the per-project
// backends can contend for the same host ports.
func (d *Dispatcher) otherRunningInstances(ctx context.Context, requested Runtime) ([]string, error) {
	own := d.ownInstanceName(requested)
	var conflicts []string
	for _, rt := range supportedRuntimes() {
		b := d.backends[rt]
		ib, ok := b.(instanceBackend)
		if !ok {
			// Backend without per-instance enumeration (none today outside
			// tests): its project-scoped IsRunning is the only signal.
			if rt == requested {
				continue
			}
			running, err := b.IsRunning(ctx)
			if err != nil {
				return nil, err
			}
			if running {
				conflicts = append(conflicts, string(rt))
			}
			continue
		}
		instances, err := ib.RunningInstances(ctx)
		if err != nil {
			if commandNotFound(err) {
				continue
			}
			return nil, err
		}
		for _, name := range instances {
			if rt == requested && name == own {
				// The current project's own instance of the requested
				// runtime: not a conflict, Prepare reuses it.
				continue
			}
			conflicts = append(conflicts, name)
		}
	}
	return conflicts, nil
}

// ownInstanceName returns the current project's instance name for a runtime,
// or "" when the backend is not instance-bound.
func (d *Dispatcher) ownInstanceName(rt Runtime) string {
	switch b := d.backends[rt].(type) {
	case *Tart:
		return b.VMName()
	case *AgentVM:
		return b.VMName()
	case *MicrosandboxRuntime:
		return b.InstanceName()
	}
	return ""
}

// instanceBackendFor returns the backend able to stop the named instance.
func (d *Dispatcher) instanceBackendFor(name string) (instanceBackend, bool) {
	for _, rt := range supportedRuntimes() {
		ib, ok := d.backends[rt].(instanceBackend)
		if !ok {
			continue
		}
		if d.ownInstanceName(rt) == name {
			return ib, true
		}
		// Names not bound to this project may belong to this runtime's
		// global namespace; probe by enumeration.
		instances, err := ib.RunningInstances(context.Background())
		if err == nil {
			for _, n := range instances {
				if n == name {
					return ib, true
				}
			}
		}
	}
	return nil, false
}

// Check verifies the single running backend and reports its Albert provider
// status, mirroring `just check`. In isolation full there is no health
// endpoint to probe: it reports the VM state and that the session runs
// interactively inside the guest.
func (d *Dispatcher) Check(ctx context.Context, client *http.Client) error {
	rt, err := d.SingleRunning(ctx)
	if err != nil {
		return err
	}
	if d.cfg.Isolation == IsolationFull {
		state, err := d.backends[rt].Status(ctx)
		if err != nil {
			return err
		}
		fmt.Println(state)
		fmt.Println("isolation full: the OpenCode session runs interactively inside the guest (no health endpoint).")
		return nil
	}
	endpoint, err := d.backends[rt].Endpoint(ctx)
	if err != nil {
		return err
	}
	return CheckBackend(ctx, client, endpoint, d.cfg.Username, d.cfg.Password)
}

func runtimeNames(rts []Runtime) []string {
	out := make([]string, len(rts))
	for i, rt := range rts {
		out[i] = string(rt)
	}
	return out
}

// isTerminal reports whether f is a character device (an interactive TTY).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
