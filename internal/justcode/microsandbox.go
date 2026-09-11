package justcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/etalab-ia/just-code/assets"
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
	msbRelaunchCommand = "nohup " + msbGuestEntrypoint + " >/var/log/opencode.log 2>&1 &"

	// msbLaunchAttempts bounds the retry while the guest agent catches up with
	// a freshly started VM.
	msbLaunchAttempts   = 10
	msbLaunchRetryDelay = 2 * time.Second
)

// MicrosandboxRuntime runs the OpenCode backend in a named Microsandbox
// microVM, delegating to the `msb` CLI. The real ALBERT_API_KEY stays on the
// host: only the secret-proxy substitution enters the microVM. The sandbox
// config is embedded in the binary and materialized under the state directory.
type MicrosandboxRuntime struct {
	cfg       Config
	Runner    Runner
	AssetsDir string
	// Probe overrides the health probe. It exists so tests can decide the
	// outcome of the "VM running, is the backend alive?" check without binding
	// a real port.
	Probe func(ctx context.Context, endpoint, username, password string) HealthProbe
}

// msbSandboxInfo is one row of `msb ls`.
type msbSandboxInfo struct {
	Name   string
	Status string
}

// NewMicrosandboxRuntime builds a Microsandbox backend with production defaults.
func NewMicrosandboxRuntime(cfg Config) *MicrosandboxRuntime {
	return &MicrosandboxRuntime{cfg: cfg, Runner: OSRunner{}, AssetsDir: DefaultAssetsDir()}
}

func (m *MicrosandboxRuntime) ID() Runtime { return RuntimeMicrosandbox }

func (m *MicrosandboxRuntime) ensureAssets() (string, error) {
	if m.AssetsDir == "" {
		m.AssetsDir = DefaultAssetsDir()
	}
	return assets.Materialize(m.AssetsDir)
}

// env returns the child environment for `msb` commands. Microsandbox resolves
// the `ALBERT_API_KEY` secret from the host environment, so it must be passed
// explicitly rather than relying on inheritance.
func (m *MicrosandboxRuntime) env() []string {
	if m.cfg.APIKey == "" {
		return nil
	}
	return []string{"ALBERT_API_KEY=" + m.cfg.APIKey}
}

func (m *MicrosandboxRuntime) run(ctx context.Context, name string, args ...string) (ExecResult, error) {
	return runEnv(m.Runner, ctx, m.env(), name, args...)
}

func (m *MicrosandboxRuntime) runOK(ctx context.Context, name string, args ...string) error {
	return runEnvOK(m.Runner, ctx, m.env(), name, args...)
}

// findSandbox looks the sandbox up in `msb ls`. Exit codes from `msb inspect`
// are unreliable for existence checks, and `msb ls` also yields the status,
// which decides whether the backend needs relaunching.
func (m *MicrosandboxRuntime) findSandbox(ctx context.Context) (msbSandboxInfo, bool) {
	res, err := m.run(ctx, "msb", "ls")
	if err != nil || res.ExitCode != 0 {
		return msbSandboxInfo{}, false
	}
	return parseMsbLs(res.Stdout)
}

// parseMsbLs finds the managed sandbox row in `msb ls` output. Columns are
// space-padded; only the CREATED column contains a space, so a whitespace split
// keeps name and status in the first three fields.
func parseMsbLs(output string) (msbSandboxInfo, bool) {
	for _, line := range strings.Split(output, "\n") {
		columns := strings.Fields(strings.TrimSpace(line))
		if len(columns) < 3 {
			continue
		}
		name, status := columns[0], columns[2]
		if name == "NAME" {
			continue
		}
		if name == msbSandbox {
			return msbSandboxInfo{Name: name, Status: status}, true
		}
	}
	return msbSandboxInfo{}, false
}

// parseMsbWorkspaceMount reads the host directory currently mounted at the
// guest's /workspace from `msb inspect`.
func parseMsbWorkspaceMount(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(trimmed, "/workspace")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		rest = strings.TrimPrefix(rest, "\u2192") // →
		rest = strings.TrimPrefix(rest, "->")
		if fields := strings.Fields(rest); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func (m *MicrosandboxRuntime) Start(ctx context.Context) error {
	if m.cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := os.MkdirAll(m.cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}

	sandbox, exists := m.findSandbox(ctx)
	if exists {
		m.warnIfWorkspaceMountIsStale(ctx)
	}

	if exists && sandbox.Status == "running" {
		if m.backendHealthy(ctx) {
			fmt.Printf("%s is running with a healthy OpenCode backend.\n", msbSandbox)
			return nil
		}
		fmt.Printf("%s is running but the OpenCode backend is not responding; restarting it inside the microVM...\n", msbSandbox)
		return m.launchBackend(ctx)
	}

	if exists {
		fmt.Printf("Starting %s...\n", msbSandbox)
		if err := m.runOK(ctx, "msb", "modify", msbSandbox,
			"--env", "OPENCODE_SERVER_PASSWORD="+m.cfg.Password,
			"--env", "OPENCODE_SERVER_USERNAME="+m.cfg.Username,
			"--next-start"); err != nil {
			return err
		}
		if err := m.runOK(ctx, "msb", "start", msbSandbox); err != nil {
			return err
		}
		// Booting a stopped VM does not re-run the container entrypoint, so the
		// backend has to be launched explicitly.
		fmt.Printf("Launching OpenCode inside %s...\n", msbSandbox)
		return m.launchBackend(ctx)
	}

	dir, err := m.ensureAssets()
	if err != nil {
		return err
	}
	fmt.Printf("Creating %s microVM...\n", msbSandbox)
	fmt.Println("First start installs the toolchain inside the microVM (build-base, node, python); this can take several minutes.")
	return m.runOK(ctx, "msb", "run",
		"--name", msbSandbox,
		"--detach",
		"--conf", filepath.Join(dir, "microsandbox.yaml"),
		"--root-disk", "8G",
		"--volume", m.cfg.WorkspaceDir+":/workspace",
		"--env", "OPENCODE_SERVER_PASSWORD="+m.cfg.Password,
		"--env", "OPENCODE_SERVER_USERNAME="+m.cfg.Username,
		msbImage,
	)
}

// launchBackend runs the container entrypoint inside a live VM. Recreating the
// sandbox runs it automatically; booting an existing one does not. The guest
// agent can lag the VM by a moment after start, hence the bounded retry.
func (m *MicrosandboxRuntime) launchBackend(ctx context.Context) error {
	var last error
	for attempt := 1; attempt <= msbLaunchAttempts; attempt++ {
		res, err := m.run(ctx, "msb", "exec", msbSandbox, "--", "sh", "-c", msbRelaunchCommand)
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("msb exec exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
		}
		if attempt == msbLaunchAttempts {
			break
		}
		select {
		case <-time.After(msbLaunchRetryDelay):
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
// configured workspace. `msb modify` cannot change mounts, so a sandbox keeps
// whichever host directory it was created with.
func (m *MicrosandboxRuntime) warnIfWorkspaceMountIsStale(ctx context.Context) {
	res, err := m.run(ctx, "msb", "inspect", msbSandbox)
	if err != nil || res.ExitCode != 0 {
		return
	}
	mounted := parseMsbWorkspaceMount(res.Stdout)
	if mounted == "" || mounted == m.cfg.WorkspaceDir {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: %s was created with /workspace mounted from %s, but the workspace is now %s. "+
		"Mounts are fixed when a sandbox is created, so /workspace will not reflect the new directory. "+
		"Run 'just-code restart --microsandbox' to recreate it.\n", msbSandbox, mounted, m.cfg.WorkspaceDir)
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
	return m.runOK(ctx, "msb", "stop", "--timeout", "3", msbSandbox)
}

func (m *MicrosandboxRuntime) Build(ctx context.Context) error {
	return m.runOK(ctx, "msb", "pull", msbImage)
}

func (m *MicrosandboxRuntime) Restart(ctx context.Context) error {
	if err := m.Clean(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *MicrosandboxRuntime) Clean(ctx context.Context) error {
	if _, exists := m.findSandbox(ctx); !exists {
		fmt.Printf("%s does not exist.\n", msbSandbox)
		return nil
	}
	return m.runOK(ctx, "msb", "rm", "--force", msbSandbox)
}

func (m *MicrosandboxRuntime) Doctor(ctx context.Context) error {
	return m.runOK(ctx, "msb", "doctor")
}

func (m *MicrosandboxRuntime) Logs() error {
	return RunInteractive("msb", "logs", "--follow", msbSandbox)
}

func (m *MicrosandboxRuntime) Shell() error {
	return RunInteractive("msb", "exec", msbSandbox, "--", "/bin/bash")
}

func (m *MicrosandboxRuntime) IsRunning(ctx context.Context) (bool, error) {
	sandbox, exists := m.findSandbox(ctx)
	return exists && sandbox.Status == "running", nil
}

func (m *MicrosandboxRuntime) Endpoint(context.Context) (string, error) {
	return "http://localhost:" + strconv.Itoa(DefaultPort), nil
}
