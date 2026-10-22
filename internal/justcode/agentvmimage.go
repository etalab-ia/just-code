package justcode

import (
	"context"
	"fmt"
	"strings"
)

// templateValidateSupported reports whether this limactl can resolve a
// template locator before using it.
//
// Lima 1.0.x has no `template` subcommand: `limactl validate` there treats its
// argument as a filesystem path, so it rejects every locator form that
// `limactl create` resolves happily (`template://debian-12`, an https URL).
// Probing the subcommand's help answers the question the caller actually has —
// "can this limactl check the image?" — without parsing a version string that
// would need updating for every future release.
func templateValidateSupported(ctx context.Context, r Runner) (bool, error) {
	res, err := r.Run(ctx, "limactl", "template", "validate", "--help")
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// ValidateBaseTemplateImage reports whether `limactl create` can resolve the
// configured AGENT_VM_IMAGE.
//
// An image that Lima cannot resolve fails the build several minutes in, after
// the previous template has already been deleted, with output that runOK
// discarded. Checking it first turns that into one line naming the variable,
// the value, and Lima's own diagnostic.
//
// The check is best-effort by design: when this limactl cannot validate
// locators, the image is not rejected for it. Refusing to build on a Lima that
// simply lacks the subcommand would break every 1.0.x install over a check
// that did not exist there.
func ValidateBaseTemplateImage(ctx context.Context, r Runner, image string) error {
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("AGENT_VM_IMAGE is empty; set it to a Lima image (for example %s)", DefaultAgentVMImage)
	}
	supported, err := templateValidateSupported(ctx, r)
	if err != nil {
		return err
	}
	if !supported {
		return nil
	}
	res, err := r.Run(ctx, "limactl", "template", "validate", image)
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	// A failed check does not yet say the image is wrong: limactl refuses to
	// run at all for some callers (`must not run as the root user`), and that
	// refusal arrives on this same command. Lima gates that on the whole CLI —
	// a PersistentPreRunE on the root command — so a subcommand that takes no
	// image of its own fails identically and serves as a witness. Re-validating
	// the default image instead would leave this undiagnosed whenever the
	// configured image IS the default, which is the common case. No message
	// text is matched, so the attribution does not depend on Lima's wording.
	// Only paid on the failure path.
	witness, witnessErr := r.Run(ctx, "limactl", "list", "--format", "{{.Name}}")
	if witnessErr != nil {
		return witnessErr
	}
	if witness.ExitCode != 0 {
		return fmt.Errorf("limactl cannot run here, so AGENT_VM_IMAGE=%q was not checked: %s",
			image, commandDiagnostic(witness))
	}
	return fmt.Errorf("AGENT_VM_IMAGE=%q is not a Lima image this limactl can resolve: %s; set AGENT_VM_IMAGE to a resolvable image (for example %s)",
		image, commandDiagnostic(res), DefaultAgentVMImage)
}

// commandDiagnostic returns the most useful explanation a failed command left
// behind, or a neutral phrase when it left none.
func commandDiagnostic(res ExecResult) string {
	for _, candidate := range []string{res.Stderr, res.Stdout} {
		if detail := strings.TrimSpace(candidate); detail != "" {
			return detail
		}
	}
	return fmt.Sprintf("no diagnostic output (exit %d)", res.ExitCode)
}
