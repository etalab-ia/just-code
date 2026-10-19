package justcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stubKindStore is a CredentialStore whose only interesting property is the
// Kind() it reports, which is what store selection keys on.
type stubKindStore struct{ kind string }

func (s *stubKindStore) Kind() string { return s.kind }
func (s *stubKindStore) Put(context.Context, CredentialKind, string) error {
	return nil
}
func (s *stubKindStore) Get(context.Context, CredentialKind) (string, error) {
	return "", ErrCredentialNotFound
}
func (s *stubKindStore) Remove(context.Context, CredentialKind) error { return nil }
func (s *stubKindStore) Verify(context.Context) error                 { return nil }
func (s *stubKindStore) Generation(context.Context, CredentialKind) (string, error) {
	return "", nil
}

// writeRecordedStoreKind writes an index.json recording the given store kind,
// exactly as an import would leave behind.
func writeRecordedStoreKind(t *testing.T, root, state, kind string) string {
	t.Helper()
	_, namespace, err := projectSecretScope(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "project-secrets", namespace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	idx := projectSecretIndex{
		SchemaVersion: 1,
		Namespace:     namespace,
		Store:         kind,
		Generation:    strings.Repeat("a", 32),
	}
	raw, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return namespace
}

// TestRecordedProjectSecretStoreKindReadsTheIndex is the cheap half of the
// fix: the store kind an import recorded must be readable without a store.
func TestRecordedProjectSecretStoreKindReadsTheIndex(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	namespace := writeRecordedStoreKind(t, root, state, "file")
	if got := RecordedProjectSecretStoreKind(state, namespace); got != "file" {
		t.Fatalf("RecordedProjectSecretStoreKind = %q, want %q", got, "file")
	}
}

// TestRecordedProjectSecretStoreKindAbsent covers the no-index case: a project
// that never imported anything records nothing, and selection falls back to
// the platform default rather than inventing a store kind.
func TestRecordedProjectSecretStoreKindAbsent(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	_, namespace, err := projectSecretScope(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := RecordedProjectSecretStoreKind(state, namespace); got != "" {
		t.Fatalf("RecordedProjectSecretStoreKind = %q, want empty", got)
	}
}

// TestProjectSecretStoreForHonorsRecordedFileStore is the #199 regression.
//
// On a host where the values were imported into the consented file store, the
// runtime's project secret store must be built on that store. Selection used to
// come from a fresh DefaultCredentialStore() probe, which on Linux always
// returns the Secret Service adapter — so the file store was never consulted
// and every start failed with a spurious index/store mismatch.
func TestProjectSecretStoreForHonorsRecordedFileStore(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	writeRecordedStoreKind(t, root, state, "file")

	store, err := projectSecretStoreFor(root, state)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Kind(); got != "file" {
		t.Fatalf("project secret store kind = %q, want %q (recorded kind was ignored)", got, "file")
	}
}

// TestPreferRecordedCredentialStoreKeepsMatchingStore pins the no-op path: a
// store that already matches the record is returned unchanged, so nothing is
// rebuilt when the flag and the record agree.
func TestPreferRecordedCredentialStoreKeepsMatchingStore(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	writeRecordedStoreKind(t, root, state, "matching-kind")
	active := &stubKindStore{kind: "matching-kind"}

	got, err := PreferRecordedCredentialStore(active, root, state)
	if err != nil {
		t.Fatal(err)
	}
	if got != CredentialStore(active) {
		t.Fatalf("matching store was replaced: got kind %q", got.Kind())
	}
}

// TestPreferRecordedCredentialStoreSwapsMismatchedStore pins the swap: a store
// that does not match the record loses to the recorded kind, which is what
// makes a --fallback-less `secrets bindings list` work on a headless host.
func TestPreferRecordedCredentialStoreSwapsMismatchedStore(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	writeRecordedStoreKind(t, root, state, "file")
	active := &stubKindStore{kind: "secret-service"}

	got, err := PreferRecordedCredentialStore(active, root, state)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != "file" {
		t.Fatalf("kind = %q, want file (recorded kind must win over the probe)", got.Kind())
	}
}

// TestPreferRecordedCredentialStoreResolvesWithNoActiveStore covers the
// no-native-adapter case: with no active store and no record, the platform
// default is resolved (or a clear error is returned), never a nil store.
func TestPreferRecordedCredentialStoreResolvesWithNoActiveStore(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	got, err := PreferRecordedCredentialStore(nil, root, state)
	if err != nil {
		// A platform with no native adapter and no record is a legitimate
		// error; it must name the recovery rather than fail obscurely.
		if !strings.Contains(err.Error(), "--fallback") {
			t.Fatalf("error must name the recovery path: %v", err)
		}
		return
	}
	want := "secret-service"
	switch runtime.GOOS {
	case "darwin":
		want = "keychain"
	case "windows":
		want = "credential-manager"
	}
	if got.Kind() != want {
		t.Fatalf("kind = %q, want %q", got.Kind(), want)
	}
}

// TestCredentialStoreForKindRejectsUnknownKind pins the fail-closed branch: an
// index naming a store this build does not know must error, not silently read
// somewhere else.
func TestCredentialStoreForKindRejectsUnknownKind(t *testing.T) {
	if _, err := credentialStoreForKind("gpg-agent"); err == nil {
		t.Fatal("unknown recorded store kind must be refused")
	}
}
