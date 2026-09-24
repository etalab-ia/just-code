package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// bindingsCmd implements `just-code bindings <subcommand>` (P09): the
// host-local per-project approval of optional credential bindings. An
// optional binding (github, context7) is inert until the project approves
// it; the record lives in host state, never in the repository.
//
//	bindings list
//	bindings approve <github|context7>
//	bindings revoke <github|context7>
func bindingsCmd(args []string, instance string) (int, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: just-code bindings list | approve <kind> | revoke <kind>")
		return 2, nil
	}
	stateDir := justcode.DefaultStateDir()
	path := justcode.BindingApprovalsPath(stateDir, instance)
	switch args[0] {
	case "list":
		return bindingsListCmd(path, instance)
	case "approve":
		return bindingsApproveCmd(path, args[1:])
	case "revoke":
		return bindingsRevokeCmd(path, instance, args[1:])
	default:
		return 2, fmt.Errorf("Unknown bindings command: %s (expected list, approve or revoke)", args[0])
	}
}

// bindingsListCmd shows every registered binding with its approval and
// storage state for this project. Values are never shown.
func bindingsListCmd(path, instance string) (int, error) {
	approvals, err := justcode.ReadBindingApprovals(justcode.DefaultFS, path)
	if err != nil {
		return 1, err
	}
	fmt.Printf("Credential bindings for %s:\n", instance)
	for _, b := range justcode.RegisteredBindings() {
		if !b.Optional {
			fmt.Printf("  %-10s always bound as %s (allowed hosts: %s)\n", b.Kind, b.GuestEnv, strings.Join(b.AllowHosts, ", "))
			continue
		}
		state := "not approved"
		if approvals.Approves(b.Kind) {
			state = "approved"
			if _, err := justcode.ReadStoredCredential(context.Background(), b.Kind); err != nil {
				state = "approved, but no credential stored (just-code auth add " + string(b.Kind) + ")"
			}
		}
		fmt.Printf("  %-10s %-12s as %s (allowed hosts: %s)\n", b.Kind, state, b.GuestEnv, strings.Join(b.AllowHosts, ", "))
	}
	return 0, nil
}

// bindingsApproveCmd records the project's approval of an optional binding.
// The binding takes effect at the next start of the project's instance.
func bindingsApproveCmd(path string, args []string) (int, error) {
	if len(args) != 1 {
		return 2, fmt.Errorf("Usage: just-code bindings approve <github|context7>")
	}
	kind := justcode.CredentialKind(args[0])
	if err := justcode.ApproveBinding(justcode.DefaultFS, path, kind); err != nil {
		return 1, err
	}
	fmt.Printf("Approved the %s binding for this project. It takes effect at the next start; the credential must be stored (just-code auth add %s).\n", kind, kind)
	return 0, nil
}

// bindingsRevokeCmd lifts the approval and revokes the binding from the
// project's Microsandbox instance when one exists (live when running,
// persisted when stopped).
func bindingsRevokeCmd(path, instance string, args []string) (int, error) {
	if len(args) != 1 {
		return 2, fmt.Errorf("Usage: just-code bindings revoke <github|context7>")
	}
	kind := justcode.CredentialKind(args[0])
	if err := justcode.RevokeBindingApproval(justcode.DefaultFS, path, kind); err != nil {
		return 1, err
	}
	revoked, live, err := justcode.RevokeInstanceBinding(context.Background(), instance, kind)
	if err != nil {
		return 1, err
	}
	switch {
	case revoked && live:
		fmt.Printf("Revoked the %s binding from the running instance %s. Its guest environment keeps the inert placeholder until the next restart.\n", kind, instance)
	case revoked:
		fmt.Printf("Cleared the persisted %s binding from %s (effective at its next start).\n", kind, instance)
	default:
		fmt.Printf("Lifted the %s approval for this project (no running or stopped instance to update).\n", kind)
	}
	return 0, nil
}
