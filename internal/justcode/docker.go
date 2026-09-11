package justcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/etalab-ia/just-code/assets"
)

const dockerContainer = "albert-opencode-sandbox"

// DockerRuntime runs the OpenCode backend in a Docker container. The Compose and
// Docker files are embedded in the binary and materialized under the state
// directory, so the CLI does not depend on the checkout.
type DockerRuntime struct {
	cfg       Config
	Runner    Runner
	AssetsDir string
}

// NewDockerRuntime builds a Docker backend with production defaults.
func NewDockerRuntime(cfg Config) *DockerRuntime {
	return &DockerRuntime{cfg: cfg, Runner: OSRunner{}, AssetsDir: DefaultAssetsDir()}
}

func (d *DockerRuntime) ID() Runtime { return RuntimeDocker }

func (d *DockerRuntime) ensureAssets() (string, error) {
	if d.AssetsDir == "" {
		d.AssetsDir = DefaultAssetsDir()
	}
	return assets.Materialize(d.AssetsDir)
}

// composeEnv is the environment Compose interpolates the service definition
// with. Passing it explicitly makes Config authoritative (including the
// empty-password "no auth" case) and removes any dependency on a .env file
// next to the materialized Compose file.
func (d *DockerRuntime) composeEnv() []string {
	return []string{
		"WORKSPACE_DIR=" + d.cfg.WorkspaceDir,
		"ALBERT_API_KEY=" + d.cfg.APIKey,
		"OPENCODE_SERVER_PASSWORD=" + d.cfg.Password,
		"OPENCODE_SERVER_USERNAME=" + d.cfg.Username,
	}
}

// composeArgs returns the argv for a `docker compose` invocation against the
// materialized assets, with the global flags before the subcommand.
func (d *DockerRuntime) composeArgs(dir string, rest ...string) []string {
	args := []string{
		"compose",
		"-f", filepath.Join(dir, "docker-compose.yml"),
		"--project-directory", dir,
	}
	return append(args, rest...)
}

// compose runs `docker compose <rest...>` against the materialized assets.
func (d *DockerRuntime) compose(ctx context.Context, rest ...string) error {
	dir, err := d.ensureAssets()
	if err != nil {
		return err
	}
	return runEnvOK(d.Runner, ctx, d.composeEnv(), "docker", d.composeArgs(dir, rest...)...)
}

func (d *DockerRuntime) Start(ctx context.Context) error {
	if d.cfg.APIKey == "" {
		return fmt.Errorf("set ALBERT_API_KEY in the environment or .env")
	}
	if err := os.MkdirAll(d.cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	// Report the backend's real state rather than assuming that a running
	// container means a working backend.
	if running, err := d.IsRunning(ctx); err == nil && running {
		if endpoint, err := d.Endpoint(ctx); err == nil {
			ReportRunningHealth(ctx, d.ID(), endpoint, d.cfg.Username, d.cfg.Password)
		}
		return nil
	}
	return d.compose(ctx, "up", "-d", "--quiet-pull")
}

func (d *DockerRuntime) Stop(ctx context.Context) error {
	return d.compose(ctx, "down", "--timeout", "3")
}

func (d *DockerRuntime) Build(ctx context.Context) error {
	return d.compose(ctx, "build", "--quiet")
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
	return d.compose(ctx, "down", "--timeout", "3", "--rmi", "local")
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
	dir, err := d.ensureAssets()
	if err != nil {
		return err
	}
	return RunInteractiveEnv(d.composeEnv(), "docker", d.composeArgs(dir, "logs", "--follow")...)
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
