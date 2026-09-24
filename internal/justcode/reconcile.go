package justcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Reconciliation and non-destructive restart (P07). The goal: applying setup
// changes to an existing instance must not mean recreating it. Changes are
// classified into a small typed plan; identical desired state is a no-op that
// writes nothing; an interrupted apply resumes at the unfinished operation;
// and destruction is never a side effect — it is an explicit, named
// operation (Recreate).

// ReconcileOp is one operation of a reconciliation plan.
type ReconcileOp string

const (
	// OpNoOp: desired state is applied and the guest is healthy. Writes
	// nothing.
	OpNoOp ReconcileOp = "noop"
	// OpCreate: the instance does not exist; the full creation path runs.
	OpCreate ReconcileOp = "create"
	// OpRefreshCredentials: persist the proxied credential and server
	// credentials for the next boot. Idempotent.
	OpRefreshCredentials ReconcileOp = "refresh-credentials"
	// OpStartVM: boot a stopped instance.
	OpStartVM ReconcileOp = "start-vm"
	// OpRestartBackend: relaunch the in-guest backend process on a running
	// VM that is up but not serving.
	OpRestartBackend ReconcileOp = "restart-backend"
	// OpRestartVM: stop and start the VM. Non-destructive: disk and guest
	// state survive; this is how a running guest picks up refreshed
	// credentials without being recreated.
	OpRestartVM ReconcileOp = "restart-vm"
	// OpRecreate: creation-fixed attributes changed (isolation, mount,
	// image). Never executed automatically: the plan surfaces it and the
	// user runs the explicit recreate.
	OpRecreate ReconcileOp = "recreate"
)

// ReconcilePlan is the classified set of operations to apply.
type ReconcilePlan struct {
	Ops []ReconcileOp
	// Reason explains the classification, for logs and errors.
	Reason string
}

// IsNoOp reports whether the plan applies nothing.
func (p ReconcilePlan) IsNoOp() bool {
	return len(p.Ops) == 0 || (len(p.Ops) == 1 && p.Ops[0] == OpNoOp)
}

// NeedsRecreate reports whether the plan classifies the change as requiring
// (explicit) recreation.
func (p ReconcilePlan) NeedsRecreate() bool {
	for _, op := range p.Ops {
		if op == OpRecreate {
			return true
		}
	}
	return false
}

// credentialGenJSON reads the rotation marker from either its historical
// numeric form (P07 wrote `credentialGen: 0`) or the current string composite
// (P09). A decode failure in the old format would otherwise make every
// pre-existing state file unreadable, blocking reconciliation of instances
// the upgrade should simply refresh.
type credentialGenJSON string

func (g *credentialGenJSON) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		*g = ""
		return nil
	}
	// Numeric form (possibly quoted by a middle version): accept 0 and any
	// integer, mapping them to the empty marker so the next apply records
	// the real composite.
	if n, err := strconv.Atoi(strings.Trim(s, `"`)); err == nil {
		_ = n
		*g = ""
		return nil
	}
	return json.Unmarshal(data, (*string)(g))
}

func (g credentialGenJSON) MarshalJSON() ([]byte, error) {
	if g == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(g))
}

// InstanceState is the persisted desired/applied record of one instance
// (P07). It lives in host state, never in the workspace, and never carries
// credential values: the config revision is a hash of non-secret fields, and
// credentials are tracked only through the binding-set revision (P09) — a
// hash of kinds, sources, guest variables and allowed hosts — plus the list
// of store-bound kinds, which revocation uses to decide what it can reach.
type InstanceState struct {
	SchemaVersion int `json:"schemaVersion"`
	// Instance is the instance this state belongs to.
	Instance string `json:"instance"`
	// Isolation and Image are the creation-fixed attributes the instance was
	// created with. A change classifies as recreate.
	//
	// The host workspace path is deliberately NOT recorded here: with the
	// sealed workspace (P22) the sandbox holds no reference to it — the
	// directory is only the transfer source, read at sync time — so changing
	// it must not look like a difference the sandbox was created with.
	Isolation string `json:"isolation"`
	Image     string `json:"image"`
	// ConfigRevision is the hash of the non-secret desired config that was
	// applied. Identical revision + healthy guest = no-op.
	ConfigRevision string `json:"configRevision"`
	// CredentialRev is the hash of the applied binding-set descriptor (P09).
	// A value rotation within an unchanged set does not move it: the secret
	// reference re-resolves at the next boot.
	CredentialRev string `json:"credentialRev,omitempty"`
	// CredentialGen is the non-secret rotation marker applied (P09): the
	// composite store generation of the binding set at apply time. A value
	// rotation bumps it, which is the only signal a rotation has on a
	// healthy running instance. P07-era state wrote it as a JSON number
	// (always 0); both forms are accepted on read.
	CredentialGen credentialGenJSON `json:"credentialGen,omitempty"`
	// BoundCredentials lists the credential kinds that were store-sourced at
	// apply time, sorted. Revocation (auth remove) uses it to skip instances
	// whose credential came from the environment, which just-code cannot
	// revoke. A missing state file means "unknown", treated as bound.
	BoundCredentials []string `json:"boundCredentials,omitempty"`
	// Pending journals the operations of the current apply that have not
	// completed yet, so an interrupted apply resumes instead of restarting
	// from scratch or silently skipping.
	Pending []string `json:"pending,omitempty"`
}

const instanceStateSchemaVersion = 1

// maxSupportedInstanceStateSchema is the newest state schema this build can
// read. A newer schema is an error, never a trigger for deletion: unknown
// persisted state must not destroy anything.
const maxSupportedInstanceStateSchema = 1

// DesiredState is the configuration just-code wants the instance to have.
type DesiredState struct {
	Instance  string
	Isolation Isolation
	Image     string
	// CredentialRev is the hash of the desired binding-set descriptor (P09).
	CredentialRev string
	// CredentialGeneration is the non-secret rotation marker of the stored
	// credential (P09): a host-side counter bumped by every `auth add`. The
	// binding-set revision is unchanged by a pure value rotation (the
	// reference re-resolves at boot), so without this marker a rotation on a
	// healthy running instance would take the no-op path and keep serving
	// the old credential.
	CredentialGeneration string
	// BoundCredentials lists the store-sourced credential kinds (P09).
	BoundCredentials []string
	// Non-secret server credentials participate in the revision.
	Username string
}

// ConfigRevision hashes the non-secret configuration. Credential values
// (API key, server password) are deliberately excluded: the state file must
// not contain anything derived from a secret. The binding-set revision is a
// hash of metadata, so it participates.
func (d DesiredState) ConfigRevision() string {
	h := sha256.New()
	for _, part := range []string{
		"jc-state-v1",
		d.Instance,
		string(d.Isolation),
		d.Image,
		d.Username,
		"rev:" + d.CredentialRev,
		"gen:" + d.CredentialGeneration,
	} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ReconcileFacts is what the runtime observed about the instance when the
// plan was requested. The plan function itself stays pure: all interaction
// with the guest happens in the executor.
type ReconcileFacts struct {
	// Exists: the instance is known to the runtime.
	Exists bool
	// Running: the VM is up.
	Running bool
	// Healthy: the backend answers its health endpoint (backend mode).
	Healthy bool
	// CreationFixedChanged: isolation, mount or image differ from what the
	// instance was created with.
	CreationFixedChanged bool
	// CredentialChanged: the desired binding-set revision differs from the
	// applied one (a value rotation within an unchanged set does NOT set it:
	// the reference re-resolves at the next boot).
	CredentialChanged bool
}

// PlanReconcile classifies the desired change into operations. It is pure:
// same inputs, same plan, no guest interaction. The classification rules:
//
//   - missing instance -> create
//   - creation-fixed attribute changed -> recreate (explicit, never auto)
//   - credential change -> refresh credentials; a running VM additionally
//     restarts (non-destructively) so the change is effective, not just
//     promised for the next boot
//   - identical revision: healthy running guest -> no-op; running but not
//     serving -> restart backend; stopped -> start
func PlanReconcile(applied *InstanceState, desired DesiredState, facts ReconcileFacts) ReconcilePlan {
	if !facts.Exists {
		return ReconcilePlan{Ops: []ReconcileOp{OpCreate}, Reason: "instance does not exist"}
	}
	if facts.CreationFixedChanged {
		return ReconcilePlan{
			Ops:    []ReconcileOp{OpRecreate},
			Reason: "isolation, workspace mount or image changed; these are fixed at creation",
		}
	}
	if applied == nil {
		// The instance exists but predates state tracking. Its creation-fixed
		// attributes match the facts (no change detected), so the safe
		// classification is a full non-destructive refresh.
		if facts.Running {
			return ReconcilePlan{Ops: []ReconcileOp{OpRefreshCredentials, OpRestartVM}, Reason: "instance predates reconciliation state; refreshing"}
		}
		return ReconcilePlan{Ops: []ReconcileOp{OpRefreshCredentials, OpStartVM}, Reason: "instance predates reconciliation state; refreshing for the next boot"}
	}
	revChanged := applied.ConfigRevision != desired.ConfigRevision()
	if revChanged || facts.CredentialChanged {
		if facts.Running {
			return ReconcilePlan{
				Ops:    []ReconcileOp{OpRefreshCredentials, OpRestartVM},
				Reason: "configuration changed; restarting the VM (non-destructive) to apply it",
			}
		}
		return ReconcilePlan{
			Ops:    []ReconcileOp{OpRefreshCredentials, OpStartVM},
			Reason: "configuration changed; refreshing for the next boot",
		}
	}
	switch {
	case facts.Running && facts.Healthy:
		return ReconcilePlan{Ops: []ReconcileOp{OpNoOp}, Reason: "desired state already applied"}
	case facts.Running:
		return ReconcilePlan{Ops: []ReconcileOp{OpRestartBackend}, Reason: "VM is up but the backend is not serving"}
	default:
		return ReconcilePlan{Ops: []ReconcileOp{OpStartVM}, Reason: "instance is stopped"}
	}
}

// ErrRecreateNeeded is the typed refusal returned when a change classifies
// as recreation. It carries the loss summary; the caller surfaces it and the
// user decides. No code path may turn this into an automatic Clean.
type ErrRecreateNeeded struct {
	Instance string
	Reason   string
}

func (e *ErrRecreateNeeded) Error() string {
	return fmt.Sprintf("%s cannot apply this change in place: %s. "+
		"Recreating is destructive and loses guest sessions, tools installed in the guest, and guest-only files; "+
		"run 'just-code recreate --microsandbox' if that is acceptable",
		e.Instance, e.Reason)
}

// instanceStatePath is the per-instance reconcile state file, in host state.
func instanceStatePath(stateDir, instance string) string {
	return filepath.Join(InstanceStateDir(stateDir, instance), "reconcile.json")
}

// ReadInstanceState loads the persisted state. A missing file is not an
// error: it returns (nil, nil), meaning the instance predates state
// tracking. A newer schema is an error — never a deletion trigger.
func ReadInstanceState(fs FS, path string) (*InstanceState, error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var st InstanceState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("instance state %s: %w", path, err)
	}
	if st.SchemaVersion > maxSupportedInstanceStateSchema {
		return nil, fmt.Errorf("instance state %s: schemaVersion %d is newer than this build supports (%d); "+
			"refusing to touch the instance rather than guess at unknown state", path, st.SchemaVersion, maxSupportedInstanceStateSchema)
	}
	return &st, nil
}

// WriteInstanceState atomically persists the state, with a sorted pending
// list for stable diffs.
func WriteInstanceState(fs FS, path string, st InstanceState) error {
	st.SchemaVersion = instanceStateSchemaVersion
	pending := append([]string(nil), st.Pending...)
	sort.Strings(pending)
	st.Pending = pending
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := fs.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWrite(fs, path, append(data, '\n'), 0o600)
}

// Reconcile applies the desired configuration to this backend's instance
// through the typed plan, journaling progress so an interruption resumes.
// It is the P07 apply mechanism; the destructive path (Recreate) is never
// taken implicitly — a recreate classification is surfaced as an error for
// the user to act on.
func (m *MicrosandboxRuntime) Reconcile(ctx context.Context) error {
	stateDir := DefaultStateDir()
	lock := &ProjectLock{Path: SetupLockPath(stateDir, m.InstanceName()), FS: DefaultFS}
	release, err := lock.Acquire()
	if err != nil {
		return fmt.Errorf("another just-code setup is running for %s: %w", m.InstanceName(), err)
	}
	defer release()

	if err := m.validateConfig(); err != nil {
		return err
	}
	// Resolve the binding set once for the whole apply: the desired state,
	// the persisted BoundCredentials record, and the refresh operation all
	// derive from it. Values travel only through withHostSecrets below.
	// A resolution failure must not block the apply: an instance that only
	// needs a restart (or a stop/clean) must not become unusable because a
	// credential is missing. The set is then recorded as unresolved rather
	// than empty, so revocation cannot mistake it for "nothing was bound".
	bindings, err := m.resolveBindings(ctx)
	resolved := err == nil
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot resolve the credentials of %s (%v); reusing the persisted secret references. Store the credential and restart to bind it.\n", m.InstanceName(), err)
	}
	if err := os.MkdirAll(m.cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	// The scan is host hygiene advice, not a gate: with the sealed workspace
	// (P22) the checkout is not mounted, so a secret in it cannot reach the
	// guest. The transfer filter is the enforceable boundary.
	warnWorkspaceHygiene(ctx, m.cfg.WorkspaceDir)

	path := instanceStatePath(stateDir, m.InstanceName())
	applied, err := ReadInstanceState(DefaultFS, path)
	if err != nil {
		return err
	}
	desired := m.desiredState(resolved, bindings, applied)
	facts, err := m.reconcileFacts(ctx, applied, desired)
	if err != nil {
		return err
	}
	plan := PlanReconcile(applied, desired, facts)
	if plan.NeedsRecreate() {
		return &ErrRecreateNeeded{Instance: m.InstanceName(), Reason: plan.Reason}
	}

	// Resume support: a journal of pending ops from an interrupted run of
	// the same revision is honored; a journal from a different revision is
	// stale and replaced by the fresh plan. This check precedes the no-op
	// return on purpose: a failed op journals the desired revision with
	// Pending set, and the next run must finish that journal even when the
	// guest currently looks healthy — otherwise a failed credential or
	// configuration update is never retried.
	pending := plan.Ops
	if applied != nil && applied.ConfigRevision == desired.ConfigRevision() && len(applied.Pending) > 0 {
		// The journal is authoritative for this revision: a fresh plan of
		// [noop] must not override it, or a failed update is never retried.
		pending = journalToOps(applied.Pending)
		fmt.Printf("Resuming interrupted apply for %s at: %s\n", m.InstanceName(), joinOps(pending))
	} else if plan.IsNoOp() {
		return nil
	}

	// A credential-dependent operation cannot run on an unresolved binding
	// set. Deriving the desired secret set from nothing and applying it would
	// turn "reuse the persisted references" into their removal — the patch
	// semantics make the empty set authoritative. Stop before the first side
	// effect, with the journal intact, so a later run with a resolvable
	// credential resumes here.
	if !resolved && containsCredentialOps(pending) {
		return fmt.Errorf("cannot apply the credential operations of %s (%s): the credential set could not be resolved (%v). "+
			"Store or restore the credential, then retry; nothing was changed",
			m.InstanceName(), joinOps(pending), err)
	}

	// Persist the journal before the first side effect and advance it after
	// each successful operation: an abrupt kill between ops must leave a
	// progress record, or the next run would replay completed mutations.
	// The whole apply runs under withHostSecrets so the SDK's env references
	// (refresh-credentials, and the Start paths that re-resolve at boot)
	// find their transport variables without any value being persisted.
	st := desired.toState()
	st.Pending = opsToJournal(pending)
	if err := WriteInstanceState(DefaultFS, path, st); err != nil {
		return fmt.Errorf("persisting reconcile journal for %s: %w", m.InstanceName(), err)
	}
	return withHostSecrets(bindings, func() error {
		for i, op := range pending {
			if err := m.applyReconcileOp(ctx, op, bindings); err != nil {
				// Journal the remaining ops: the next run resumes here.
				st.Pending = opsToJournal(pending[i:])
				_ = WriteInstanceState(DefaultFS, path, st)
				return fmt.Errorf("reconcile %s failed at %s (will resume there): %w", m.InstanceName(), op, err)
			}
			if i < len(pending)-1 {
				st.Pending = opsToJournal(pending[i+1:])
				if err := WriteInstanceState(DefaultFS, path, st); err != nil {
					return fmt.Errorf("advancing reconcile journal for %s: %w", m.InstanceName(), err)
				}
			}
		}
		st.Pending = nil
		return WriteInstanceState(DefaultFS, path, st)
	})
}

// desiredState builds the desired record for an apply. When the binding set
// could not be resolved, the credential fields are carried over from the
// applied state rather than computed from an empty set: the bound-entry
// markers must survive so a later revocation still knows what the instance
// resolved from, and the revision must not appear to change for a reason that
// is only a missing credential. The apply itself refuses the
// credential-dependent operations in that case (see Reconcile), so carrying
// the revision cannot leave a stale refresh unperformed.
func (m *MicrosandboxRuntime) desiredState(resolved bool, bindings []resolvedBinding, applied *InstanceState) DesiredState {
	d := DesiredState{
		Instance:  m.InstanceName(),
		Isolation: m.cfg.Isolation,
		Image:     msbImage,
		Username:  m.cfg.Username,
	}
	if resolved {
		d.CredentialRev = bindingsRevision(bindings)
		d.BoundCredentials = storeBoundEntries(bindings)
		d.CredentialGeneration = storeGenerationOf(DefaultFS, DefaultStateDir(), bindings)
		return d
	}
	if applied != nil {
		d.CredentialRev = applied.CredentialRev
		d.CredentialGeneration = string(applied.CredentialGen)
		d.BoundCredentials = append([]string(nil), applied.BoundCredentials...)
	} else {
		// Nothing applied to carry over, but an earlier apply outside the
		// journal may still have left a store-bound secret behind: record
		// the unknown marker so revocation does not skip this instance.
		d.BoundCredentials = []string{pendingBoundEntry}
	}
	return d
}

func (d DesiredState) toState() InstanceState {
	return InstanceState{
		SchemaVersion:    instanceStateSchemaVersion,
		Instance:         d.Instance,
		Isolation:        string(d.Isolation),
		Image:            d.Image,
		ConfigRevision:   d.ConfigRevision(),
		CredentialRev:    d.CredentialRev,
		CredentialGen:    credentialGenJSON(d.CredentialGeneration),
		BoundCredentials: append([]string(nil), d.BoundCredentials...),
	}
}

// reconcileFacts gathers what the plan needs from the guest. All guest
// interaction lives here and in applyReconcileOp; the plan itself is pure.
func (m *MicrosandboxRuntime) reconcileFacts(ctx context.Context, applied *InstanceState, desired DesiredState) (ReconcileFacts, error) {
	// The binding-set revision travels inside the config revision, so when
	// the applied revision matches the desired one the credential set is by
	// definition unchanged; without this, every reconcile would take the
	// refresh+restart path and a true no-op would be unreachable.
	facts := ReconcileFacts{CredentialChanged: applied == nil || applied.ConfigRevision != desired.ConfigRevision()}
	sandbox, exists, err := m.Client.Lookup(ctx, m.InstanceName())
	if err != nil {
		return facts, err
	}
	facts.Exists = exists
	if !exists {
		return facts, nil
	}
	facts.Running = sandbox.Status == "running"
	facts.Healthy = facts.Running && m.backendHealthy(ctx)

	// Creation-fixed comparison. The applied state is the authority when it
	// exists; without it, the live checks (isolation script, mount) stand in.
	if applied != nil {
		creationFixed := applied.Isolation != string(desired.Isolation) ||
			applied.Image != desired.Image
		if creationFixed {
			facts.CreationFixedChanged = true
			return facts, nil
		}
	}
	if err := m.rejectIsolationMismatch(ctx, sandbox); err != nil {
		facts.CreationFixedChanged = true
		return facts, nil
	}
	if m.workspaceProvenanceChanged(ctx) {
		facts.CreationFixedChanged = true
	}
	return facts, nil
}

// workspaceProvenanceChanged reports whether the instance's workspace
// provenance differs from the sealed model (P22). A /workspace backed by a
// host path is the pre-P22 bind-mount instance: the guest can read the host
// checkout, and no in-place change fixes that, so reconciliation must treat
// the instance as requiring recreation.
//
// The old check compared the mounted *path* with the configured workspace,
// which asked the wrong question: a mount pointing at the right directory is
// still a host mount, and a mount pointing elsewhere is not the problem the
// sealed model exists to remove.
func (m *MicrosandboxRuntime) workspaceProvenanceChanged(ctx context.Context) bool {
	mounted, err := m.Client.WorkspaceMount(ctx, m.InstanceName())
	if err != nil {
		// Unreadable provenance cannot be proven sealed: require recreation
		// rather than assuming the safer answer.
		return true
	}
	if mounted != "" {
		return true
	}
	// No recognized bind source is not proof of a sealed workspace. Sharing
	// the guard's question here keeps the plan consistent: a shape the
	// runtime does not recognize as owned must classify as recreate, or
	// Reconcile would plan a refresh and then fail mid-apply in Start.
	owned, err := m.Client.WorkspaceOwned(ctx, m.InstanceName())
	if err != nil {
		return true
	}
	return !owned
}

// applyReconcileOp executes one plan operation. Every operation is
// idempotent, which is what makes the journal safe to replay.
func (m *MicrosandboxRuntime) applyReconcileOp(ctx context.Context, op ReconcileOp, bindings []resolvedBinding) error {
	switch op {
	case OpCreate, OpStartVM:
		return m.Start(ctx)
	case OpRefreshCredentials:
		return m.Client.ModifyNextStart(ctx, m.InstanceName(), m.nextStartEnv(), bindingsMetadata(bindings))
	case OpRestartBackend:
		return m.launchBackend(ctx)
	case OpRestartVM:
		if err := m.Stop(ctx); err != nil {
			return err
		}
		return m.Start(ctx)
	default:
		return fmt.Errorf("unsupported reconcile op %q", op)
	}
}

// containsCredentialOps reports whether a plan depends on the resolved
// credential set. OpRefreshCredentials persists the secret references;
// OpCreate and OpStartVM resolve them while starting. The restart and
// backend-relaunch operations do not (their boots re-resolve whatever the
// persisted references name), so an unresolved set can still serve them.
func containsCredentialOps(ops []ReconcileOp) bool {
	for _, op := range ops {
		switch op {
		case OpRefreshCredentials, OpCreate, OpStartVM:
			return true
		}
	}
	return false
}

func opsToJournal(ops []ReconcileOp) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op))
	}
	return out
}

func journalToOps(journal []string) []ReconcileOp {
	out := make([]ReconcileOp, 0, len(journal))
	for _, s := range journal {
		out = append(out, ReconcileOp(s))
	}
	return out
}

func sameOps(a, b []ReconcileOp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func joinOps(ops []ReconcileOp) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, string(op))
	}
	return fmt.Sprint(parts)
}

// Recreate is the explicit, destructive rebuild of the instance. It names
// the loss before and after. It is the only path that may invoke the
// destructive Clean, and no reconcile plan reaches it implicitly
// (NeedsRecreate is surfaced as an error, never executed).
func (m *MicrosandboxRuntime) Recreate(ctx context.Context) error {
	lossSummary := "guest sessions, tools installed in the guest, and guest-only files"
	fmt.Printf("Recreating %s. This DESTROYS: %s.\n", m.InstanceName(), lossSummary)
	if err := m.validateConfig(); err != nil {
		return err
	}
	// Resolve credentials before the destructive Clean: a resolution failure
	// must not destroy a sandbox that Start would then refuse to recreate.
	if _, err := m.resolveBindings(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(m.cfg.WorkspaceDir, 0o755); err != nil {
		return err
	}
	warnWorkspaceHygiene(ctx, m.cfg.WorkspaceDir)
	if err := m.Clean(ctx); err != nil {
		return err
	}
	if err := m.Start(ctx); err != nil {
		return err
	}
	// The recreated instance is a new creation: drop any stale journal.
	_ = DefaultFS.Remove(instanceStatePath(DefaultStateDir(), m.InstanceName()))
	fmt.Printf("%s recreated.\n", m.InstanceName())
	return nil
}
