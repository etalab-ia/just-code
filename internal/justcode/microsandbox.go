package justcode

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	msbConfigFile = "microsandbox.yaml"
	msbImage      = "ghcr.io/anomalyco/opencode:latest"
	msbSandbox    = "albert-opencode-sandbox"
)

// MicrosandboxRuntime runs the OpenCode backend in a named Microsandbox
// microVM, delegating to the `msb` CLI. The real ALBERT_API_KEY stays on the
// host: only the secret-proxy substitution enters the microVM.
type MicrosandboxRuntime struct {
	cfg    Config
	Runner Runner
}

// NewMicrosandboxRuntime builds a Microsandbox backend with production defaults.
func NewMicrosandboxRuntime(cfg Config) *MicrosandboxRuntime {
	return &MicrosandboxRuntime{cfg: cfg, Runner: OSRunner{}}
}

func (m *MicrosandboxRuntime) ID() Runtime { return RuntimeMicrosandbox }

func (m *MicrosandboxRuntime) Start(ctx context.Context) error {
	if m.cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := os.MkdirAll(m.cfg.ProjectDir, 0o755); err != nil {
		return err
	}

	running, err := m.IsRunning(ctx)
	if err != nil {
		return err
	}
	if running {
		fmt.Printf("%s is already running.\n", msbSandbox)
		return nil
	}

	if res, err := m.Runner.Run(ctx, "msb", "inspect", msbSandbox); err == nil && res.ExitCode == 0 {
		if err := runOK(m.Runner, ctx, "msb", "modify", msbSandbox,
			"--env", "OPENCODE_SERVER_PASSWORD="+m.cfg.Password,
			"--env", "OPENCODE_SERVER_USERNAME="+m.cfg.Username,
			"--next-start"); err != nil {
			return err
		}
		return runOK(m.Runner, ctx, "msb", "start", msbSandbox)
	}

	return runOK(m.Runner, ctx, "msb", "run",
		"--name", msbSandbox,
		"--detach",
		"--conf", msbConfigFile,
		"--root-disk", "8G",
		"--volume", m.cfg.ProjectDir+":/workspace",
		"--env", "OPENCODE_SERVER_PASSWORD="+m.cfg.Password,
		"--env", "OPENCODE_SERVER_USERNAME="+m.cfg.Username,
		msbImage,
	)
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
	return runOK(m.Runner, ctx, "msb", "stop", "--timeout", "3", msbSandbox)
}

func (m *MicrosandboxRuntime) Build(ctx context.Context) error {
	return runOK(m.Runner, ctx, "msb", "pull", msbImage)
}

func (m *MicrosandboxRuntime) Restart(ctx context.Context) error {
	if err := m.Clean(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *MicrosandboxRuntime) Clean(ctx context.Context) error {
	res, err := m.Runner.Run(ctx, "msb", "inspect", msbSandbox)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		fmt.Printf("%s does not exist.\n", msbSandbox)
		return nil
	}
	return runOK(m.Runner, ctx, "msb", "rm", "--force", msbSandbox)
}

func (m *MicrosandboxRuntime) Doctor(ctx context.Context) error {
	return runOK(m.Runner, ctx, "msb", "doctor")
}

func (m *MicrosandboxRuntime) Logs() error {
	return RunInteractive("msb", "logs", "--follow", msbSandbox)
}

func (m *MicrosandboxRuntime) Shell() error {
	return RunInteractive("msb", "exec", msbSandbox, "--", "/bin/bash")
}

func (m *MicrosandboxRuntime) IsRunning(ctx context.Context) (bool, error) {
	res, err := m.Runner.Run(ctx, "msb", "ls", "--running", "-q")
	if err != nil {
		if commandNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(line) == msbSandbox {
			return true, nil
		}
	}
	return false, nil
}

func (m *MicrosandboxRuntime) Endpoint(context.Context) (string, error) {
	return "http://localhost:" + strconv.Itoa(DefaultPort), nil
}
