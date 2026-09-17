package justcode

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestBaseTemplateCreateArgs(t *testing.T) {
	got := baseTemplateCreateArgs(BaseTemplateSpec{
		Name: "agent-vm-base", Image: "template:debian-13",
		DiskGB: 20, MemoryGB: 4, CPUs: 2,
	})
	want := []string{
		"create",
		"--name=agent-vm-base",
		"template:debian-13",
		"--set", ".mounts=[]",
		"--disk=20",
		"--memory=4",
		"--cpus=2",
		"--tty=false",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("baseTemplateCreateArgs =\n  %v\nwant\n  %v", got, want)
	}
}

// The base template must be created mountless: a workspace mount baked in here
// would leak into every VM cloned from it.
func TestBaseTemplateCreateArgsHasNoMounts(t *testing.T) {
	got := strings.Join(baseTemplateCreateArgs(BaseTemplateSpec{
		Name: "agent-vm-base", Image: "template:debian-13", DiskGB: 20, MemoryGB: 4, CPUs: 2,
	}), " ")
	if !strings.Contains(got, "--set .mounts=[]") {
		t.Errorf("template must be created with empty mounts, got %q", got)
	}
	// --set consumes the next argv element, so a bare mount value would be a
	// sign the flag was dropped.
	if strings.Contains(got, "writable") {
		t.Errorf("template creation must not carry a workspace mount: %q", got)
	}
}

func TestBuildBaseTemplateLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	var out strings.Builder
	spec := BaseTemplateSpec{Name: "agent-vm-base", Image: "template:debian-13", DiskGB: 20, MemoryGB: 4, CPUs: 2}
	if err := BuildBaseTemplate(context.Background(), runner, spec, "echo prep\n", &out); err != nil {
		t.Fatalf("BuildBaseTemplate: %v", err)
	}
	order := []string{
		"limactl delete agent-vm-base --force",
		"limactl create --name=agent-vm-base",
		"limactl start agent-vm-base",
		"limactl shell agent-vm-base sh -s",
		"limactl stop agent-vm-base",
	}
	last := -1
	for _, want := range order {
		idx := -1
		for i, c := range runner.calls {
			if strings.Contains(c, want) {
				idx = i
				break
			}
		}
		if idx == -1 {
			t.Fatalf("missing call %q; calls: %v", want, runner.calls)
		}
		if idx < last {
			t.Errorf("call %q ran out of order; calls: %v", want, runner.calls)
		}
		last = idx
	}
}

// A failed provisioning must not leave a half-built template behind: the next
// start would treat it as valid and clone an image with no OpenCode in it.
func TestBuildBaseTemplateRollsBackOnProvisionFailure(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && len(args) > 2 && args[0] == "shell" && args[len(args)-1] == "-s" {
			return ExecResult{ExitCode: 1, Stderr: "apk not found"}
		}
		return ExecResult{ExitCode: 0}
	}}
	var out strings.Builder
	spec := BaseTemplateSpec{Name: "agent-vm-base", Image: "template:debian-13", DiskGB: 20, MemoryGB: 4, CPUs: 2}
	err := BuildBaseTemplate(context.Background(), runner, spec, "exit 1\n", &out)
	if err == nil {
		t.Fatal("expected provisioning failure to be reported")
	}
	// Two deletes: the pre-clean and the rollback.
	deletes := 0
	for _, c := range runner.calls {
		if strings.Contains(c, "limactl delete agent-vm-base --force") {
			deletes++
		}
	}
	if deletes < 2 {
		t.Errorf("expected a rollback delete after provisioning failure, got %d delete(s): %v", deletes, runner.calls)
	}
	if strings.Contains(strings.Join(runner.calls, "\n"), "limactl stop") {
		t.Errorf("a failed build must not stop the template; calls: %v", runner.calls)
	}
}

// The provisioning script is piped on stdin, so it must never reach argv:
// argv is visible in the process table.
func TestBuildBaseTemplatePipesScriptOnStdin(t *testing.T) {
	runner := &recordingStdinRunner{}
	var out strings.Builder
	spec := BaseTemplateSpec{Name: "agent-vm-base", Image: "template:debian-13", DiskGB: 20, MemoryGB: 4, CPUs: 2}
	if err := BuildBaseTemplate(context.Background(), runner, spec, "SECRET_MARKER\n", &out); err != nil {
		t.Fatalf("BuildBaseTemplate: %v", err)
	}
	for _, c := range runner.calls {
		if strings.Contains(c, "SECRET_MARKER") {
			t.Fatalf("provisioning script leaked into argv: %q", c)
		}
	}
	if !strings.Contains(runner.stdin, "SECRET_MARKER") {
		t.Errorf("provisioning script must be piped on stdin, got %q", runner.stdin)
	}
}

// recordingStdinRunner captures both argv and the stdin payload.
type recordingStdinRunner struct {
	fakeRunner
	stdin string
}

func (r *recordingStdinRunner) RunStdin(_ context.Context, stdin io.Reader, name string, args ...string) (ExecResult, error) {
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		r.stdin = string(b)
	}
	return r.fakeRunner.run(name, args...)
}

func TestDeleteBaseTemplateToleratesAbsentInstance(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		return ExecResult{ExitCode: 1, Stderr: "instance does not exist"}
	}}
	if err := DeleteBaseTemplate(context.Background(), runner, "agent-vm-base"); err != nil {
		t.Errorf("deleting an absent template must not be an error: %v", err)
	}
}
