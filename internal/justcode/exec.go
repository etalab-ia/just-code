package justcode

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// ExecResult is the outcome of a host command. A nonzero ExitCode is a normal
// result (predicates such as pgrep rely on it); Err is set only when the
// command could not be launched at all.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Runner executes host-side commands (the `tart` CLI and friends).
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (ExecResult, error)
}

// Starter launches a detached, long-running process (tart run / tart exec -i)
// with optional stdin and its output appended to a log file. It returns once
// the process has started, leaving it to outlive the CLI.
type Starter interface {
	Start(stdin io.Reader, logPath string, name string, args ...string) error
}

// OSRunner is the real Runner, backed by os/exec.
type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	res := ExecResult{Stdout: string(out), Stderr: string(out)}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// OSStarter is the real Starter. It detaches the child (new session) so the
// long-running process survives the CLI exiting, matching `nohup ... &`.
type OSStarter struct{}

func (OSStarter) Start(stdin io.Reader, logPath string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return err
	}
	log.Close() // the child keeps its own copy of the descriptor
	return cmd.Process.Release()
}
