package justcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AgentVM orchestrates the OpenCode backend inside a Lima VM cloned from an
// agent-vm base template (https://github.com/sylvinus/agent-vm). just-code
// drives `limactl` directly rather than sourcing agent-vm.sh: the only
// agent-vm artifact it needs is the base template, built by the user with
// `agent-vm setup`. Runner and Starter are injectable so the lifecycle logic
// is testable without Lima.
type AgentVM struct {
	Config   Config
	Runner   Runner
	Starter  Starter
	StateDir string // host state dir; default ~/.local/state/just-code

	// KillPollInterval and KillMaxPolls bound the stale-backend kill loop.
	KillPollInterval time.Duration
	KillMaxPolls     int
}

// NewAgentVM builds an agent-vm orchestrator with production defaults.
func NewAgentVM(cfg Config) *AgentVM {
	return &AgentVM{
		Config:           cfg,
		Runner:           OSRunner{},
		Starter:          OSStarter{},
		StateDir:         DefaultStateDir(),
		KillPollInterval: time.Second,
		KillMaxPolls:     10,
	}
}

// LogPath returns the host path of the agent-vm backend log.
func (a *AgentVM) LogPath() string {
	return AgentVMLogPath(a.StateDir)
}

// ParseLimaList parses `limactl list --format '{{.Name}}|{{.Status}}'` output
// into name -> running. A VM in any other state (Stopped, Breakout...) is
// present but not running.
func ParseLimaList(output string) map[string]bool {
	vms := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}
		vms[name] = strings.TrimSpace(fields[1]) == "Running"
	}
	return vms
}

// limaList queries `limactl list` and returns the parsed VM map. It is the
// single choke point for existence checks, so the tristate discipline lives
// here: a failed query is an error, never "the VM does not exist".
func (a *AgentVM) limaList(ctx context.Context) (map[string]bool, error) {
	res, err := a.Runner.Run(ctx, "limactl", "list", "--format", "{{.Name}}|{{.Status}}")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("limactl list failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	return ParseLimaList(res.Stdout), nil
}

// vmExists reports whether the managed VM is known to Lima in any state.
func (a *AgentVM) vmExists(ctx context.Context) (bool, error) {
	vms, err := a.limaList(ctx)
	if err != nil {
		return false, err
	}
	_, ok := vms[a.Config.AgentVMVM]
	return ok, nil
}

// vmRunning reports whether the managed VM is running.
func (a *AgentVM) vmRunning(ctx context.Context) (bool, error) {
	vms, err := a.limaList(ctx)
	if err != nil {
		return false, err
	}
	return vms[a.Config.AgentVMVM], nil
}

// templateExists reports whether the base template is available to clone from.
func (a *AgentVM) templateExists(ctx context.Context) (bool, error) {
	vms, err := a.limaList(ctx)
	if err != nil {
		return false, err
	}
	_, ok := vms[a.Config.AgentVMTemplate]
	return ok, nil
}

// mountsJSON builds the Lima mounts array for the workspace: the host
// WORKSPACE_DIR mounted writable at the same path inside the guest, mirroring
// agent-vm's per-directory mount model.
func mountsJSON(workspaceDir string) string {
	mounts := []map[string]any{{
		"location": workspaceDir,
		"writable": true,
	}}
	b, _ := json.Marshal(mounts)
	return string(b)
}

// createVM clones the base template into the managed VM and applies the
// workspace mount, mirroring agent-vm's clone-then-edit flow. Editing is
// only allowed on a stopped instance, so it happens right after the clone.
func (a *AgentVM) createVM(ctx context.Context) error {
	fmt.Printf("Cloning %s to %s...\n", a.Config.AgentVMTemplate, a.Config.AgentVMVM)
	res, err := a.Runner.Run(ctx, "limactl", "clone", a.Config.AgentVMTemplate, a.Config.AgentVMVM, "--tty=false")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("limactl clone failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	res, err = a.Runner.Run(ctx, "limactl", "edit", a.Config.AgentVMVM, "--tty=false",
		"--set", ".mounts = "+mountsJSON(a.Config.WorkspaceDir))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		// Roll back the incomplete clone: a registered VM without the
		// workspace mount is unusable, and Start would treat it as a valid
		// existing instance instead of re-creating it.
		fmt.Printf("limactl edit (mounts) failed (exit %d): %s\n", res.ExitCode, res.Stderr)
		fmt.Printf("Removing the incomplete clone %s...\n", a.Config.AgentVMVM)
		if _, delErr := a.Runner.Run(ctx, "limactl", "delete", a.Config.AgentVMVM, "--force"); delErr != nil {
			return fmt.Errorf("limactl edit (mounts) failed (exit %d): %s; cleanup also failed: %v (run 'limactl delete %s --force' manually)",
				res.ExitCode, res.Stderr, delErr, a.Config.AgentVMVM)
		}
		return fmt.Errorf("limactl edit (mounts) failed (exit %d): %s (the incomplete clone was removed; retry the start)",
			res.ExitCode, res.Stderr)
	}
	return nil
}

// startVM boots the VM detached. The Starter's log captures limactl output.
func (a *AgentVM) startVM(ctx context.Context) error {
	return a.Starter.Start(nil, a.LogPath(), "limactl", "start", a.Config.AgentVMVM)
}

// guestRun runs a command inside the VM synchronously via limactl shell.
func (a *AgentVM) guestRun(ctx context.Context, args ...string) error {
	full := append([]string{"shell", a.Config.AgentVMVM}, args...)
	return runOK(a.Runner, ctx, "limactl", full...)
}

// waitForAgent polls `limactl shell <vm> true` until the guest responds, up
// to 120 seconds. A Lima boot is slower than a microVM: the guest runs a
// full systemd.
func (a *AgentVM) waitForAgent(ctx context.Context) error {
	for i := 0; i < 120; i++ {
		res, err := a.Runner.Run(ctx, "limactl", "shell", a.Config.AgentVMVM, "true")
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timed out waiting for %s guest agent", a.Config.AgentVMVM)
}

// StopBackend terminates a stale opencode process inside the VM: SIGTERM via
// pkill, then SIGKILL after KillMaxPolls failed pgrep checks. Same recovery
// pattern as the Tart backend.
func (a *AgentVM) StopBackend(ctx context.Context) error {
	vm := a.Config.AgentVMVM
	// SIGTERM; "no process" (nonzero exit) is expected and ignored.
	_, _ = a.Runner.Run(ctx, "limactl", "shell", vm, "pkill", "-x", "opencode")

	for i := 0; ; i++ {
		res, err := a.Runner.Run(ctx, "limactl", "shell", vm, "pgrep", "-x", "opencode")
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			break // process exited
		}
		if i+1 >= a.KillMaxPolls {
			fmt.Fprintln(os.Stderr, "opencode ignored SIGTERM; force-killing...")
			_, _ = a.Runner.Run(ctx, "limactl", "shell", vm, "pkill", "-9", "-x", "opencode")
			break
		}
		time.Sleep(a.KillPollInterval)
	}

	res, err := a.Runner.Run(ctx, "limactl", "shell", vm, "pgrep", "-x", "opencode")
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return fmt.Errorf("failed to stop the previous opencode process; refusing to relaunch")
	}
	return nil
}

// writeSecretsEnv writes the backend secrets to a 0600 host file that is
// pushed into the guest with limactl copy and sourced by the launch script.
// Secrets never appear in argv or in the Lima instance config.
func (a *AgentVM) writeSecretsEnv(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "opencode.env")
	// The file is sourced by /bin/sh in the guest, so every value must be
	// single-quoted: raw values containing spaces, $(), backticks or
	// semicolons would break parsing or be evaluated.
	content := fmt.Sprintf("OPENCODE_SERVER_PASSWORD=%s\nOPENCODE_SERVER_USERNAME=%s\nALBERT_API_KEY=%s\n",
		shellQuote(a.Config.Password), shellQuote(a.Config.Username), shellQuote(a.Config.APIKey))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// shellQuote makes s safe for a POSIX shell to parse as a single-quoted
// word: wrap in single quotes, replacing embedded single quotes with the
// '\” idiom. This is the standard safe representation for arbitrary data.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// launchBackend pushes the secrets env file into the guest and starts the
// OpenCode server detached, mirroring the Tart launch flow.
func (a *AgentVM) launchBackend(ctx context.Context) error {
	cfg := a.Config
	fmt.Printf("Launching OpenCode server inside %s...\n", cfg.AgentVMVM)
	if err := os.MkdirAll(a.StateDir, 0o755); err != nil {
		return err
	}
	stageDir := AgentVMStageDir(a.StateDir)
	secretsPath, err := a.writeSecretsEnv(stageDir)
	if err != nil {
		return err
	}
	// Push the env file into the guest at a fixed location.
	if err := runOK(a.Runner, ctx, "limactl", "copy", secretsPath, cfg.AgentVMVM+":/tmp/just-code-opencode.env"); err != nil {
		return err
	}
	// Ensure the file is only readable by the guest user.
	if err := a.guestRun(ctx, "chmod", "600", "/tmp/just-code-opencode.env"); err != nil {
		return err
	}
	// Launch detached: source the env file, then exec opencode serve in the
	// background. The Starter writes output to the host-side log.
	launch := fmt.Sprintf(`set -a; . /tmp/just-code-opencode.env; set +a; exec opencode serve --hostname 0.0.0.0 --port %d`, DefaultPort)
	args := []string{"shell", cfg.AgentVMVM, "sh", "-c", launch}
	return a.Starter.Start(nil, a.LogPath(), "limactl", args...)
}

// Start brings the OpenCode backend up on the agent-vm runtime.
func (a *AgentVM) Start(ctx context.Context) error {
	cfg := a.Config
	if cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := os.MkdirAll(cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(a.StateDir, 0o755); err != nil {
		return err
	}

	running, err := a.vmRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		if err := a.waitForAgent(ctx); err != nil {
			return err
		}
		endpoint, err := a.Endpoint(ctx)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 5 * time.Second}
		if ProbeHealth(ctx, client, endpoint, cfg.Username, cfg.Password).Healthy {
			fmt.Printf("%s is running with a healthy OpenCode backend.\n", cfg.AgentVMVM)
			return nil
		}
		fmt.Printf("%s is running but OpenCode is not healthy; restarting backend...\n", cfg.AgentVMVM)
		if err := a.StopBackend(ctx); err != nil {
			return err
		}
		return a.launchBackend(ctx)
	}

	exists, err := a.vmExists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		templateOK, err := a.templateExists(ctx)
		if err != nil {
			return err
		}
		if !templateOK {
			return fmt.Errorf("base template %s not found; build it with 'agent-vm setup' (see https://github.com/sylvinus/agent-vm)", cfg.AgentVMTemplate)
		}
		if err := a.createVM(ctx); err != nil {
			return err
		}
	}

	fmt.Printf("Starting %s with Lima...\n", cfg.AgentVMVM)
	if err := a.startVM(ctx); err != nil {
		return err
	}
	if err := a.waitForAgent(ctx); err != nil {
		return err
	}
	return a.launchBackend(ctx)
}

// Stop stops the managed VM. Unlike Tart (which stops every managed VM),
// agent-vm has a single managed instance, so there is exactly one to stop.
func (a *AgentVM) Stop(ctx context.Context) error {
	running, err := a.vmRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		fmt.Println("No just-code agent-vm VM is running.")
		return nil
	}
	fmt.Printf("Stopping %s...\n", a.Config.AgentVMVM)
	res, err := a.Runner.Run(ctx, "limactl", "stop", a.Config.AgentVMVM)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("limactl stop failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	return nil
}

// Clean stops and deletes the managed VM and its local state.
func (a *AgentVM) Clean(ctx context.Context) error {
	vm := a.Config.AgentVMVM
	running, err := a.vmRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		if _, err := a.Runner.Run(ctx, "limactl", "stop", vm); err != nil {
			return err
		}
	}
	exists, err := a.vmExists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s does not exist.\n", vm)
		// The staged secrets env (Albert API key, HTTP password) is host-side
		// state and must go even when the VM was already deleted manually.
		_ = os.RemoveAll(AgentVMStageDir(a.StateDir))
		return nil
	}
	// Deleting is destructive: say so before and after, so a silent success
	// can never be mistaken for "nothing happened".
	fmt.Printf("Deleting %s (VM and local state)...\n", vm)
	res, err := a.Runner.Run(ctx, "limactl", "delete", vm, "--force")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("limactl delete failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	_ = os.RemoveAll(AgentVMStageDir(a.StateDir))
	fmt.Printf("%s deleted.\n", vm)
	return nil
}

// Doctor verifies the Lima installation and the base template, mirroring the
// pre-flight contract of the other runtimes.
func (a *AgentVM) Doctor(ctx context.Context) error {
	res, err := a.Runner.Run(ctx, "limactl", "--version")
	if err != nil {
		if commandNotFound(err) {
			return fmt.Errorf("limactl is not installed; install Lima from https://lima-vm.io and build the base template with 'agent-vm setup'")
		}
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("limactl is not installed")
	}
	fmt.Print(res.Stdout)
	templateOK, err := a.templateExists(ctx)
	if err != nil {
		return err
	}
	if !templateOK {
		return fmt.Errorf("base template %s not found; build it with 'agent-vm setup' (see https://github.com/sylvinus/agent-vm)", a.Config.AgentVMTemplate)
	}
	fmt.Printf("agent-vm runtime is ready (template %s).\n", a.Config.AgentVMTemplate)
	return nil
}

func (a *AgentVM) ID() Runtime { return RuntimeAgentVM }

// Restart recreates the VM from scratch.
func (a *AgentVM) Restart(ctx context.Context) error {
	if err := a.Clean(ctx); err != nil {
		return err
	}
	return a.Start(ctx)
}

// Logs follows the backend log.
func (a *AgentVM) Logs() error {
	return RunInteractive("tail", "-f", a.LogPath())
}

// Shell opens an interactive shell in the VM.
func (a *AgentVM) Shell() error {
	return RunInteractive("limactl", "shell", a.Config.AgentVMVM)
}

// IsRunning reports whether the managed VM is running.
func (a *AgentVM) IsRunning(ctx context.Context) (bool, error) {
	running, err := a.vmRunning(ctx)
	if err != nil {
		if commandNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return running, nil
}

// Endpoint returns the backend URL. Lima's dynamic port forwarding exposes
// guest localhost ports on the host at 127.0.0.1, so the endpoint is
// host-local and needs no IP discovery.
func (a *AgentVM) Endpoint(ctx context.Context) (string, error) {
	return "http://127.0.0.1:" + strconv.Itoa(DefaultPort), nil
}
