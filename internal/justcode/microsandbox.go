package justcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	msbImage = "ghcr.io/anomalyco/opencode:latest"

	// msbSandbox is the legacy singleton instance name, used before project
	// identity (P05). It is kept only to recognize the legacy instance: an
	// existing sandbox with this name is discovered as legacy, never renamed
	// or deleted automatically, and never adopted for a project.
	msbSandbox = "albert-opencode-sandbox"

	// msbGuestEntrypoint is the container entrypoint inside the microVM. It is
	// what launches `opencode serve`.
	msbGuestEntrypoint = "/.msb/scripts/start"

	// msbGuestLog collects the start script's output inside the guest; it is
	// the first place to look when the backend never becomes healthy.
	msbGuestLog = "/var/log/opencode.log"

	// msbToolchainMarker is written by guest-prep.sh once the toolchain
	// install completes. It doubles as the readiness signal for full mode,
	// which has no health endpoint.
	msbToolchainMarker = "/var/lib/just-code/toolchain-ready"

	// msbGuestPrepareProbe reports the guest's preparation state through its
	// exit code (Exec surfaces stderr only): ready, preparing, or idle with
	// nothing running. The process scan reads /proc rather than using pgrep,
	// which guest-prep only installs partway through — during a first install
	// pgrep is absent exactly when "is something preparing?" matters. The
	// probe skips its own PID: its command line necessarily contains the
	// entrypoint path it greps for.
	msbGuestPrepareProbe = "if [ -f " + msbToolchainMarker + " ]; then exit 0; fi; " +
		"self=$$; " +
		"for p in /proc/[0-9]*/cmdline; do " +
		"[ \"$p\" = \"/proc/$self/cmdline\" ] && continue; " +
		"tr '\\0' ' ' < \"$p\" 2>/dev/null | grep -q '" + msbGuestEntrypoint + "' && exit 2; " +
		"done; exit 3"

	msbPrepareReady    = 0
	msbPrepareProgress = 2
	msbPrepareIdle     = 3

	// msbToolchainRelaunchLimit bounds how often the wait relaunches a guest
	// where nothing is preparing, so a start script that dies repeatedly
	// cannot spin.
	msbToolchainRelaunchLimit = 3
	// msbRelaunchCommand restarts that entrypoint from inside a live VM.
	// Microsandbox keeps a VM across restarts but runs the entrypoint only at
	// creation, so a VM that came back from a restart reports `running` with no
	// backend process listening: "VM up" is not "backend ready".
	//
	// The script is invoked through `sh` because the Go SDK stores script
	// bodies verbatim (no shebang is prepended, unlike the Rust builder and
	// the CLI), and sandboxes created before the shebang was added to
	// guest-prep.sh persist a script that cannot be exec'd directly.
	// The launcher is backgrounded, then checked after one second: a bare
	// `nohup ... &` would report the exit status of the trailing sleep (0)
	// even when the script died instantly, so the compound ends with
	// `kill -0` on the launcher PID to make an immediate death visible.
	msbRelaunchCommand = "nohup sh " + msbGuestEntrypoint + " >" + msbGuestLog + " 2>&1 & pid=$!; sleep 1; kill -0 \"$pid\" 2>/dev/null"

	// msbLaunchAttempts bounds the retry while the guest agent catches up with
	// a freshly started VM.
	msbLaunchAttempts   = 10
	msbLaunchRetryDelay = 2 * time.Second
	msbDoctorTimeout    = 5 * time.Minute

	// msbAllowHosts restricts where the ALBERT_API_KEY secret may be
	// substituted: only the Albert API host ever sees the real value.
	msbAllowHost = "albert.api.etalab.gouv.fr"

	// msbAPISecretEnv is the guest environment variable OpenCode reads for the
	// Albert provider. Microsandbox exposes the secret *placeholder* under this
	// name; the real key stays on the host and is swapped in at the network
	// boundary for msbAllowHost only.
	//
	// Earlier versions instead persisted the real key under this same name in
	// isolation full, so the name doubles as the marker for a sandbox that must
	// be recreated rather than booted (see rejectLegacyRawKey). Naming it is
	// safe now: the guest only ever holds the placeholder.
	msbAPISecretEnv = "ALBERT_API_KEY"

	// msbAPISecretPlaceholder is the exact value the runtime substitutes for
	// the secret in the guest. It mirrors the runtime's own
	// default_placeholder(env_var) = "$MSB_" + env_var. Only this exact value
	// is treated as protected: matching the "$MSB_" prefix instead would let a
	// real credential that happens to start that way pass as a placeholder.
	msbAPISecretPlaceholder = "$MSB_" + msbAPISecretEnv
)

// MicrosandboxRuntime runs the OpenCode backend in a named Microsandbox
// microVM through the embedded Microsandbox Go SDK. No `msb` CLI install is
// required: the runtime downloads to ~/.microsandbox/ on first use. The real
// ALBERT_API_KEY stays on the host; only the secret-proxy substitution enters
// the microVM, and only for the Albert API host.
type MicrosandboxRuntime struct {
	cfg Config
	// instance is the sandbox name this backend operates on. It is the
	// project-derived instance name (P05); when Instance is empty the
	// backend stays on the legacy singleton name for compatibility.
	instance string
	// Client is the Microsandbox seam. Tests inject a fake; production uses the
	// SDK adapter (see microsandbox_sdk.go).
	Client msbClient
	// Probe overrides the health probe. It exists so tests can decide the
	// outcome of the "VM running, is the backend alive?" check without binding
	// a real port.
	Probe func(ctx context.Context, endpoint, username, password string) HealthProbe
	// launchRetryDelay is configurable for tests; production uses two seconds.
	launchRetryDelay time.Duration
	// toolchainPollDelay paces the full-mode readiness wait; configurable for tests.
	toolchainPollDelay time.Duration
}

// NewMicrosandboxRuntime builds a Microsandbox backend with production
// defaults. Without a project context it operates on the legacy singleton
// instance name, so existing behavior is unchanged until callers pass an
// instance (P06 wires the CLI).
func NewMicrosandboxRuntime(cfg Config) *MicrosandboxRuntime {
	return &MicrosandboxRuntime{
		cfg:                cfg,
		instance:           msbSandbox,
		Client:             sdkMSBClient{},
		launchRetryDelay:   msbLaunchRetryDelay,
		toolchainPollDelay: msbLaunchRetryDelay,
	}
}

// NewMicrosandboxRuntimeForInstance builds a backend bound to a
// project-derived instance name (P05).
func NewMicrosandboxRuntimeForInstance(cfg Config, instance string) *MicrosandboxRuntime {
	m := NewMicrosandboxRuntime(cfg)
	if instance != "" {
		m.instance = instance
	}
	return m
}

// InstanceName returns the sandbox name this backend operates on.
func (m *MicrosandboxRuntime) InstanceName() string {
	if m.instance != "" {
		return m.instance
	}
	return msbSandbox
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
	// Env returns the persisted guest environment of an existing sandbox. An
	// error means the environment could not be inspected, which the caller
	// treats as a refusal rather than as an empty environment. The values are
	// used only to detect a plaintext credential persisted by an earlier
	// version; they are never copied into a new sandbox.
	Env(ctx context.Context, name string) (map[string]string, error)
	// Logs streams sandbox logs to the terminal until interrupted.
	Logs(name string) error
	// Shell opens an interactive shell in the sandbox.
	Shell(name string) error
	// AttachInteractive runs a command interactively in the sandbox with a
	// working directory, blocking until it exits. It is the TUI channel used
	// by isolation full; Shell() is the /bin/bash special case of it.
	AttachInteractive(ctx context.Context, name, cmd, cwd string) (int, error)
	// List returns every managed sandbox known to the runtime, in
	// deterministic order. Managed means carrying the ownership label; the
	// legacy singleton is reported separately by the runtime (it predates
	// labels).
	List(ctx context.Context) ([]msbSandboxInfo, error)
}

// msbSandboxInfo is the status snapshot of the managed sandbox.
type msbSandboxInfo struct {
	Name   string
	Status string
}

// msbSandboxSpec is the full sandbox configuration passed to the SDK.
type msbSandboxSpec struct {
	Name        string // instance name (P05); empty means the legacy singleton
	Image       string
	Env         map[string]string
	Workspace   string   // host path bind-mounted at /workspace
	APIKey      string   // host-side secret value, never a guest environment entry
	AllowHosts  []string // hosts allowed to see the real secret value
	StartScript string   // guest start script body
}

// validateConfig checks the deterministic start-time configuration (API key,
// full-mode start timeout). Restart calls it before the destructive Clean so
// a configuration error cannot destroy a sandbox that Start would then
// refuse to recreate.
func (m *MicrosandboxRuntime) validateConfig() error {
	if m.cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	// A typo in JUST_CODE_START_TIMEOUT must be reported rather than silently
	// replaced by the default. Full mode waits on the guest without ever
	// reaching the validation the backend path performs before its health
	// wait, so validate before doing any runtime work.
	if m.cfg.Isolation == IsolationFull && m.cfg.StartTimeoutErr != nil {
		return m.cfg.StartTimeoutErr
	}
	return nil
}

func (m *MicrosandboxRuntime) Start(ctx context.Context) error {
	if err := m.validateConfig(); err != nil {
		return err
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

	sandbox, exists, err := m.Client.Lookup(ctx, m.InstanceName())
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
		// A sandbox created by an earlier version may still persist the real
		// Albert key in its guest environment. Refuse to boot it: the key
		// would stay readable inside the guest.
		if err := m.rejectLegacyRawKey(ctx); err != nil {
			return err
		}
	}

	if exists && sandbox.Status == "running" {
		if m.cfg.Isolation == IsolationFull {
			// "Running" only means the VM is up: a previous creation may have
			// timed out waiting for the toolchain while the background
			// installer kept going. Gate on the readiness marker here too,
			// or a retry would attach the TUI to an unprepared guest.
			if err := m.waitForToolchain(ctx); err != nil {
				return err
			}
			fmt.Printf("%s is running (isolation full; the TUI runs inside the microVM).\n", m.InstanceName())
			return nil
		}
		if m.backendHealthy(ctx) {
			fmt.Printf("%s is running with a healthy OpenCode backend.\n", m.InstanceName())
			return nil
		}
		fmt.Printf("%s is running but the OpenCode backend is not responding; restarting it inside the microVM...\n", m.InstanceName())
		return m.launchBackend(ctx)
	}

	if exists {
		fmt.Printf("Starting %s...\n", m.InstanceName())
		// Both isolation modes use the same secret-proxy configuration: the
		// guest environment carries the placeholder, never the real key.
		if err := m.Client.ModifyNextStart(ctx, m.InstanceName(), m.nextStartEnv(), m.cfg.APIKey); err != nil {
			return err
		}
		if err := m.Client.Start(ctx, m.InstanceName()); err != nil {
			return err
		}
		if m.cfg.Isolation == IsolationFull {
			// Booting a stopped VM does not re-run the entrypoint: relaunch
			// the (idempotent) start script so a sandbox stopped mid-
			// preparation finishes installing, then gate on readiness as
			// at creation.
			if err := m.launchBackend(ctx); err != nil {
				return err
			}
			if err := m.waitForToolchain(ctx); err != nil {
				return err
			}
			fmt.Printf("%s started (isolation full; the TUI runs inside the microVM).\n", m.InstanceName())
			return nil
		}
		// Booting a stopped VM does not re-run the container entrypoint, so the
		// backend has to be launched explicitly.
		fmt.Printf("Launching OpenCode inside %s...\n", m.InstanceName())
		return m.launchBackend(ctx)
	}

	fmt.Printf("Creating %s microVM...\n", m.InstanceName())
	fmt.Println("First start installs the toolchain inside the microVM (build-base, node, python); this can take several minutes.")
	if err := m.Client.Create(ctx, m.sandboxSpec()); err != nil {
		return err
	}
	// `msb create` boots an idle VM: the Go SDK has no equivalent of the Rust
	// SDK's transient LaunchIntent::Background, so the persisted start script
	// (which installs the toolchain and then execs `opencode serve`, or keeps
	// the VM alive in full mode) is never run by creation itself. Launch it
	// explicitly, the same way a restarted VM would.
	fmt.Printf("Launching the start script inside %s...\n", m.InstanceName())
	if err := m.launchBackend(ctx); err != nil {
		return err
	}
	if m.cfg.Isolation == IsolationFull {
		// Full mode has no health endpoint and attach runs the TUI
		// immediately after Start returns, so creation must not report
		// success while the guest is still preparing — or after the start
		// script died, in which case the marker never appears.
		return m.waitForToolchain(ctx)
	}
	return nil
}

// waitForToolchain blocks until the guest prep script signals completion by
// writing its marker, bounded by the configured start timeout. A guest where
// nothing is preparing is relaunched rather than polled: an idle VM left by
// the pre-#61 creation path, or a start script whose launch retries were
// exhausted, would otherwise wait out the whole timeout on every attempt and
// only be recoverable by destructive recreation.
func (m *MicrosandboxRuntime) waitForToolchain(ctx context.Context) error {
	timeout := m.cfg.StartTimeout
	if timeout <= 0 {
		timeout = DefaultStartTimeout
	}
	delay := m.toolchainPollDelay
	if delay <= 0 {
		delay = msbLaunchRetryDelay
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	relaunches := 0
	for first := true; ; first = false {
		code, _, err := m.Client.Exec(ctx, m.InstanceName(), msbGuestPrepareProbe)
		if err == nil {
			switch {
			case code == msbPrepareReady:
				return nil
			case code == msbPrepareProgress:
				// The installer is alive; keep waiting.
			case code == msbPrepareIdle && relaunches < msbToolchainRelaunchLimit:
				relaunches++
				fmt.Printf("Nothing is preparing the guest in %s; launching the start script...\n", m.InstanceName())
				if err := m.launchBackend(ctx); err != nil {
					return err
				}
			}
		}
		if first {
			// Quiet in the common case (sandbox already prepared); the wait
			// message only makes sense when there is something to wait for.
			fmt.Println("Waiting for the guest toolchain to finish installing...")
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("the guest toolchain was not ready within %s (the start script may have failed); "+
				"run 'just-code logs --microsandbox' or check %s inside the guest: %w",
				timeout, msbGuestLog, ctx.Err())
		}
	}
}

// sandboxSpec builds the full sandbox configuration from the config and the
// embedded OpenCode config and start script. The secret proxy is configured
// identically for both isolation modes: where the TUI happens to run must not
// change whether credentials are protected.
func (m *MicrosandboxRuntime) sandboxSpec() msbSandboxSpec {
	return msbSandboxSpec{
		Name:        m.InstanceName(),
		Image:       msbImage,
		Env:         m.sandboxEnv(),
		Workspace:   m.cfg.WorkspaceDir,
		APIKey:      m.cfg.APIKey,
		AllowHosts:  []string{msbAllowHost},
		StartScript: msbStartScript(m.cfg.Isolation),
	}
}

// nextStartEnv builds the guest env persisted for the next boot of an
// existing stopped sandbox. The real ALBERT_API_KEY is deliberately absent:
// it travels as the proxy secret passed to ModifyNextStart, and the guest
// reads the placeholder Microsandbox exposes under the same variable name.
// ModifyNextStart merges, so the OpenCode config persisted at creation stays
// in place and only the server credentials need refreshing here.
func (m *MicrosandboxRuntime) nextStartEnv() map[string]string {
	return map[string]string{
		"OPENCODE_SERVER_PASSWORD": m.cfg.Password,
		"OPENCODE_SERVER_USERNAME": m.cfg.Username,
	}
}

// sandboxEnv builds the guest environment for the sandbox. As with
// nextStartEnv, the Albert key is provided by the secret proxy rather than
// the environment.
func (m *MicrosandboxRuntime) sandboxEnv() map[string]string {
	return map[string]string{
		"OPENCODE_SERVER_PASSWORD": m.cfg.Password,
		"OPENCODE_SERVER_USERNAME": m.cfg.Username,
		"OPENCODE_CONFIG_CONTENT":  opencodeConfigContent,
	}
}

// rejectLegacyRawKey refuses to boot a sandbox that persists a plaintext
// Albert key in its guest environment. Versions before proxy secret injection
// wrote the real ALBERT_API_KEY there in isolation full. The SDK cannot edit a
// persisted secret value, and booting such a sandbox would keep serving the
// plaintext key to the guest, so the only safe recovery is to recreate it.
//
// The inspection fails closed. Scrub-on-next-start (EnvRemove in
// msbNextStartOptions) only runs on the stopped path, and even there it cannot
// repair a guest that is already up: a running sandbox is returned early by
// Start and never reconfigured. So when the guest environment cannot be read
// there is no way to tell a protected sandbox from a leaking one, and treating
// the unreadable case as safe would let exactly the case this guard exists for
// slip through.
func (m *MicrosandboxRuntime) rejectLegacyRawKey(ctx context.Context) error {
	env, err := m.Client.Env(ctx, m.InstanceName())
	if err != nil {
		return fmt.Errorf("cannot read the guest environment of %s to check whether it stores a plaintext %s; "+
			"refusing to boot a sandbox whose credentials cannot be verified. "+
			"Run 'just-code restart --microsandbox' to recreate it with the key behind the secret proxy: %w",
			m.InstanceName(), msbAPISecretEnv, err)
	}
	value, ok := env[msbAPISecretEnv]
	if !ok {
		return nil
	}
	// Only the exact documented placeholder is the protected configuration and
	// safe to reuse. Anything else is a value the guest can read, including a
	// credential that merely looks like a runtime placeholder.
	if value == msbAPISecretPlaceholder {
		return nil
	}
	return fmt.Errorf("%s was created before proxy secret injection and stores a plaintext %s in its guest environment; "+
		"the value cannot be replaced in place. "+
		"Run 'just-code restart --microsandbox' to recreate the sandbox with the key behind the secret proxy: %w",
		m.InstanceName(), msbAPISecretEnv, errLegacyRawKeySandbox)
}

// warnUnprotectedRuntime tells the user that a runtime without a secret proxy
// hands the real credential to the guest. Switching to isolation backend does
// not avoid this: Tart and agent-vm expose the key in backend mode too, so the
// warning is about the runtime, not the isolation level.
func warnUnprotectedRuntime(runtimeName string) {
	fmt.Fprintf(os.Stderr, "Warning: %s has no secret injection, so the real ALBERT_API_KEY is readable inside the VM "+
		"(isolation backend and full alike). Any process in the guest, including the agent itself, can read and exfiltrate it. "+
		"Prefer --microsandbox for untrusted work, and treat the guest as holding a live credential.\n", runtimeName)
}

// errLegacyRawKeySandbox marks the refusal to boot a sandbox that stores a
// plaintext credential.
var errLegacyRawKeySandbox = errors.New("legacy sandbox stores a plaintext API key")

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
		code, stderr, err := m.Client.Exec(ctx, m.InstanceName(), msbRelaunchCommand)
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
	return fmt.Errorf("failed to launch OpenCode inside %s: %w", m.InstanceName(), last)
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
	mounted, err := m.Client.WorkspaceMount(ctx, m.InstanceName())
	if err != nil || mounted == "" {
		return
	}
	// Both sides are absolutized: the runtime persists the mount as an
	// absolute path while WORKSPACE_DIR commonly stays relative ("./workspace"),
	// so a raw string comparison would warn on every default-config start.
	mountedClean := filepath.Clean(mounted)
	if abs, err := filepath.Abs(mountedClean); err == nil {
		mountedClean = abs
	}
	workspaceClean := filepath.Clean(m.cfg.WorkspaceDir)
	if abs, err := filepath.Abs(workspaceClean); err == nil {
		workspaceClean = abs
	}
	if mountedClean == workspaceClean || (runtime.GOOS == "windows" && strings.EqualFold(mountedClean, workspaceClean)) {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: %s was created with /workspace mounted from %s, but the workspace is now %s. "+
		"Mounts are fixed when a sandbox is created, so /workspace will not reflect the new directory. "+
		"Run 'just-code restart --microsandbox' to recreate it.\n", m.InstanceName(), mounted, m.cfg.WorkspaceDir)
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

func (m *MicrosandboxRuntime) Stop(ctx context.Context) error {
	running, err := m.IsRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		fmt.Printf("%s is not running.\n", m.InstanceName())
		return nil
	}
	fmt.Printf("Stopping %s...\n", m.InstanceName())
	return m.Client.Stop(ctx, m.InstanceName())
}

func (m *MicrosandboxRuntime) Restart(ctx context.Context) error {
	// Preflight the workspace before the destructive Clean: a rejected
	// workspace must not cost the sandbox and its persistent state. Create
	// the directory first, as Start does, so a workspace that does not
	// exist yet (default ./workspace) is not an error. The deterministic
	// configuration checks run first: a bad config must not reach Clean,
	// which would destroy a sandbox that Start then refuses to recreate.
	if err := m.validateConfig(); err != nil {
		return err
	}
	if err := os.MkdirAll(m.cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := CheckWorkspaceGate(ctx, m.cfg.WorkspaceDir); err != nil {
		return err
	}
	if err := m.Clean(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *MicrosandboxRuntime) Clean(ctx context.Context) error {
	_, exists, err := m.Client.Lookup(ctx, m.InstanceName())
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s does not exist.\n", m.InstanceName())
		return nil
	}
	// Disposing is destructive: say so before and after, so a silent success
	// can never be mistaken for "nothing happened".
	fmt.Printf("Removing %s (sandbox and local state)...\n", m.InstanceName())
	if err := m.Client.Remove(ctx, m.InstanceName()); err != nil {
		return err
	}
	fmt.Printf("%s removed.\n", m.InstanceName())
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
	return m.Client.Logs(m.InstanceName())
}

func (m *MicrosandboxRuntime) Shell() error {
	return m.Client.Shell(m.InstanceName())
}

// RunAgent launches the OpenCode TUI in the foreground inside the microVM,
// with /workspace as its working directory (isolation full). The SDK attach
// channel passes the host terminal through.
func (m *MicrosandboxRuntime) RunAgent(ctx context.Context) error {
	code, err := m.Client.AttachInteractive(ctx, m.InstanceName(), "opencode", "/workspace")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("opencode TUI exited %d inside %s", code, m.InstanceName())
	}
	return nil
}

// Status describes the sandbox's current state, for `check` in isolation
// full where there is no health endpoint to probe.
func (m *MicrosandboxRuntime) Status(ctx context.Context) (string, error) {
	sandbox, exists, err := m.Client.Lookup(ctx, m.InstanceName())
	if err != nil {
		return "", err
	}
	if !exists {
		return fmt.Sprintf("%s does not exist", m.InstanceName()), nil
	}
	return fmt.Sprintf("%s is %s", m.InstanceName(), sandbox.Status), nil
}

func (m *MicrosandboxRuntime) IsRunning(ctx context.Context) (bool, error) {
	sandbox, exists, err := m.Client.Lookup(ctx, m.InstanceName())
	if err != nil {
		return false, err
	}
	return exists && sandbox.Status == "running", nil
}

// RunningInstances returns the names of all managed Microsandbox sandboxes
// that are up, plus the legacy singleton when it is up (it predates the
// ownership label, so it is invisible to List). The order is deterministic.
func (m *MicrosandboxRuntime) RunningInstances(ctx context.Context) ([]string, error) {
	var names []string
	sandboxes, err := m.Client.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sb := range sandboxes {
		if sb.Status == "running" {
			names = append(names, sb.Name)
		}
	}
	// The legacy singleton is enumerated by name: it was created before the
	// ownership label existed.
	if m.InstanceName() == msbSandbox || m.instance != "" {
		if legacy, exists, err := m.Client.Lookup(ctx, msbSandbox); err == nil && exists && legacy.Status == "running" {
			names = append(names, msbSandbox)
		} else if err != nil {
			return nil, err
		}
	}
	sort.Strings(names)
	return names, nil
}

// StopInstance stops a named managed instance. It is the per-project form of
// Stop: stopping project A's instance never touches project B's.
func (m *MicrosandboxRuntime) StopInstance(ctx context.Context, name string) error {
	sandbox, exists, err := m.Client.Lookup(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s does not exist.\n", name)
		return nil
	}
	if sandbox.Status != "running" {
		fmt.Printf("%s is not running.\n", name)
		return nil
	}
	fmt.Printf("Stopping %s...\n", name)
	return m.Client.Stop(ctx, name)
}

func (m *MicrosandboxRuntime) Endpoint(context.Context) (string, error) {
	return "http://localhost:" + strconv.Itoa(DefaultPort), nil
}
