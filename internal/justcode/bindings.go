package justcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
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

// credentialReader reads a stored credential and reports which store
// answered. It is the seam through which both resolution and revocation
// touch the stores, so tests can drive every source without a real keychain.
type credentialReader func(ctx context.Context, kind CredentialKind, from string) (value, store string, err error)

// readStoredWith reads kind from the requested store only: "native" is the
// OS-native store (Keychain / Credential Manager / Secret Service), "file"
// the consented fallback, "" the normal order (native, then fallback). It
// returns the store that answered. A store operation is scoped to the store
// being edited; a present-but-failing native store (locked, denied, corrupt)
// is a hard error for the native choice, never a silent read elsewhere.
func readStoredWith(ctx context.Context, kind CredentialKind, from string) (string, string, error) {
	switch from {
	case "native":
		s := DefaultCredentialStore()
		if s == nil {
			return "", "", ErrCredentialNotFound
		}
		v, err := s.Get(ctx, kind)
		if err != nil {
			return "", "", err
		}
		return v, "native", nil
	case "file":
		fs, err := NewFileCredentialStore()
		if err != nil {
			return "", "", err
		}
		v, err := fs.Get(ctx, kind)
		if errors.Is(err, ErrFileStoreAbsent) || errors.Is(err, ErrFileStoreNotConsented) {
			return "", "", ErrCredentialNotFound
		}
		if err != nil {
			return "", "", err
		}
		return v, "file", nil
	default:
		return readStoredCredential(ctx, kind)
	}
}

// readStoredWithNative is readStoredCredential with both stores injected, so
// the no-silent-fallback contract can be pinned without a real keychain.
func readStoredWithNative(ctx context.Context, kind CredentialKind, native, fileStore func(context.Context, CredentialKind) (string, error)) (string, string, error) {
	if v, err := native(ctx, kind); err == nil {
		return v, "native", nil
	} else if !isNotFound(err) {
		// Locked, denied, corrupt, unavailable: hard errors. The fallback
		// file may hold a different credential exactly when the expected one
		// cannot be verified, so binding it silently is forbidden.
		return "", "", err
	}
	if v, err := fileStore(ctx, kind); err == nil {
		return v, "file", nil
	} else if !isNotFound(err) && !errors.Is(err, ErrFileStoreAbsent) && !errors.Is(err, ErrFileStoreNotConsented) {
		return "", "", err
	}
	return "", "", ErrCredentialNotFound
}

// resolvedBinding is a binding plus its value at operation time. The value
// field must never be persisted, logged, or passed to the SDK spec: it only
// ever transits through withHostSecrets.
type resolvedBinding struct {
	msbSecretBinding
	source bindingSource
	value  string
	// store names which credential store the value came from ("native" or
	// "file"; empty for env-sourced). It is metadata, not a secret, and it is
	// what makes `auth remove --fallback` revoke only the instances bound to
	// that store.
	store string
	// entry is the credential-store entry name the value was read from, when
	// it is not the binding's own kind. An Albert binding fed by
	// `credentialRef: "github"` is guest binding ALBERT_API_KEY resolved from
	// store entry "github": revocation addresses entries, so it must know
	// which entry to match and which guest binding that entry fed.
	entry string
}

// storeEntry is the credential-store entry a resolved binding addresses:
// the explicit entry name when set (a credentialRef), otherwise the kind.
func (r resolvedBinding) storeEntry() string {
	if r.entry != "" {
		return r.entry
	}
	return string(r.Kind)
}

// storeGenerationOf builds the composite rotation marker for the binding
// set: every store-sourced binding's entry, store and generation, sorted and
// joined. Inspecting only the first store-sourced binding would hide a
// rotation of any later one (an approved GitHub token rotated while Albert is
// first in the registry), leaving a healthy instance on the no-op path with
// the old token. The marker is non-secret by construction — counters and
// names, never anything derived from a value.
func storeGenerationOf(fs FS, stateDir string, bindings []resolvedBinding) string {
	var parts []string
	for _, b := range bindings {
		if b.source != bindingSourceStore {
			continue
		}
		entry := b.storeEntry()
		gen := ""
		// The generation source follows the store the binding resolved from.
		// Reading the file store's counter for a native-sourced binding would
		// let a stale fallback generation (entries survive Remove) mask a
		// native rotation: the composite would not move and a healthy
		// instance would keep serving the old credential.
		if b.store == "file" {
			if fstore, err := NewFileCredentialStore(); err == nil {
				if g, gerr := fstore.Generation(context.Background(), CredentialKind(entry)); gerr == nil {
					gen = g
				}
			}
		}
		if gen == "" {
			if n, nerr := credentialGenerationIn(fs, stateDir, CredentialKind(entry)); nerr == nil {
				gen = strconv.Itoa(n)
			}
		}
		parts = append(parts, entry+"@"+b.store+":"+gen)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
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
// source, guest variable, allowed hosts, and — for store-sourced bindings —
// the store entry and the store it was read from. Values are excluded by
// construction, so the revision is safe to persist; a value rotation within
// an unchanged set does not move it (rotation applies at the next boot,
// which re-resolves the reference).
//
// Including the store matters: when both stores hold the same entry, losing
// the native one changes nothing but which store answers, so a revision that
// hashed only the generic "store" source would match and a healthy running
// instance would take the no-op path, never rebinding from the fallback.
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
		for _, part := range append([]string{string(b.Kind), string(b.source), b.GuestEnv, b.store, b.storeEntry()}, hosts...) {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// boundStoreEntry is one persisted record of a store-sourced binding:
// "entry@store#binding". The entry is the credential-store entry the value
// came from (usually the kind, but a credentialRef can name another), the
// store is where it was read, and the binding is the guest secret it fed.
// Revocation addresses entries, so it needs all three: which entry to match,
// which store to edit, and which guest binding to drop.
type boundStoreEntry struct {
	Entry   string
	Store   string
	Binding CredentialKind
}

func formatBoundStoreEntry(e boundStoreEntry) string {
	return e.Entry + "@" + e.Store + "#" + string(e.Binding)
}

// boundRecordForm distinguishes the persisted record generations, because
// each carries a different amount of knowledge about a binding's origin.
type boundRecordForm int

const (
	// boundRecordPending is the "?" marker: a store-sourced value may exist
	// but nothing about it is known.
	boundRecordPending boundRecordForm = iota
	// boundRecordLegacy is the original bare "entry": the store is unknown,
	// so the record's entry name is all there is to go on.
	boundRecordLegacy
	// boundRecordInterim is "entry@store": no binding recorded, so the entry
	// fed the guest binding of the same name.
	boundRecordInterim
	// boundRecordFull is "entry@store#binding": complete.
	boundRecordFull
)

// parseBoundStoreEntry reads a persisted record into its parts. Every record
// names the credential-store entry the value came from; the store and the
// guest binding it fed are known only in the newer forms.
func parseBoundStoreEntry(record string) (boundStoreEntry, boundRecordForm) {
	if record == pendingBoundEntry {
		return boundStoreEntry{}, boundRecordPending
	}
	entry, rest, hasStore := strings.Cut(record, "@")
	if entry == "" {
		return boundStoreEntry{}, boundRecordPending
	}
	if !hasStore || rest == "" || entry == "?" || rest == "?" {
		return boundStoreEntry{Entry: entry, Binding: CredentialKind(entry)}, boundRecordLegacy
	}
	store, binding, hasBinding := strings.Cut(rest, "#")
	if store == "" {
		return boundStoreEntry{}, boundRecordPending
	}
	if !hasBinding || binding == "" {
		return boundStoreEntry{Entry: entry, Store: store, Binding: CredentialKind(entry)}, boundRecordInterim
	}
	return boundStoreEntry{Entry: entry, Store: store, Binding: CredentialKind(binding)}, boundRecordFull
}

// storeBoundEntries lists a resolved set's store-sourced bindings, sorted.
// It is what InstanceState.BoundCredentials persists. Revocation can only
// reach store-bound credentials (an env-sourced value is not just-code's to
// revoke), and it must target both the store and the entry the value was
// read from — a native and a fallback entry can both exist, and a
// credentialRef can make an Albert binding come from a differently-named
// entry.
func storeBoundEntries(bindings []resolvedBinding) []string {
	var out []string
	for _, b := range bindings {
		if b.source == bindingSourceStore {
			out = append(out, formatBoundStoreEntry(boundStoreEntry{
				Entry:   b.storeEntry(),
				Store:   b.store,
				Binding: b.Kind,
			}))
		}
	}
	sort.Strings(out)
	return out
}

// pendingBoundEntry is the placeholder written to BoundCredentials for an
// instance whose binding set could not be resolved at apply time (e.g. the
// credential was absent). It means "a store-sourced value may be persisted
// by an earlier apply; do not skip its revocation". Every real entry has a
// store suffix, so the marker cannot collide with one.
const pendingBoundEntry = "?@?"

// entryVerdicts reports what a persisted BoundCredentials list says about a
// credential-store entry being edited, across every record for that entry.
// One entry can feed several guest bindings at once — `credentialRef:
// "github"` with the GitHub binding approved records both
// `github@native#albert` and `github@native#github` — so the verdict collects
// every binding rather than stopping at the first record.
//
// A record naming the entry in this store contributes its guest binding;
// naming it in the other store is a known "elsewhere"; a legacy bare record
// (no store) means the store being edited cannot be ruled out. Records for
// other entries are ignored. known is false when no record mentions the entry.
//
// An empty store means the normal lookup order (native, then fallback): a
// removal on that path addresses whichever store answered, so any store in
// the records matches.
func entryVerdicts(bound []string, entry, store string) (verdict bindingVerdict, bindings []CredentialKind, known bool) {
	verdict = bindingOther
	for _, record := range bound {
		parsed, form := parseBoundStoreEntry(record)
		if form == boundRecordPending || parsed.Entry != entry {
			continue
		}
		known = true
		switch form {
		case boundRecordLegacy:
			// No store recorded: the store being edited cannot be ruled out.
			verdict = bindingThisStore
			bindings = appendUniqueKind(bindings, parsed.Binding)
		case boundRecordInterim, boundRecordFull:
			if store == "" || parsed.Store == store {
				verdict = bindingThisStore
				bindings = appendUniqueKind(bindings, parsed.Binding)
			}
			// A record naming the other store contributes no binding here
			// and does not flip the verdict to ThisStore.
		}
	}
	if !known {
		return bindingThisStore, nil, false
	}
	return verdict, bindings, known
}

func appendUniqueKind(list []CredentialKind, kind CredentialKind) []CredentialKind {
	for _, k := range list {
		if k == kind {
			return list
		}
	}
	return append(list, kind)
}

// placeholderFor returns the exact value the runtime exposes in the guest for
// a secret registered under envVar: default_placeholder(env_var) = "$MSB_" +
// env_var. Only this exact value marks a protected secret; a prefix match
// would let a real credential starting with "$MSB_" pass as a placeholder.
func placeholderFor(envVar string) string { return "$MSB_" + envVar }

// readStoredCredential reads kind from the credential store: the native
// store first, then the consented file fallback when the native store
// reports not-found. A failing native store (unavailable, locked, denied,
// corrupt) is a hard error, never a silent fallback: the fallback file may
// hold a stale or different credential precisely when the expected one
// cannot be verified, and the runtime must not start a sandbox on it without
// the reason being surfaced.
//
// The returned store name records which store answered, so the caller can
// persist it: revocation must target the store it was read from.
func readStoredCredential(ctx context.Context, kind CredentialKind) (string, string, error) {
	native := func(ctx context.Context, kind CredentialKind) (string, error) {
		if s := DefaultCredentialStore(); s == nil {
			return "", ErrCredentialNotFound
		} else {
			return s.Get(ctx, kind)
		}
	}
	fileGet := func(ctx context.Context, kind CredentialKind) (string, error) {
		fs, err := NewFileCredentialStore()
		if err != nil {
			return "", err
		}
		return fs.Get(ctx, kind)
	}
	return readStoredWithNative(ctx, kind, native, fileGet)
}

// ResolveAlbert is the exported form of resolveAlbert, for the CLI's
// catalogue validation. It never exposes the value beyond the return.
func ResolveAlbert(ctx context.Context, cfg Config, from string) (string, bindingSource, string, string, error) {
	return resolveAlbert(ctx, cfg, from)
}

// ReadStoredCredential is the exported form of readStoredCredential, for the
// CLI's status reporting. It never exposes the value beyond the return.
func ReadStoredCredential(ctx context.Context, kind CredentialKind) (string, error) {
	v, _, err := readStoredCredential(ctx, kind)
	return v, err
}

// resolveAlbert applies the Albert credential precedence chain: an explicit
// credentialRef (project manifest over user settings, collapsed into
// cfg.CredentialRef by the caller; JUST_CODE_CREDENTIAL_REF wins over both),
// then the legacy ALBERT_API_KEY environment, then the stored albert
// credential. A credentialRef that names a missing credential is a hard
// error: the user asked for that exact credential.
//
// `from` scopes the store lookup to one store ("" = native then fallback);
// revocation uses it so a removal targets the instances that actually
// resolved from the store being edited. It returns the value, its source, the
// store that answered, and the credential-store entry it was read from.
func resolveAlbert(ctx context.Context, cfg Config, from string) (string, bindingSource, string, string, error) {
	return resolveAlbertWith(readStoredWith, ctx, cfg, from)
}

// resolveAlbertWith is resolveAlbert over an injected reader, so tests can
// exercise the precedence chain and the store scoping without a real
// credential store. It returns the resolved value, its source, the store it
// came from, and the credential-store entry it was read from.
func resolveAlbertWith(read credentialReader, ctx context.Context, cfg Config, from string) (value string, source bindingSource, store, entry string, err error) {
	if ref := strings.TrimSpace(cfg.CredentialRef); ref != "" {
		v, s, rerr := read(ctx, CredentialKind(ref), from)
		if rerr != nil {
			return "", "", "", "", fmt.Errorf("credentialRef %q: %w (store it with 'just-code auth add %s', or fix the reference)", ref, rerr, ref)
		}
		return v, bindingSourceStore, s, ref, nil
	}
	if cfg.APIKey != "" {
		return cfg.APIKey, bindingSourceEnv, "", "", nil
	}
	v, s, rerr := read(ctx, CredentialAlbert, from)
	if rerr == nil {
		return v, bindingSourceStore, s, string(CredentialAlbert), nil
	}
	if !isNotFound(rerr) {
		return "", "", "", "", rerr
	}
	return "", "", "", "", fmt.Errorf("no Albert credential found: set ALBERT_API_KEY in the environment or .env, or store one with 'just-code auth add albert'")
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
