package justcode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/etalab-ia/just-code/assets"
)

// Tart orchestrates the OpenCode backend inside a Tart macOS VM. Runner and
// Starter are injectable so the lifecycle logic is testable without a real
// hypervisor.
type Tart struct {
	Config         Config
	Runner         Runner
	Starter        Starter
	StateDir       string // host state dir; default ~/.local/state/just-code
	GuestBootstrap string // bootstrap path inside the guest

	KillPollInterval time.Duration
	KillMaxPolls     int
}

// NewTart builds a Tart orchestrator with production defaults.
func NewTart(cfg Config) *Tart {
	return &Tart{
		Config:           cfg,
		Runner:           OSRunner{},
		Starter:          OSStarter{},
		StateDir:         DefaultStateDir(),
		GuestBootstrap:   "/Volumes/My Shared Files/just-code/tart-bootstrap.sh",
		KillPollInterval: time.Second,
		KillMaxPolls:     10,
	}
}

// LogPath returns the host path of the Tart backend log.
func (t *Tart) LogPath() string {
	return TartLogPath(t.StateDir)
}

// ParseTartList extracts local, running VMs under prefix from `tart list`
// output. Columns are: source, name, ip, disk, size, state.
func ParseTartList(output, prefix string) []string {
	var vms []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "local" {
			continue
		}
		name := fields[1]
		if !strings.HasPrefix(name, prefix) || fields[len(fields)-1] != "running" {
			continue
		}
		vms = append(vms, name)
	}
	return vms
}

// RunningVMs returns the local, running managed VMs.
func (t *Tart) RunningVMs(ctx context.Context) ([]string, error) {
	res, err := t.Runner.Run(ctx, "tart", "list")
	if err != nil {
		return nil, err
	}
	return ParseTartList(res.Stdout, managedVMPrefix), nil
}

// IP returns the NAT IP of a VM, mirroring `tart ip --wait 60 <vm>`.
func (t *Tart) IP(ctx context.Context, vm string) (string, error) {
	res, err := t.Runner.Run(ctx, "tart", "ip", "--wait", "60", vm)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// vmRunning reports whether a specific VM is local and running.
func (t *Tart) vmRunning(ctx context.Context, vm string) (bool, error) {
	vms, err := t.RunningVMs(ctx)
	if err != nil {
		return false, err
	}
	for _, v := range vms {
		if v == vm {
			return true, nil
		}
	}
	return false, nil
}

// vmExists reports whether a VM is known locally in any state.
func (t *Tart) vmExists(ctx context.Context) (bool, error) {
	res, err := t.Runner.Run(ctx, "tart", "list")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "local" && fields[1] == t.Config.TartVM {
			return true, nil
		}
	}
	return false, nil
}

// Stop stops every local managed VM (not just the current one), mirroring
// `just stop --tart`.
func (t *Tart) Stop(ctx context.Context) error {
	vms, err := t.RunningVMs(ctx)
	if err != nil {
		return err
	}
	if len(vms) == 0 {
		fmt.Println("No just-code Tart VM is running.")
		return nil
	}
	exitCode := 0
	for _, vm := range vms {
		fmt.Printf("Stopping %s...\n", vm)
		res, err := t.Runner.Run(ctx, "tart", "stop", vm, "--timeout", "5")
		if err != nil {
			if exitCode == 0 {
				exitCode = 1
			}
			continue
		}
		if res.ExitCode != 0 {
			exitCode = res.ExitCode
		}
	}
	if exitCode != 0 {
		return fmt.Errorf("tart stop failed (exit %d)", exitCode)
	}
	return nil
}

// StopBackend terminates a stale opencode process inside the VM: SIGTERM via
// pkill, then SIGKILL after KillMaxPolls failed pgrep checks. It returns an
// error if the process is still alive afterward.
func (t *Tart) StopBackend(ctx context.Context) error {
	vm := t.Config.TartVM
	// SIGTERM; "no process" (nonzero exit) is expected and ignored.
	_, _ = t.Runner.Run(ctx, "tart", "exec", vm, "pkill", "-x", "opencode")

	for i := 0; ; i++ {
		res, err := t.Runner.Run(ctx, "tart", "exec", vm, "pgrep", "-x", "opencode")
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			break // process exited
		}
		if i+1 >= t.KillMaxPolls {
			fmt.Fprintln(os.Stderr, "opencode ignored SIGTERM; force-killing...")
			_, _ = t.Runner.Run(ctx, "tart", "exec", vm, "pkill", "-9", "-x", "opencode")
			break
		}
		time.Sleep(t.KillPollInterval)
	}

	res, err := t.Runner.Run(ctx, "tart", "exec", vm, "pgrep", "-x", "opencode")
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return fmt.Errorf("failed to stop the previous opencode process; refusing to relaunch")
	}
	return nil
}

// stageBootstrap writes the embedded tart-bootstrap.sh into a dedicated
// read-only share so the guest never sees the checkout, its .env, or other
// host-only files.
func (t *Tart) stageBootstrap() error {
	stageDir := TartStageDir(t.StateDir)
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}
	data, err := assets.Read("tart-bootstrap.sh")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stageDir, "tart-bootstrap.sh"), data, 0o644)
}

// BackendArgs returns the argv for
// `tart exec -i <vm> /bin/sh <guest> <port> <username> <mtu>`.
// Secrets are deliberately absent: they travel on stdin only.
func BackendArgs(vm, guestBootstrap, port, username, mtu string) []string {
	return []string{"exec", "-i", vm, "/bin/sh", guestBootstrap, port, username, mtu}
}

// SecretsReader returns the bootstrap stdin payload: password on line 1, API
// key on line 2.
func SecretsReader(password, apiKey string) io.Reader {
	return strings.NewReader(password + "\n" + apiKey + "\n")
}

// launchBackend streams the credentials over stdin and starts the OpenCode
// server inside the guest in the background.
func (t *Tart) launchBackend(ctx context.Context) error {
	cfg := t.Config
	fmt.Printf("Launching OpenCode server inside %s...\n", cfg.TartVM)
	if err := os.MkdirAll(t.StateDir, 0o755); err != nil {
		return err
	}
	stdin := SecretsReader(cfg.Password, cfg.APIKey)
	args := BackendArgs(cfg.TartVM, t.GuestBootstrap, strconv.Itoa(DefaultPort), cfg.Username, cfg.TartMTU)
	return t.Starter.Start(stdin, t.LogPath(), "tart", args...)
}

// waitForAgent polls `tart exec <vm> true` until the guest agent responds, up
// to 60 seconds.
func (t *Tart) waitForAgent(ctx context.Context) error {
	for i := 0; i < 60; i++ {
		res, err := t.Runner.Run(ctx, "tart", "exec", t.Config.TartVM, "true")
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timed out waiting for %s guest agent", t.Config.TartVM)
}

func (t *Tart) clone(ctx context.Context) error {
	res, err := t.Runner.Run(ctx, "tart", "clone", t.Config.TartImage, t.Config.TartVM)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tart clone failed (exit %d)", res.ExitCode)
	}
	return nil
}

func (t *Tart) startVM(ctx context.Context) error {
	args := []string{
		"run", "--no-graphics",
		"--dir=workspace:" + t.Config.ProjectDir,
		"--dir=just-code:" + TartStageDir(t.StateDir) + ":ro",
		t.Config.TartVM,
	}
	return t.Starter.Start(nil, t.LogPath(), "tart", args...)
}

// Start brings the OpenCode backend up, mirroring the Tart branch of
// `just start` and `just code`.
func (t *Tart) Start(ctx context.Context) error {
	cfg := t.Config
	if cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := ValidateMTU(cfg.TartMTU); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.ProjectDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(t.StateDir, 0o755); err != nil {
		return err
	}

	running, err := t.vmRunning(ctx, cfg.TartVM)
	if err != nil {
		return err
	}
	if running {
		if err := t.waitForAgent(ctx); err != nil {
			return err
		}
		ip, err := t.IP(ctx, cfg.TartVM)
		if err == nil && ip != "" {
			client := &http.Client{Timeout: 5 * time.Second}
			endpoint := "http://" + ip + ":" + strconv.Itoa(DefaultPort)
			if healthy, _ := probeHealthy(ctx, client, endpoint, cfg.Username, cfg.Password); healthy {
				fmt.Printf("%s is running with a healthy OpenCode backend.\n", cfg.TartVM)
				return nil
			}
		}
		fmt.Printf("%s is running but OpenCode is not healthy; restarting backend...\n", cfg.TartVM)
		if err := t.StopBackend(ctx); err != nil {
			return err
		}
		if err := t.stageBootstrap(); err != nil {
			return err
		}
		return t.launchBackend(ctx)
	}

	exists, err := t.vmExists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("Cloning %s to %s...\n", cfg.TartImage, cfg.TartVM)
		if err := t.clone(ctx); err != nil {
			return err
		}
	}

	if err := t.stageBootstrap(); err != nil {
		return err
	}
	fmt.Printf("Starting %s with Tart...\n", cfg.TartVM)
	if err := t.startVM(ctx); err != nil {
		return err
	}
	if err := t.waitForAgent(ctx); err != nil {
		return err
	}
	return t.launchBackend(ctx)
}

// Build pulls or updates the base image, mirroring `just build --tart`.
func (t *Tart) Build(ctx context.Context) error {
	res, err := t.Runner.Run(ctx, "tart", "pull", t.Config.TartImage)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tart pull failed (exit %d)", res.ExitCode)
	}
	return nil
}

// Clean stops and deletes the VM and its writable state, mirroring
// `just clean --tart`.
func (t *Tart) Clean(ctx context.Context) error {
	vm := t.Config.TartVM
	running, err := t.vmRunning(ctx, vm)
	if err != nil {
		return err
	}
	if running {
		if _, err := t.Runner.Run(ctx, "tart", "stop", vm, "--timeout", "5"); err != nil {
			return err
		}
	}
	exists, err := t.vmExists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s does not exist.\n", vm)
		return nil
	}
	res, err := t.Runner.Run(ctx, "tart", "delete", vm)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tart delete failed (exit %d)", res.ExitCode)
	}
	return nil
}

// Doctor verifies the Tart installation, mirroring `just doctor --tart`.
func (t *Tart) Doctor(ctx context.Context) error {
	res, err := t.Runner.Run(ctx, "tart", "--version")
	if err != nil {
		return err
	}
	fmt.Print(res.Stdout)
	fmt.Println("Tart runtime is ready.")
	return nil
}

func (t *Tart) ID() Runtime { return RuntimeTart }

// Restart recreates the VM from scratch, mirroring `just restart --tart`.
func (t *Tart) Restart(ctx context.Context) error {
	if err := t.Clean(ctx); err != nil {
		return err
	}
	return t.Start(ctx)
}

// Logs follows the backend log, mirroring `just logs --tart`.
func (t *Tart) Logs() error {
	return RunInteractive("tail", "-f", t.LogPath())
}

// Shell opens an interactive shell in the VM, mirroring `just shell --tart`.
func (t *Tart) Shell() error {
	return RunInteractive("tart", "exec", "-it", t.Config.TartVM, "/bin/zsh")
}

// IsRunning reports whether any managed Tart VM is running.
func (t *Tart) IsRunning(ctx context.Context) (bool, error) {
	vms, err := t.RunningVMs(ctx)
	if err != nil {
		if commandNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return len(vms) > 0, nil
}

// Endpoint returns the backend URL for the managed VM.
func (t *Tart) Endpoint(ctx context.Context) (string, error) {
	ip, err := t.IP(ctx, t.Config.TartVM)
	if err != nil {
		return "", err
	}
	return "http://" + ip + ":" + strconv.Itoa(DefaultPort), nil
}
