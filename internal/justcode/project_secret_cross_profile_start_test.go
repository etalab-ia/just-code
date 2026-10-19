package justcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A binding approved for another profile than the active one is a STRUCTURAL
// mismatch: no amount of waiting or retrying repairs it, because the approval
// names a profile the project is no longer using. The no-op start path used to
// tolerate every resolution failure, so `just-code start` on an already
// healthy sandbox exited 0 with a warning while the guest kept serving the
// previous profile's secrets (#204).
//
// The tolerance exists for the opposite case — a transient failure such as a
// locked store or a credential not yet stored — where an instance that only
// needs a restart must not become unusable. Both cases are pinned here so the
// fix cannot quietly drop the other.

// crossProfileRuntime builds a runtime whose sandbox is already healthy (so
// the plan is a no-op) and whose approved binding belongs to a profile other
// than the active one.
func crossProfileRuntime(t *testing.T) (*MicrosandboxRuntime, *fakeMSBClient) {
	t.Helper()
	isolateHostState(t)
	client := &fakeMSBClient{exists: true, status: "running"}
	m := projectSecretTestRuntime(t, client)
	importProjectSecrets(t, m.ProjectSecrets, "development", ".env", map[string]string{"DUPE_KEY": "dev-value"})
	// Approve under `development`, then switch the active profile to
	// `staging` — exactly the sequence the qualification protocol ran.
	approveForTest(t, m, "DUPE_KEY", "JC_DUPE_KEY")
	if err := SetProjectSecretActiveProfile(DefaultFS, m.StateDir, m.ProjectSecrets.Namespace(), "staging"); err != nil {
		t.Fatal(err)
	}
	// Make the guest genuinely healthy so the plan is a no-op: that is the
	// path the issue reports as exiting 0, not the credential-op path.
	m.cfg.Isolation = IsolationFull
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Healthy: true}
	}
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	if err := WriteInstanceState(DefaultFS, path, m.desiredState(true, testBindings(m.cfg.APIKey), nil).toState()); err != nil {
		t.Fatal(err)
	}
	return m, client
}

// TestStaleProfileStartFailsInsteadOfServingTheOldProfile pins the required
// behaviour: the mismatch must surface as a failure, not an exit-0 warning.
func TestStaleProfileStartFailsInsteadOfServingTheOldProfile(t *testing.T) {
	m, _ := crossProfileRuntime(t)

	err := m.Reconcile(context.Background())
	if err == nil {
		t.Fatal("a binding approved for another profile must fail the start, not warn and succeed")
	}
	// The message has to name both profiles, or the user cannot tell which
	// approval is stale.
	if !strings.Contains(err.Error(), "development") || !strings.Contains(err.Error(), "staging") {
		t.Fatalf("the refusal must name both the binding's profile and the active one: %v", err)
	}
	if strings.Contains(err.Error(), "reusing the persisted secret references") {
		t.Fatalf("a structural mismatch must not advertise reuse of stale references: %v", err)
	}
}

// TestTransientResolutionFailureStillStartsNoOp guards the tolerance the fix
// must not remove: a locked store, or a credential not stored yet, is
// transient and must still let an otherwise-healthy instance run.
func TestTransientResolutionFailureStillStartsNoOp(t *testing.T) {
	isolateHostState(t)
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	// The required Albert credential cannot be read: a transient condition —
	// the user simply has not stored it yet.
	m.cfg.APIKey = ""
	m.cfg.CredentialRef = ""
	m.credentialRead = func(context.Context, CredentialKind, string) (string, string, error) {
		return "", "", ErrCredentialNotFound
	}
	// A healthy guest whose recorded state matches the unresolved desired
	// state: the plan is a no-op, which is the path that must stay tolerant.
	m.cfg.Isolation = IsolationFull
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Healthy: true}
	}
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	if err := WriteInstanceState(DefaultFS, path, m.desiredState(false, nil, nil).toState()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(path)
		_ = os.RemoveAll(filepath.Dir(path))
	}()

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("a transient resolution failure must not make a healthy instance unusable: %v", err)
	}
}
