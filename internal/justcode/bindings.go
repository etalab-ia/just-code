package justcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// This file implements the P09 binding model: named credentials bound into a
// Microsandbox guest through the secret proxy. The central invariant is that
// no raw credential value is ever persisted — not in the runtime's host
// database, not in the guest environment, not in just-code's state files.
// Values are resolved immediately before the runtime operation that needs
// them and handed to the SDK through a host environment reference
// ({"kind":"env","var":...}), which the runtime re-reads from the just-code
// process environment at apply/boot time.

// msbSecretBinding describes one credential bound into a guest through the
// secret proxy. It deliberately carries no value: metadata only.
type msbSecretBinding struct {
	// Kind is the credential store kind the value resolves from.
	Kind CredentialKind
	// GuestEnv is the variable name the guest sees. Microsandbox exposes the
	// secret *placeholder* under this name; the real value is swapped in at
	// the network boundary for AllowHosts only.
	GuestEnv string
	// HostEnv is the host-side transport variable the env reference resolves
	// from. It is namespaced under JUST_CODE_ and exists only for the
	// duration of an SDK call (see withHostSecrets), so a dedicated name —
	// never the user's own variable — avoids clobbering anything.
	HostEnv string
	// AllowHosts are the only hosts that ever receive the real value. Hosts
	// are disjoint across bindings (validateSecretBindings), so one binding's
	// credential can never be substituted toward another binding's
	// destination.
	AllowHosts []string
	// Optional bindings are inert until the project approves them explicitly
	// (host-local bindings.json); global storage alone never binds.
	Optional bool
}

// msbSecretBootstrapValue is the non-secret sentinel the create path
// registers for each binding. The SDK's create surface only accepts inline
// values (the env-reference form exists on the modify surface only), so the
// sandbox is created with this inert value and every binding is rotated to
// its host env reference immediately after creation, before the start script
// runs. A failed rotation removes the sandbox rather than leaving the
// sentinel behind; the value itself is recognizable and grants nothing.
const msbSecretBootstrapValue = "$MSB_BOOTSTRAP_UNSET"

// msbSecretBindings is the registry of credentials just-code can bind into a
// Microsandbox guest. Albert is required; GitHub and Context7 are optional
// and gated on explicit per-project approval.
func msbSecretBindings() []msbSecretBinding {
	return []msbSecretBinding{
		{
			Kind:       CredentialAlbert,
			GuestEnv:   msbAPISecretEnv,
			HostEnv:    "JUST_CODE_HOST_ALBERT_API_KEY",
			AllowHosts: []string{msbAllowHost},
		},
		{
			Kind:       CredentialGithub,
			GuestEnv:   "GITHUB_TOKEN",
			HostEnv:    "JUST_CODE_HOST_GITHUB_TOKEN",
			AllowHosts: []string{"github.com", "api.github.com", "raw.githubusercontent.com"},
			Optional:   true,
		},
		{
			Kind:       CredentialContext7,
			GuestEnv:   "CONTEXT7_API_KEY",
			HostEnv:    "JUST_CODE_HOST_CONTEXT7_API_KEY",
			AllowHosts: []string{"context7.com"},
			Optional:   true,
		},
	}
}

// bindingForKind returns the registered binding for a kind, or false.
func bindingForKind(kind CredentialKind) (msbSecretBinding, bool) {
	for _, b := range msbSecretBindings() {
		if b.Kind == kind {
			return b, true
		}
	}
	return msbSecretBinding{}, false
}

// BindingInfo is the display form of a registered binding, for the CLI. It
// carries no value, only the binding's metadata.
type BindingInfo struct {
	Kind       CredentialKind
	GuestEnv   string
	AllowHosts []string
	Optional   bool
}

// RegisteredBindings lists every credential binding just-code can proxy into
// a guest, in registry order.
func RegisteredBindings() []BindingInfo {
	registered := msbSecretBindings()
	out := make([]BindingInfo, len(registered))
	for i, b := range registered {
		out[i] = BindingInfo{
			Kind:       b.Kind,
			GuestEnv:   b.GuestEnv,
			AllowHosts: append([]string(nil), b.AllowHosts...),
			Optional:   b.Optional,
		}
	}
	return out
}

// validateSecretBindings checks the structural invariants the proxy relies
// on: distinct guest variables, disjoint allowed hosts (a host reachable by
// two bindings could receive either credential), and real DNS names.
func validateSecretBindings(bindings []msbSecretBinding) error {
	guestEnvs := map[string]CredentialKind{}
	hostFor := map[string]CredentialKind{}
	for _, b := range bindings {
		if b.GuestEnv == "" || b.HostEnv == "" {
			return fmt.Errorf("binding %s: guest and host variable names are required", b.Kind)
		}
		if prev, dup := guestEnvs[b.GuestEnv]; dup {
			return fmt.Errorf("bindings %s and %s share guest variable %s", prev, b.Kind, b.GuestEnv)
		}
		guestEnvs[b.GuestEnv] = b.Kind
		if len(b.AllowHosts) == 0 {
			return fmt.Errorf("binding %s: at least one allowed host is required", b.Kind)
		}
		for _, host := range b.AllowHosts {
			if !isDNSName(host) {
				return fmt.Errorf("binding %s: %q is not a DNS name", b.Kind, host)
			}
			if prev, dup := hostFor[host]; dup {
				return fmt.Errorf("bindings %s and %s both allow host %s: a credential must have exactly one destination set", prev, b.Kind, host)
			}
			hostFor[host] = b.Kind
		}
	}
	return nil
}

// isDNSName reports whether host is a plausible DNS name: dot-separated
// labels of letters, digits and hyphens, no leading or trailing hyphen, at
// least one dot. Wildcards and schemes are rejected.
func isDNSName(host string) bool {
	if !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// bindingSource records how a binding's value was obtained — never the value
// itself. The source participates in the binding-set revision and in the
// persisted BoundCredentials list, where it decides what revocation can
// reach.
type bindingSource string

const (
	// bindingSourceStore: the value came from the P08 credential store (via
	// credentialRef, approval, or the default albert entry).
	bindingSourceStore bindingSource = "store"
	// bindingSourceEnv: the value came from the legacy ALBERT_API_KEY
	// environment. Env-sourced credentials cannot be revoked through the
	// store; the variable itself is the credential.
	bindingSourceEnv bindingSource = "env"
)

// resolvedBinding is a binding plus its value at operation time. The value
// field must never be persisted, logged, or passed to the SDK spec: it only
// ever transits through withHostSecrets.
type resolvedBinding struct {
	msbSecretBinding
	source bindingSource
	value  string
}

// metadata drops the value and source, for the SDK-facing spec.
func (r resolvedBinding) metadata() msbSecretBinding { return r.msbSecretBinding }

// bindingsMetadata strips values from a resolved set, for the SDK spec. The
// spec crossing into msbCreateOptions/ModifyOptions is the structural
// guarantee that no secret value can appear in a persisted config or an SDK
// error dump.
func bindingsMetadata(bindings []resolvedBinding) []msbSecretBinding {
	out := make([]msbSecretBinding, len(bindings))
	for i, b := range bindings {
		out[i] = b.metadata()
	}
	return out
}

// bindingsRevision hashes the non-secret descriptor of a binding set: kind,
// source, guest variable and allowed hosts per binding. Values are excluded
// by construction, so the revision is safe to persist; a value rotation
// within an unchanged set does not move it (rotation applies at the next
// boot, which re-resolves the reference).
func bindingsRevision(bindings []resolvedBinding) string {
	h := sha256.New()
	_, _ = h.Write([]byte("jc-bindings-v1"))
	_, _ = h.Write([]byte{0})
	sorted := make([]resolvedBinding, len(bindings))
	copy(sorted, bindings)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Kind < sorted[j].Kind })
	for _, b := range sorted {
		hosts := append([]string(nil), b.AllowHosts...)
		sort.Strings(hosts)
		for _, part := range append([]string{string(b.Kind), string(b.source), b.GuestEnv}, hosts...) {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// storeBoundKinds lists the kinds whose value came from the credential store,
// sorted. It is what InstanceState.BoundCredentials persists: revocation can
// only reach store-bound credentials (an env-sourced value is not just-code's
// to revoke).
func storeBoundKinds(bindings []resolvedBinding) []string {
	var out []string
	for _, b := range bindings {
		if b.source == bindingSourceStore {
			out = append(out, string(b.Kind))
		}
	}
	sort.Strings(out)
	return out
}

// placeholderFor returns the exact value the runtime exposes in the guest for
// a secret registered under envVar: default_placeholder(env_var) = "$MSB_" +
// env_var. Only this exact value marks a protected secret; a prefix match
// would let a real credential starting with "$MSB_" pass as a placeholder.
func placeholderFor(envVar string) string { return "$MSB_" + envVar }

// readStoredCredential reads kind from the credential store: the native
// store first, then the consented file fallback when the native store is
// absent or unavailable. A present-but-failing native store (locked, denied,
// corrupt) is a hard error — silently falling back would mask it.
func readStoredCredential(ctx context.Context, kind CredentialKind) (string, error) {
	if s := DefaultCredentialStore(); s != nil {
		v, err := s.Get(ctx, kind)
		if err == nil {
			return v, nil
		}
		var se *StoreError
		if !isNotFound(err) && !(errors.As(err, &se) && se.State == "unavailable") {
			return "", err
		}
	}
	if fs, err := NewFileCredentialStore(); err == nil {
		if v, gerr := fs.Get(ctx, kind); gerr == nil {
			return v, nil
		} else if !isNotFound(gerr) && !errors.Is(gerr, ErrFileStoreAbsent) && !errors.Is(gerr, ErrFileStoreNotConsented) {
			return "", gerr
		}
	}
	return "", ErrCredentialNotFound
}

// ReadStoredCredential is the exported form of readStoredCredential, for the
// CLI's status reporting. It never exposes the value beyond the return.
func ReadStoredCredential(ctx context.Context, kind CredentialKind) (string, error) {
	return readStoredCredential(ctx, kind)
}

// resolveAlbert applies the Albert credential precedence chain: an explicit
// credentialRef (project manifest over user settings, collapsed into
// cfg.CredentialRef by the caller; JUST_CODE_CREDENTIAL_REF wins over both),
// then the legacy ALBERT_API_KEY environment, then the default stored albert
// credential. A credentialRef that names a missing credential is a hard
// error: the user asked for that exact credential.
func resolveAlbert(ctx context.Context, cfg Config) (string, bindingSource, error) {
	if ref := strings.TrimSpace(cfg.CredentialRef); ref != "" {
		v, err := readStoredCredential(ctx, CredentialKind(ref))
		if err != nil {
			return "", "", fmt.Errorf("credentialRef %q: %w (store it with 'just-code auth add %s', or fix the reference)", ref, err, ref)
		}
		return v, bindingSourceStore, nil
	}
	if cfg.APIKey != "" {
		return cfg.APIKey, bindingSourceEnv, nil
	}
	v, err := readStoredCredential(ctx, CredentialAlbert)
	if err == nil {
		return v, bindingSourceStore, nil
	}
	if !isNotFound(err) {
		return "", "", err
	}
	return "", "", fmt.Errorf("no Albert credential found: set ALBERT_API_KEY in the environment or .env, or store one with 'just-code auth add albert'")
}

// withHostSecrets publishes each resolved value under its binding's host
// transport variable for the duration of fn, then restores the prior
// environment exactly (a pre-existing variable is put back, not lost). The
// SDK's env-reference secret path reads the value from this process's
// environment at apply/boot time, so the raw value never enters a persisted
// spec, the guest environment, or an error dump.
func withHostSecrets(bindings []resolvedBinding, fn func() error) (err error) {
	type prior struct {
		value string
		set   bool
	}
	saved := make([]prior, 0, len(bindings))
	for _, b := range bindings {
		prev, ok := os.LookupEnv(b.HostEnv)
		saved = append(saved, prior{prev, ok})
		if err := os.Setenv(b.HostEnv, b.value); err != nil {
			return fmt.Errorf("publishing %s transport variable: %w", b.Kind, err)
		}
	}
	defer func() {
		for i, b := range bindings {
			if saved[i].set {
				_ = os.Setenv(b.HostEnv, saved[i].value)
			} else {
				_ = os.Unsetenv(b.HostEnv)
			}
		}
	}()
	return fn()
}
