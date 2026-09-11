package justcode

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

const (
	dockerContainer = "albert-opencode-sandbox"
)

// DockerRuntime runs the OpenCode backend in a Docker container, delegating to
// `docker compose` with the repo's docker-compose.yml.
type DockerRuntime struct {
	cfg    Config
	Runner Runner
}

// NewDockerRuntime builds a Docker backend with production defaults.
func NewDockerRuntime(cfg Config) *DockerRuntime {
	return &DockerRuntime{cfg: cfg, Runner: OSRunner{}}
}

func (d *DockerRuntime) ID() Runtime { return RuntimeDocker }

func (d *DockerRuntime) Start(ctx context.Context) error {
	if d.cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := os.MkdirAll(d.cfg.ProjectDir, 0o755); err != nil {
		return err
	}
	return runOK(d.Runner, ctx, "docker", "compose", "up", "-d", "--quiet-pull")
}

func (d *DockerRuntime) Stop(ctx context.Context) error {
	return runOK(d.Runner, ctx, "docker", "compose", "down", "--timeout", "3")
}

func (d *DockerRuntime) Build(ctx context.Context) error {
	return runOK(d.Runner, ctx, "docker", "compose", "build", "--quiet")
}

func (d *DockerRuntime) Restart(ctx context.Context) error {
	if err := d.Stop(ctx); err != nil {
		return err
	}
	if err := d.Build(ctx); err != nil {
		return err
	}
	return d.Start(ctx)
}

func (d *DockerRuntime) Clean(ctx context.Context) error {
	return runOK(d.Runner, ctx, "docker", "compose", "down", "--timeout", "3", "--rmi", "local")
}

func (d *DockerRuntime) Doctor(ctx context.Context) error {
	res, err := d.Runner.Run(ctx, "docker", "info")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("docker daemon is not running")
	}
	ver, err := d.Runner.Run(ctx, "docker", "compose", "version")
	if err != nil {
		return err
	}
	if ver.ExitCode != 0 {
		return fmt.Errorf("docker compose is not available")
	}
	fmt.Print(ver.Stdout)
	fmt.Println("Docker runtime is ready.")
	return nil
}

func (d *DockerRuntime) Logs() error {
	return RunInteractive("docker", "compose", "logs", "--follow")
}

func (d *DockerRuntime) Shell() error {
	return RunInteractive("docker", "exec", "-it", dockerContainer, "bash")
}

func (d *DockerRuntime) IsRunning(ctx context.Context) (bool, error) {
	res, err := d.Runner.Run(ctx, "docker", "container", "top", dockerContainer)
	if err != nil {
		if commandNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return res.ExitCode == 0, nil
}

func (d *DockerRuntime) Endpoint(context.Context) (string, error) {
	return "http://localhost:" + strconv.Itoa(DefaultPort), nil
}
