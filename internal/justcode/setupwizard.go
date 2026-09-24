package justcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// This file implements the P11 setup engine. The CLI layer (cmd/just-code)
// renders the prompts; this layer owns the ordered operations and the
// journal transitions, so the flow is testable without a terminal. Every
// operation is one from a preceding PR (P08 store, P10 catalogue): the
// wizard composes real domain operations, it does not reimplement them.

// SetupStore abstracts the credential-store operations the wizard needs.
// Production uses the real stores via the P08 path; tests inject fakes.
type SetupStore interface {
	Kind() string
	Verify(ctx context.Context) error
	Put(ctx context.Context, kind CredentialKind, value string) error
}

// SetupWizard drives the stages over injectable operations.
type SetupWizard struct {
	FS       FS
	StateDir string
	// Store resolves the credential store for the Albert (and optional
	// GitHub) credential.
	Store func(ctx context.Context) (SetupStore, error)
	// ValidateAlbert probes the Albert endpoint with the candidate key.
	// Nil skips validation (tests decide the outcome explicitly).
	ValidateAlbert func(ctx context.Context, key string) error
	// EnsureRuntime installs the managed runtime when needed. Nil uses
	// ensureMSBRuntime.
	EnsureRuntime func(ctx context.Context) error
}

// ResolveStore resolves and verifies the credential store without storing
// anything, so the CLI can fail on a locked or unavailable store BEFORE the
// user types a secret. A consented-but-absent file store is the normal
// first-run state and passes: the first Put creates it.
func (w SetupWizard) ResolveStore(ctx context.Context) (SetupStore, error) {
	store, err := w.store(ctx)
	if err != nil {
		return nil, err
	}
	if err := store.Verify(ctx); err != nil {
		if !errors.Is(err, ErrFileStoreAbsent) && !errors.Is(err, ErrFileStoreNotConsented) {
			return nil, err
		}
	}
	return store, nil
}

// CredentialProbe distinguishes the validation outcomes the plan requires:
// a rejected key is reported as rejected (the user retries), an unreachable
// endpoint is reported as network trouble (the user may continue or stop),
// and neither blocks the store write when validation is unavailable.
type CredentialProbe struct {
	Rejected    bool
	Unreachable bool
	Detail      string
}

// ValidateAlbertKey probes the models endpoint with the key: a 401/403 is a
// rejection (the key is wrong or revoked), other transport errors are
// unreachable, and a 200 with a models listing means the key is valid.
// Redirects are NOT followed: the Authorization header must never be replayed
// to another host, so a 3xx is reported as unreachable (the endpoint moved
// or is misconfigured, which is a configuration problem, not a key problem).
// No chat completion is ever performed — validating with a catalogue listing
// never spends inference.
func ValidateAlbertKey(ctx context.Context, client *http.Client, key string) CredentialProbe {
	return validateAlbertKeyAt(ctx, client, key, albertCatalogueURL)
}

// validateAlbertKeyAt is ValidateAlbertKey against an explicit URL (tests).
func validateAlbertKeyAt(ctx context.Context, client *http.Client, key, url string) CredentialProbe {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	} else if client.CheckRedirect == nil {
		// A caller-supplied client without a redirect policy gets the same
		// no-follow policy; an explicit caller policy is respected.
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CredentialProbe{Unreachable: true, Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return CredentialProbe{Unreachable: true, Detail: err.Error()}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		// Sniff the body: the models endpoint returns a JSON object with a
		// "data" array. A 200 with anything else (a captive portal, an HTML
		// error page) is not a validated key.
		var raw struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
			return CredentialProbe{Unreachable: true, Detail: "HTTP 200 with a non-catalogue body (endpoint misconfigured?)"}
		}
		return CredentialProbe{}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return CredentialProbe{Unreachable: true, Detail: fmt.Sprintf("HTTP %d (redirect not followed; the Authorization header is never replayed to another host)", resp.StatusCode)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return CredentialProbe{Rejected: true, Detail: fmt.Sprintf("HTTP %d: the key was not accepted", resp.StatusCode)}
	default:
		return CredentialProbe{Unreachable: true, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
}

// StoreCredential validates and stores one credential, journaling the kind
// (never the value) on success. A rejected key returns an error inviting a
// retry; an unreachable endpoint is returned as a distinct signal so the
// caller can let the user decide (store anyway, or stop).
func (w SetupWizard) StoreCredential(ctx context.Context, kind CredentialKind, value string) (CredentialProbe, error) {
	store, err := w.store(ctx)
	if err != nil {
		return CredentialProbe{}, err
	}
	// Probe the store first: an unavailable or locked store fails BEFORE
	// the user types anything (the auth add contract, kept here).
	if err := store.Verify(ctx); err != nil {
		// An absent file store is a normal first-run state for the
		// consented fallback: the wizard's own Put creates it. Any other
		// failure (locked, denied, unavailable) stays fatal.
		if !errors.Is(err, ErrFileStoreAbsent) && !errors.Is(err, ErrFileStoreNotConsented) {
			return CredentialProbe{}, err
		}
	}
	probe := CredentialProbe{}
	if kind == CredentialAlbert && w.ValidateAlbert != nil {
		if verr := w.ValidateAlbert(ctx, value); verr != nil {
			var rejected RejectedCredentialError
			if errors.As(verr, &rejected) {
				return CredentialProbe{Rejected: true, Detail: rejected.Detail}, nil
			}
			probe = CredentialProbe{Unreachable: true, Detail: verr.Error()}
		}
	}
	if err := store.Put(ctx, kind, value); err != nil {
		return probe, err
	}
	j, err := ReadSetupJournal(w.FS, w.StateDir)
	if err != nil {
		return probe, err
	}
	if !containsStr(j.CredentialKinds, string(kind)) {
		j.CredentialKinds = append(j.CredentialKinds, string(kind))
	}
	return probe, WriteSetupJournal(w.FS, w.StateDir, j)
}

// RejectedCredentialError marks a key the endpoint refused: retryable by
// re-entering the key, distinct from any network failure.
type RejectedCredentialError struct{ Detail string }

func (e RejectedCredentialError) Error() string {
	return "the Albert key was rejected: " + e.Detail
}

// SaveIdentity journals the git identity (no store involvement).
func (w SetupWizard) SaveIdentity(name, email string) error {
	// The identity is persisted in the durable user settings, not just the
	// journal: the journal is removed at completion, and the guest
	// bootstrap reads the settings to configure git. The journal copy
	// tracks wizard progress for the review display.
	path, err := UserSettingsPath()
	if err != nil {
		return err
	}
	us, err := ReadUserSettings(w.FS, path)
	if err != nil {
		return err
	}
	us.GitName = name
	us.GitEmail = email
	if err := WriteUserSettings(w.FS, path, us); err != nil {
		return err
	}
	j, err := ReadSetupJournal(w.FS, w.StateDir)
	if err != nil {
		return err
	}
	j.GitName = name
	j.GitEmail = email
	return WriteSetupJournal(w.FS, w.StateDir, j)
}

// SaveSettings writes the resolved global settings and journals the model.
// The settings file is the P04 managed schema; the identity fields are not
// part of it (they live as git config at runtime), so only the model is
// persisted here.
func (w SetupWizard) SaveSettings(model string) error {
	path, err := UserSettingsPath()
	if err != nil {
		return err
	}
	us, err := ReadUserSettings(w.FS, path)
	if err != nil {
		return err
	}
	if model != "" {
		us.DefaultModel = model
	}
	if err := WriteUserSettings(w.FS, path, us); err != nil {
		return err
	}
	j, err := ReadSetupJournal(w.FS, w.StateDir)
	if err != nil {
		return err
	}
	j.DefaultModel = model
	return WriteSetupJournal(w.FS, w.StateDir, j)
}

// InstallRuntime installs the managed runtime (resumable: a trusted
// installation is a no-op) and completes setup.
func (w SetupWizard) InstallRuntime(ctx context.Context) error {
	ensure := w.EnsureRuntime
	if ensure == nil {
		ensure = func(ctx context.Context) error {
			return ensureMSBRuntime(ctx, nil)
		}
	}
	if err := ensure(ctx); err != nil {
		return err
	}
	return RemoveSetupJournal(w.FS, w.StateDir)
}

func (w SetupWizard) store(ctx context.Context) (SetupStore, error) {
	if w.Store != nil {
		return w.Store(ctx)
	}
	s := DefaultCredentialStore()
	if s == nil {
		return nil, fmt.Errorf("no native credential store on this platform; run 'just-code auth add --fallback' to consent to the owner-only file store, or retry setup with --fallback")
	}
	return s, nil
}
