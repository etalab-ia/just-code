package justcode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tart orchestrates the OpenCode backend inside a Tart macOS VM. Runner and
// Starter are injectable so the lifecycle logic is testable without a real
// hypervisor.
type Tart struct {
	Config   Config
	Runner   Runner
	Starter  Starter
	StateDir string // host state dir; default ~/.local/state/just-code
	// Instance is the project-derived instance name (P05/P06). Empty keeps
	// the legacy singleton VM name from the config, so existing users' VMs
	// are found and never renamed.
	Instance string
	// SelfBinary is the binary staged into the guest. Empty means the running
	// executable (os.Executable), which is the production path.
	SelfBinary string
	// Interactive runs a foreground command attached to the terminal. It is a
	// seam for tests; production uses RunInteractive.
	Interactive func(name string, args ...string) error
	// CredentialRead reads a stored credential; production uses
	// readStoredWith. It is a test seam.
	CredentialRead credentialReader
	// OpenCodeOverlay is the managed OpenCode configuration layer (P10).
	OpenCodeOverlay ManagedOverlay

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
		KillPollInterval: time.Second,
		KillMaxPolls:     10,
	}
}

// NewTartForInstance builds a Tart orchestrator bound to a project-derived
// instance name (P06). The VM name becomes opencode-<instance>, and logs and
// staged files live under the instance's own state directory.
func NewTartForInstance(cfg Config, instance string) *Tart {
	t := NewTart(cfg)
	t.Instance = instance
	return t
}

// VMName returns the name of the VM this backend operates on: the project-
// derived name when an instance is set, the legacy config name otherwise.
func (t *Tart) VMName() string {
	if t.Instance != "" {
		return ManagedVMName(t.Instance)
	}
	return t.Config.TartVM
}

// IsLegacy reports whether this backend operates on the legacy singleton VM.
func (t *Tart) IsLegacy() bool { return t.Instance == "" }

// LogPath returns the host path of the Tart backend log. The legacy
// singleton keeps its historical path; a project instance logs under its
// own state directory.
func (t *Tart) LogPath() string {
	if t.IsLegacy() {
		return TartLogPath(t.StateDir)
	}
	return filepath.Join(InstanceStateDir(t.StateDir, t.Instance), "tart.log")
}

// StageDir returns the host directory for files staged into the guest.
func (t *Tart) StageDir() string {
	if t.IsLegacy() {
		return TartStageDir(t.StateDir)
	}
	return filepath.Join(InstanceStateDir(t.StateDir, t.Instance), "tart")
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
		if len(fields) >= 2 && fields[0] == "local" && fields[1] == t.VMName() {
			return true, nil
		}
	}
	return false, nil
}

// Stop stops this backend's VM. It is project-scoped (P06): stopping project
// A's VM never touches project B's. The all-instance sweep lives in
// StopInstance over RunningInstances, driven by the dispatcher.
func (t *Tart) Stop(ctx context.Context) error {
	return t.StopInstance(ctx, t.VMName())
}

// RunningInstances returns the names of all managed Tart VMs that are up,
// in deterministic order. Managed means carrying the just-code prefix.
func (t *Tart) RunningInstances(ctx context.Context) ([]string, error) {
	vms, err := t.RunningVMs(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(vms)
	return vms, nil
}

// StopInstance stops a named managed VM. It is the per-project form of Stop.
func (t *Tart) StopInstance(ctx context.Context, vm string) error {
	running, err := t.vmRunning(ctx, vm)
	if err != nil {
		return err
	}
	if !running {
		fmt.Printf("%s is not running.\n", vm)
		return nil
	}
	fmt.Printf("Stopping %s...\n", vm)
	res, err := t.Runner.Run(ctx, "tart", "stop", vm, "--timeout", "5")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tart stop failed (exit %d)", res.ExitCode)
	}
	return nil
}

// StopAll stops every running managed Tart VM, the explicit all-instance
// operation (P06). The legacy singleton is included when it carries the
// managed prefix, which it does by construction.
func (t *Tart) StopAll(ctx context.Context) error {
	vms, err := t.RunningInstances(ctx)
	if err != nil {
		return err
	}
	if len(vms) == 0 {
		fmt.Println("No just-code Tart VM is running.")
		return nil
	}
	var firstErr error
	for _, vm := range vms {
		if err := t.StopInstance(ctx, vm); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// StopBackend terminates a stale opencode process inside the VM: SIGTERM via
// pkill, then SIGKILL after KillMaxPolls failed pgrep checks. It returns an
// error if the process is still alive afterward.
func (t *Tart) StopBackend(ctx context.Context) error {
	vm := t.VMName()
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

// stageGuestBinary copies the running binary into the read-only share so the
// guest can execute it as the in-VM bootstrap. The checkout, its .env, and
// other host-only files are never shared.
func (t *Tart) stageGuestBinary() error {
	stageDir := t.StageDir()
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}
	src := t.SelfBinary
	if src == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		src = exe
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stageDir, guestBinaryName), data, 0o755)
}

// guestBinaryName is the file name of the staged binary inside the share.
const guestBinaryName = "just-code"

// guestRun runs a command inside the VM synchronously.
func (t *Tart) guestRun(ctx context.Context, args ...string) error {
	full := append([]string{"exec", t.VMName()}, args...)
	return runOK(t.Runner, ctx, "tart", full...)
}

// BackendArgs returns the argv for the detached bootstrap invocation:
// `tart exec -i <vm> <local binary> __guest-bootstrap <port> <username> <mtu> <model> <gitName> <gitEmail>`.
// Secrets are deliberately absent: they travel on stdin only. The model
// selection (P10) and the git identity (P11) ride argv: both are non-secret
// managed configuration.
func BackendArgs(vm, localBinary, port, username, mtu, model, gitName, gitEmail string) []string {
	return []string{"exec", "-i", vm, localBinary, GuestBootstrapCommand, port, username, mtu, model, gitName, gitEmail}
}

// hostGitIdentity reads the git identity (P11, written by setup) from the
// host user settings. The guest helpers cannot read it themselves: they run
// inside the VM, where the host settings file does not exist, so the
// identity travels on argv like the model — never on stdin (it is not a
// secret, but stdin is the secrets channel and must stay unambiguous).
func hostGitIdentity() (name, email string) {
	path, err := UserSettingsPath()
	if err != nil {
		return "", ""
	}
	us, err := ReadUserSettings(DefaultFS, path)
	if err != nil {
		return "", ""
	}
	return us.GitName, us.GitEmail
}

// SecretsReader returns the bootstrap stdin payload: password on line 1, API
// key on line 2.
func SecretsReader(password, apiKey string) io.Reader {
	return strings.NewReader(password + "\n" + apiKey + "\n")
}

// launchBackend stages the binary, copies it to the guest's local disk, and
// starts the OpenCode server inside the guest in the background.
func (t *Tart) launchBackend(ctx context.Context) error {
	cfg := t.Config
	vm := t.VMName()
	fmt.Printf("Launching OpenCode server inside %s...\n", vm)
	if err := os.MkdirAll(t.StateDir, 0o755); err != nil {
		return err
	}
	if err := t.stageGuestBinary(); err != nil {
		return err
	}

	// Executing straight from the virtiofs share is not reliably supported, so
	// the payload is copied onto the guest's local disk first.
	if err := t.guestRun(ctx, "/bin/cp", guestShareBinary, guestLocalBinary); err != nil {
		return err
	}
	if err := t.guestRun(ctx, "/bin/chmod", "755", guestLocalBinary); err != nil {
		return err
	}

	// The credential is resolved at launch time (credentialRef, legacy
	// environment, or the store) and travels on stdin only — never argv.
	key, _, _, _, err := t.resolveAlbertFor(ctx)
	if err != nil {
		return err
	}
	stdin := SecretsReader(cfg.Password, key)
	name, email := hostGitIdentity()
	args := BackendArgs(vm, guestLocalBinary, strconv.Itoa(DefaultPort), cfg.Username, cfg.TartMTU, t.OpenCodeOverlay.EffectiveOverlay().Model, name, email)
	return t.Starter.Start(stdin, t.LogPath(), "tart", args...)
}

// waitForAgent polls `tart exec <vm> true` until the guest agent responds, up
// to 60 seconds.
func (t *Tart) waitForAgent(ctx context.Context) error {
	for i := 0; i < 60; i++ {
		res, err := t.Runner.Run(ctx, "tart", "exec", t.VMName(), "true")
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timed out waiting for %s guest agent", t.VMName())
}

func (t *Tart) clone(ctx context.Context) error {
	res, err := t.Runner.Run(ctx, "tart", "clone", t.Config.TartImage, t.VMName())
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
		"--dir=workspace:" + t.Config.WorkspaceDir,
		"--dir=just-code:" + t.StageDir() + ":ro",
		t.VMName(),
	}
	return t.Starter.Start(nil, t.LogPath(), "tart", args...)
}

// Start brings the OpenCode backend up, mirroring the Tart branch of
// `just start` and `just code`. In isolation full it only brings the VM up:
// the TUI is launched interactively by RunAgent, so no server is started.
// validateConfig checks the deterministic start-time configuration
// (credential, acknowledgement, MTU). Restart calls it before the destructive
// Clean so a configuration error cannot destroy a VM that Start would then
// refuse to recreate.
//
// Tart has no secret proxy: the credential travels into the guest in
// plaintext (stdin at launch, 0600 env file for the TUI), where any process —
// the agent included — can read and exfiltrate it. Starting therefore
// requires the explicit --acknowledge-guest-credentials flag (P09, issue
// #74); the warning stays so the choice is visible on every start.
func (t *Tart) validateConfig(ctx context.Context) error {
	if !t.Config.GuestCredentialsAcknowledged {
		return fmt.Errorf("tart hands the Albert credential to the guest in plaintext, where any process (the agent included) can read it; " +
			"pass --acknowledge-guest-credentials to accept this, or use --microsandbox, which keeps the credential behind the secret proxy")
	}
	if _, _, _, _, err := t.resolveAlbertFor(ctx); err != nil {
		return err
	}
	warnUnprotectedRuntime("Tart")
	return ValidateMTU(t.Config.TartMTU)
}

// resolveAlbertFor routes the Albert resolution through the test seam.
func (t *Tart) resolveAlbertFor(ctx context.Context) (string, bindingSource, string, string, error) {
	if t.CredentialRead != nil {
		return resolveAlbertWith(t.CredentialRead, ctx, t.Config, "")
	}
	return resolveAlbert(ctx, t.Config, "")
}

func (t *Tart) Start(ctx context.Context) error {
	cfg := t.Config
	if err := t.validateConfig(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := CheckWorkspaceGate(ctx, cfg.WorkspaceDir); err != nil {
		return err
	}
	if err := os.MkdirAll(t.StateDir, 0o755); err != nil {
		return err
	}

	vm := t.VMName()
	running, err := t.vmRunning(ctx, vm)
	if err != nil {
		return err
	}
	if running {
		if err := t.waitForAgent(ctx); err != nil {
			return err
		}
		if cfg.Isolation == IsolationFull {
			fmt.Printf("%s is running (isolation full; the TUI runs inside the VM).\n", vm)
			return nil
		}
		ip, err := t.IP(ctx, vm)
		if err == nil && ip != "" {
			client := &http.Client{Timeout: 5 * time.Second}
			endpoint := "http://" + ip + ":" + strconv.Itoa(DefaultPort)
			if ProbeHealth(ctx, client, endpoint, cfg.Username, cfg.Password).Healthy {
				fmt.Printf("%s is running with a healthy OpenCode backend.\n", vm)
				return nil
			}
		}
		fmt.Printf("%s is running but OpenCode is not healthy; restarting backend...\n", vm)
		if err := t.StopBackend(ctx); err != nil {
			return err
		}
		return t.launchBackend(ctx)
	}

	exists, err := t.vmExists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("Cloning %s to %s...\n", cfg.TartImage, vm)
		if err := t.clone(ctx); err != nil {
			return err
		}
	}

	// Stage before booting so the share already holds the binary.
	if err := t.stageGuestBinary(); err != nil {
		return err
	}
	fmt.Printf("Starting %s with Tart...\n", vm)
	if err := t.startVM(ctx); err != nil {
		return err
	}
	if err := t.waitForAgent(ctx); err != nil {
		return err
	}
	if cfg.Isolation == IsolationFull {
		fmt.Printf("%s is running (isolation full; the TUI runs inside the VM).\n", vm)
		return nil
	}
	return t.launchBackend(ctx)
}

// Clean stops and deletes the VM and its writable state, mirroring
// `just clean --tart`.
func (t *Tart) Clean(ctx context.Context) error {
	vm := t.VMName()
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
	// Disposing is destructive: say so before and after, so a silent success
	// can never be mistaken for "nothing happened".
	fmt.Printf("Deleting %s (VM and local state)...\n", vm)
	res, err := t.Runner.Run(ctx, "tart", "delete", vm)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tart delete failed (exit %d)", res.ExitCode)
	}
	fmt.Printf("%s deleted.\n", vm)
	return nil
}

// Doctor verifies the Tart installation, mirroring `just doctor --tart`.
func (t *Tart) Doctor(ctx context.Context) error {
	res, err := t.Runner.Run(ctx, "tart", "--version")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tart is not installed")
	}
	fmt.Print(res.Stdout)
	fmt.Println("Tart runtime is ready.")
	return nil
}

func (t *Tart) ID() Runtime { return RuntimeTart }

// Restart is non-destructive (P07): it stops and starts the existing VM,
// preserving disk and guest state. The preflights run first so a rejected
// workspace or a bad config aborts before anything is touched. The
// destructive rebuild is Recreate.
func (t *Tart) Restart(ctx context.Context) error {
	if err := t.RestartPreflights(ctx); err != nil {
		return err
	}
	if err := t.Stop(ctx); err != nil {
		return err
	}
	return t.Start(ctx)
}

// Recreate is the explicit destructive rebuild of the VM (P07). It names the
// loss before and after. No lifecycle path reaches it implicitly.
func (t *Tart) Recreate(ctx context.Context) error {
	fmt.Printf("Recreating %s. This DESTROYS: guest sessions, tools installed in the guest, and guest-only files.\n", t.VMName())
	if err := t.RestartPreflights(ctx); err != nil {
		return err
	}
	if err := t.Clean(ctx); err != nil {
		return err
	}
	if err := t.Start(ctx); err != nil {
		return err
	}
	fmt.Printf("%s recreated.\n", t.VMName())
	return nil
}

// RestartPreflights runs the deterministic prechecks shared by the lifecycle
// commands: configuration validation, workspace creation and the workspace
// gate. A rejected workspace or bad config must abort before any destructive
// step, so the VM and its persistent state survive.
func (t *Tart) RestartPreflights(ctx context.Context) error {
	if err := t.validateConfig(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(t.Config.WorkspaceDir, 0o755); err != nil {
		return err
	}
	return CheckWorkspaceGate(ctx, t.Config.WorkspaceDir)
}

// Logs follows the backend log, mirroring `just logs --tart`.
func (t *Tart) Logs() error {
	return RunInteractive("tail", "-f", t.LogPath())
}

// Shell opens an interactive shell in the VM, mirroring `just shell --tart`.
func (t *Tart) Shell() error {
	return RunInteractive("tart", "exec", "-it", t.VMName(), "/bin/zsh")
}

// RunAgent launches the OpenCode TUI in the foreground inside the VM
// (isolation full). Secrets are pushed first via the staged guest binary
// (stdin only, never argv), then the TUI runs under `tart exec -it`, sourcing
// the 0600 env file and execing opencode in the workspace share.
func (t *Tart) RunAgent(ctx context.Context) error {
	cfg := t.Config
	vm := t.VMName()
	if err := t.stageGuestBinary(); err != nil {
		return err
	}
	// Copy the staged binary to guest-local disk (the share is not reliably
	// executable), then push secrets and prepare the guest.
	if err := t.guestRun(ctx, "/bin/cp", guestShareBinary, guestLocalBinary); err != nil {
		return err
	}
	if err := t.guestRun(ctx, "/bin/chmod", "755", guestLocalBinary); err != nil {
		return err
	}
	key, _, _, _, err := t.resolveAlbertFor(ctx)
	if err != nil {
		return err
	}
	model := t.OpenCodeOverlay.EffectiveOverlay().Model
	if err := runStdinOK(t.Runner, ctx, SecretsReader(cfg.Password, key),
		// argv: __guest-secrets <username> <model> — the consumer reads
		// Username at position 1 and Model at position 2.
		"tart", "exec", "-i", vm, guestLocalBinary, GuestSecretsCommand, cfg.Username, model); err != nil {
		return err
	}
	name, email := hostGitIdentity()
	// argv: __guest-prepare <username> <gitName> <gitEmail> — the consumer
	// reads Username at 1, GitName at 2, GitEmail at 3.
	if err := t.guestRun(ctx, guestLocalBinary, GuestPrepareCommand, cfg.Username, name, email); err != nil {
		return err
	}
	interactive := t.Interactive
	if interactive == nil {
		interactive = RunInteractive
	}
	return interactive("tart", "exec", "-it", vm, "/bin/zsh", "-lc", tartAgentLaunch(guestSecretsEnvPath, guestWorkspaceDir))
}

// tartAgentLaunch builds the in-guest launch line for isolation full: source
// the 0600 secrets env file, cd into the workspace share, exec the TUI. Both
// paths are shell-quoted: the workspace share contains spaces, and the line
// runs under `zsh -lc`.
func tartAgentLaunch(secretsPath, workspaceDir string) string {
	return fmt.Sprintf("set -a; . %s; set +a; cd %s; exec opencode", shellQuote(secretsPath), shellQuote(workspaceDir))
}

// Status describes the managed VM's current state, for `check` in isolation
// full where there is no health endpoint to probe.
func (t *Tart) Status(ctx context.Context) (string, error) {
	running, err := t.vmRunning(ctx, t.VMName())
	if err != nil {
		return "", err
	}
	if running {
		return fmt.Sprintf("%s is running", t.VMName()), nil
	}
	exists, err := t.vmExists(ctx)
	if err != nil {
		return "", err
	}
	if exists {
		return fmt.Sprintf("%s is stopped", t.VMName()), nil
	}
	return fmt.Sprintf("%s does not exist", t.VMName()), nil
}

// IsRunning reports whether the VM bound to this project is running.
// Scope la vérification à la VM du projet (t.VMName()) : compter toutes les
// VM gérées ferait dire « running » pour un projet sans VM dès qu'un autre
// projet en a une d'active.
func (t *Tart) IsRunning(ctx context.Context) (bool, error) {
	vms, err := t.RunningVMs(ctx)
	if err != nil {
		if commandNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, vm := range vms {
		if vm == t.VMName() {
			return true, nil
		}
	}
	return false, nil
}

// Endpoint returns the backend URL for the managed VM.
func (t *Tart) Endpoint(ctx context.Context) (string, error) {
	ip, err := t.IP(ctx, t.VMName())
	if err != nil {
		return "", err
	}
	return "http://" + ip + ":" + strconv.Itoa(DefaultPort), nil
}
