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
	// os.UserHomeDir reads HOME on unix and USERPROFILE on windows;
	// os.UserConfigDir reads XDG_CONFIG_HOME on unix and AppData on
	// windows. Set both families so the disposable HOME is honored on
	// every CI leg.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
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

func TestSetupDoctorPlainOutputSeparatesSections(t *testing.T) {
	setupTestEnv(t)
	origCollect := setupDoctorCollectFn
	setupDoctorCollectFn = func() justcode.DiagnosticsReport {
		return justcode.DiagnosticsReport{
			Platform: "test/arch",
			Sections: []justcode.DiagnosticSection{
				{Title: "Host capability", Items: []justcode.DiagnosticItem{
					{Name: "Virtualization", Value: "ok", Status: justcode.DiagnosticOK},
				}},
				{Title: "Runtime and VM state", Items: []justcode.DiagnosticItem{
					{Name: "Managed Microsandbox runtime", Value: "ok", Status: justcode.DiagnosticOK},
				}},
				{Title: "Provider verification", Items: []justcode.DiagnosticItem{
					{Name: "Albert endpoint", Value: "verified", Status: justcode.DiagnosticOK},
				}},
			},
		}
	}
	origRender := doctorRendererFn
	doctorRendererFn = func() bool { return false }
	t.Cleanup(func() { setupDoctorCollectFn = origCollect; doctorRendererFn = origRender })

	out := captureStdout(t, func() {
		code, err := setupDoctorCmd(false)
		if err != nil || code != 0 {
			t.Fatalf("setup doctor: code=%d err=%v", code, err)
		}
	})
	for _, want := range []string{"Host capability:", "Runtime and VM state:", "Provider verification:", "no blocking issue"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q: %q", want, out)
		}
	}
}

func TestSetupDoctorFailedItemReturnsNonZero(t *testing.T) {
	setupTestEnv(t)
	origCollect := setupDoctorCollectFn
	setupDoctorCollectFn = func() justcode.DiagnosticsReport {
		return justcode.DiagnosticsReport{Platform: "test/arch", Sections: []justcode.DiagnosticSection{{
			Title: "Provider verification",
			Items: []justcode.DiagnosticItem{{Name: "Albert endpoint", Value: "rejected", Status: justcode.DiagnosticFailed}},
		}}}
	}
	origRender := doctorRendererFn
	doctorRendererFn = func() bool { return false }
	t.Cleanup(func() { setupDoctorCollectFn = origCollect; doctorRendererFn = origRender })

	out := captureStdout(t, func() {
		code, err := setupDoctorCmd(false)
		if err != nil || code != 1 {
			t.Fatalf("failed diagnostics must return code 1: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "one or more checks failed") {
		t.Fatalf("doctor output must state the failure: %q", out)
	}
}

func TestSetupDoctorJSONOutputIsMachineReadable(t *testing.T) {
	setupTestEnv(t)
	origCollect := setupDoctorCollectFn
	setupDoctorCollectFn = func() justcode.DiagnosticsReport {
		return justcode.DiagnosticsReport{Platform: "test/arch", Sections: []justcode.DiagnosticSection{{
			Title: "Host capability",
			Items: []justcode.DiagnosticItem{{Name: "Virtualization", Value: "ok", Status: justcode.DiagnosticOK}},
		}}}
	}
	t.Cleanup(func() { setupDoctorCollectFn = origCollect })

	out := captureStdout(t, func() {
		code, err := setupCmd([]string{"doctor", "--json"})
		if err != nil || code != 0 {
			t.Fatalf("setup doctor --json: code=%d err=%v", code, err)
		}
	})
	var report justcode.DiagnosticsReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor JSON output is invalid: %v: %q", err, out)
	}
	if report.Platform != "test/arch" || len(report.Sections) != 1 || report.Sections[0].Title != "Host capability" {
		t.Fatalf("doctor JSON report = %+v", report)
	}
}

func TestSetupDoctorNoColorUsesPlainOutput(t *testing.T) {
	setupTestEnv(t)
	origCollect, origRender, origNoColor := setupDoctorCollectFn, doctorRendererFn, wizardNoColor
	setupDoctorCollectFn = func() justcode.DiagnosticsReport {
		return justcode.DiagnosticsReport{Platform: "test/arch", Sections: []justcode.DiagnosticSection{{
			Title: "Host capability",
			Items: []justcode.DiagnosticItem{{Name: "Virtualization", Value: "ok", Status: justcode.DiagnosticOK}},
		}}}
	}
	doctorRendererFn = func() bool { return !wizardNoColor }
	wizardNoColor = false
	t.Cleanup(func() {
		setupDoctorCollectFn, doctorRendererFn, wizardNoColor = origCollect, origRender, origNoColor
	})

	out := captureStdout(t, func() {
		code, err := setupCmd([]string{"doctor", "--no-color"})
		if err != nil || code != 0 {
			t.Fatalf("setup doctor --no-color: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Host capability:") || strings.Contains(out, "│") {
		t.Fatalf("--no-color did not use the plain doctor renderer: %q", out)
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
		func(context.Context) error { return nil },
		func(string) string { return "" })
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

func TestSetupCompletionMessageDescribesFullDefault(t *testing.T) {
	message := setupCompletionMessage()
	if !strings.Contains(message, "Microsandbox/full") || !strings.Contains(message, "TUI running inside the guest") {
		t.Fatalf("setup completion message = %q, want full-mode default guidance", message)
	}
	if strings.Contains(message, "start the backend and attach the TUI") {
		t.Fatalf("setup completion message still describes the legacy backend flow: %q", message)
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
		func(context.Context) error { return nil },
		func(string) string { return "" })
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

// TestSetupWizardEmptyNameFallsBackToDefault pins the documented default:
// an empty name input must persist "Albert Code Agent", not an empty
// gitName that would print as a blank author in the review.
func TestSetupWizardEmptyNameFallsBackToDefault(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// key, no GitHub, empty name, empty email (defaults), empty model, apply.
	input := "test-key\nn\n\n\n\ny\n"
	code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
		func(context.Context, string) error { return nil },
		func(context.Context) error { return nil },
		func(string) string { return "" })
	if code != 0 || err != nil {
		t.Fatalf("wizard run: code=%d err=%v", code, err)
	}
	m := readUserSettingsFile(t)
	if m == nil {
		t.Fatal("settings file must exist after a completed setup")
	}
	if got, _ := m["gitName"].(string); got != "Albert Code Agent" {
		t.Fatalf("empty name must fall back to the documented default, got %q", got)
	}
	if got, _ := m["gitEmail"].(string); got != "albert-code@noreply.etalab.gouv.fr" {
		t.Fatalf("empty email must fall back to the documented default, got %q", got)
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
		func(context.Context) error { return nil },
		func(string) string { return "" })
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
