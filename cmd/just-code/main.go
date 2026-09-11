// Command just-code is a single Go binary that replaces the original justfile.
// It manages an OpenCode backend in Docker, Microsandbox, or a Tart macOS VM.
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

	cfg := justcode.LoadConfigEnv()
	d := justcode.NewDispatcher(cfg)

	parsed, err := parseArgs(args)
	if err != nil {
		return 2, err
	}
	if parsed.version {
		printBuildInfo()
		return 0, nil
	}

	// These actions resolve their own target (or need none), so they must not be
	// gated on a configured runtime.
	switch parsed.action {
	case "help":
		usage()
		return 0, nil
	case "version":
		printBuildInfo()
		return 0, nil
	case "stop":
		return 0, d.StopAll(context.Background())
	case "check":
		return 0, d.Check(context.Background(), nil)
	}

	rt, err := justcode.ResolveRuntime(parsed.runtime, os.Getenv("RUNTIME"))
	if err != nil {
		return 2, err
	}

	switch parsed.action {
	case "attach":
		return attachCmd(d, cfg, rt)
	case "start", "logs", "shell", "build", "restart", "clean", "doctor":
		return lifecycleCmd(d, rt, parsed.action)
	default:
		return 2, fmt.Errorf("Unknown argument: %s", parsed.action)
	}
}

// parsedArgs is the result of a single pass over argv. Commands, runtime flags,
// and the version flag may appear in any order.
type parsedArgs struct {
	// action is "attach" (the default) or one of the named commands.
	action string
	// runtime is the raw --docker/--microsandbox/--tart flag, or "".
	runtime string
	version bool
}

// actionNames lists the commands that can be typed. It deliberately excludes
// "code": a bare invocation starts the backend and attaches the TUI, so there
// is no such command to type. "version" is accepted as a convenience beyond the
// TypeScript CLI, which exposes only -V/--version.
var actionNames = map[string]bool{
	"start": true, "stop": true, "check": true, "logs": true, "shell": true,
	"build": true, "restart": true, "clean": true, "doctor": true,
	"help": true, "version": true,
}

func runtimeFlag(a string) bool {
	return a == "--docker" || a == "--microsandbox" || a == "--tart"
}

// parseArgs mirrors the TypeScript CLI's parser: a single pass that accepts the
// action and runtime flags in any order, and rejects anything unrecognised
// rather than silently ignoring it.
func parseArgs(args []string) (parsedArgs, error) {
	var p parsedArgs
	actionSet := false
	for _, a := range args {
		switch {
		case runtimeFlag(a):
			if p.runtime != "" && p.runtime != a {
				return p, fmt.Errorf("Select exactly one runtime.")
			}
			p.runtime = a
		case a == "-h" || a == "--help":
			p.action = "help"
			actionSet = true
		case a == "-V" || a == "--version" || a == "-v":
			p.version = true
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
	case "build":
		err = b.Build(ctx)
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
	fmt.Println(`just-code - a single Go binary for the OpenCode sandbox

Usage:
  just-code [command] [--docker | --microsandbox | --tart]

Run just-code with no command to start the selected backend and attach the
native OpenCode TUI.

Commands:
  start      Start a backend without attaching the TUI
  stop       Stop every running just-code runtime
  check      Check the active backend and Albert provider
  build      Build or pull the selected runtime image
  restart    Recreate the selected sandbox (destructive)
  logs       Follow logs for the selected runtime
  shell      Open a shell inside the selected runtime
  clean      Remove the selected sandbox and its local state
  doctor     Check the selected runtime installation
  version    Print the build identity
  help       Show this help

Runtime selection:
  Pass --docker, --microsandbox, or --tart. RUNTIME in .env is used when no
  flag is provided; an explicit flag always takes precedence.`)
}
