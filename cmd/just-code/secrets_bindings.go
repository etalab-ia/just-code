package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// `just-code secrets bindings ...` and `just-code secrets profile ...` (Phase
// 2): the host-local approvals that decide which imported project secrets the
// Microsandbox proxy may expose, and to which destinations. Approving a
// destination is the security decision, so it is recorded on this host and
// never in the project.

type projectSecretBindingsOptions struct {
	Root       string
	Profile    string
	Component  string
	Name       string
	GuestEnv   string
	Destin     []string
	Clear      bool
	Fallback   bool
	ConfirmYes bool
}

func parseProjectSecretBindingsArgs(args []string) (projectSecretBindingsOptions, error) {
	opts := projectSecretBindingsOptions{Root: "."}
	seen := map[string]bool{}
	positional := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--root", "--profile", "--component", "--name", "--guest-env", "--destination":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return opts, fmt.Errorf("%s requires a value", a)
			}
			i++
			value := args[i]
			switch a {
			case "--root":
				opts.Root = value
			case "--profile":
				opts.Profile = value
			case "--component":
				opts.Component = value
			case "--name":
				opts.Name = value
			case "--guest-env":
				opts.GuestEnv = value
			case "--destination":
				opts.Destin = append(opts.Destin, value)
			}
			seen[a] = true
		case "--clear", "--fallback", "--yes":
			if seen[a] {
				return opts, fmt.Errorf("%s may be given only once", a)
			}
			seen[a] = true
			switch a {
			case "--clear":
				opts.Clear = true
			case "--fallback":
				opts.Fallback = true
			case "--yes":
				opts.ConfirmYes = true
			}
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown secrets bindings option %q", a)
			}
			positional = append(positional, a)
		}
	}
	// The component and variable may be given positionally, which reads better
	// for the common case: `bindings approve development ".env" API_KEY`.
	if len(positional) > 0 && opts.Profile == "" {
		opts.Profile = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 && opts.Component == "" {
		opts.Component = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 && opts.Name == "" {
		opts.Name = positional[0]
		positional = positional[1:]
	}
	if len(positional) > 0 {
		return opts, fmt.Errorf("unexpected argument %q", positional[0])
	}
	return opts, nil
}

// projectSecretStoreForCLI resolves the store for the current project, using
// the same credential backend the import wrote to. The fallback store is
// selected only with the explicit --fallback flag, exactly like `auth`.
func projectSecretStoreForCLI(root string, fallback bool) (*justcode.ProjectSecretStore, error) {
	project, err := justcode.DiscoverProject(root)
	if err != nil {
		return nil, err
	}
	store, err := projectSecretsCredentialStoreFn(fallback)
	if err != nil {
		return nil, err
	}
	return projectSecretsStoreFactoryFn(project.Root, projectSecretsStateDirFn(), store)
}

func secretsProfileCmd(args []string) (int, error) {
	opts, err := parseProjectSecretBindingsArgs(args)
	if err != nil {
		return 2, err
	}
	if opts.Clear && opts.Profile != "" {
		return 2, fmt.Errorf("give either a profile name or --clear, not both")
	}
	store, err := projectSecretStoreForCLI(opts.Root, opts.Fallback)
	if err != nil {
		return 1, err
	}
	if err := justcode.SetProjectSecretActiveProfile(justcode.DefaultFS, projectSecretsStateDirFn(), store.Namespace(), opts.Profile); err != nil {
		return 1, err
	}
	if opts.Clear {
		fmt.Println("Cleared the active project-secret profile. No project secret is delivered until a profile is selected again.")
		return 0, nil
	}
	fmt.Printf("Active project-secret profile for %s: %s.\n", store.Root(), opts.Profile)
	fmt.Println("Approved bindings for this profile are delivered at the next start.")
	return 0, nil
}

func secretsBindingsCmd(args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: just-code secrets bindings <list|approve|revoke> [options]")
		return 2, nil
	}
	switch args[0] {
	case "list":
		return secretsBindingsListCmd(args[1:])
	case "approve":
		return secretsBindingsApproveCmd(args[1:])
	case "revoke":
		return secretsBindingsRevokeCmd(args[1:])
	default:
		return 2, fmt.Errorf("unknown secrets bindings command %q (expected list, approve or revoke)", args[0])
	}
}

func secretsBindingsListCmd(args []string) (int, error) {
	opts, err := parseProjectSecretBindingsArgs(args)
	if err != nil {
		return 2, err
	}
	store, err := projectSecretStoreForCLI(opts.Root, opts.Fallback)
	if err != nil {
		return 1, err
	}
	record, err := justcode.ReadProjectSecretBindingsRecordFor(store)
	if err != nil {
		return 1, err
	}
	profile := record.Profile
	if profile == "" {
		profile = "(none selected)"
	}
	fmt.Printf("Project secret bindings for %s (profile: %s)\n", store.Root(), profile)
	if len(record.Bindings) == 0 {
		fmt.Println("  No approved binding.")
	} else {
		for _, b := range record.Bindings {
			fmt.Printf("  %s#%s -> %s\n", b.Component, b.Name, b.GuestEnv)
			fmt.Printf("      destinations: %s (host-local approval, never versioned)\n", strings.Join(b.Destinations, ", "))
		}
	}
	if record.Pending != "" {
		fmt.Printf("  NOT DELIVERABLE: %s\n", record.Pending)
	}
	if len(record.Undelivered) > 0 {
		sort.Strings(record.Undelivered)
		fmt.Printf("  Imported but not delivered: %s\n", strings.Join(record.Undelivered, ", "))
		fmt.Println("  Approve one with: just-code secrets bindings approve <profile> <component> <NAME> --destination <host>")
	}
	fmt.Println("Values are never shown here, and approving a binding does not display or copy any value.")
	return 0, nil
}

func secretsBindingsApproveCmd(args []string) (int, error) {
	opts, err := parseProjectSecretBindingsArgs(args)
	if err != nil {
		return 2, err
	}
	if opts.Profile == "" || opts.Component == "" || opts.Name == "" {
		return 2, fmt.Errorf("usage: just-code secrets bindings approve <profile> <component> <NAME> --destination <host> [--destination <host>...] [--guest-env NAME]")
	}
	if len(opts.Destin) == 0 {
		return 2, fmt.Errorf("at least one --destination is required: approving a destination is what allows the value to be substituted toward it")
	}
	store, err := projectSecretStoreForCLI(opts.Root, opts.Fallback)
	if err != nil {
		return 1, err
	}
	stateDir := projectSecretsStateDirFn()
	binding := justcode.ProjectSecretBinding{
		Profile:      opts.Profile,
		Component:    opts.Component,
		Name:         opts.Name,
		GuestEnv:     opts.GuestEnv,
		Destinations: opts.Destin,
	}
	if binding.GuestEnv == "" {
		binding.GuestEnv = justcode.ProjectSecretGuestEnv(binding.Component, binding.Name)
	}
	fmt.Printf("Approving %s#%s for %s -> guest %s, destinations: %s\n",
		binding.Component, binding.Name, binding.Profile, binding.GuestEnv, strings.Join(binding.Destinations, ", "))
	fmt.Println("The value stays in the credential store; only its placeholder enters the guest, and it is substituted toward those destinations alone.")
	if err := justcode.SetProjectSecretActiveProfile(justcode.DefaultFS, stateDir, store.Namespace(), opts.Profile); err != nil {
		return 1, err
	}
	if err := justcode.ApproveProjectSecretBinding(justcode.DefaultFS, stateDir, store.Namespace(), binding); err != nil {
		return 1, err
	}
	// Verify the approval resolves now, so a typo in the variable name is
	// reported here rather than at the next start.
	if _, _, err := store.ApprovedDeliveries(); err != nil {
		return 1, fmt.Errorf("the approval was recorded but does not resolve: %w", err)
	}
	fmt.Println("Approved. It takes effect at the next start of this project's sandbox.")
	return 0, nil
}

func secretsBindingsRevokeCmd(args []string) (int, error) {
	opts, err := parseProjectSecretBindingsArgs(args)
	if err != nil {
		return 2, err
	}
	if opts.Profile == "" || opts.Component == "" || opts.Name == "" {
		return 2, fmt.Errorf("usage: just-code secrets bindings revoke <profile> <component> <NAME>")
	}
	store, err := projectSecretStoreForCLI(opts.Root, opts.Fallback)
	if err != nil {
		return 1, err
	}
	stateDir := projectSecretsStateDirFn()
	removed, err := justcode.RevokeProjectSecretBinding(justcode.DefaultFS, stateDir, store.Namespace(), opts.Profile, opts.Component, opts.Name)
	if err != nil {
		return 1, err
	}
	if !removed {
		fmt.Printf("No approval matched %s#%s for profile %s; nothing changed.\n", opts.Component, opts.Name, opts.Profile)
		return 0, nil
	}
	fmt.Printf("Revoked the %s#%s project secret binding. The imported value stays in the credential store.\n", opts.Component, opts.Name)
	fmt.Println("Run 'just-code restart' (or 'just-code start') to unregister it from the running sandbox: the proxy registration is dropped at the next apply, and the guest keeps an inert placeholder until it restarts.")
	return 0, nil
}
