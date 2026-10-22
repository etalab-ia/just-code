package justcode

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecordCredentialStoreRoundTrips pins the record itself: a write records
// the store kind, a read returns it, and nothing about the value is stored.
func TestRecordCredentialStoreRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if got := RecordedCredentialStoreForKind(dir, CredentialAlbert); got != "" {
		t.Fatalf("no record yet, got %q", got)
	}
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	if got := RecordedCredentialStoreForKind(dir, CredentialAlbert); got != "file" {
		t.Fatalf("recorded store = %q, want file", got)
	}
	// A second kind is independent: recording one must not answer for another.
	if got := RecordedCredentialStoreForKind(dir, CredentialGithub); got != "" {
		t.Fatalf("github must have no record, got %q", got)
	}
	// The record is host state and must be owner-only: it names stores, and a
	// world-readable state file invites a reader to assume it holds more.
	raw, err := DefaultFS.ReadFile(filepath.Join(dir, "credential-stores.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"value", "secret", "key"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Fatalf("record mentions %q; it must be store kinds only: %s", forbidden, raw)
		}
	}
}

// TestRecordCredentialStoreRepointsOnRewrites: storing natively after a fallback
// write must repoint the record, otherwise reads stay pinned to the older file
// value while the user believes they rotated the credential.
func TestRecordCredentialStoreRepointsOnRewrites(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	if err := RecordCredentialStore(dir, CredentialAlbert, "secret-service"); err != nil {
		t.Fatal(err)
	}
	if got := RecordedCredentialStoreForKind(dir, CredentialAlbert); got != "secret-service" {
		t.Fatalf("recorded store = %q, want secret-service", got)
	}
	if recordedFileStorePin(dir, CredentialAlbert) {
		t.Fatal("a native record must not pin the file store")
	}
}

// TestForgetCredentialStoreIsScopedToTheEditedStore: removing from one store
// must not erase the record of a value the other store still holds.
func TestForgetCredentialStoreIsScopedToTheEditedStore(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	// Removing the native entry does not touch the file value.
	if err := ForgetCredentialStore(dir, CredentialAlbert, "secret-service"); err != nil {
		t.Fatal(err)
	}
	if got := RecordedCredentialStoreForKind(dir, CredentialAlbert); got != "file" {
		t.Fatalf("record = %q, want file to survive an unrelated removal", got)
	}
	// Removing from the recorded store drops it.
	if err := ForgetCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	if got := RecordedCredentialStoreForKind(dir, CredentialAlbert); got != "" {
		t.Fatalf("record = %q, want empty after removing from its own store", got)
	}
}

// TestReadStoredCredentialHonoursRecordedFileStore is the #201 case: on a host
// whose native adapter exists but cannot answer (no D-Bus session), a value the
// user stored with --fallback must still be read. The native reader is never
// called, which is the assertion: probing it would produce the denied error that
// blocked the start.
func TestReadStoredCredentialHonoursRecordedFileStore(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	nativeCalled := false
	readers := credentialStoreReaders{
		native: func(context.Context, CredentialKind) (string, error) {
			nativeCalled = true
			return "", storeErrorf("get", "secret-service", "denied", "exit 1: secret-tool: Cannot autolaunch D-Bus without X11 $DISPLAY")
		},
		file: func(context.Context, CredentialKind) (string, error) { return "file-value", nil },
	}
	v, store, err := readStoredCredentialIn(context.Background(), CredentialAlbert, dir, readers)
	if err != nil {
		t.Fatalf("a recorded file store must answer without the native probe: %v", err)
	}
	if v != "file-value" || store != "file" {
		t.Fatalf("read = %q from %q, want file-value from file", v, store)
	}
	if nativeCalled {
		t.Fatal("the native store was probed despite a recorded file store")
	}
}

// TestReadStoredCredentialIgnoresNonFileRecords: recording a native kind must
// change nothing, because the native store already leads the documented chain.
// Pinning it would turn a not-found into a hard error where falling through to
// the consented file remains the documented outcome.
func TestReadStoredCredentialIgnoresNonFileRecords(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "keychain"); err != nil {
		t.Fatal(err)
	}
	readers := credentialStoreReaders{
		native: func(context.Context, CredentialKind) (string, error) { return "", ErrCredentialNotFound },
		file:   func(context.Context, CredentialKind) (string, error) { return "file-value", nil },
	}
	v, store, err := readStoredCredentialIn(context.Background(), CredentialAlbert, dir, readers)
	if err != nil || v != "file-value" || store != "file" {
		t.Fatalf("a native record must keep the documented chain: %q, %q, %v", v, store, err)
	}
}

// TestReadStoredCredentialWithoutRecordKeepsTheContract: with nothing recorded,
// a native store that answers denied/locked/unavailable stays a hard error, even
// though the fallback file holds the kind. This is the documented no-silent-
// fallback rule and the fix must not weaken it.
func TestReadStoredCredentialWithoutRecordKeepsTheContract(t *testing.T) {
	dir := t.TempDir()
	for _, state := range []string{"denied", "locked", "unavailable", "corrupt"} {
		t.Run(state, func(t *testing.T) {
			readers := credentialStoreReaders{
				native: func(context.Context, CredentialKind) (string, error) {
					return "", storeErrorf("get", "secret-service", state, "detail")
				},
				file: func(context.Context, CredentialKind) (string, error) { return "fallback-value", nil },
			}
			if _, _, err := readStoredCredentialIn(context.Background(), CredentialAlbert, dir, readers); err == nil || !strings.Contains(err.Error(), state) {
				t.Fatalf("native %s must surface without a record: %v", state, err)
			}
		})
	}
}

// TestReadStoredCredentialRecordedFileStoreMissing: a record pointing at a file
// store that no longer exists must read as "not stored", not as a store-internals
// error. The remedy is the same as never having stored it.
func TestReadStoredCredentialRecordedFileStoreMissing(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	readers := credentialStoreReaders{
		native: func(context.Context, CredentialKind) (string, error) { return "", ErrCredentialNotFound },
		file:   func(context.Context, CredentialKind) (string, error) { return "", ErrFileStoreAbsent },
	}
	_, _, err := readStoredCredentialIn(context.Background(), CredentialAlbert, dir, readers)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("a vanished recorded store must read as not-found: %v", err)
	}
}

// TestReadStoredCredentialRecordedFileStoreError: an unreadable-but-present file
// store (a symlink, a corrupt body) must surface as itself, not as not-found.
// Swallowing it would hide a real fault behind a "store a credential" hint.
func TestReadStoredCredentialRecordedFileStoreError(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	corrupt := &StoreError{Op: "get", Kind: "file", State: "corrupt", Err: errors.New("is a symlink; refusing to use it")}
	readers := credentialStoreReaders{
		native: func(context.Context, CredentialKind) (string, error) { return "", ErrCredentialNotFound },
		file:   func(context.Context, CredentialKind) (string, error) { return "", corrupt },
	}
	_, _, err := readStoredCredentialIn(context.Background(), CredentialAlbert, dir, readers)
	if !errors.Is(err, corrupt) {
		t.Fatalf("a corrupt file store must surface: %v", err)
	}
}

// TestReadStoredCredentialWiringIsReachable proves the production entry point
// consults the record, not just the helper the other tests exercise. Without
// this, the seam could be correct while readStoredCredential still called the
// old chain — the bug would live in the one line no test covered.
func TestReadStoredCredentialWiringIsReachable(t *testing.T) {
	dir := t.TempDir()
	if err := RecordCredentialStore(dir, CredentialAlbert, "file"); err != nil {
		t.Fatal(err)
	}
	nativeCalls := 0
	prevDir, prevReaders := credentialStateDirFn, credentialStoreReadersFn
	credentialStateDirFn = func() string { return dir }
	credentialStoreReadersFn = func() credentialStoreReaders {
		return credentialStoreReaders{
			native: func(context.Context, CredentialKind) (string, error) {
				nativeCalls++
				return "", storeErrorf("get", "secret-service", "denied", "exit 1: cannot autolaunch D-Bus")
			},
			file: func(context.Context, CredentialKind) (string, error) { return "file-value", nil },
		}
	}
	t.Cleanup(func() { credentialStateDirFn, credentialStoreReadersFn = prevDir, prevReaders })

	v, store, err := readStoredCredential(context.Background(), CredentialAlbert)
	if err != nil || v != "file-value" || store != "file" {
		t.Fatalf("readStoredCredential = %q, %q, %v", v, store, err)
	}
	if nativeCalls != 0 {
		t.Fatalf("readStoredCredential probed the native store %d time(s) despite the record", nativeCalls)
	}
}
