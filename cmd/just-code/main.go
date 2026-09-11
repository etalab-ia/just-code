// Command just-code is the Go port of the just-code CLI. This first PR
// implements the Tart runtime lifecycle; Docker and Microsandbox remain in the
// justfile.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "just-code:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "start":
		return startCmd(rest)
	case "stop":
		return stopCmd()
	case "check":
		return checkCmd()
	case "logs":
		return logsCmd(rest)
	case "build":
		return buildCmd(rest)
	case "clean":
		return cleanCmd(rest)
	case "doctor":
		return doctorCmd(rest)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// resolveRuntime scans args for --docker, --microsandbox, or --tart (the
// explicit flag wins) and otherwise falls back to RUNTIME.
func resolveRuntime(args []string) (justcode.Runtime, error) {
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

func requireTart(rt justcode.Runtime) error {
	if rt != justcode.RuntimeTart {
		return fmt.Errorf("the %s runtime is not ported to Go yet; use `just code --%s`", rt, rt)
	}
	return nil
}

func startCmd(args []string) error {
	wait := false
	var rest []string
	for _, a := range args {
		if a == "--wait" {
			wait = true
		} else {
			rest = append(rest, a)
		}
	}
	rt, err := resolveRuntime(rest)
	if err != nil {
		return err
	}
	if err := requireTart(rt); err != nil {
		return err
	}
	ctx := context.Background()
	t := justcode.NewTart(justcode.LoadConfig(nil))
	if err := t.Start(ctx); err != nil {
		return err
	}
	if !wait {
		return nil
	}
	ip, err := t.IP(ctx, t.Config.TartVM)
	if err != nil {
		return err
	}
	endpoint := "http://" + ip + ":" + strconv.Itoa(justcode.DefaultPort)
	return justcode.WaitHealthy(ctx, endpoint, t.Config.Username, t.Config.Password, justcode.DefaultHealthConfig(), nil)
}

func stopCmd() error {
	t := justcode.NewTart(justcode.LoadConfig(nil))
	return t.StopAll(context.Background())
}

func checkCmd() error {
	t := justcode.NewTart(justcode.LoadConfig(nil))
	return t.Check(context.Background(), nil)
}

func logsCmd(args []string) error {
	rt, err := resolveRuntime(args)
	if err != nil {
		return err
	}
	if err := requireTart(rt); err != nil {
		return err
	}
	t := justcode.NewTart(justcode.LoadConfig(nil))
	cmd := exec.Command("tail", "-f", t.LogPath())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func buildCmd(args []string) error {
	rt, err := resolveRuntime(args)
	if err != nil {
		return err
	}
	if err := requireTart(rt); err != nil {
		return err
	}
	t := justcode.NewTart(justcode.LoadConfig(nil))
	return t.Build(context.Background())
}

func cleanCmd(args []string) error {
	rt, err := resolveRuntime(args)
	if err != nil {
		return err
	}
	if err := requireTart(rt); err != nil {
		return err
	}
	t := justcode.NewTart(justcode.LoadConfig(nil))
	return t.Clean(context.Background())
}

func doctorCmd(args []string) error {
	rt, err := resolveRuntime(args)
	if err != nil {
		return err
	}
	if err := requireTart(rt); err != nil {
		return err
	}
	t := justcode.NewTart(justcode.LoadConfig(nil))
	return t.Doctor(context.Background())
}

func usage() {
	fmt.Println(`just-code — Go port (Tart runtime)

Usage:
  just-code start [--tart] [--wait]   start the backend, optionally wait for health
  just-code stop                      stop every running just-code runtime
  just-code check                     health + Albert provider of the running backend
  just-code logs --tart               follow the backend log
  just-code build --tart              pull or update the base image
  just-code clean --tart              stop and delete the VM and its state
  just-code doctor --tart             verify the Tart installation

Runtime: pass --tart explicitly, or set RUNTIME=tart in the environment.
Docker and Microsandbox are not ported to Go yet; use the justfile for them.`)
}
