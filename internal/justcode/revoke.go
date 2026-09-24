package justcode

import (
	"context"
	"fmt"
	"sort"
)

// This file implements P09 revocation: removing a credential from the store
// must revoke the access it granted. For Microsandbox instances the proxy
// registration is dropped — live on a running sandbox (NoRestart), persisted
// for the next boot on a stopped one. Tart and agent-vm hand the plaintext
// credential to the guest, where any process may already have copied it, so
// removal while such an instance runs is refused outright: reporting success
// there would claim a revocation just-code cannot perform.

// RevokeReport describes what a revocation did, per instance.
type RevokeReport struct {
	// LiveRevoked lists running Microsandbox instances whose proxy
	// registration was dropped. Their guest environment keeps the dangling
	// placeholder until the next restart (see PlaceholderDangles).
	LiveRevoked []string
	// StoppedCleared lists stopped Microsandbox instances whose persisted
	// secret reference was removed; the next boot has no secret at all.
	StoppedCleared []string
	// SkippedEnv lists instances whose credential was environment-sourced
	// (per the persisted BoundCredentials record): just-code cannot revoke a
	// variable it does not own, so nothing was touched there.
	SkippedEnv []string
	// PlaceholderDangles is set when any live revocation ran: the guest
	// environment still holds the placeholder, which travels literally to
	// the formerly allowed host (the request fails server-side, but the
	// placeholder pattern is disclosed) until the instance restarts.
	PlaceholderDangles bool
}

// ErrRevocationBlocked refuses a removal that cannot take effect: the named
// running instances hold the plaintext credential (Tart/agent-vm transport),
// so deleting the stored copy would revoke nothing.
type ErrRevocationBlocked struct {
	Kind      CredentialKind
	Instances []string
}

func (e *ErrRevocationBlocked) Error() string {
	return fmt.Sprintf("credential %q is held in plaintext by running instances %v; "+
		"stop them first (just-code stop) — removing the stored credential cannot revoke a copy the guest already holds",
		e.Kind, e.Instances)
}

// Revoker carries the seams RevokeCredential needs, so tests can drive the
// flow without runtimes. The zero value is valid: production defaults are
// filled in (embedded SDK client, OS runners, default state dir).
type Revoker struct {
	MSB            msbClient
	TartRunning    func(ctx context.Context) ([]string, error)
	AgentVMRunning func(ctx context.Context) ([]string, error)
	StateDir       string
	FS             FS
}

func (r Revoker) withDefaults() Revoker {
	if r.MSB == nil {
		r.MSB = sdkMSBClient{}
	}
	if r.TartRunning == nil {
		t := NewTart(Config{})
		r.TartRunning = t.RunningInstances
	}
	if r.AgentVMRunning == nil {
		a := NewAgentVM(Config{})
		r.AgentVMRunning = a.RunningInstances
	}
	if r.StateDir == "" {
		r.StateDir = DefaultStateDir()
	}
	if r.FS == nil {
		r.FS = DefaultFS
	}
	return r
}

// RevokeCredential revokes kind everywhere just-code can reach, ahead of its
// removal from the store. See the file header for the per-runtime semantics.
func RevokeCredential(ctx context.Context, kind CredentialKind) (RevokeReport, error) {
	return (Revoker{}).withDefaults().Revoke(ctx, kind)
}

func (r Revoker) Revoke(ctx context.Context, kind CredentialKind) (RevokeReport, error) {
	var rep RevokeReport

	// Tart and agent-vm transport only the Albert credential, in plaintext.
	if kind == CredentialAlbert {
		var running []string
		for _, enumerate := range []func(context.Context) ([]string, error){r.TartRunning, r.AgentVMRunning} {
			vms, err := enumerate(ctx)
			if err != nil {
				if commandNotFound(err) {
					continue // runtime not installed on this host
				}
				return rep, err
			}
			running = append(running, vms...)
		}
		if len(running) > 0 {
			sort.Strings(running)
			return rep, &ErrRevocationBlocked{Kind: kind, Instances: running}
		}
	}

	binding, known := bindingForKind(kind)
	if !known {
		// A credential just-code never binds into a guest (e.g. an arbitrary
		// credentialRef target): removing it from the store is the whole
		// revocation.
		return rep, nil
	}

	sandboxes, err := r.enumerateSandboxes(ctx)
	if err != nil {
		return rep, err
	}
	for _, sb := range sandboxes {
		if !r.instanceBinds(kind, sb.Name) {
			rep.SkippedEnv = append(rep.SkippedEnv, sb.Name)
			continue
		}
		live := sb.Status == "running"
		if err := r.MSB.RemoveSecrets(ctx, sb.Name, []string{binding.GuestEnv}, live); err != nil {
			return rep, fmt.Errorf("revoking %s from %s: %w", kind, sb.Name, err)
		}
		if live {
			rep.LiveRevoked = append(rep.LiveRevoked, sb.Name)
			rep.PlaceholderDangles = true
		} else {
			rep.StoppedCleared = append(rep.StoppedCleared, sb.Name)
		}
	}
	return rep, nil
}

// enumerateSandboxes lists every Microsandbox sandbox revocation must reach:
// managed instances plus the legacy singleton (which predates the ownership
// label and is invisible to List).
func (r Revoker) enumerateSandboxes(ctx context.Context) ([]msbSandboxInfo, error) {
	seen := map[string]bool{}
	var out []msbSandboxInfo
	sandboxes, err := r.MSB.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, sb := range sandboxes {
		seen[sb.Name] = true
		out = append(out, sb)
	}
	if legacy, exists, err := r.MSB.Lookup(ctx, msbSandbox); err != nil {
		return nil, err
	} else if exists && !seen[legacy.Name] {
		out = append(out, legacy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// instanceBinds reports whether the instance's credential of the given kind
// came from the store at apply time. The persisted BoundCredentials record is
// the authority; a missing or unreadable record means "unknown", treated as
// bound — skipping revocation on a guess could leave a live binding behind.
func (r Revoker) instanceBinds(kind CredentialKind, instance string) bool {
	st, err := ReadInstanceState(r.FS, instanceStatePath(r.StateDir, instance))
	if err != nil || st == nil {
		return true
	}
	for _, k := range st.BoundCredentials {
		if k == string(kind) {
			return true
		}
	}
	return false
}

// RevokeInstanceBinding drops kind's proxy binding from a single Microsandbox
// instance: live when it is running, persisted for the next boot when it is
// stopped. It backs `bindings revoke`, which is project-scoped. revoked is
// false (with nil error) when the instance does not exist.
func RevokeInstanceBinding(ctx context.Context, instance string, kind CredentialKind) (revoked bool, live bool, err error) {
	binding, ok := bindingForKind(kind)
	if !ok {
		return false, false, fmt.Errorf("unknown credential kind %q", kind)
	}
	client := sdkMSBClient{}
	sb, exists, err := client.Lookup(ctx, instance)
	if err != nil || !exists {
		return false, false, err
	}
	live = sb.Status == "running"
	if err := client.RemoveSecrets(ctx, instance, []string{binding.GuestEnv}, live); err != nil {
		return false, false, fmt.Errorf("revoking %s from %s: %w", kind, instance, err)
	}
	return true, live, nil
}
