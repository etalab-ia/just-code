package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// authCmd implements `just-code auth <subcommand>` (P08): global credentials
// stored in the OS-native store (macOS Keychain, Windows Credential
// Manager, Linux Secret Service), with an owner-only file fallback that
// requires explicit consent. The secret is never accepted through argv,
// never printed, and never written to a project file.
//
//	auth add [albert] [--stdin] [--fallback]
//	auth status
//	auth remove [albert] [--fallback]
func authCmd(args []string) (int, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: just-code auth add|status|remove [kind] [--stdin] [--fallback]")
		return 2, nil
	}
	switch args[0] {
	case "add":
		return authAddCmd(args[1:])
	case "status":
		return authStatusCmd()
	case "remove":
		return authRemoveCmd(args[1:])
	default:
		return 2, fmt.Errorf("Unknown auth command: %s (expected add, status or remove)", args[0])
	}
}

// parseAuthArgs returns the credential kind, the --stdin flag and the
// --fallback flag. The kind defaults to albert, the only credential the
// current runtimes transport; unknown kinds are rejected so a typo does
// not store a credential nothing will ever read.
func parseAuthArgs(args []string) (justcode.CredentialKind, bool, bool, error) {
	kind := justcode.CredentialAlbert
	stdin, fallback := false, false
	for _, a := range args {
		switch a {
		case "--stdin":
			stdin = true
		case "--fallback":
			fallback = true
		case "albert":
			kind = justcode.CredentialAlbert
		default:
			return kind, stdin, fallback, fmt.Errorf("Unknown credential kind or flag: %s (expected albert, --stdin or --fallback)", a)
		}
	}
	return kind, stdin, fallback, nil
}

// authStore resolves the store for an operation. Without --fallback it is
// the native store; with --fallback it is the consented file store. A nil
// native store (unsupported platform) is an explicit error, not a silent
// fallback.
func authStore(fallback bool) (justcode.CredentialStore, error) {
	if fallback {
		return justcode.ConsentFileStore()
	}
	s := justcode.DefaultCredentialStore()
	if s == nil {
		return nil, fmt.Errorf("no native credential store on this platform; pass --fallback to use the owner-only file store after consenting")
	}
	return s, nil
}

// readSecret obtains the secret without echoing it. Interactively it
// reads a masked line (the terminal is put in no-echo mode when
// supported); with --stdin it reads one line from standard input, the
// documented noninteractive path. argv is never a source.
func readSecret(stdin bool) (string, error) {
	if stdin || !isTTY() {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read secret from stdin: %w", err)
		}
		return strings.TrimSpace(line), nil
	}
	// Interactive: no-echo when the terminal supports it. The no-echo
	// helper is best-effort; a terminal that cannot be configured still
	// reads a line, and the user is warned on the line above.
	fmt.Print("Enter the secret (input hidden where the terminal supports it): ")
	value, err := readPassword()
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func authAddCmd(args []string) (int, error) {
	kind, useStdin, fallback, err := parseAuthArgs(args)
	if err != nil {
		return 2, err
	}
	store, err := authStore(fallback)
	if err != nil {
		return 1, err
	}
	// Probe the store first: an unavailable or locked store must fail
	// BEFORE the user types the secret, and must never silently create
	// a plaintext fallback.
	if err := store.Verify(context.Background()); err != nil {
		return 1, err
	}
	value, err := readSecret(useStdin)
	if err != nil {
		return 1, err
	}
	if value == "" {
		return 1, fmt.Errorf("empty secret; nothing was stored")
	}
	if err := store.Put(context.Background(), kind, value); err != nil {
		return 1, err
	}
	fmt.Printf("Stored credential %q in the %s store. It is referenced by name (credentialRef), never written to a project file.\n", kind, store.Kind())
	return 0, nil
}

func authStatusCmd() (int, error) {
	ctx := context.Background()
	native := justcode.DefaultCredentialStore()
	fmt.Printf("Native store: %s\n", justcode.StoreAvailability())
	if native != nil {
		if err := native.Verify(ctx); err != nil {
			var se *justcode.StoreError
			if errors.As(err, &se) {
				fmt.Printf("  state: %s (%s)\n", se.State, se.Err)
			} else {
				fmt.Printf("  state: error (%v)\n", err)
			}
		} else {
			fmt.Println("  state: reachable")
		}
		if _, err := native.Get(ctx, justcode.CredentialAlbert); err == nil {
			fmt.Printf("  albert: stored\n")
		} else if justcode.IsCredentialNotFound(err) {
			fmt.Println("  albert: not stored")
		} else {
			fmt.Printf("  albert: unreadable (%v)\n", err)
		}
	}
	// The fallback store is reported but never created by a status read.
	fileStore, err := justcode.NewFileCredentialStore()
	if err == nil {
		if err := fileStore.Verify(ctx); err != nil {
			if errors.Is(err, justcode.ErrFileStoreAbsent) {
				fmt.Println("File fallback: absent (never created; 'auth add --fallback' consents to it)")
				return 0, nil
			}
			var se *justcode.StoreError
			if errors.As(err, &se) {
				fmt.Printf("File fallback: %s (%s)\n", se.State, se.Err)
			} else {
				fmt.Printf("File fallback: error (%v)\n", err)
			}
		} else if fileStore.HasCredential(justcode.CredentialAlbert) {
			fmt.Println("File fallback: present, albert stored")
		} else {
			fmt.Println("File fallback: present, albert not stored")
		}
	}
	return 0, nil
}

func authRemoveCmd(args []string) (int, error) {
	kind, _, fallback, err := parseAuthArgs(args)
	if err != nil {
		return 2, err
	}
	store, err := authStore(fallback)
	if err != nil {
		return 1, err
	}
	// P08/P09 boundary: removing a credential that a running sandbox
	// still binds cannot revoke the guest's copy. Until P09 enforces
	// revocation, refuse with an actionable message rather than claim
	// the access was revoked. The check is host-side only: a sandbox
	// that is not running keeps nothing live.
	if justcode.CredentialBoundToRunningInstance(kind) {
		return 1, fmt.Errorf("credential %q is bound to a running sandbox; stop it first (just-code stop) so the removal can take effect, or remove the credential after P09 revocation lands", kind)
	}
	if err := store.Remove(context.Background(), kind); err != nil {
		if justcode.IsCredentialNotFound(err) {
			fmt.Printf("No %q credential stored in the %s store.\n", kind, store.Kind())
			return 0, nil
		}
		return 1, err
	}
	fmt.Printf("Removed credential %q from the %s store.\n", kind, store.Kind())
	return 0, nil
}
