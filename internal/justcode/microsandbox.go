package justcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	msbImage   = "ghcr.io/anomalyco/opencode:latest"
	msbSandbox = "albert-opencode-sandbox"

	// msbGuestEntrypoint is the container entrypoint inside the microVM. It is
	// what launches `opencode serve`.
	msbGuestEntrypoint = "/.msb/scripts/start"
	// msbRelaunchCommand restarts that entrypoint from inside a live VM.
	// Microsandbox keeps a VM across restarts but runs the entrypoint only at
	// creation, so a VM that came back from a restart reports `running` with no
	// backend process listening: "VM up" is not "backend ready".
	//
	// The script is invoked through `sh` because the Go SDK stores script
	// bodies verbatim (no shebang is prepended, unlike the Rust builder and
	// the CLI), and sandboxes created before the shebang was added to
	// guest-prep.sh persist a script that cannot be exec'd directly.
	// The trailing `sleep 1` makes the launcher's own survival observable:
	// a bare `nohup ... &` exits 0 even when the script dies immediately.
	msbRelaunchCommand = "nohup sh " + msbGuestEntrypoint + " >/var/log/opencode.log 2>&1 & sleep 1"

	// msbLaunchAttempts bounds the retry while the guest agent catches up with
	// a freshly started VM.
	msbLaunchAttempts   = 10
	msbLaunchRetryDelay = 2 * time.Second
	msbDoctorTimeout    = 5 * time.Minute

	// msbAllowHosts restricts where the ALBERT_API_KEY secret may be
	// substituted: only the Albert API host ever sees the real value.
	msbAllowHost = "albert.api.etalab.gouv.fr"
)

// MicrosandboxRuntime runs the OpenCode backend in a named Microsandbox
// microVM through the embedded Microsandbox Go SDK. No `msb` CLI install is
// required: the runtime downloads to ~/.microsandbox/ on first use. The real
// ALBERT_API_KEY stays on the host; only the secret-proxy substitution enters
// the microVM, and only for the Albert API host.
type MicrosandboxRuntime struct {
	cfg Config
	// Client is the Microsandbox seam. Tests inject a fake; production uses the
	// SDK adapter (see microsandbox_sdk.go).
	Client msbClient
	// Probe overrides the health probe. It exists so tests can decide the
	// outcome of the "VM running, is the backend alive?" check without binding
	// a real port.
	Probe func(ctx context.Context, endpoint, username, password string) HealthProbe
	// launchRetryDelay is configurable for tests; production uses two seconds.
	launchRetryDelay time.Duration
}

// NewMicrosandboxRuntime builds a Microsandbox backend with production defaults.
func NewMicrosandboxRuntime(cfg Config) *MicrosandboxRuntime {
	return &MicrosandboxRuntime{
		cfg:              cfg,
		Client:           sdkMSBClient{},
		launchRetryDelay: msbLaunchRetryDelay,
	}
}

func (m *MicrosandboxRuntime) ID() Runtime { return RuntimeMicrosandbox }

// msbClient is the Microsandbox surface just-code uses, narrowed from the SDK
// so tests can fake it without a real microVM runtime.
type msbClient interface {
	// EnsureInstalled downloads the microsandbox runtime if missing.
	EnsureInstalled(ctx context.Context) error
	// Doctor runs the diagnostic command from the SDK-managed runtime.
	Doctor(ctx context.Context) (string, error)
	// Lookup returns the managed sandbox's status, or false if it does not exist.
	Lookup(ctx context.Context, name string) (msbSandboxInfo, bool, error)
	// Create creates and boots the sandbox with the given spec.
	Create(ctx context.Context, spec msbSandboxSpec) error
	// Start boots a stopped sandbox.
	Start(ctx context.Context, name string) error
	// ModifyNextStart persists env changes for the next boot.
	ModifyNextStart(ctx context.Context, name string, env map[string]string, apiKey string) error
	// Exec runs a shell command in the sandbox, returning its exit code and stderr.
	Exec(ctx context.Context, name, command string) (int, string, error)
	// Stop gracefully stops a running sandbox.
	Stop(ctx context.Context, name string) error
	// Remove deletes the sandbox and its local state.
	Remove(ctx context.Context, name string) error
	// WorkspaceMount returns the host path mounted at the guest /workspace.
	WorkspaceMount(ctx context.Context, name string) (string, error)
	// StartScript returns the persisted start script of an existing sandbox,
	// or "" when it cannot be read. The script is fixed at creation, so it is
	// how the sandbox's original isolation mode is detected.
	StartScript(ctx context.Context, name string) (string, error)
	// GuestEnv returns the persisted guest env of an existing sandbox, or an
	// error when the config cannot be read. It backs the plaintext-key
	// migration guard: old full-mode sandboxes stored the real
	// ALBERT_API_KEY there.
	GuestEnv(ctx context.Context, name string) (map[string]string, error)
	// Logs streams sandbox logs to the terminal until interrupted.
	Logs() error
	// Shell opens an interactive shell in the sandbox.
	Shell() error
	// AttachInteractive runs a command interactively in the sandbox with a
	// working directory, blocking until it exits. It is the TUI channel used
	// by isolation full; Shell() is the /bin/bash special case of it.
	AttachInteractive(ctx context.Context, name, cwd string) (int, error)
}

// msbSandboxInfo is the status snapshot of the managed sandbox.
type msbSandboxInfo struct {
	Name   string
	Status string
}

// msbSandboxSpec is the full sandbox configuration passed to the SDK.
type msbSandboxSpec struct {
	Image       string
	Env         map[string]string
	Workspace   string   // host path bind-mounted at /workspace
	APIKey      string   // host-side secret value, never a guest environment entry
	AllowHosts  []string // hosts allowed to see the real secret value
	StartScript string   // guest start script body
}

func (m *MicrosandboxRuntime) Start(ctx context.Context) error {
	if m.cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := os.MkdirAll(m.cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := CheckWorkspaceGate(ctx, m.cfg.WorkspaceDir); err != nil {
		return err
	}
	if err := m.Client.EnsureInstalled(ctx); err != nil {
		return err
	}

	sandbox, exists, err := m.Client.Lookup(ctx, msbSandbox)
	if err != nil {
		return err
	}
	if exists {
		m.warnIfWorkspaceMountIsStale(ctx)
		// The mode-specific start script is persisted at creation and the SDK
		// cannot rewrite it, so switching isolation on an existing sandbox
		// would boot the wrong process (a full-mode sandbox would start
		// `opencode serve`; a backend-mode one would only sleep). Fail with
		// guidance instead of booting into the wrong mode.
		if err := m.rejectIsolationMismatch(ctx, sandbox); err != nil {
			return err
		}
	}

	if exists && sandbox.Status == "running" {
		if m.cfg.Isolation == IsolationFull {
			// No health endpoint exists in full mode: the sandbox only keeps
			// the VM alive and the TUI is attached afterwards, so a plain
			// "running" VM is the ready state. Probing here would always
			// report a dead backend and relaunch into the wrong mode.
			fmt.Printf("%s is running (isolation full; the TUI runs inside the microVM).\n", msbSandbox)
			return nil
		}
		if m.backendHealthy(ctx) {
			fmt.Printf("%s is running with a healthy OpenCode backend.\n", msbSandbox)
			return nil
		}
		fmt.Printf("%s is running but the OpenCode backend is not responding; restarting it inside the microVM...\n", msbSandbox)
		return m.launchBackend(ctx)
	}

	if exists {
		fmt.Printf("Starting %s...\n", msbSandbox)
		if err := m.rejectPlaintextKeySandbox(ctx, sandbox); err != nil {
			return err
		}
		if err := m.Client.ModifyNextStart(ctx, msbSandbox, m.nextStartEnv(), m.cfg.APIKey); err != nil {
			return err
		}
		if err := m.Client.Start(ctx, msbSandbox); err != nil {
			return err
		}
		if m.cfg.Isolation == IsolationFull {
			fmt.Printf("%s started (isolation full; the TUI runs inside the microVM).\n", msbSandbox)
			return nil
		}
		// Booting a stopped VM does not re-run the container entrypoint, so the
		// backend has to be launched explicitly.
		fmt.Printf("Launching OpenCode inside %s...\n", msbSandbox)
		return m.launchBackend(ctx)
	}

	fmt.Printf("Creating %s microVM...\n", msbSandbox)
	fmt.Println("First start installs the toolchain inside the microVM (build-base, node, python); this can take several minutes.")
	if err := m.Client.Create(ctx, m.sandboxSpec()); err != nil {
		return err
	}
	// `msb create` boots an idle VM: the Go SDK has no equivalent of the Rust
	// SDK's transient LaunchIntent::Background, so the persisted start script
	// (which installs the toolchain and then execs `opencode serve`, or keeps
	// the VM alive in full mode) is never run by creation itself. Launch it
	// explicitly, the same way a restarted VM would.
	fmt.Printf("Launching the start script inside %s...\n", msbSandbox)
	return m.launchBackend(ctx)
}

// sandboxSpec builds the full sandbox configuration from the config and the
// embedded OpenCode config and start script.
func (m *MicrosandboxRuntime) sandboxSpec() msbSandboxSpec {
	return msbSandboxSpec{
		Image:       msbImage,
		Env:         m.sandboxEnv(),
		Workspace:   m.cfg.WorkspaceDir,
		APIKey:      m.cfg.APIKey,
		AllowHosts:  []string{msbAllowHost},
		StartScript: msbStartScript(m.cfg.Isolation),
	}
}

// nextStartEnv builds the guest env persisted for the next boot of an
// existing stopped sandbox. The real ALBERT_API_KEY is never in it: the
// network proxy substitutes the secret placeholder on requests to the
// allowed host, in both isolation modes.
func (m *MicrosandboxRuntime) nextStartEnv() map[string]string {
	return map[string]string{
		"OPENCODE_SERVER_PASSWORD": m.cfg.Password,
		"OPENCODE_SERVER_USERNAME": m.cfg.Username,
	}
}

// sandboxEnv builds the guest environment for the sandbox. The real
// ALBERT_API_KEY is deliberately absent in both isolation modes: the guest
// only ever sees the secret placeholder, and the network proxy substitutes
// the real value on requests to the allowed host. The in-guest TUI of
// isolation full goes through the same substitution as the backend mode's
// `opencode serve`.
func (m *MicrosandboxRuntime) sandboxEnv() map[string]string {
	return map[string]string{
		"OPENCODE_SERVER_PASSWORD": m.cfg.Password,
		"OPENCODE_SERVER_USERNAME": m.cfg.Username,
		"OPENCODE_CONFIG_CONTENT":  opencodeConfigContent,
	}
}

// launchBackend runs the container entrypoint inside a live VM. Recreating the
// sandbox runs it automatically; booting an existing one does not. The guest
// agent can lag the VM by a moment after start, hence the bounded retry.
//
// The command backgrounds the entrypoint and then sleeps one second, so a
// launcher that dies immediately (missing toolchain, ENOEXEC on an old
// shebang-less script) surfaces as a nonzero exit here instead of a silent
// success that only the downstream health wait would catch, minutes later.
func (m *MicrosandboxRuntime) launchBackend(ctx context.Context) error {
	delay := m.launchRetryDelay
	if delay == 0 {
		delay = msbLaunchRetryDelay
	}
	var last error
	for attempt := 1; attempt <= msbLaunchAttempts; attempt++ {
		code, stderr, err := m.Client.Exec(ctx, msbSandbox, msbRelaunchCommand)
		if err == nil && code == 0 {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("sandbox exec exited %d: %s", code, strings.TrimSpace(stderr))
		}
		if attempt == msbLaunchAttempts {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("failed to launch OpenCode inside %s: %w", msbSandbox, last)
}

// backendHealthy probes the backend that should be listening in the VM.
func (m *MicrosandboxRuntime) backendHealthy(ctx context.Context) bool {
	endpoint, err := m.Endpoint(ctx)
	if err != nil {
		return false
	}
	probe := m.Probe
	if probe == nil {
		probe = func(ctx context.Context, endpoint, username, password string) HealthProbe {
			return ProbeHealth(ctx, nil, endpoint, username, password)
		}
	}
	return probe(ctx, endpoint, m.cfg.Username, m.cfg.Password).Healthy
}

// warnIfWorkspaceMountIsStale compares the VM's /workspace mount with the
// configured workspace. Mounts are fixed when a sandbox is created, so a
// sandbox keeps whichever host directory it was created with.
func (m *MicrosandboxRuntime) warnIfWorkspaceMountIsStale(ctx context.Context) {
	mounted, err := m.Client.WorkspaceMount(ctx, msbSandbox)
	if err != nil || mounted == "" {
		return
	}
	mountedClean := filepath.Clean(mounted)
	workspaceClean := filepath.Clean(m.cfg.WorkspaceDir)
	if mountedClean == workspaceClean || (runtime.GOOS == "windows" && strings.EqualFold(mountedClean, workspaceClean)) {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: %s was created with /workspace mounted from %s, but the workspace is now %s. "+
		"Mounts are fixed when a sandbox is created, so /workspace will not reflect the new directory. "+
		"Run 'just-code restart --microsandbox' to recreate it.\n", msbSandbox, mounted, m.cfg.WorkspaceDir)
}

// rejectIsolationMismatch compares the isolation mode the sandbox was
// created in (inferred from its persisted start script) with the requested
// mode. The script cannot be rewritten in place, so a mismatch is an error:
// booting would run the wrong process for the requested mode.
func (m *MicrosandboxRuntime) rejectIsolationMismatch(ctx context.Context, sandbox msbSandboxInfo) error {
	script, err := m.Client.StartScript(ctx, sandbox.Name)
	if err != nil || script == "" {
		// Undetectable (older runtime, unreadable config): proceed as before
		// rather than blocking every start on a best-effort check.
		return nil
	}
	createdFull := strings.Contains(script, "sleep infinity")
	if createdFull == (m.cfg.Isolation == IsolationFull) {
		return nil
	}
	createdMode := IsolationBackend
	requestedMode := m.cfg.Isolation
	if createdFull {
		createdMode = IsolationFull
	}
	if requestedMode == "" {
		requestedMode = IsolationBackend
	}
	return fmt.Errorf("%s was created in isolation %s mode and cannot be switched to %s mode in place: "+
		"the start script is fixed when the sandbox is created. "+
		"Run 'just-code restart --microsandbox' (or 'just-code clean --microsandbox') to recreate it in %s mode",
		sandbox.Name, createdMode, requestedMode, requestedMode)
}

// rejectPlaintextKeySandbox refuses to boot a sandbox whose persisted config
// still carries the real ALBERT_API_KEY as a guest env entry. Full-mode
// sandboxes created before the secret-proxy migration stored the key in
// plaintext in the guest environment; booting them would re-expose it.
// Removing the entry would also drop the key the guest needs, since the
// secret proxy was never registered for these sandboxes, so the only safe
// path is recreation. The check reads the raw persisted JSON because the
// Go SDK's Config() decode does not surface the spec's env array.
func (m *MicrosandboxRuntime) rejectPlaintextKeySandbox(ctx context.Context, sandbox msbSandboxInfo) error {
	env, err := m.Client.GuestEnv(ctx, sandbox.Name)
	if err != nil {
		// Undetectable (older runtime, unreadable config): proceed as before
		// rather than blocking every start on a best-effort check.
		return nil
	}
	for key := range env {
		if key == "ALBERT_API_KEY" {
			return fmt.Errorf("%s was created before the secret-proxy migration and still has the real ALBERT_API_KEY persisted in its guest environment; "+
				"booting it would expose the key inside the microVM. "+
				"Run 'just-code clean --microsandbox' (or 'just-code restart --microsandbox') to recreate it with placeholder-only injection",
				sandbox.Name)
		}
	}
	return nil
}

func (m *MicrosandboxRuntime) Stop(ctx context.Context) error {
	running, err := m.IsRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		fmt.Printf("%s is not running.\n", msbSandbox)
		return nil
	}
	fmt.Printf("Stopping %s...\n", msbSandbox)
	return m.Client.Stop(ctx, msbSandbox)
}

func (m *MicrosandboxRuntime) Restart(ctx context.Context) error {
	// Preflight the workspace before the destructive Clean: a rejected
	// workspace must not cost the sandbox and its persistent state.
	if err := CheckWorkspaceGate(ctx, m.cfg.WorkspaceDir); err != nil {
		return err
	}
	if err := m.Clean(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *MicrosandboxRuntime) Clean(ctx context.Context) error {
	_, exists, err := m.Client.Lookup(ctx, msbSandbox)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s does not exist.\n", msbSandbox)
		return nil
	}
	// Disposing is destructive: say so before and after, so a silent success
	// can never be mistaken for "nothing happened".
	fmt.Printf("Removing %s (sandbox and local state)...\n", msbSandbox)
	if err := m.Client.Remove(ctx, msbSandbox); err != nil {
		return err
	}
	fmt.Printf("%s removed.\n", msbSandbox)
	return nil
}

// Doctor verifies the embedded runtime. Unlike lifecycle commands, its output
// must reach the user even on success — a doctor that prints nothing is
// indistinguishable from one that did not run. A context bounds the check so a
// hung runtime download cannot stall the command.
func (m *MicrosandboxRuntime) Doctor(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, msbDoctorTimeout)
	defer cancel()
	if err := m.Client.EnsureInstalled(dctx); err != nil {
		if dctx.Err() != nil {
			return fmt.Errorf("runtime installation timed out after %s", msbDoctorTimeout)
		}
		return err
	}
	output, err := m.Client.Doctor(dctx)
	if err != nil {
		return err
	}
	fmt.Print(output)
	if !strings.HasSuffix(output, "\n") {
		fmt.Println()
	}
	fmt.Println("Microsandbox runtime is ready.")
	return nil
}

func (m *MicrosandboxRuntime) Logs() error {
	return m.Client.Logs()
}

func (m *MicrosandboxRuntime) Shell() error {
	return m.Client.Shell()
}

// RunAgent launches the OpenCode TUI in the foreground inside the microVM,
// with /workspace as its working directory (isolation full). The SDK attach
// channel passes the host terminal through.
func (m *MicrosandboxRuntime) RunAgent(ctx context.Context) error {
	code, err := m.Client.AttachInteractive(ctx, "opencode", "/workspace")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("opencode TUI exited %d inside %s", code, msbSandbox)
	}
	return nil
}

// Status describes the sandbox's current state, for `check` in isolation
// full where there is no health endpoint to probe.
func (m *MicrosandboxRuntime) Status(ctx context.Context) (string, error) {
	sandbox, exists, err := m.Client.Lookup(ctx, msbSandbox)
	if err != nil {
		return "", err
	}
	if !exists {
		return fmt.Sprintf("%s does not exist", msbSandbox), nil
	}
	return fmt.Sprintf("%s is %s", msbSandbox, sandbox.Status), nil
}

func (m *MicrosandboxRuntime) IsRunning(ctx context.Context) (bool, error) {
	sandbox, exists, err := m.Client.Lookup(ctx, msbSandbox)
	if err != nil {
		return false, err
	}
	return exists && sandbox.Status == "running", nil
}

func (m *MicrosandboxRuntime) Endpoint(context.Context) (string, error) {
	return "http://localhost:" + strconv.Itoa(DefaultPort), nil
}
