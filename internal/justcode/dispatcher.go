package justcode

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Dispatcher wires the three runtimes together and implements the cross-runtime
// commands: stop (all), check (single), and runtime-conflict resolution.
type Dispatcher struct {
	cfg      Config
	backends map[Runtime]Backend
}

// NewDispatcher builds a dispatcher over the three runtimes for a config.
func NewDispatcher(cfg Config) *Dispatcher {
	return NewDispatcherWith(cfg, map[Runtime]Backend{
		RuntimeDocker:       NewDockerRuntime(cfg),
		RuntimeMicrosandbox: NewMicrosandboxRuntime(cfg),
		RuntimeTart:         NewTart(cfg),
	})
}

// NewDispatcherWith builds a dispatcher over an explicit set of backends,
// primarily for tests.
func NewDispatcherWith(cfg Config, backends map[Runtime]Backend) *Dispatcher {
	return &Dispatcher{cfg: cfg, backends: backends}
}

// Backend returns the backend for a runtime.
func (d *Dispatcher) Backend(rt Runtime) (Backend, error) {
	b, ok := d.backends[rt]
	if !ok {
		return nil, fmt.Errorf("unknown runtime %q", rt)
	}
	return b, nil
}

// Running returns the active runtimes in deterministic order.
func (d *Dispatcher) Running(ctx context.Context) ([]Runtime, error) {
	var out []Runtime
	for _, rt := range allRuntimes {
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

// StopAll stops every active runtime, mirroring `just stop`.
func (d *Dispatcher) StopAll(ctx context.Context) error {
	running, err := d.Running(ctx)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		fmt.Println("No just-code runtime is running.")
		return nil
	}
	var firstErr error
	for _, rt := range running {
		if err := d.backends[rt].Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Prepare resolves conflicts before starting a runtime: if a different runtime
// is already active, it prompts to stop it (interactive) or refuses.
func (d *Dispatcher) Prepare(ctx context.Context, requested Runtime) error {
	running, err := d.Running(ctx)
	if err != nil {
		return err
	}
	var conflicts []Runtime
	for _, rt := range running {
		if rt != requested {
			conflicts = append(conflicts, rt)
		}
	}
	if len(conflicts) == 0 {
		return nil
	}

	names := strings.Join(runtimeNames(conflicts), " ")
	if !isTerminal(os.Stdin) {
		return fmt.Errorf("%s is already running. Run `just-code stop` before starting %s.", names, requested)
	}
	fmt.Printf("%s is already running. Stop it and start %s? [y/N] ", names, requested)
	reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "y", "yes":
		for _, rt := range conflicts {
			if err := d.backends[rt].Stop(ctx); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("keeping %s running", names)
	}
}

// Check verifies the single running backend and reports its Albert provider
// status, mirroring `just check`.
func (d *Dispatcher) Check(ctx context.Context, client *http.Client) error {
	rt, err := d.SingleRunning(ctx)
	if err != nil {
		return err
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
