package justcode

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRegisteredBindingsAreValid(t *testing.T) {
	if err := validateSecretBindings(msbSecretBindings()); err != nil {
		t.Fatalf("the built-in registry must be valid: %v", err)
	}
}

func TestValidateSecretBindingsRejectsCrossedDestinations(t *testing.T) {
	base := func() []msbSecretBinding {
		return []msbSecretBinding{
			{Kind: CredentialAlbert, GuestEnv: "ALBERT_API_KEY", HostEnv: "H1", AllowHosts: []string{"albert.example.fr"}},
			{Kind: CredentialGithub, GuestEnv: "GITHUB_TOKEN", HostEnv: "H2", AllowHosts: []string{"github.com"}, Optional: true},
		}
	}
	// A host allowed by two bindings could receive either credential: the
	// "no crossed destinations" invariant (issue #74) forbids it.
	crossed := base()
	crossed[1].AllowHosts = []string{"albert.example.fr"}
	if err := validateSecretBindings(crossed); err == nil || !strings.Contains(err.Error(), "albert.example.fr") {
		t.Fatalf("shared allowed host must be rejected: %v", err)
	}
	// Two bindings exposing the same guest variable would fight over it.
	dup := base()
	dup[1].GuestEnv = "ALBERT_API_KEY"
	if err := validateSecretBindings(dup); err == nil || !strings.Contains(err.Error(), "ALBERT_API_KEY") {
		t.Fatalf("duplicate guest variable must be rejected: %v", err)
	}
	// A non-DNS allow host (wildcard, scheme, bare word) is a config error.
	for _, host := range []string{"*.example.fr", "https://example.fr", "localhost", "example"} {
		bad := base()
		bad[1].AllowHosts = []string{host}
		if err := validateSecretBindings(bad); err == nil {
			t.Fatalf("allow host %q must be rejected", host)
		}
	}
	if err := validateSecretBindings(base()); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}
}

func TestBindingsRevisionIgnoresValues(t *testing.T) {
	a := testBindings("first-value")
	b := testBindings("rotated-value")
	if bindingsRevision(a) != bindingsRevision(b) {
		t.Fatal("a value rotation within an unchanged set must not move the revision")
	}
	// The source participates: an env-sourced and a store-sourced albert
	// binding are different states (revocation can only reach the latter).
	c := testBindings("first-value")
	c[0].source = bindingSourceStore
	if bindingsRevision(a) == bindingsRevision(c) {
		t.Fatal("the credential source must participate in the revision")
	}
	// Adding an optional binding moves the revision.
	d := append(testBindings("first-value"), resolvedBinding{
		msbSecretBinding: msbSecretBindings()[1], source: bindingSourceStore, value: "tok",
	})
	if bindingsRevision(a) == bindingsRevision(d) {
		t.Fatal("adding a binding must move the revision")
	}
}

func TestStoreBoundKinds(t *testing.T) {
	bindings := append(testBindings("k"), resolvedBinding{
		msbSecretBinding: msbSecretBindings()[1], source: bindingSourceStore, value: "tok",
	})
	// albert is env-sourced here, github store-sourced.
	if got := storeBoundKinds(bindings); !reflect.DeepEqual(got, []string{"github"}) {
		t.Fatalf("storeBoundKinds = %v", got)
	}
}

func TestWithHostSecretsPublishesAndRestores(t *testing.T) {
	bindings := testBindings("secret-value")
	hostEnv := bindings[0].HostEnv
	os.Unsetenv(hostEnv)
	// A pre-existing variable is restored, not lost.
	t.Setenv("JUST_CODE_HOST_PREEXISTING", "original")
	pre := resolvedBinding{msbSecretBinding: msbSecretBinding{
		Kind: CredentialGithub, GuestEnv: "G", HostEnv: "JUST_CODE_HOST_PREEXISTING", AllowHosts: []string{"example.fr"},
	}, source: bindingSourceStore, value: "override"}
	all := append(bindings, pre)

	var seen, seenPre string
	err := withHostSecrets(all, func() error {
		seen = os.Getenv(hostEnv)
		seenPre = os.Getenv("JUST_CODE_HOST_PREEXISTING")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "secret-value" || seenPre != "override" {
		t.Fatalf("transport variables not published: %q, %q", seen, seenPre)
	}
	if _, ok := os.LookupEnv(hostEnv); ok {
		t.Fatalf("transport variable %s was not removed afterwards", hostEnv)
	}
	if got := os.Getenv("JUST_CODE_HOST_PREEXISTING"); got != "original" {
		t.Fatalf("pre-existing variable clobbered: %q", got)
	}
}

func TestResolveAlbertPrecedence(t *testing.T) {
	// The legacy environment wins over the default stored credential and
	// loses to an explicit credentialRef.
	v, src, err := resolveAlbert(context.Background(), Config{APIKey: "env-key"})
	if err != nil || v != "env-key" || src != bindingSourceEnv {
		t.Fatalf("env source: %q, %q, %v", v, src, err)
	}
	// An explicit credentialRef that names nothing stored is a hard error
	// naming the reference, never a silent fallback to another source.
	_, _, err = resolveAlbert(context.Background(), Config{
		APIKey:        "env-key",
		CredentialRef: "definitely-not-stored-anywhere",
	})
	if err == nil || !strings.Contains(err.Error(), "definitely-not-stored-anywhere") {
		t.Fatalf("unknown credentialRef must fail by name: %v", err)
	}
}

func TestResolveBindingsSkipsUnapprovedOptionals(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{})
	m.readStored = func(context.Context, CredentialKind) (string, error) {
		return "stored-token", nil
	}
	bindings, err := m.resolveBindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].Kind != CredentialAlbert {
		t.Fatalf("only the required binding may resolve without approval: %+v", bindings)
	}
}

func TestResolveBindingsApprovedOptionalIsBound(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{})
	m.readStored = func(_ context.Context, kind CredentialKind) (string, error) {
		return "stored-" + string(kind), nil
	}
	path := bindingApprovalsPath(m.StateDir, m.InstanceName())
	if err := ApproveBinding(DefaultFS, path, CredentialGithub); err != nil {
		t.Fatal(err)
	}
	bindings, err := m.resolveBindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[1].Kind != CredentialGithub || bindings[1].value != "stored-github" {
		t.Fatalf("approved optional binding must resolve from the store: %+v", bindings)
	}
	if bindings[1].source != bindingSourceStore {
		t.Fatalf("optional bindings are store-sourced: %q", bindings[1].source)
	}
}

func TestResolveBindingsApprovedButUnstoredIsSkipped(t *testing.T) {
	m := newTestMicrosandbox(t, &fakeMSBClient{})
	m.readStored = func(context.Context, CredentialKind) (string, error) {
		return "", ErrCredentialNotFound
	}
	path := bindingApprovalsPath(m.StateDir, m.InstanceName())
	if err := ApproveBinding(DefaultFS, path, CredentialContext7); err != nil {
		t.Fatal(err)
	}
	bindings, err := m.resolveBindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("an approved-but-unstored optional must be skipped, not fatal: %+v", bindings)
	}
}

func TestApproveBindingRejectsUnknownAndRequired(t *testing.T) {
	path := bindingApprovalsPath(t.TempDir(), "inst")
	if err := ApproveBinding(DefaultFS, path, CredentialKind("gitlab")); err == nil {
		t.Fatal("unknown kind approved")
	}
	if err := ApproveBinding(DefaultFS, path, CredentialAlbert); err == nil {
		t.Fatal("the required albert binding needs no approval and must be rejected")
	}
	if err := ApproveBinding(DefaultFS, path, CredentialGithub); err != nil {
		t.Fatal(err)
	}
	a, err := ReadBindingApprovals(DefaultFS, path)
	if err != nil || !a.Approves(CredentialGithub) {
		t.Fatalf("approval round trip: %+v, %v", a, err)
	}
	if err := RevokeBindingApproval(DefaultFS, path, CredentialGithub); err != nil {
		t.Fatal(err)
	}
	a, err = ReadBindingApprovals(DefaultFS, path)
	if err != nil || a.Approves(CredentialGithub) {
		t.Fatalf("revocation round trip: %+v, %v", a, err)
	}
}

// TestStartCreateRotatesSentinelBeforeLaunch pins the create-path ordering:
// create (sentinel) -> rotate to env reference -> launch. A rotation failure
// must remove the incomplete sandbox.
func TestStartCreateRotatesSentinelBeforeLaunch(t *testing.T) {
	client := &fakeMSBClient{}
	m := newTestMicrosandbox(t, client)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var createIdx, rotateIdx, execIdx = -1, -1, -1
	for i, call := range client.calls {
		switch {
		case call == "create":
			createIdx = i
		case call == "rotate "+msbSandbox:
			rotateIdx = i
		case strings.HasPrefix(call, "exec "):
			if execIdx == -1 {
				execIdx = i
			}
		}
	}
	if createIdx == -1 || rotateIdx == -1 || execIdx == -1 || !(createIdx < rotateIdx && rotateIdx < execIdx) {
		t.Fatalf("expected create -> rotate -> launch ordering: %v", client.calls)
	}
}

func TestStartCreateRotationFailureRemovesSandbox(t *testing.T) {
	client := &fakeMSBClient{modifyErr: errTest}
	m := newTestMicrosandbox(t, client)
	err := m.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("rotation failure must be reported with the rollback: %v", err)
	}
	if !hasCall(client, "remove "+msbSandbox) {
		t.Fatalf("the incomplete sandbox must be removed: %v", client.calls)
	}
	if hasCall(client, "exec ") {
		t.Fatalf("the start script must not run on an unrotated sandbox: %v", client.calls)
	}
}

var errTest = errorString("test failure")

type errorString string

func (e errorString) Error() string { return string(e) }
