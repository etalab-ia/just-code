package justcode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// GuestBootstrapCommand is a hidden subcommand of this same binary. The binary
// is staged into the Tart guest and re-invoked with it, so the in-VM bootstrap
// is Go rather than a shell script. It is an implementation detail, not a
// user-facing command.
const GuestBootstrapCommand = "__guest-bootstrap"

const (
	// guestShareDir is the read-only share mounted into the VM. It holds only
	// the staged binary, never the checkout or its .env.
	guestShareDir = "/Volumes/My Shared Files/just-code"
	// guestShareBinary is the staged binary on that share.
	guestShareBinary = guestShareDir + "/just-code"
	// guestLocalBinary is the copy the guest executes. Running from the
	// virtiofs share is not reliably executable, so the payload is copied to
	// local disk first.
	guestLocalBinary = "/tmp/just-code-guest"

	guestWorkspaceDir  = "/Volumes/My Shared Files/workspace"
	opencodeInstallURL = "https://opencode.ai/install"

	// opencodeConfigContent configures the Albert provider inside the guest. It
	// is embedded here (Tart) and interpolated into the Compose and
	// Microsandbox configs for the other runtimes.
	opencodeConfigContent = `{"$schema":"https://opencode.ai/config.json","provider":{"albert":{"npm":"@ai-sdk/openai-compatible","name":"Albert API (État)","options":{"baseURL":"https://albert.api.etalab.gouv.fr/v1","apiKey":"{env:ALBERT_API_KEY}"},"models":{"deepseek-v4-flash":{"name":"DeepSeek V4 Flash (Albert)","limit":{"context":131072,"output":65536}}}}},"model":"albert/deepseek-v4-flash","small_model":"albert/deepseek-v4-flash","permission":{"edit":"allow","external_directory":"allow","bash":{".*":"allow","git push.*(--force|-f | --force-with-lease)":"deny","sudo .*":"deny"},"webfetch":"allow","websearch":"allow","skill":"allow","task":"allow"}}`
)

// Execer replaces the current process image, so the server inherits the
// terminal, signals, and stdio.
type Execer interface {
	Exec(path string, argv []string, env []string) error
}

// SysExecer is the real Execer, backed by syscall.Exec.
type SysExecer struct{}

func (SysExecer) Exec(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}

// GuestConfig configures the in-VM bootstrap. Every dependency is injectable so
// the logic is testable without a macOS guest.
type GuestConfig struct {
	Port     string
	Username string
	// MTU is TART_MTU. Empty means "unset" and falls back to DefaultTartMTU;
	// "auto" leaves the guest network untouched.
	MTU    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Runner Runner
	Execer Execer
	// Chdir sets the working directory before exec. Injectable so tests do not
	// mutate the test process's cwd.
	Chdir   func(dir string) error
	Home    string
	Environ []string
	HTTPGet func(url string) ([]byte, error)
}

// RunGuestBootstrap is the in-VM entry point: it clamps the MTU, installs
// OpenCode if needed, and execs `opencode serve`. It replaces the former
// tart-bootstrap.sh.
func RunGuestBootstrap(ctx context.Context, cfg GuestConfig) error {
	applyGuestDefaults(&cfg)

	pathEnv := guestPath(cfg.Environ, cfg.Home)
	env := withEnv(cfg.Environ, "PATH="+pathEnv)

	mtu := cfg.MTU
	if mtu == "" {
		mtu = DefaultTartMTU
	}
	if mtu != MTUAuto {
		if err := applyMTU(ctx, cfg, env, mtu); err != nil {
			return err
		}
	}

	password, apiKey, err := readSecrets(cfg.Stdin)
	if err != nil {
		return err
	}

	g := &guest{cfg: cfg, pathEnv: pathEnv, env: env}
	if _, err := g.resolve("opencode"); err != nil {
		fmt.Fprintln(cfg.Stdout, "Installing OpenCode inside macOS VM...")
		if err := g.installOpencode(ctx); err != nil {
			return err
		}
	}
	// The curl installer drops the binary in ~/.opencode/bin; guestPath already
	// includes it, so a fresh resolve picks it up.
	if _, err := g.resolve("opencode"); err != nil {
		return fmt.Errorf("opencode is not available on PATH inside the VM")
	}

	// Best-effort git identity and safe.directory, matching the shell behavior.
	_, _ = g.run(ctx, "git", "config", "--global", "user.name", "Albert Code Agent")
	_, _ = g.run(ctx, "git", "config", "--global", "user.email", "albert-code@noreply.etalab.gouv.fr")
	_, _ = g.run(ctx, "git", "config", "--global", "--add", "safe.directory", "*")

	workspace := guestWorkspaceDir
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		workspace = filepath.Join(cfg.Home, "workspace")
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			return err
		}
	}
	if err := cfg.Chdir(workspace); err != nil {
		return err
	}

	opencodePath, err := g.resolve("opencode")
	if err != nil {
		return err
	}
	childEnv := withEnv(env,
		"OPENCODE_SERVER_PASSWORD="+password,
		"OPENCODE_SERVER_USERNAME="+cfg.Username,
		"ALBERT_API_KEY="+apiKey,
		"OPENCODE_CONFIG_CONTENT="+opencodeConfigContent,
	)
	return cfg.Execer.Exec(opencodePath, []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", cfg.Port}, childEnv)
}

// guest holds the resolved PATH for one bootstrap run.
type guest struct {
	cfg     GuestConfig
	pathEnv string
	env     []string
}

func applyGuestDefaults(cfg *GuestConfig) {
	if cfg.Stdin == nil {
		cfg.Stdin = os.Stdin
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Runner == nil {
		cfg.Runner = OSRunner{}
	}
	if cfg.Execer == nil {
		cfg.Execer = SysExecer{}
	}
	if cfg.Chdir == nil {
		cfg.Chdir = os.Chdir
	}
	if cfg.Environ == nil {
		cfg.Environ = os.Environ()
	}
	if cfg.Home == "" {
		cfg.Home, _ = os.UserHomeDir()
	}
	if cfg.HTTPGet == nil {
		cfg.HTTPGet = httpGet
	}
	if cfg.Port == "" {
		cfg.Port = fmt.Sprint(DefaultPort)
	}
	if cfg.Username == "" {
		cfg.Username = DefaultUsername
	}
}

// guestPath builds the guest PATH: the inherited PATH plus the macOS
// administration directories (route, ifconfig) and the curl installer's
// ~/.opencode/bin.
func guestPath(environ []string, home string) string {
	current := ""
	for _, kv := range environ {
		if strings.HasPrefix(kv, "PATH=") {
			current = strings.TrimPrefix(kv, "PATH=")
		}
	}
	dirs := []string{current, "/usr/sbin", "/sbin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".opencode", "bin"))
	}
	var out []string
	for _, d := range dirs {
		if d != "" {
			out = append(out, d)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// applyMTU validates and applies the requested MTU to the guest's default
// IPv4 interface. The value was already validated host-side; this is a backstop.
func applyMTU(ctx context.Context, cfg GuestConfig, env []string, mtu string) error {
	if err := ValidateMTU(mtu); err != nil {
		return err
	}
	res, err := cfg.Runner.RunEnv(ctx, env, "route", "-n", "get", "default")
	if err != nil {
		return err
	}
	iface := parseDefaultInterface(res.Stdout)
	if iface == "" {
		return fmt.Errorf("cannot determine the guest default network interface for TART_MTU")
	}
	res, err = cfg.Runner.RunEnv(ctx, env, "sudo", "-n", "ifconfig", iface, "mtu", mtu)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("cannot apply TART_MTU=%s to %s (passwordless sudo required)", mtu, iface)
	}
	fmt.Fprintf(cfg.Stdout, "Guest network: %s MTU=%s\n", iface, mtu)
	return nil
}

// parseDefaultInterface extracts the interface from `route -n get default`
// output, e.g. "  interface: en0".
func parseDefaultInterface(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "interface:" {
			return fields[1]
		}
	}
	return ""
}

// readSecrets reads the password (line 1) and ALBERT_API_KEY (line 2) from the
// host's stdin stream. A missing password line falls back to the default; an
// explicitly empty line means "no auth".
func readSecrets(stdin io.Reader) (string, string, error) {
	r := bufio.NewReader(stdin)
	password := DefaultPassword
	if line, err := r.ReadString('\n'); err == nil {
		password = trimLine(line)
	} else if err != io.EOF {
		return "", "", err
	}
	apiKey := ""
	if line, err := r.ReadString('\n'); err == nil {
		apiKey = trimLine(line)
	} else if err != io.EOF {
		return "", "", err
	}
	return password, apiKey, nil
}

func trimLine(line string) string {
	return strings.TrimRight(line, "\r\n")
}

// resolve finds an executable by name on the guest PATH.
func (g *guest) resolve(name string) (string, error) {
	for _, dir := range filepath.SplitList(g.pathEnv) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH", name)
}

func (g *guest) run(ctx context.Context, name string, args ...string) (int, error) {
	res, err := g.cfg.Runner.RunEnv(ctx, g.env, name, args...)
	if err != nil {
		return -1, err
	}
	return res.ExitCode, nil
}

// installOpencode mirrors the shell fallback chain: Homebrew, then npm, then
// the vendor installer script. The vendor installer is itself a shell script,
// so it is run with bash — that is the vendor's choice, not our logic.
func (g *guest) installOpencode(ctx context.Context) error {
	if _, err := g.resolve("brew"); err == nil {
		if code, _ := g.run(ctx, "brew", "install", "anomalyco/tap/opencode"); code == 0 {
			return nil
		}
		if _, err := g.resolve("npm"); err == nil {
			if code, _ := g.run(ctx, "npm", "install", "-g", "opencode-ai"); code == 0 {
				return nil
			}
		}
	}
	return g.runInstallerScript(ctx)
}

func (g *guest) runInstallerScript(ctx context.Context) error {
	script, err := g.cfg.HTTPGet(opencodeInstallURL)
	if err != nil {
		return fmt.Errorf("cannot download the OpenCode installer: %w", err)
	}
	tmp, err := os.CreateTemp("", "just-code-opencode-install-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(script); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if code, err := g.run(ctx, "bash", tmp.Name()); err != nil {
		return err
	} else if code != 0 {
		return fmt.Errorf("the OpenCode installer failed (exit %d)", code)
	}
	return nil
}

// httpGet is the default installer fetch, bounded by a timeout.
func httpGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
