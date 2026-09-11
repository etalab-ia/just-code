package justcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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

// Runner executes host-side commands (the `tart`, `docker`, and `msb` CLIs).
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (ExecResult, error)
	// RunEnv runs a command with extra environment variables appended to the
	// inherited environment.
	RunEnv(ctx context.Context, env []string, name string, args ...string) (ExecResult, error)
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
	return osRun(ctx, nil, name, args...)
}

func (OSRunner) RunEnv(ctx context.Context, env []string, name string, args ...string) (ExecResult, error) {
	return osRun(ctx, env, name, args...)
}

func osRun(ctx context.Context, env []string, name string, args ...string) (ExecResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = withEnv(os.Environ(), env...)
	}
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

// maxStdinPayload bounds the detached child's stdin payload. os/exec only
// passes a descriptor straight through when Stdin is an *os.File; anything else
// gets a copying goroutine, which a detached child can outlive. Writing
// synchronously into a pipe avoids that, and keeping the payload well under any
// platform pipe buffer guarantees the write cannot block before the child
// starts. Passwords and API keys are far smaller than this.
const maxStdinPayload = 4096

// OSStarter is the real Starter. It detaches the child (new session) so the
// long-running process survives the CLI exiting, matching `nohup ... &`.
type OSStarter struct{}

func (OSStarter) Start(stdin io.Reader, logPath string, name string, args ...string) error {
	cmd := exec.Command(name, args...)

	var stdinPipe *os.File
	if stdin != nil {
		payload, err := io.ReadAll(io.LimitReader(stdin, maxStdinPayload+1))
		if err != nil {
			return err
		}
		if len(payload) > maxStdinPayload {
			return fmt.Errorf("stdin payload exceeds %d bytes", maxStdinPayload)
		}
		pr, pw, err := os.Pipe()
		if err != nil {
			return err
		}
		if _, err := pw.Write(payload); err != nil {
			pw.Close()
			pr.Close()
			return err
		}
		pw.Close() // the child sees EOF once it drains the buffer
		stdinPipe = pr
		cmd.Stdin = pr
	}

	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		if stdinPipe != nil {
			stdinPipe.Close()
		}
		return err
	}
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		if stdinPipe != nil {
			stdinPipe.Close()
		}
		log.Close()
		return err
	}
	if stdinPipe != nil {
		stdinPipe.Close() // the child keeps its own copy of the descriptor
	}
	log.Close() // the child keeps its own copy of the descriptor
	return cmd.Process.Release()
}

// runOK runs a command and treats a nonzero exit as an error. It is used for
// lifecycle commands where success is required (docker compose up, msb run).
func runOK(r Runner, ctx context.Context, name string, args ...string) error {
	res, err := r.Run(ctx, name, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s %s failed (exit %d)", name, strings.Join(args, " "), res.ExitCode)
	}
	return nil
}

// runEnvOK is runOK with additional environment variables.
func runEnvOK(r Runner, ctx context.Context, env []string, name string, args ...string) error {
	res, err := r.RunEnv(ctx, env, name, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s %s failed (exit %d)", name, strings.Join(args, " "), res.ExitCode)
	}
	return nil
}

// RunInteractive runs a command attached to the current terminal's stdio. It is
// used for `logs --follow` and interactive `shell`/`attach` commands.
func RunInteractive(name string, args ...string) error {
	return RunInteractiveEnv(nil, name, args...)
}

// RunInteractiveEnv is RunInteractive with additional environment variables.
func RunInteractiveEnv(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if len(env) > 0 {
		cmd.Env = withEnv(os.Environ(), env...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// withEnv returns base with the given KEY=VALUE pairs applied, replacing any
// existing entry for the same key. Appending instead would leave duplicates,
// and env lookup returns the first match, so the inherited value would win.
func withEnv(base []string, kv ...string) []string {
	replaced := make(map[string]bool, len(kv))
	for _, pair := range kv {
		if i := strings.IndexByte(pair, '='); i >= 0 {
			replaced[pair[:i]] = true
		}
	}
	out := make([]string, 0, len(base)+len(kv))
	for _, entry := range base {
		key := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			key = entry[:i]
		}
		if replaced[key] {
			continue
		}
		out = append(out, entry)
	}
	return append(out, kv...)
}

// commandNotFound reports whether err means the command binary is missing.
func commandNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}
