package justcode

import (
	"reflect"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// P02 contract fixtures: pin the SDK secret surface that just-code relies on
// for credential transport, as characterized empirically in
// docs/decisions/2026-09-24-microsandbox-credential-transport.md.
//
// The SDK's wire serializer (buildModifyRequestJSON) is unexported, so these
// fixtures pin the exported option shapes plus just-code's own call sites.
// The two persistence paths observed on the host DB (msb 0.7.0 runtime,
// SDK v0.7.2):
//   - CLI/env reference path: only {"kind":"env","var":...} is persisted,
//     never the raw value.
//   - SDK Value path (what msbCreateOptions/msbNextStartOptions use today):
//     the raw value is persisted in cleartext in the sandbox config JSON
//     (~/.microsandbox/db/msb.db, mode 0644).
// P09 must migrate just-code to the reference path; until then these
// fixtures keep the gap visible instead of silently drifting.

// TestP02SecretEntryDefaultsPinsTheTransportContract pins the SDK option
// shape: headers-only substitution, TLS identity required, auto placeholder.
func TestP02SecretEntryDefaultsPinsTheTransportContract(t *testing.T) {
	entry := msb.Secret.Env("ALBERT_API_KEY", "secret-value", msb.SecretEnvOptions{
		Allow: []string{"albert.api.etalab.gouv.fr"},
	})
	if entry.EnvVar != "ALBERT_API_KEY" || entry.Value != "secret-value" {
		t.Fatalf("entry = %+v", entry)
	}
	if !reflect.DeepEqual(entry.Allow, []string{"albert.api.etalab.gouv.fr"}) {
		t.Fatalf("allow = %v", entry.Allow)
	}
	if entry.Placeholder != "" {
		t.Fatalf("custom placeholder set unexpectedly: %q", entry.Placeholder)
	}
	if entry.RequireTLSIdentity != nil {
		t.Fatalf("require TLS identity = %+v, want nil (defaults to true server-side)", entry.RequireTLSIdentity)
	}
	if entry.Substitution.Headers != nil || entry.Substitution.Query || entry.Substitution.Body {
		t.Fatalf("substitution = %+v, want zero value (headers-only server default)", entry.Substitution)
	}
	if entry.ViolationAction != "" {
		t.Fatalf("violation action = %q, want empty (sandbox default block-and-log)", entry.ViolationAction)
	}
}

// TestP02CreateOptionsPersistRawValueOnHost documents the P02 finding: the
// current create path carries the raw key in SecretEntry.Value, which the
// runtime persists in cleartext in ~/.microsandbox/db/msb.db (mode 0644).
// The guest never sees it, but the host does. If this test starts failing
// because msbCreateOptions moved to a reference-based source, that is the
// P09 migration landing: update the decision record, then relax it.
func TestP02CreateOptionsPersistRawValueOnHost(t *testing.T) {
	spec := msbSandboxSpec{
		Image:      msbImage,
		Workspace:  "/workspace-on-host",
		APIKey:     "raw-secret-value",
		AllowHosts: []string{msbAllowHost},
	}
	var cfg msb.SandboxConfig
	for _, option := range msbCreateOptions(spec) {
		option(&cfg)
	}
	if len(cfg.Secrets) != 1 {
		t.Fatalf("secrets = %+v", cfg.Secrets)
	}
	if cfg.Secrets[0].Value != "raw-secret-value" {
		t.Fatalf("create path no longer inlines the value: %+v", cfg.Secrets[0])
	}
}

// TestP02NextStartOptionsPersistRawValueOnHost is the modify-path twin of
// the create-path finding: the secret refresh also carries the raw value.
func TestP02NextStartOptionsPersistRawValueOnHost(t *testing.T) {
	options := msbNextStartOptions(nil, "raw-secret-value")
	spec, ok := options.Secrets[msbAPISecretEnv]
	if !ok {
		t.Fatalf("secrets = %+v", options.Secrets)
	}
	if spec.Value != "raw-secret-value" {
		t.Fatalf("refresh path no longer inlines the value: %+v", spec)
	}
	if spec.Env != "" || spec.Store != "" {
		t.Fatalf("value path must be exclusive: %+v", spec)
	}
}

// TestP02SecretModifySpecSourceKinds pins the three mutually exclusive
// secret sources on the modify surface. Env is the only source that avoids
// persisting the raw value on the host today; Store is a host-side secret
// store reference with no CLI/SDK backend in 0.7.0/v0.7.2 (wire-only).
func TestP02SecretModifySpecSourceKinds(t *testing.T) {
	specType := reflect.TypeOf(msb.SecretModifySpec{})
	for _, want := range []string{"Env", "Value", "Store", "Placeholder"} {
		if field, ok := specType.FieldByName(want); !ok || field.Type.Kind() != reflect.String {
			t.Fatalf("SecretModifySpec.%s missing or not a string: %+v", want, field)
		}
	}
	if field, ok := specType.FieldByName("AllowedHosts"); !ok || field.Type.Kind() != reflect.Slice {
		t.Fatalf("SecretModifySpec.AllowedHosts missing or not a slice: %+v", field)
	}
	byEnv := msb.SecretModifySpec{Env: "HOST_VAR", AllowedHosts: []string{"h.example"}}
	byStore := msb.SecretModifySpec{Store: "vault://prod/db", AllowedHosts: []string{"h.example"}}
	if byEnv.Value != "" || byEnv.Store != "" {
		t.Fatalf("env source must not carry value/store: %+v", byEnv)
	}
	if byStore.Value != "" || byStore.Env != "" {
		t.Fatalf("store source must not carry value/env: %+v", byStore)
	}
}
