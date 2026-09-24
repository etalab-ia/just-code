package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// The cmd-level tests exercise the wizard flow end to end with a disposable
// HOME (so the real state dir and user settings are never touched), piped
// non-TTY input (the documented degraded mode), and injected seams for the
// network-facing operations (Albert validation, runtime install) — the
// tests never hit the network and never download the runtime.
//
// The preflight runs for real against the disposable HOME; on a host without
// KVM it is fatal before any prompt, so tests that need the prompt flow set
// the journal past preflight first (the same resumability the wizard itself
// provides).

// setupTestEnv isolates HOME and returns the state dir and settings path.
func setupTestEnv(t *testing.T) (stateDir, settingsPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	// msbRuntimeHome reads HOME; an empty dir means "not installed", which
	// keeps the primitive probe path deterministic.
	return justcode.DefaultStateDir(), ""
}

// forceNonTTY pins the wizard's TTY probe to false for the test (the piped
// path is the one under test); the original is restored on cleanup.
func forceNonTTY(t *testing.T) {
	t.Helper()
	orig := stdinIsTTYFn
	stdinIsTTYFn = func() bool { return false }
	t.Cleanup(func() { stdinIsTTYFn = orig })
}

// journalPastPreflight writes a journal whose preflight already passed, so
// the flow starts at the credential stage even on a KVM-less host.
func journalPastPreflight(t *testing.T, stateDir string) {
	t.Helper()
	j, err := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	j.Stage = justcode.StageCredential
	if err := justcode.WriteSetupJournal(justcode.DefaultFS, stateDir, j); err != nil {
		t.Fatal(err)
	}
}

func readUserSettingsFile(t *testing.T) map[string]any {
	t.Helper()
	path, err := justcode.UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSetupWizardRejectedKeyStoresNothing(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// The store seam: a fake that records puts. The wizard's Store func is
	// not injectable per-run through setupWizardRun (only validate and
	// ensure are), so this test uses the file fallback store: a rejected
	// key must leave it absent (nothing was stored).
	input := "bad-key\n"
	code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
		func(context.Context, string) error {
			return justcode.RejectedCredentialError{Detail: "HTTP 401"}
		},
		func(context.Context) error { return nil })
	if code == 0 || err != nil {
		t.Fatalf("a rejected key must exit non-zero: code=%d err=%v", code, err)
	}
	// The journal must not claim the credential was stored.
	j, rerr := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if justcode.ContainsCredentialKind(j.CredentialKinds, "albert") {
		t.Fatal("a rejected key must not be journaled as stored")
	}
	// The file fallback store must not exist (no Put happened).
	if store, err := justcode.ConsentFileStore(); err == nil {
		if _, serr := os.Stat(store.Path); serr == nil {
			t.Fatal("a rejected key must not create the credential store")
		}
	}
}

func TestSetupWizardNonTTYAnswersTrimNewlines(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// Piped answers: key with a trailing newline (the raw pipe line), no
	// GitHub, empty identity (defaults), empty model, apply.
	input := "test-key\nn\n\n\n\n\ny\n"
	var seenKey string
	code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
		func(_ context.Context, key string) error {
			seenKey = key
			return nil
		},
		func(context.Context) error { return nil })
	if code != 0 || err != nil {
		t.Fatalf("wizard run: code=%d err=%v", code, err)
	}
	if seenKey != "test-key" {
		t.Fatalf("the piped key must arrive trimmed, got %q", seenKey)
	}
	// A completed setup removes the journal.
	if _, serr := os.Stat(justcode.SetupJournalPath(stateDir)); !os.IsNotExist(serr) {
		t.Fatal("a completed setup must remove the journal")
	}
}

func TestSetupWizardCancelAtApplyLeavesSettingsUntouched(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	if readUserSettingsFile(t) != nil {
		t.Fatal("precondition: no user settings file may exist")
	}
	input := "test-key\nn\n\n\nsome-model\nn\n"
	code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
		func(context.Context, string) error { return nil },
		func(context.Context) error { return nil })
	if code == 0 || err == nil {
		t.Fatalf("cancel at apply must exit non-zero: code=%d err=%v", code, err)
	}
	m := readUserSettingsFile(t)
	// The identity is persisted at its own stage (durable by design); the
	// model must NOT be written by a cancelled apply.
	if m != nil {
		if _, ok := m["defaultModel"]; ok {
			t.Fatalf("a cancelled apply must not write the model setting, got %v", m)
		}
	}
}
