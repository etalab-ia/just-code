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
	cfg := justcode.LoadConfigEnv()
	d := justcode.NewDispatcher(cfg)

	cmd, rest := resolveCommand(args)

	switch cmd {
	case "code":
		return codeCmd(d, cfg, rest)
	case "start":
		return lifecycleCmd(d, rest, "start")
	case "stop":
		return 0, d.StopAll(context.Background())
	case "check":
		return 0, d.Check(context.Background(), nil)
	case "logs", "shell", "build", "restart", "clean", "doctor":
		return lifecycleCmd(d, rest, cmd)
	case "help", "-h", "--help":
		usage()
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown command %q", cmd)
	}
}

// resolveCommand maps an invocation to a command and its remaining arguments.
// A bare invocation (or one leading with a flag) runs `code`, matching the old
// `just code` default; a known command name dispatches to its handler.
func resolveCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "code", nil
	}
	switch args[0] {
	case "code", "start", "stop", "check", "logs", "shell", "build", "restart", "clean", "doctor", "help", "-h", "--help":
		return args[0], args[1:]
	default:
		return "code", args
	}
}

func codeCmd(d *justcode.Dispatcher, cfg justcode.Config, args []string) (int, error) {
	rt, err := resolveRuntimeArg(args)
	if err != nil {
		return 0, err
	}
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
	if err := justcode.WaitHealthy(ctx, endpoint, cfg.Username, cfg.Password, justcode.DefaultHealthConfig(), nil); err != nil {
		return 0, err
	}

	attachErr := justcode.RunInteractive("opencode", "attach", endpoint, "--username", cfg.Username, "--password", cfg.Password)
	code := exitCodeOf(attachErr)
	askToStop(ctx, d, rt)
	return code, attachErr
}

func lifecycleCmd(d *justcode.Dispatcher, args []string, action string) (int, error) {
	rt, err := resolveRuntimeArg(args)
	if err != nil {
		return 0, err
	}
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

// resolveRuntimeArg scans args for --docker, --microsandbox, or --tart (the
// explicit flag wins) and otherwise falls back to RUNTIME.
func resolveRuntimeArg(args []string) (justcode.Runtime, error) {
	var flag string
	for _, a := range args {
		switch a {
		case "--docker", "--microsandbox", "--tart":
			if flag != "" && flag != a {
				return "", fmt.Errorf("conflicting runtime flags %q and %q", flag, a)
			}
			flag = a
		default:
			if strings.HasPrefix(a, "--") {
				return "", fmt.Errorf("unknown argument %q", a)
			}
		}
	}
	return justcode.ResolveRuntime(flag, os.Getenv("RUNTIME"))
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
  just-code [--docker|--microsandbox|--tart]         start a backend and attach the OpenCode TUI
  just-code code [--docker|--microsandbox|--tart]    alias for the bare command
  just-code start [--docker|--microsandbox|--tart]   start a backend without attaching
  just-code stop                                     stop every running runtime
  just-code check                                    health + Albert provider of the running backend
  just-code logs --docker|--microsandbox|--tart      follow the backend log
  just-code shell --docker|--microsandbox|--tart     open a shell inside the runtime
  just-code build --docker|--microsandbox|--tart     pull or update the runtime image
  just-code restart --docker|--microsandbox|--tart   recreate the sandbox (destructive)
  just-code clean --docker|--microsandbox|--tart     remove the sandbox and its state
  just-code doctor --docker|--microsandbox|--tart    verify the runtime installation
  just-code help                                     show this help

Runtime: pass --docker, --microsandbox, or --tart explicitly, or set RUNTIME in
.env to omit the flag. An explicit flag always takes precedence.`)
}
