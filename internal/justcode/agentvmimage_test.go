package justcode

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// imageRunner fakes limactl for the image check: the capability probe answers
// helpExit, any real validation answers validateExit with validateStderr.
func imageRunner(helpExit, validateExit int, validateStderr string) *fakeRunner {
	return &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if name == "limactl" && len(args) >= 3 && args[0] == "template" && args[1] == "validate" {
			if args[2] == "--help" {
				return ExecResult{ExitCode: helpExit}
			}
			return ExecResult{ExitCode: validateExit, Stderr: validateStderr}
		}
		return ExecResult{ExitCode: 0}
	}}
}

func TestValidateBaseTemplateImageAcceptsResolvableImage(t *testing.T) {
	runner := imageRunner(0, 0, "")
	if err := ValidateBaseTemplateImage(context.Background(), runner, DefaultAgentVMImage); err != nil {
		t.Fatalf("a resolvable image must pass: %v", err)
	}
	if !runner.hasCall("validate " + DefaultAgentVMImage) {
		t.Errorf("the configured image must actually be validated; calls: %v", runner.calls)
	}
}

// The failure a user can act on names the variable, the value, and Lima's own
// explanation. Without the diagnostic this is "exit 1" and nothing else.
func TestValidateBaseTemplateImageReportsLimaDiagnostic(t *testing.T) {
	diagnostic := `time="2026-10-22T14:33:00Z" level=fatal msg="template \"debian-13.yaml\" not found"`
	runner := imageRunner(0, 1, diagnostic)
	err := ValidateBaseTemplateImage(context.Background(), runner, "template://debian-13")
	if err == nil {
		t.Fatal("an unresolvable image must be refused")
	}
	for _, want := range []string{"AGENT_VM_IMAGE", "template://debian-13", "debian-13.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q: %v", want, err)
		}
	}
}

// A runner that fills only Stdout still explains the failure: OSRunner happens
// to populate both streams, but the helper must not depend on that.
func TestValidateBaseTemplateImageFallsBackToStdout(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if len(args) >= 3 && args[0] == "template" && args[2] == "--help" {
			return ExecResult{ExitCode: 0}
		}
		return ExecResult{ExitCode: 1, Stdout: "fatal: no such template"}
	}}
	err := ValidateBaseTemplateImage(context.Background(), runner, "template://nope")
	if err == nil || !strings.Contains(err.Error(), "no such template") {
		t.Fatalf("expected the stdout diagnostic to be surfaced, got %v", err)
	}
}

// Lima 1.0.x has no `template` subcommand, so it cannot verify a locator. That
// is "cannot check", never "the image is wrong": blocking the build there would
// break every 1.0.x install over a check it does not have.
func TestValidateBaseTemplateImageSkipsWhenLimaCannotValidate(t *testing.T) {
	runner := imageRunner(1, 1, "unknown command")
	if err := ValidateBaseTemplateImage(context.Background(), runner, DefaultAgentVMImage); err != nil {
		t.Fatalf("an old limactl must not fail the build over an unavailable check: %v", err)
	}
	if runner.hasCall("validate " + DefaultAgentVMImage) {
		t.Errorf("validation must not run when the capability probe failed; calls: %v", runner.calls)
	}
}

func TestValidateBaseTemplateImageRejectsEmptyValue(t *testing.T) {
	runner := &fakeRunner{}
	err := ValidateBaseTemplateImage(context.Background(), runner, "  ")
	if err == nil || !strings.Contains(err.Error(), "AGENT_VM_IMAGE") {
		t.Fatalf("an empty image must name the variable, got %v", err)
	}
	if runner.hasCall("template") {
		t.Errorf("an empty value needs no limactl call; calls: %v", runner.calls)
	}
}

// erroringRunner fails every command, standing in for a missing or
// unrunnable limactl binary.
type erroringRunner struct{ err error }

func (r erroringRunner) Run(context.Context, string, ...string) (ExecResult, error) {
	return ExecResult{}, r.err
}

func (r erroringRunner) RunEnv(context.Context, []string, string, ...string) (ExecResult, error) {
	return ExecResult{}, r.err
}

func (r erroringRunner) RunStdin(context.Context, io.Reader, string, ...string) (ExecResult, error) {
	return ExecResult{}, r.err
}

// A runner that cannot be executed at all is an error, not a verdict on the
// image: the caller must not read it as "Lima is missing the capability".
func TestValidateBaseTemplateImagePropagatesRunnerError(t *testing.T) {
	runner := erroringRunner{err: errors.New("boom")}
	if err := ValidateBaseTemplateImage(context.Background(), runner, DefaultAgentVMImage); err == nil {
		t.Fatal("a runner failure must be reported")
	}
}

// The default must be a locator Lima can resolve. `template:debian-13` looked
// similar and was rejected by `limactl create`, so the form itself is pinned
// here rather than only the value.
func TestDefaultAgentVMImageIsAResolvableLocator(t *testing.T) {
	if DefaultAgentVMImage != "template://debian-12" {
		t.Errorf("DefaultAgentVMImage = %q, want the pinned template://debian-12", DefaultAgentVMImage)
	}
	if !strings.HasPrefix(DefaultAgentVMImage, "template://") {
		t.Errorf("DefaultAgentVMImage = %q: a template locator needs the template:// scheme", DefaultAgentVMImage)
	}
}

// createBaseTemplate is the step that resolves the image, so its failure must
// carry Lima's explanation.
// A failed validate does not always mean a bad image: limactl refuses to run
// at all as root ("must not run as the root user"), and that refusal arrives on
// the same command. Blaming AGENT_VM_IMAGE there would send the user to change
// a value that was never the problem. Probing the known-good default separates
// the two without depending on Lima's wording.
func TestValidateBaseTemplateImageDistinguishesLimaRefusal(t *testing.T) {
	refusal := `time="2026-10-22T14:51:44Z" level=fatal msg="must not run as the root user"`
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		switch {
		case len(args) >= 3 && args[0] == "template" && args[2] == "--help":
			return ExecResult{ExitCode: 0}
		case len(args) >= 3 && args[0] == "template" && args[2] == DefaultAgentVMImage:
			return ExecResult{ExitCode: 1, Stderr: refusal}
		}
		return ExecResult{ExitCode: 1, Stderr: refusal}
	}}
	err := ValidateBaseTemplateImage(context.Background(), runner, "template://debian-13")
	if err == nil {
		t.Fatal("an unvalidatable image must not be silently accepted")
	}
	if strings.Contains(err.Error(), "is not a Lima image") {
		t.Errorf("must not blame the configured image when limactl could not run: %v", err)
	}
	// Still actionable: the real reason travels with the message.
	if !strings.Contains(err.Error(), "must not run as the root user") {
		t.Errorf("the limactl refusal must be surfaced: %v", err)
	}
}

// When the configured image IS the default, a failure needs no probe to be
// attributed, and the message stays the plain "not resolvable" one.
func TestValidateBaseTemplateImageAttributesFailureOnDefaultImage(t *testing.T) {
	runner := imageRunner(0, 1, `level=fatal msg="template \"debian-12.yaml\" not found"`)
	err := ValidateBaseTemplateImage(context.Background(), runner, DefaultAgentVMImage)
	if err == nil || !strings.Contains(err.Error(), "is not a Lima image") {
		t.Fatalf("a missing default template must be attributed to the image: %v", err)
	}
}

func TestCreateBaseTemplateKeepsLimaDiagnostic(t *testing.T) {
	runner := &fakeRunner{onRun: func(name string, args []string) ExecResult {
		if len(args) > 0 && args[0] == "create" {
			return ExecResult{ExitCode: 1, Stderr: `level=fatal msg="template \"debian-13.yaml\" not found"`}
		}
		return ExecResult{ExitCode: 0}
	}}
	spec := BaseTemplateSpec{Name: "agent-vm-base", Image: "template://debian-13", DiskGB: 20, MemoryGB: 4, CPUs: 2}
	err := createBaseTemplate(context.Background(), runner, spec)
	if err == nil {
		t.Fatal("a failed create must be reported")
	}
	for _, want := range []string{"agent-vm-base", "exit 1", "debian-13.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("create error must contain %q: %v", want, err)
		}
	}
}

// doctorRunner installs limactl but no template, so Doctor reaches the
// "pending build" branch where the image check lives.
func doctorRunner(validateExit int, validateStderr string) *fakeRunner {
	return &fakeRunner{onRun: func(name string, args []string) ExecResult {
		switch {
		case name != "limactl":
			return ExecResult{ExitCode: 0}
		case args[0] == "--version":
			return ExecResult{ExitCode: 0, Stdout: "limactl version 1.2.0\n"}
		case args[0] == "list":
			return ExecResult{ExitCode: 0}
		case args[0] == "template" && len(args) >= 3 && args[2] == "--help":
			return ExecResult{ExitCode: 0}
		case args[0] == "template":
			return ExecResult{ExitCode: validateExit, Stderr: validateStderr}
		}
		return ExecResult{ExitCode: 0}
	}}
}

// Doctor is the last moment an invalid image can be reported before a start
// rebuilds the template, so it fails there instead of announcing a pending
// build that cannot succeed.
func TestAgentVMDoctorRefusesUnresolvableImage(t *testing.T) {
	runner := doctorRunner(1, `level=fatal msg="template \"debian-13.yaml\" not found"`)
	a := newTestAgentVM(t, runner)
	a.Config.AgentVMImage = "template://debian-13"
	err := a.Doctor(context.Background())
	if err == nil {
		t.Fatal("doctor must refuse an image limactl cannot resolve")
	}
	if !strings.Contains(err.Error(), "AGENT_VM_IMAGE") {
		t.Errorf("the error must name the variable: %v", err)
	}
	if runner.hasCall("limactl create") {
		t.Errorf("doctor stays read-only; calls: %v", runner.calls)
	}
}

func TestAgentVMDoctorReportsResolvableImageAsPendingBuild(t *testing.T) {
	runner := doctorRunner(0, "")
	a := newTestAgentVM(t, runner)
	if err := a.Doctor(context.Background()); err != nil {
		t.Fatalf("a resolvable image pending a build is not an error: %v", err)
	}
	if !runner.hasCall("validate " + DefaultAgentVMImage) {
		t.Errorf("doctor must validate the configured image; calls: %v", runner.calls)
	}
	if runner.hasCall("limactl create") {
		t.Errorf("doctor must not build the template; calls: %v", runner.calls)
	}
}

// ensureBaseTemplate is the path a start takes. It must reject the image
// before BuildBaseTemplate deletes the previous template.
func TestEnsureBaseTemplateValidatesImageBeforeBuilding(t *testing.T) {
	runner := doctorRunner(1, `level=fatal msg="template \"debian-13.yaml\" not found"`)
	a := newTestAgentVM(t, runner)
	a.Config.AgentVMImage = "template://debian-13"
	err := a.ensureBaseTemplate(context.Background())
	if err == nil {
		t.Fatal("an unresolvable image must stop the build")
	}
	if !strings.Contains(err.Error(), "AGENT_VM_IMAGE") {
		t.Errorf("the error must name the variable: %v", err)
	}
	for _, forbidden := range []string{"limactl create", "limactl delete", "limactl shell"} {
		if runner.hasCall(forbidden) {
			t.Errorf("%q ran before the image was validated; calls: %v", forbidden, runner.calls)
		}
	}
}
