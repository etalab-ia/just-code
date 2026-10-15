package justcode

import (
	"context"
	"strings"
	"testing"
)

// The composition the start path runs: approvals -> resolved value -> file in
// the guest -> inventory. The earlier phases each had a bug that lived exactly
// here, in the seam between a tested unit and the path that calls it.

func TestStartPathDeliversGuestReadableValue(t *testing.T) {
	m, client := newGuestFSRuntime(t, nil)
	store := newProjectSecretFakeStore()
	s := projectSecretFixture(t, m.cfg.WorkspaceDir, m.StateDir, store)
	m.ProjectSecrets = s
	importProjectSecrets(t, s, "development", ".env", map[string]string{"DB_PASSWORD": "local-secret", "API_KEY": "remote-secret"})
	if err := SetProjectSecretActiveProfile(DefaultFS, m.StateDir, s.Namespace(), "development"); err != nil {
		t.Fatal(err)
	}
	if err := ApproveProjectSecretBinding(DefaultFS, m.StateDir, s.Namespace(), ProjectSecretBinding{
		Profile: "development", Component: ".env", Name: "DB_PASSWORD", Delivery: ProjectSecretDeliveryGuest,
	}); err != nil {
		t.Fatal(err)
	}

	// The runtime is invoked the way the start path invokes it, with the
	// bindings it resolved.
	m.cfg.APIKey = ""
	m.credentialRead = stubReader("stored")
	bindings, err := m.resolveBindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.syncProjectSecretDelivery(context.Background(), bindings); err != nil {
		t.Fatal(err)
	}

	guestPath := msbGuestWorkspace + "/.env"
	content, ok := client.files[guestPath]
	if !ok {
		t.Fatalf("the delivered file is missing: %v", client.files)
	}
	if !strings.Contains(content, "DB_PASSWORD='local-secret'\n") {
		t.Fatalf("the real value must be delivered: %q", content)
	}
	if mode := client.modes[guestPath]; mode != 0o600 {
		t.Fatalf("the delivered file must be 0600, got %v", mode)
	}
	// A guest-readable binding contributes no proxy variable, so the applied
	// inventory must not claim one: that inventory is what a later revocation
	// unregisters.
	applied, err := ReadProjectSecretApplied(DefaultFS, m.StateDir, s.Namespace())
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Envs) != 0 {
		t.Fatalf("no proxy variable may be recorded for a guest-readable value: %v", applied.Envs)
	}
	if len(applied.Files) != 1 || applied.Files[0] != guestPath {
		t.Fatalf("the delivered file must be recorded: %v", applied.Files)
	}

	// Withdrawing the approval removes the delivered file at the next apply.
	if _, err := RevokeProjectSecretBinding(DefaultFS, m.StateDir, s.Namespace(), "development", ".env", "DB_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	bindings, err = m.resolveBindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.syncProjectSecretDelivery(context.Background(), bindings); err != nil {
		t.Fatal(err)
	}
	if _, still := client.files[guestPath]; still {
		t.Fatal("revoking the approval must remove the delivered value")
	}
}
