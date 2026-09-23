// Command just-code is a single Go binary that replaces the original justfile.
// It manages an OpenCode backend in a Microsandbox microVM or a Tart macOS VM.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "just-code:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	// Hidden flag for CI (msb-runtime-watch workflow): prints the Microsandbox
	// SDK version the binary embeds, without touching config or runtimes.
	if len(args) == 1 && args[0] == "-print-msb-sdk-version" {
		fmt.Println(justcode.MSBSDKVersion())
		return 0, nil
	}

	// The in-VM bootstrap is a hidden subcommand of this same binary. It runs
	// inside the Tart guest and must not touch host config or load .env.
	if len(args) > 0 && args[0] == justcode.GuestBootstrapCommand {
		cfg := justcode.GuestConfig{
			Port:     argOr(args, 1, ""),
			Username: argOr(args, 2, ""),
			MTU:      argOr(args, 3, ""),
		}
		return 0, justcode.RunGuestBootstrap(context.Background(), cfg)
	}

	// Isolation-full in-VM helpers, same hidden-subcommand contract: no host
	// config, no .env. __guest-prepare installs OpenCode; __guest-secrets
	// writes the 0600 env file from stdin.
	if len(args) > 0 && args[0] == justcode.GuestPrepareCommand {
		cfg := justcode.GuestConfig{Username: argOr(args, 1, "")}
		return exitCodeOf(nil), justcode.RunGuestPrepare(context.Background(), cfg)
	}
	if len(args) > 0 && args[0] == justcode.GuestSecretsCommand {
		cfg := justcode.GuestConfig{Username: argOr(args, 1, "")}
		return exitCodeOf(nil), justcode.RunGuestSecrets(cfg)
	}

	cfg := justcode.LoadConfigEnv()

	parsed, err := parseArgs(args)
	if err != nil {
		return 2, err
	}
	if parsed.version {
		printBuildInfo()
		return 0, nil
	}

	// Resolve the isolation level before any backend is constructed: the
	// backends read cfg.Isolation at construction time, so applying the flag
	// afterwards would leave them in the wrong mode.
	cfg, isoErr := applyIsolation(cfg, parsed.isolation)

	// The dispatcher and its backends are built from the resolved config.
	d := justcode.NewDispatcher(cfg)

	// These actions resolve their own target (or need none), so they must not be
	// gated on a configured runtime. check consumes the isolation level, so it
	// is the one action here that must surface a deferred isolation error.
	switch parsed.action {
	case "help":
		usage()
		return 0, nil
	case "version":
		printBuildInfo()
		return 0, nil
	case "config":
		return configCmd(parsed.configArgs)
	case "stop":
		return 0, d.StopAll(context.Background())
	case "check":
		if isoErr != nil {
			return 2, isoErr
		}
		return 0, d.Check(context.Background(), nil)
	}

	rt, err := justcode.ResolveRuntime(parsed.runtime, os.Getenv("RUNTIME"))
	if err != nil {
		return 2, err
	}
	// A typo in ISOLATION must not block commands that never read it, so it is
	// validated only here, where the level is actually consumed.
	if isoErr != nil {
		return 2, isoErr
	}

	switch parsed.action {
	case "attach":
		return attachCmd(d, cfg, rt)
	case "start", "logs", "shell", "restart", "clean", "doctor":
		return lifecycleCmd(d, rt, parsed.action)
	default:
		return 2, fmt.Errorf("Unknown argument: %s", parsed.action)
	}
}

// applyIsolation returns cfg with the effective isolation level applied, plus
// any deferred error. It exists as a separate step because the backends read
// cfg.Isolation when they are constructed: resolving the level after building
// the dispatcher would leave them in the wrong mode.
func applyIsolation(cfg justcode.Config, flag string) (justcode.Config, error) {
	iso, err := justcode.ResolveIsolationLevel(flag, cfg.Isolation, cfg.IsolationErr)
	if err != nil {
		return cfg, err
	}
	cfg.Isolation = iso
	// The level is resolved now, so a deferred env error would be stale.
	cfg.IsolationErr = nil
	return cfg, nil
}

// parsedArgs is the result of a single pass over argv. Commands, runtime flags,
// the isolation flag, and the version flag may appear in any order.
type parsedArgs struct {
	// action is "attach" (the default) or one of the named commands.
	action string
	// runtime is the raw --microsandbox/--tart flag, or "".
	runtime string
	// isolation is the raw --isolation value (backend or full), or "".
	isolation string
	version   bool
	// configArgs holds the words after the config command.
	configArgs []string
}

// actionNames lists the commands that can be typed. It deliberately excludes
// "code": a bare invocation starts the backend and attaches the TUI, so there
// is no such command to type. "version" is accepted as a convenience beyond the
// TypeScript CLI, which exposes only -V/--version.
var actionNames = map[string]bool{
	"start": true, "stop": true, "check": true, "logs": true, "shell": true,
	"restart": true, "clean": true, "doctor": true,
	"help": true, "version": true, "config": true,
}

func runtimeFlag(a string) bool {
	return a == "--microsandbox" || a == "--tart" || a == "--agent-vm"
}

// parseArgs mirrors the TypeScript CLI's parser: a single pass that accepts the
// action and runtime flags in any order, and rejects anything unrecognised
// rather than silently ignoring it. The isolation flag takes a value, either
// as the next token (--isolation full) or inline (--isolation=full), so the
// pass is index-based to consume the value.
func parseArgs(args []string) (parsedArgs, error) {
	var p parsedArgs
	actionSet := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case runtimeFlag(a):
			if p.runtime != "" && p.runtime != a {
				return p, fmt.Errorf("Select exactly one runtime.")
			}
			p.runtime = a
		case a == "--isolation" || strings.HasPrefix(a, "--isolation="):
			value := ""
			if a == "--isolation" {
				if i+1 >= len(args) {
					return p, fmt.Errorf("--isolation requires a value: backend or full")
				}
				i++
				value = args[i]
			} else {
				value = strings.TrimPrefix(a, "--isolation=")
			}
			if value == "" {
				return p, fmt.Errorf("--isolation requires a value: backend or full")
			}
			if p.isolation != "" && p.isolation != value {
				return p, fmt.Errorf("Select exactly one isolation level.")
			}
			p.isolation = value
		case a == "-h" || a == "--help":
			p.action = "help"
			actionSet = true
		case a == "-V" || a == "--version" || a == "-v":
			p.version = true
		case a == "config" && !actionSet:
			p.action = "config"
			actionSet = true
			// Everything after the config command belongs to it.
			p.configArgs = args[i+1:]
			return p, nil
		case actionNames[a] && !actionSet:
			p.action = a
			actionSet = true
		case a == "code":
			return p, fmt.Errorf("The 'code' command was removed: run 'just-code' with no command to start the backend and attach the TUI.")
		default:
			return p, fmt.Errorf("Unknown argument: %s", a)
		}
	}
	if p.action == "" {
		p.action = "attach"
	}
	return p, nil
}

// argOr returns args[i], or def when it is absent or empty.
func argOr(args []string, i int, def string) string {
	if i < len(args) && args[i] != "" {
		return args[i]
	}
	return def
}

// configCmd implements `just-code config <subcommand>`. It is read-only: it
// resolves and explains the effective configuration without mutating the
// environment or writing any file. `explain` shows each managed field with
// its winning source (flags > JUST_CODE_* env > project > user > default),
// secrets never included; `import-env` previews a bounded legacy .env import
// without touching the original file.
func configCmd(args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: just-code config explain | import-env <path>")
		return 2, nil
	}
	switch args[0] {
	case "explain":
		return configExplainCmd()
	case "import-env":
		if len(args) < 2 {
			return 2, fmt.Errorf("Usage: just-code config import-env <path-to-.env>")
		}
		return configImportEnvCmd(args[1])
	default:
		return 2, fmt.Errorf("Unknown config command: %s (expected explain or import-env)", args[0])
	}
}

// configExplainCmd resolves the managed fields and prints them with
// provenance. The legacy environment variables (RUNTIME, ISOLATION, ...) are
// NOT part of the new resolution: explain reports the new-path view only,
// so the two paths cannot be confused during the deprecation interval.
func configExplainCmd() (int, error) {
	settingsPath, err := justcode.UserSettingsPath()
	if err != nil {
		return 0, err
	}
	us, err := justcode.ReadUserSettings(justcode.DefaultFS, settingsPath)
	if err != nil {
		return 0, err
	}
	user := map[string]string{}
	if us.DefaultRuntime != "" {
		user["runtime"] = us.DefaultRuntime
	}
	if us.DefaultIsolation != "" {
		user["isolation"] = us.DefaultIsolation
	}
	if us.DefaultModel != "" {
		user["model"] = us.DefaultModel
	}
	if us.CredentialRef != "" {
		user["credential_ref"] = us.CredentialRef
	}
	env := justcode.EnvSettings(os.LookupEnv)
	cwd, _ := os.Getwd()
	proj := map[string]string{}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(cwd))
	if err == nil {
		for field, v := range map[string]string{
			"runtime":        pm.Runtime,
			"isolation":      pm.Isolation,
			"model":          pm.Model,
			"credential_ref": pm.CredentialRef,
		} {
			if v != "" {
				proj[field] = v
			}
		}
		if pm.CPUs > 0 {
			proj["cpus"] = fmt.Sprintf("%d", pm.CPUs)
		}
		if pm.MemoryMB > 0 {
			proj["memory_mb"] = fmt.Sprintf("%d", pm.MemoryMB)
		}
	}
	entries := justcode.Explain(justcode.Settings{
		Env:     env,
		Project: proj,
		User:    user,
	})
	fmt.Print(justcode.FormatExplain(entries))
	return 0, nil
}

// configImportEnvCmd previews the bounded legacy .env import. It never writes
// the original or any managed file; the user reviews the preview and applies
// the result explicitly later (P11's wizard owns the apply step).
func configImportEnvCmd(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	imp := justcode.ImportLegacyDotenv(string(data))
	fmt.Print(imp.FormatLegacyImport())
	return 0, nil
}

func attachCmd(d *justcode.Dispatcher, cfg justcode.Config, rt justcode.Runtime) (int, error) {
	b, err := d.Backend(rt)
	if err != nil {
		return 0, err
	}
	ctx := context.Background()
	if err := d.Prepare(ctx, rt); err != nil {
		return 0, err
	}
	if err := b.Start(ctx); err != nil {
		return 0, err
	}
	if cfg.Isolation == justcode.IsolationFull {
		// The whole agent lives in the guest: no host opencode preflight, no
		// health endpoint, no attach. The host is only a terminal passthrough.
		agentErr := b.RunAgent(ctx)
		code := exitCodeOf(agentErr)
		askToStop(ctx, d, rt)
		return code, agentErr
	}
	endpoint, err := b.Endpoint(ctx)
	if err != nil {
		return 0, err
	}
	// A typo in JUST_CODE_START_TIMEOUT must not block commands that never start
	// a runtime, so it is validated only here, where it is actually read.
	if cfg.StartTimeoutErr != nil {
		return 0, cfg.StartTimeoutErr
	}
	// Fail before starting a runtime rather than after, when the TUI cannot open.
	if _, err := exec.LookPath("opencode"); err != nil {
		return 0, fmt.Errorf("the 'opencode' CLI is required to attach the TUI but was not found on PATH; install it with 'npm install -g opencode-ai'")
	}
	fmt.Fprintf(os.Stderr, "Waiting for the backend at %s to become healthy...\n", endpoint)
	health := justcode.DefaultHealthConfig()
	health.Deadline = cfg.StartTimeout
	health.Progress = os.Stderr
	if err := justcode.WaitHealthy(ctx, endpoint, cfg.Username, cfg.Password, health, nil); err != nil {
		return 0, fmt.Errorf("%w\n  Run 'just-code logs --%s' to see why, or 'just-code restart --%s' to recreate it.", err, rt, rt)
	}

	attachErr := justcode.RunInteractive("opencode", "attach", endpoint, "--username", cfg.Username, "--password", cfg.Password)
	code := exitCodeOf(attachErr)
	askToStop(ctx, d, rt)
	return code, attachErr
}

func lifecycleCmd(d *justcode.Dispatcher, rt justcode.Runtime, action string) (int, error) {
	b, err := d.Backend(rt)
	if err != nil {
		return 0, err
	}
	ctx := context.Background()

	// start and restart refuse to run while another runtime is active.
	if action == "start" || action == "restart" {
		if err := d.Prepare(ctx, rt); err != nil {
			return 0, err
		}
	}

	switch action {
	case "start":
		err = b.Start(ctx)
	case "restart":
		err = b.Restart(ctx)
	case "clean":
		err = b.Clean(ctx)
	case "doctor":
		err = b.Doctor(ctx)
	case "logs":
		err = b.Logs()
	case "shell":
		err = b.Shell()
	default:
		err = fmt.Errorf("unsupported action %q", action)
	}
	return 0, err
}

func askToStop(ctx context.Context, d *justcode.Dispatcher, rt justcode.Runtime) {
	b, err := d.Backend(rt)
	if err != nil {
		return
	}
	running, err := b.IsRunning(ctx)
	if err != nil || !running {
		return
	}
	if !isTTY() {
		fmt.Printf("%s left running. Run `just-code stop` when finished.\n", rt)
		return
	}
	fmt.Printf("Stop the %s runtime? [y/N] ", rt)
	reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(reply)) {
	case "y", "yes":
		_ = b.Stop(ctx)
	default:
		fmt.Printf("%s left running. Run `just-code stop` when finished.\n", rt)
	}
}

func isTTY() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func usage() {
	fmt.Println(`just-code - manage the OpenCode sandbox

Usage:
  just-code [command] [--microsandbox | --tart | --agent-vm] [--isolation backend | full]

Run just-code with no command to start the selected backend and attach the
native OpenCode TUI.

Commands:
  start      Start a backend without attaching the TUI
  stop       Stop every running just-code runtime
  check      Check the active backend and Albert provider
  restart    Recreate the selected sandbox (destructive)
  logs       Follow logs for the selected runtime
  shell      Open a shell inside the selected runtime
  clean      Remove the selected sandbox and its local state
  doctor     Check the selected runtime installation
  config     Show or preview managed configuration (explain, import-env)
  version    Print the build identity
  help       Show this help

Runtime selection:
  Pass --microsandbox, --tart or --agent-vm. RUNTIME in .env is used when no
  flag is provided; an explicit flag always takes precedence. On Windows,
  --microsandbox is the default and the only supported runtime.

Isolation:
  --isolation backend (default): the agent runs as a server inside the
  sandbox and the TUI attaches from the host.
  --isolation full: the whole agent, TUI included, runs inside the sandbox;
  the host is only a terminal passthrough. ISOLATION in .env is used when no
  flag is provided; the flag always takes precedence.`)
}
