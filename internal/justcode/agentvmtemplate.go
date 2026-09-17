package justcode

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// BaseTemplateSpec describes the Lima base template just-code builds for the
// agent-vm runtime. Building it ourselves is what removes the dependency on
// the agent-vm tool: `agent-vm setup` cannot be pinned to a release (upstream
// publishes no tags), so executing it would mean running an unpinned
// third-party script with the user's privileges.
type BaseTemplateSpec struct {
	// Name is the Lima instance name of the template.
	Name string
	// Image is the Lima image reference to create it from.
	Image    string
	DiskGB   int
	MemoryGB int
	CPUs     int
}

// baseTemplateCreateArgs builds the `limactl create` argument list. The
// template is created with no mounts: it is a builder image, not a project VM.
// Workspace mounts are applied per VM when the managed instance is cloned,
// which is also why this must stay mountless — a mount baked in here would
// leak into every VM cloned from the template.
func baseTemplateCreateArgs(spec BaseTemplateSpec) []string {
	return []string{
		"create",
		"--name=" + spec.Name,
		spec.Image,
		"--set", ".mounts=[]",
		"--disk=" + strconv.Itoa(spec.DiskGB),
		"--memory=" + strconv.Itoa(spec.MemoryGB),
		"--cpus=" + strconv.Itoa(spec.CPUs),
		"--tty=false",
	}
}

// DeleteBaseTemplate removes a base template, ignoring absence. It is used to
// clear partial state before a build and to roll back a failed provisioning
// attempt: a template that exists but was never provisioned would otherwise be
// treated as valid by the next start.
func DeleteBaseTemplate(ctx context.Context, r Runner, name string) error {
	res, err := r.Run(ctx, "limactl", "delete", name, "--force")
	if err != nil {
		return err
	}
	// A missing instance is not an error for a delete.
	_ = res
	return nil
}

// BuildBaseTemplate creates, boots, provisions and stops the Lima base
// template. It is idempotent only in the sense that it clears prior state
// first: any existing instance with this name is removed, so calling it on a
// populated template rebuilds from scratch. Callers must therefore gate it on
// the template being absent (see AgentVM.ensureBaseTemplate).
func BuildBaseTemplate(ctx context.Context, r Runner, spec BaseTemplateSpec, script string, out io.Writer) error {
	fmt.Fprintf(out, "Building the %s base template (this takes several minutes)...\n", spec.Name)

	// Clear partial state: an interrupted earlier build leaves an instance
	// Lima knows about but that was never provisioned.
	if err := DeleteBaseTemplate(ctx, r, spec.Name); err != nil {
		return err
	}

	if err := runOK(r, ctx, "limactl", baseTemplateCreateArgs(spec)...); err != nil {
		return fmt.Errorf("limactl create failed for %s: %w", spec.Name, err)
	}

	fmt.Fprintf(out, "Starting %s...\n", spec.Name)
	if err := runOK(r, ctx, "limactl", "start", spec.Name); err != nil {
		// Roll back so a retry starts from a clean slate rather than an
		// instance that exists in a broken half-created state.
		_ = DeleteBaseTemplate(ctx, r, spec.Name)
		return fmt.Errorf("limactl start failed for %s: %w", spec.Name, err)
	}

	fmt.Fprintf(out, "Provisioning %s...\n", spec.Name)
	// The script is piped on stdin and runs as the guest user, using sudo
	// internally. A regular user matters: OpenCode installs under $HOME, which
	// would be root's home if the script ran as root.
	if err := runStdinOK(r, ctx, strings.NewReader(script), "limactl", "shell", spec.Name, "sh", "-s"); err != nil {
		_ = DeleteBaseTemplate(ctx, r, spec.Name)
		return fmt.Errorf("provisioning %s failed: %w", spec.Name, err)
	}

	fmt.Fprintf(out, "Stopping %s...\n", spec.Name)
	if err := runOK(r, ctx, "limactl", "stop", spec.Name); err != nil {
		return fmt.Errorf("limactl stop failed for %s: %w", spec.Name, err)
	}

	fmt.Fprintf(out, "Base template %s is ready.\n", spec.Name)
	return nil
}
