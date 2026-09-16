package justcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestAgentVM(t *testing.T, runner Runner) *AgentVM {
	t.Helper()
	return &AgentVM{
		Config: Config{
			AgentVMTemplate: DefaultAgentVMTemplate,
			AgentVMVM:      DefaultAgentVMVM,
			Username:       "opencode",
			Password:       "pw",
			PasswordSet:    true,
			WorkspaceDir:   t.TempDir(),
			APIKey:         "key",
		},
		Runner:           runner,
		Starter:          &fakeStarter{},
		StateDir:         t.TempDir(),
		KillPollInterval: time.Millisecond,
		KillMaxPolls:     3,
	}
}

func TestParseLimaList(t *testing.T) {
	out := "agent-vm-base|Stopped\nopencode-agent-vm|Running\nother|Broken\n"
	got := ParseLimaList(out)
	if len(got) != 3 {
		t.Fatalf("ParseLimaList = %v, want 3 entries", got)
	}
	if !got["opencode-agent-vm"] {
		t.Errorf("opencode-agent-vm should be running")
	}
	if got["agent-vm-base"] {
		t.Errorf("agent-vm-base is Stopped, must not read as running")
	}
	if got["other"] {
		t.Errorf("Broken state must not read as running")
	}
}

func TestLimaListFailedQueryIsError(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && len(args) > 0 && args[0] == "list" {
			return ExecResult{ExitCode: 1, Stderr: "limactl failed"}
		}
		return ExecResult{ExitCode: 0}
	}}
	a := newTestAgentVM(t, runner)
	if _, err := a.vmExists(context.Background()); err == nil {
		t.Errorf("a failed limactl list must be an error, never \"VM absent\"")
	}
}

func TestMountsJSON(t *testing.T) {
	got := mountsJSON("/tmp/ws")
	want := `[{"location":"/tmp/ws","writable":true}]`
	if got != want {
		t.Errorf("mountsJSON = %q, want %q", got, want)
	}
}

func TestAgentVMStartCreatesClonesEditsAndLaunches(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		switch {
		case name == "limactl" && args[0] == "list":
			// Template exists, managed VM does not.
			return ExecResult{ExitCode: 0, Stdout: DefaultAgentVMTemplate + "|Stopped\n"}
		default:
			return ExecResult{ExitCode: 0}
		}
	}}
	a := newTestAgentVM(t, runner)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !runner.hasCall("limactl clone " + DefaultAgentVMTemplate + " " + DefaultAgentVMVM) {
		t.Errorf("expected clone from template, calls: %v", runner.calls)
	}
	if !runner.hasCall("limactl edit " + DefaultAgentVMVM) {
		t.Errorf("expected mount edit after clone, calls: %v", runner.calls)
	}
	if !runner.hasCall("limactl copy") {
		t.Errorf("expected secrets env push via limactl copy, calls: %v", runner.calls)
	}
	starter := a.Starter.(*fakeStarter)
	if starter.lastName != "limactl" {
		t.Errorf("expected detached limactl launch, got %q", starter.lastName)
	}
	launch := strings.Join(starter.lastArgs, " ")
	if !strings.Contains(launch, "opencode serve") {
		t.Errorf("launch args must run opencode serve: %q", launch)
	}
	if strings.Contains(launch, "pw") || strings.Contains(launch, "key") {
		t.Errorf("secrets must never appear in argv: %q", launch)
	}
}

func TestAgentVMStartMissingTemplate(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && args[0] == "list" {
			return ExecResult{ExitCode: 0, Stdout: ""}
		}
		return ExecResult{ExitCode: 0}
	}}
	a := newTestAgentVM(t, runner)
	err := a.Start(context.Background())
	if err == nil {
		t.Fatal("expected an error when the base template is missing")
	}
	if !strings.Contains(err.Error(), "agent-vm setup") {
		t.Errorf("error must point at agent-vm setup: %v", err)
	}
}

func TestAgentVMStartRunningDeadBackendRelaunches(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		switch {
		case name == "limactl" && args[0] == "list":
			return ExecResult{ExitCode: 0, Stdout: DefaultAgentVMVM + "|Running\n"}
		case name == "limactl" && len(args) > 2 && args[2] == "pgrep":
			// No opencode process: the stale-backend kill loop must see it exit.
			return ExecResult{ExitCode: 1}
		default:
			return ExecResult{ExitCode: 0}
		}
	}}
	a := newTestAgentVM(t, runner)
	// The health probe fails (nothing listens on the fixed endpoint), so a
	// running VM with a dead backend must be relaunched, not re-cloned.
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start with dead backend must relaunch, got: %v", err)
	}
	if runner.hasCall("limactl clone") {
		t.Errorf("a running VM must not be re-cloned")
	}
	starter := a.Starter.(*fakeStarter)
	if starter.lastName != "limactl" {
		t.Errorf("expected backend relaunch, got %q", starter.lastName)
	}
}

func TestAgentVMStop(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && args[0] == "list" {
			return ExecResult{ExitCode: 0, Stdout: DefaultAgentVMVM + "|Running\n"}
		}
		return ExecResult{ExitCode: 0}
	}}
	a := newTestAgentVM(t, runner)
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !runner.hasCall("limactl stop " + DefaultAgentVMVM) {
		t.Errorf("expected limactl stop, calls: %v", runner.calls)
	}
}

func TestAgentVMStopNotRunning(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && args[0] == "list" {
			return ExecResult{ExitCode: 0, Stdout: DefaultAgentVMVM + "|Stopped\n"}
		}
		return ExecResult{ExitCode: 0}
	}}
	a := newTestAgentVM(t, runner)
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop on a stopped VM: %v", err)
	}
	if runner.hasCall("limactl stop") {
		t.Errorf("must not stop a VM that is not running")
	}
}

func TestAgentVMClean(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && args[0] == "list" {
			return ExecResult{ExitCode: 0, Stdout: DefaultAgentVMVM + "|Running\n"}
		}
		return ExecResult{ExitCode: 0}
	}}
	a := newTestAgentVM(t, runner)
	if err := a.Clean(context.Background()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !runner.hasCall("limactl delete " + DefaultAgentVMVM) {
		t.Errorf("expected limactl delete, calls: %v", runner.calls)
	}
}

func TestAgentVMDoctorMissingLimaCtl(t *testing.T) {
	// commandNotFound path is exercised in tart_test via a missing binary;
	// here the nonzero-exit path.
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1}
	}}
	a := newTestAgentVM(t, runner)
	if err := a.Doctor(context.Background()); err == nil {
		t.Errorf("doctor must fail when limactl is unusable")
	}
}

func TestAgentVMDoctorMissingTemplate(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && args[0] == "list" {
			return ExecResult{ExitCode: 0, Stdout: ""}
		}
		return ExecResult{ExitCode: 0, Stdout: "limactl version 1.0\n"}
	}}
	a := newTestAgentVM(t, runner)
	err := a.Doctor(context.Background())
	if err == nil {
		t.Fatal("expected doctor to fail on missing template")
	}
	if !strings.Contains(err.Error(), "agent-vm setup") {
		t.Errorf("error must point at agent-vm setup: %v", err)
	}
}

func TestAgentVMWriteSecretsEnv(t *testing.T) {
	a := newTestAgentVM(t, &fakeRunner{})
	dir := t.TempDir()
	path, err := a.writeSecretsEnv(dir)
	if err != nil {
		t.Fatalf("writeSecretsEnv: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "OPENCODE_SERVER_PASSWORD=pw") {
		t.Errorf("env file must carry the password: %q", content)
	}
	if !strings.Contains(content, "ALBERT_API_KEY=key") {
		t.Errorf("env file must carry the API key: %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("env file must be 0600, got %v", info.Mode().Perm())
	}
}

func TestAgentVMEndpoint(t *testing.T) {
	a := newTestAgentVM(t, &fakeRunner{})
	endpoint, err := a.Endpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://127.0.0.1:4096" {
		t.Errorf("Endpoint = %q, want http://127.0.0.1:4096", endpoint)
	}
}

func TestAgentVMLogPath(t *testing.T) {
	if got := AgentVMLogPath("/state"); got != filepath.Join("/state", "agent-vm.log") {
		t.Errorf("AgentVMLogPath = %q", got)
	}
}
