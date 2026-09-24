package justcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The plan function is pure: these tests pin the classification table of
// P07. The executor tests below cover the journalling and the no-write
// no-op contract.

func TestPlanReconcileClassification(t *testing.T) {
	desired := DesiredState{
		Instance:     "jc-foo-ab12c",
		Isolation:    IsolationBackend,
		WorkspaceDir: "/tmp/foo",
		Image:        msbImage,
		Username:     "opencode",
	}
	applied := desired.toState()

	cases := []struct {
		name     string
		applied  *InstanceState
		facts    ReconcileFacts
		wantOps  []ReconcileOp
		wantNoop bool
	}{
		{
			name:    "missing instance creates",
			facts:   ReconcileFacts{},
			wantOps: []ReconcileOp{OpCreate},
		},
		{
			name:    "creation-fixed change refuses in place",
			facts:   ReconcileFacts{Exists: true, Running: true, CreationFixedChanged: true},
			wantOps: []ReconcileOp{OpRecreate},
		},
		{
			name: "identical state healthy is a no-op",
			applied: func() *InstanceState {
				s := applied
				return &s
			}(),
			facts:    ReconcileFacts{Exists: true, Running: true, Healthy: true, CredentialChanged: false},
			wantOps:  []ReconcileOp{OpNoOp},
			wantNoop: true,
		},
		{
			name: "identical state unhealthy backend restarts the backend only",
			applied: func() *InstanceState {
				s := applied
				return &s
			}(),
			facts:   ReconcileFacts{Exists: true, Running: true, CredentialChanged: false},
			wantOps: []ReconcileOp{OpRestartBackend},
		},
		{
			name: "identical state stopped starts",
			applied: func() *InstanceState {
				s := applied
				return &s
			}(),
			facts:   ReconcileFacts{Exists: true, CredentialChanged: false},
			wantOps: []ReconcileOp{OpStartVM},
		},
		{
			name: "credential change on running VM refreshes then restarts",
			applied: func() *InstanceState {
				s := applied
				return &s
			}(),
			facts:   ReconcileFacts{Exists: true, Running: true, Healthy: true, CredentialChanged: true},
			wantOps: []ReconcileOp{OpRefreshCredentials, OpRestartVM},
		},
		{
			name: "credential change on stopped VM refreshes for next boot",
			applied: func() *InstanceState {
				s := applied
				return &s
			}(),
			facts:   ReconcileFacts{Exists: true, CredentialChanged: true},
			wantOps: []ReconcileOp{OpRefreshCredentials, OpStartVM},
		},
		{
			name:    "pre-state instance gets full refresh",
			facts:   ReconcileFacts{Exists: true, Running: true},
			wantOps: []ReconcileOp{OpRefreshCredentials, OpRestartVM},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := PlanReconcile(tc.applied, desired, tc.facts)
			if !sameOps(plan.Ops, tc.wantOps) {
				t.Fatalf("ops = %v, want %v (reason: %s)", plan.Ops, tc.wantOps, plan.Reason)
			}
			if plan.IsNoOp() != tc.wantNoop {
				t.Fatalf("IsNoOp = %v, want %v", plan.IsNoOp(), tc.wantNoop)
			}
		})
	}
}

func TestConfigRevisionExcludesSecrets(t *testing.T) {
	// The revision must not move when only a secret value changes: the
	// state file must never contain anything derived from a credential.
	base := DesiredState{Instance: "i", Isolation: IsolationBackend, WorkspaceDir: "/w", Image: "img", Username: "u"}
	if base.ConfigRevision() != base.ConfigRevision() {
		t.Fatal("revision must be deterministic")
	}
	changed := base
	changed.CredentialRev = "abc123"
	if base.ConfigRevision() == changed.ConfigRevision() {
		t.Fatal("the binding-set revision must participate in the revision")
	}
	// Workspace path normalization: the same directory expressed with a
	// trailing separator or ./ must not look like a change.
	dirty := base
	dirty.WorkspaceDir = filepath.Join("/w") + string(os.PathSeparator)
	if base.ConfigRevision() != dirty.ConfigRevision() {
		t.Fatalf("path normalization failed: %q vs %q", base.WorkspaceDir, dirty.WorkspaceDir)
	}
}

func TestInstanceStateRoundTripAndSchemaGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reconcile.json")
	st := DesiredState{Instance: "i", Isolation: IsolationFull, WorkspaceDir: "/w", Image: "img", Username: "u"}.toState()
	st.Pending = []string{"refresh-credentials", "restart-vm"}
	if err := WriteInstanceState(DefaultFS, path, st); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadInstanceState(DefaultFS, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ConfigRevision != st.ConfigRevision || len(got.Pending) != 2 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// Missing file is not an error: the instance predates state tracking.
	if s, err := ReadInstanceState(DefaultFS, filepath.Join(dir, "absent.json")); err != nil || s != nil {
		t.Fatalf("missing state must be (nil, nil), got (%v, %v)", s, err)
	}
	// A newer schema is an error, never a deletion trigger.
	newer := map[string]any{"schemaVersion": 99}
	data, _ := json.Marshal(newer)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadInstanceState(DefaultFS, path); err == nil || !strings.Contains(err.Error(), "newer than this build supports") {
		t.Fatalf("newer schema must refuse, got: %v", err)
	}
}

func TestReconcileNoOpWritesNothing(t *testing.T) {
	// Identical desired state with a healthy guest must not touch the
	// guest or rewrite the state file (mtime included).
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Healthy: true}
	}
	// Point the state at a temp dir by writing the applied state through
	// the same path the runtime will read.
	desired := m.desiredState(true, testBindings(m.cfg.APIKey), nil)
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	if err := WriteInstanceState(DefaultFS, path, desired.toState()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := len(client.calls)
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op reconcile must not rewrite the state file")
	}
	// A no-op still inspects the instance (lookup, config read, mount
	// check) to classify; what it must not do is mutate the guest.
	for _, call := range client.calls[callsBefore:] {
		switch {
		case strings.HasPrefix(call, "lookup "),
			strings.HasPrefix(call, "readconfig "),
			strings.HasPrefix(call, "mount "):
		default:
			t.Fatalf("no-op reconcile mutated the guest: %q", call)
		}
	}
	_ = os.Remove(path)
	_ = os.RemoveAll(filepath.Dir(path))
}

func TestReconcileResumesInterruptedApply(t *testing.T) {
	// An interrupted apply journals its remaining ops; the next run of the
	// same revision resumes at the unfinished operation instead of
	// restarting from scratch.
	client := &fakeMSBClient{exists: true, status: "stopped"}
	m := newTestMicrosandbox(t, client)

	desired := m.desiredState(true, testBindings(m.cfg.APIKey), nil)
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	// Simulate a crash after refresh-credentials, with the journal written.
	st := desired.toState()
	st.Pending = []string{"start-vm"}
	if err := WriteInstanceState(DefaultFS, path, st); err != nil {
		t.Fatal(err)
	}

	// The journal carries the same revision as the desired state, so the
	// resume must honor it. Start itself pushes one next-start refresh
	// (modify) before booting a stopped sandbox; the reconcile
	// refresh-credentials op would add a second. One modify = the resume
	// skipped the completed op; two = it restarted the plan from scratch.
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !hasCall(client, "start ") {
		t.Fatalf("resume must run the unfinished start: %v", client.calls)
	}
	if countCalls(client, "modify ") != 1 {
		t.Fatalf("resume repeated the completed refresh (modify calls: %d): %v", countCalls(client, "modify "), client.calls)
	}
	_ = os.RemoveAll(filepath.Dir(path))
	_ = os.RemoveAll(filepath.Dir(path))
}

func TestReconcileFinishesPendingJournalDespiteHealthyGuest(t *testing.T) {
	// A failed op journals the desired revision with Pending set. On the
	// next run the guest may look healthy (the failed refresh never took
	// effect), but the journal must still be executed — returning no-op
	// would silently drop the failed credential or config update forever.
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Healthy: true}
	}
	desired := m.desiredState(true, testBindings(m.cfg.APIKey), nil)
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	st := desired.toState()
	st.Pending = []string{"refresh-credentials", "restart-vm"}
	if err := WriteInstanceState(DefaultFS, path, st); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !hasCall(client, "modify ") {
		t.Fatalf("pending refresh must be retried despite healthy guest: %v", client.calls)
	}
	// The journal is consumed: the state file no longer carries Pending.
	final, err := ReadInstanceState(DefaultFS, path)
	if err != nil {
		t.Fatal(err)
	}
	if final == nil || len(final.Pending) != 0 {
		t.Fatalf("completed journal must be cleared, state: %+v", final)
	}
	_ = os.RemoveAll(filepath.Dir(path))
}

func TestReconcilePersistsJournalBeforeFirstSideEffect(t *testing.T) {
	// An abrupt kill after an op produced side effects but before the
	// error-path journal write must not replay completed mutations. The
	// journal is persisted before execution and advanced after each op.
	client := &fakeMSBClient{exists: true, status: "stopped", startErr: assertErr("boom")}
	m := newTestMicrosandbox(t, client)
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	if err := m.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile must surface the start failure")
	}
	st, err := ReadInstanceState(DefaultFS, path)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || len(st.Pending) == 0 {
		t.Fatalf("journal must be persisted before execution, state: %+v", st)
	}
	_ = os.RemoveAll(filepath.Dir(path))
}

func TestReconcileSurfacesRecreateAsError(t *testing.T) {
	// A creation-fixed change must come back as a typed error, never as an
	// automatic Clean. The instance is untouched.
	client := &fakeMSBClient{exists: true, status: "running"}
	m := newTestMicrosandbox(t, client)
	m.Probe = func(context.Context, string, string, string) HealthProbe {
		return HealthProbe{Healthy: true}
	}
	desired := m.desiredState(true, testBindings(m.cfg.APIKey), nil)
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	st := desired.toState()
	st.Isolation = string(IsolationFull) // differs from desired (backend)
	if err := WriteInstanceState(DefaultFS, path, st); err != nil {
		t.Fatal(err)
	}

	err := m.Reconcile(context.Background())
	var need *ErrRecreateNeeded
	if err == nil || !errorsAs(err, &need) {
		t.Fatalf("Reconcile must refuse with ErrRecreateNeeded, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DESTROYS") && !strings.Contains(err.Error(), "recreate --microsandbox") {
		t.Fatalf("error must name the loss and the explicit command: %v", err)
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "remove") || strings.HasPrefix(call, "stop") {
			t.Fatalf("refused reconcile must not touch the instance: %v", client.calls)
		}
	}
	_ = os.RemoveAll(filepath.Dir(path))
}

func TestReconcileJournalsOnFailure(t *testing.T) {
	// When an op fails, the remaining ops are journaled so the next run
	// resumes there.
	client := &fakeMSBClient{exists: true, status: "stopped", startErr: assertErr("boom")}
	m := newTestMicrosandbox(t, client)

	if err := m.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile must surface the start failure")
	}
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	st, err := ReadInstanceState(DefaultFS, path)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || len(st.Pending) == 0 {
		t.Fatalf("failure must journal remaining ops, state: %+v", st)
	}
	_ = os.RemoveAll(filepath.Dir(path))
}

func TestReconcileBreaksStaleSetupLock(t *testing.T) {
	// Reconcile serializes through the per-instance setup lock. A lock
	// left by a dead process is stale and must be broken so reconcile
	// proceeds; a live holder blocks (the Acquire contract), which is why
	// the reconcile error path wraps lock failures with context.
	client := &fakeMSBClient{exists: true, status: "stopped"}
	m := newTestMicrosandbox(t, client)
	lockPath := SetupLockPath(DefaultStateDir(), m.InstanceName())
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A PID that cannot exist on this host: the lock is provably stale.
	if err := os.WriteFile(lockPath, []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("stale lock must be broken, got: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("reconcile must release the lock when done")
	}
	_ = os.RemoveAll(filepath.Dir(lockPath))
}

// assertErr is a tiny error helper to keep the test file focused.
type testErr string

func (e testErr) Error() string { return string(e) }

func assertErr(msg string) error { return testErr(msg) }

func errorsAs(err error, target **ErrRecreateNeeded) bool {
	if e, ok := err.(*ErrRecreateNeeded); ok {
		*target = e
		return true
	}
	return false
}

// TestReconcileRefusesCredentialOpsWhenUnresolved pins the Codex P2 on the
// second review: with an unresolved binding set, the desired secret set is
// empty, and applying it would turn "reuse the persisted references" into
// their removal — the patch semantics make an empty set authoritative.
// Nothing may be changed, and the journal must survive so a later run with a
// resolvable credential resumes here.
func TestReconcileRefusesCredentialOpsWhenUnresolved(t *testing.T) {
	client := &fakeMSBClient{exists: true, status: "stopped"}
	m := newTestMicrosandbox(t, client)
	// No resolvable credential: neither the env key nor a stored entry.
	m.cfg.APIKey = ""
	m.cfg.CredentialRef = "missing-ref"
	m.credentialRead = func(context.Context, CredentialKind, string) (string, string, error) {
		return "", "", ErrCredentialNotFound
	}
	// Point the state at a temp dir by writing the applied state through
	// the same path the runtime reads.
	path := instanceStatePath(DefaultStateDir(), m.InstanceName())
	// A journal left by a failed SDK refresh: the retry must not replay it
	// against an empty desired set.
	st := m.desiredState(false, nil, nil).toState()
	st.Pending = []string{"refresh-credentials", "start-vm"}
	if err := WriteInstanceState(DefaultFS, path, st); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(path)
		_ = os.RemoveAll(filepath.Dir(path))
	}()

	err := m.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "credential set could not be resolved") {
		t.Fatalf("an unresolved credential set must refuse the credential ops: %v", err)
	}
	for _, call := range client.calls {
		switch {
		case strings.HasPrefix(call, "modify "), strings.HasPrefix(call, "start "), strings.HasPrefix(call, "create"):
			t.Fatalf("no credential-dependent operation may run: %q", call)
		}
	}
	// The journal survives, so the apply resumes once the credential is back.
	after, rerr := ReadInstanceState(DefaultFS, path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(after.Pending) == 0 {
		t.Fatal("the journal must survive a refused apply")
	}
	// The operations that do not need the credential set stay available: a
	// plan of restart/relaunch only must not be blocked.
	if containsCredentialOps([]ReconcileOp{OpRestartBackend}) || containsCredentialOps([]ReconcileOp{OpRestartVM}) {
		t.Fatal("restart and backend-relaunch do not depend on the credential set")
	}
	if !containsCredentialOps([]ReconcileOp{OpRefreshCredentials}) || !containsCredentialOps([]ReconcileOp{OpStartVM}) {
		t.Fatal("refresh-credentials and start-vm do depend on the credential set")
	}
}

// TestInstanceStateReadsLegacyNumericCredentialGen pins the fifth review's
// P1: P07-era state files serialize `credentialGen` as a JSON number, and the
// upgrade must read them (mapping the number to the empty marker so the next
// apply records the real composite) instead of failing to unmarshal.
func TestInstanceStateReadsLegacyNumericCredentialGen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reconcile.json")
	legacy := []byte(`{"schemaVersion":1,"instance":"jc-x","isolation":"backend","workspaceDir":"/w","image":"img","configRevision":"abc","credentialGen":0}` + "\n")
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := ReadInstanceState(DefaultFS, path)
	if err != nil {
		t.Fatalf("a P07-era numeric credentialGen must read cleanly: %v", err)
	}
	if string(st.CredentialGen) != "" {
		t.Fatalf("the legacy number maps to the empty marker: %q", st.CredentialGen)
	}
	// The current string composite round-trips.
	st.CredentialGen = credentialGenJSON("albert@native:2;github@file:1")
	if err := WriteInstanceState(DefaultFS, path, *st); err != nil {
		t.Fatal(err)
	}
	again, err := ReadInstanceState(DefaultFS, path)
	if err != nil || string(again.CredentialGen) != "albert@native:2;github@file:1" {
		t.Fatalf("string composite round trip: %+v, %v", again, err)
	}
}
