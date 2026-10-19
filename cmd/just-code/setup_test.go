package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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

// TestAlbertKeyScreenShowsStepAndLinks pins screen 3.2's content: the [1/3]
// counter, what the key is for, that it goes to the keychain (not the
// projects), and both links.
func TestAlbertKeyScreenShowsStepAndLinks(t *testing.T) {
	header := albertKeyScreenHeader(false)
	for _, want := range []string{
		"[1/3] Clé Albert API",
		"L'agent utilise Albert API, l'API d'IA générative de l'État.",
		"La clé est enregistrée dans le trousseau de ce poste, jamais dans vos projets.",
		"Vous n'avez pas encore de clé ?",
		albertCreateKeyURL,
		albertQuickstartURL,
	} {
		if !strings.Contains(header, want) {
			t.Errorf("Albert key screen is missing %q", want)
		}
	}
	if !strings.Contains(albertKeyPrompt, "elle ne s'affiche pas") {
		t.Errorf("the key prompt must say the value is not displayed: %q", albertKeyPrompt)
	}
}

// TestAlbertKeyScreenDescribesTheActualStore pins the fallback wording: with
// --fallback the key goes to a consented owner-only file, not the keychain, so
// the screen must not promise a keychain.
func TestAlbertKeyScreenDescribesTheActualStore(t *testing.T) {
	native := albertKeyScreenHeader(false)
	fallback := albertKeyScreenHeader(true)
	if !strings.Contains(native, "trousseau") {
		t.Errorf("the native store wording must name the keychain: %q", native)
	}
	if strings.Contains(fallback, "trousseau") {
		t.Errorf("the fallback wording must not claim a keychain: %q", fallback)
	}
	if !strings.Contains(fallback, "fichier") {
		t.Errorf("the fallback wording must say the key is stored in a file: %q", fallback)
	}
}

// TestAlbertRejectionMessageDistinguishesFromNetwork pins that a refusal names
// the key page and never reads like the network message.
func TestAlbertRejectionMessageDistinguishesFromNetwork(t *testing.T) {
	msg := albertRejectedMessage("HTTP 401")
	if !strings.Contains(msg, albertCreateKeyURL) {
		t.Errorf("a rejected key must point at the key page: %q", msg)
	}
	if !strings.Contains(msg, "refusé") {
		t.Errorf("a rejected key must say it was refused: %q", msg)
	}
	if msg == albertUnreachableMessage || albertUnreachableMessage == albertRejectedMessage("x") {
		t.Error("refusal and unreachable must be distinct messages")
	}
	if !strings.Contains(albertUnreachableMessage, "proxy") {
		t.Errorf("the unreachable message must mention the administration proxy: %q", albertUnreachableMessage)
	}
}

// TestSetupWizardRejectedKeyReasksInsteadOfExiting pins 3.2's acceptance
// criterion "une erreur ne fait jamais quitter l'assistant": after a refusal
// the wizard asks for the key again, and a valid key entered the second time
// completes setup.
func TestSetupWizardRejectedKeyReasksInsteadOfExiting(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	var attempts []string
	// First key refused, second accepted; then no GitHub and defaults.
	// key, key (re-ask), model, GitHub, name, email, apply.
	input := "bad-key\ngood-key\n\nn\n\n\n\ny\n"
	out := captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(_ context.Context, key string) error {
				attempts = append(attempts, key)
				if key == "bad-key" {
					return justcode.RejectedCredentialError{Detail: "HTTP 401"}
				}
				return nil
			},
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code != 0 || err != nil {
			t.Fatalf("a refused key followed by a valid one must complete setup: code=%d err=%v", code, err)
		}
	})
	if len(attempts) != 2 || attempts[0] != "bad-key" || attempts[1] != "good-key" {
		t.Fatalf("the wizard must re-ask after a refusal; validated keys: %v", attempts)
	}
	if !strings.Contains(out, "refusé") || !strings.Contains(out, "✅ Clé valide.") {
		t.Errorf("expected a refusal notice then a success notice; output:\n%s", out)
	}
}

// TestSetupWizardUnreachableOffersRetryAndContinue pins the network branch:
// [R] re-enters the key, and the key is stored unvalidated when the user
// continues.
func TestSetupWizardUnreachableOffersRetryAndContinue(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	calls := 0
	// key -> unreachable -> R (retry) -> key again -> reachable -> defaults.
	// key, R (retry), key, model, GitHub, name, email, apply.
	input := "first-key\nr\nsecond-key\n\nn\n\n\n\ny\n"
	out := captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error {
				calls++
				if calls == 1 {
					return fmt.Errorf("could not reach the Albert endpoint: HTTP 503")
				}
				return nil
			},
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code != 0 || err != nil {
			t.Fatalf("retry then success must complete setup: code=%d err=%v", code, err)
		}
	})
	if calls != 2 {
		t.Fatalf("R must re-validate the key; validation calls=%d", calls)
	}
	if !strings.Contains(out, albertUnreachableMessage) {
		t.Errorf("the unreachable message must be shown; output:\n%s", out)
	}
}

// TestSetupWizardUnreachableContinueStoresUnvalidated pins the [C] branch:
// continuing stores the key without validation and setup proceeds.
func TestSetupWizardUnreachableContinueStoresUnvalidated(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	calls := 0
	// key -> unreachable -> c (continue) -> defaults.
	// key, C (continue), model, GitHub, name, email, apply.
	input := "only-key\nc\n\nn\n\n\n\ny\n"
	out := captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error {
				calls++
				return fmt.Errorf("could not reach the Albert endpoint: HTTP 503")
			},
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code != 0 || err != nil {
			t.Fatalf("continue without validation must complete setup: code=%d err=%v", code, err)
		}
	})
	if calls != 1 {
		t.Fatalf("C must not re-validate; validation calls=%d", calls)
	}
	if !strings.Contains(out, "Clé enregistrée sans vérification.") {
		t.Errorf("continuing must say the key was stored unvalidated; output:\n%s", out)
	}
	// A completed setup removes the journal, so assert on the outcome the
	// journal existed for: the credential is in the store and settings were
	// written.
	m := readUserSettingsFile(t)
	if m == nil {
		t.Fatal("a completed setup must write the settings file")
	}
	_ = stateDir
}

// TestSetupWizardFlowIsKeyThenModelsThenGitHub pins the epic's announced order:
// clé Albert API -> modèles -> GitHub. The [n/3] counters and the intro's
// "3 étapes" are only truthful in that order.
func TestSetupWizardFlowIsKeyThenModelsThenGitHub(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// key, model, GitHub, name, email, apply.
	input := "test-key\n\nn\n\n\n\ny\n"
	out := captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error { return nil },
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code != 0 || err != nil {
			t.Fatalf("wizard run: code=%d err=%v", code, err)
		}
	})
	keyAt := strings.Index(out, "[1/3] Clé Albert API")
	modelAt := strings.Index(out, "Default model")
	githubAt := strings.Index(out, "GitHub credential (optional")
	if keyAt < 0 || modelAt < 0 || githubAt < 0 {
		t.Fatalf("flow markers missing (key=%d model=%d github=%d); output:\n%s", keyAt, modelAt, githubAt, out)
	}
	if !(keyAt < modelAt && modelAt < githubAt) {
		t.Errorf("order must be key -> models -> GitHub; got key=%d model=%d github=%d", keyAt, modelAt, githubAt)
	}
}

// TestExplicitDefaultModelSurvivesAnAbortAndAnUnansweredResume pins the Kimi
// review finding on #216, across two runs:
//
//  1. a completed install has a saved model, and a re-run answers the model
//     prompt EMPTY (an explicit choice of the built-in default) but is then
//     aborted at the recap;
//  2. the resumed run's model prompt is never answered (EOF).
//
// The explicit default choice from run 1 must survive: the run may not reload
// the saved model just because the resumed prompt was unanswered. That requires
// journalling the answered-ness, not only the value.
func TestExplicitDefaultModelSurvivesAnAbortAndAnUnansweredResume(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	path, err := justcode.UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	us, err := justcode.ReadUserSettings(justcode.DefaultFS, path)
	if err != nil {
		t.Fatal(err)
	}
	us.DefaultModel = "albert/old"
	if err := justcode.WriteUserSettings(justcode.DefaultFS, path, us); err != nil {
		t.Fatal(err)
	}

	// Run 1: key -> EMPTY model (explicit built-in) -> skip GitHub ->
	// identities -> refuse at the recap.
	_ = captureStdout(t, func() {
		_, _ = setupWizardRun(true, bufio.NewReader(strings.NewReader("the-key\n\n\n\n\nn\n")),
			func(context.Context, string) error { return nil },
			func(context.Context) error { return nil },
			func(string) string { return "" })
	})
	j, err := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !j.ModelChosen || j.DefaultModel != "" {
		t.Fatalf("run 1 must journal the answered-empty choice: chosen=%v model=%q", j.ModelChosen, j.DefaultModel)
	}

	// Run 2: resume. The key is already stored, so the MODEL prompt is the
	// first question, and the input makes it unanswered (EOF).
	_ = captureStdout(t, func() {
		_, _ = setupWizardRun(true, bufio.NewReader(strings.NewReader("")),
			func(context.Context, string) error { return nil },
			func(context.Context) error { return nil },
			func(string) string { return "" })
	})
	after, err := justcode.ReadUserSettings(justcode.DefaultFS, path)
	if err != nil {
		t.Fatal(err)
	}
	if after.DefaultModel != "" {
		t.Fatalf("an explicit default choice must not be undone by an unanswered resume; got %q", after.DefaultModel)
	}
}

// TestUnansweredModelPromptKeepsTheSavedModel pins the round-6 finding on #216:
// a completed install being reconfigured has no journal, so the prompt starts
// from the saved setting. If the input runs out (EOF, Ctrl-D) the prompt is
// unanswered, and the run must not clear a model the user never asked to change.
func TestUnansweredModelPromptKeepsTheSavedModel(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// A model already saved by a previous successful setup.
	path, err := justcode.UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	us, err := justcode.ReadUserSettings(justcode.DefaultFS, path)
	if err != nil {
		t.Fatal(err)
	}
	us.DefaultModel = "albert/keep-me"
	if err := justcode.WriteUserSettings(justcode.DefaultFS, path, us); err != nil {
		t.Fatal(err)
	}
	// Input runs out right after the key, so every later prompt is unanswered.
	input := "the-key\n"
	_ = captureStdout(t, func() {
		_, _ = setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error { return nil },
			func(context.Context) error { return nil },
			func(string) string { return "" })
	})
	after, uerr := justcode.ReadUserSettings(justcode.DefaultFS, path)
	if uerr != nil {
		t.Fatal(uerr)
	}
	if after.DefaultModel != "albert/keep-me" {
		t.Fatalf("an unanswered model prompt must keep the saved model; got %q", after.DefaultModel)
	}
}

// TestEmptyModelAnswerClearsAJournaledChoice pins the round-5 finding on #216:
// a resumed run that had journaled a custom model must be able to go back to
// the built-in default by answering the prompt with an empty line.
func TestEmptyModelAnswerClearsAJournaledChoice(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// Pre-journal a custom model, as an interrupted run would have left it.
	j, err := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	j.DefaultModel = "albert/previous-choice"
	if err := justcode.WriteSetupJournal(justcode.DefaultFS, stateDir, j); err != nil {
		t.Fatal(err)
	}
	// key -> EMPTY model (choose the built-in) -> skip GitHub -> identities
	// -> apply.
	input := "the-key\n\n\n\n\n\ny\n"
	_ = captureStdout(t, func() {
		code, rerr := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error { return nil },
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code != 0 || rerr != nil {
			t.Fatalf("run: code=%d err=%v", code, rerr)
		}
	})
	after, rerr := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if after.DefaultModel != "" {
		t.Fatalf("an empty answer must clear the journaled model; got %q", after.DefaultModel)
	}
	path, perr := justcode.UserSettingsPath()
	if perr != nil {
		t.Fatal(perr)
	}
	us, uerr := justcode.ReadUserSettings(justcode.DefaultFS, path)
	if uerr != nil {
		t.Fatal(uerr)
	}
	if us.DefaultModel != "" {
		t.Fatalf("an empty answer must clear the stored model; got %q", us.DefaultModel)
	}
}

// TestModelChoiceSurvivesAbortAfterTheModelStage pins the round-4 finding on
// #216. Model selection was moved ahead of the GitHub and git-identity forms,
// so a user who aborts one of those must not lose a model they already chose:
// the journal has to hold it before those cancellation points.
func TestModelChoiceSurvivesAbortAfterTheModelStage(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// key -> model -> skip GitHub (empty) -> default identity (two empties)
	// -> refuse at the recap, which aborts after the model stage.
	input := "the-key\nalbert/some-model\n\n\n\nn\n"
	_ = captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error { return nil },
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code == 0 {
			t.Errorf("an aborted run must not report success: code=%d err=%v", code, err)
		}
	})
	j, err := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if j.DefaultModel != "albert/some-model" {
		t.Fatalf("the model chosen before the abort must be journaled; got %q", j.DefaultModel)
	}
	// The settings write must still be deferred to Apply.
	path, perr := justcode.UserSettingsPath()
	if perr == nil {
		us, rerr := justcode.ReadUserSettings(justcode.DefaultFS, path)
		if rerr == nil && us.DefaultModel == "albert/some-model" {
			t.Fatal("an aborted run must not write the model into user settings")
		}
	}
}

// TestSetupUnreachableTypoReasksInsteadOfExiting pins the round-3 finding on
// #216: a typo at the [R]/[C] prompt is not a decision and must not end the
// run — the wizard asks again.
func TestSetupUnreachableTypoReasksInsteadOfExiting(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// key -> unreachable -> typo -> C (continue) -> model, no GitHub, defaults.
	input := "only-key\nz\nc\n\nn\n\n\n\ny\n"
	out := captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error {
				return fmt.Errorf("could not reach the Albert endpoint: HTTP 503")
			},
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code != 0 || err != nil {
			t.Fatalf("a typo must not end the run: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Réponse non reconnue.") {
		t.Errorf("an unrecognized answer must be reported; output:\n%s", out)
	}
	if !strings.Contains(out, "Clé enregistrée sans vérification.") {
		t.Errorf("C must still continue after a typo; output:\n%s", out)
	}
}

// TestSetupUnreachableUnansweredPromptStoresNothing pins the second review
// finding on #216: an end of input at the [R]/[C] prompt is NOT an explicit
// "continue", so it must cancel rather than store an unvalidated key.
func TestSetupUnreachableUnansweredPromptStoresNothing(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// key, then end of input at the [R]/[C] question.
	input := "only-key\n"
	code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
		func(context.Context, string) error {
			return fmt.Errorf("could not reach the Albert endpoint: HTTP 503")
		},
		func(context.Context) error { return nil },
		func(string) string { return "" })
	if code == 0 {
		t.Errorf("an unanswered retry prompt must not report success: code=%d err=%v", code, err)
	}
	if store, serr := justcode.ConsentFileStore(); serr == nil {
		if _, statErr := os.Stat(store.Path); statErr == nil {
			t.Fatal("an unanswered retry prompt must not store the key")
		}
	}
	j, rerr := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if justcode.ContainsCredentialKind(j.CredentialKinds, "albert") {
		t.Fatal("an unanswered retry prompt must not journal albert as stored")
	}
}

// TestSetupUnreachableRetryThenAbandonStoresNothing is the regression test for
// the review finding on #216: choosing R then giving up must not leave the
// first, unvalidated key in the store — otherwise a later `setup` skips this
// screen and uses a key the user explicitly chose not to accept.
func TestSetupUnreachableRetryThenAbandonStoresNothing(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// key -> unreachable -> R -> empty key (abandon).
	input := "first-key\nr\n\n"
	var negativeCalls int
	_ = captureStdout(t, func() {
		code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
			func(context.Context, string) error {
				negativeCalls++
				return fmt.Errorf("could not reach the Albert endpoint: HTTP 503")
			},
			func(context.Context) error { return nil },
			func(string) string { return "" })
		if code == 0 {
			t.Errorf("abandoning after R must not report success: code=%d err=%v", code, err)
		}
	})
	// The store must not exist: nothing was ever Put.
	if store, err := justcode.ConsentFileStore(); err == nil {
		if _, serr := os.Stat(store.Path); serr == nil {
			t.Fatal("a retry that was abandoned must not leave the key in the store")
		}
	}
	// And the journal must not claim the credential is stored.
	j, rerr := justcode.ReadSetupJournal(justcode.DefaultFS, stateDir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if justcode.ContainsCredentialKind(j.CredentialKinds, "albert") {
		t.Fatal("a retry that was abandoned must not journal albert as stored")
	}
	if negativeCalls == 0 {
		t.Fatal("precondition: the endpoint was never probed")
	}
}

func TestSetupWizardRejectedKeyStoresNothing(t *testing.T) {
	stateDir, _ := setupTestEnv(t)
	forceNonTTY(t)
	journalPastPreflight(t, stateDir)
	// The store seam: a fake that records puts. The wizard's Store func is
	// not injectable per-run through setupWizardRun (only validate and
	// ensure are), so this test uses the file fallback store: a rejected
	// key must leave it absent (nothing was stored).
	// #162 changed the contract: a refusal re-asks instead of ending the run,
	// so the flow only stops when the re-ask is answered with an empty key.
	input := "bad-key\n\n"
	code, err := setupWizardRun(true, bufio.NewReader(strings.NewReader(input)),
		func(context.Context, string) error {
			return justcode.RejectedCredentialError{Detail: "HTTP 401"}
		},
		func(context.Context) error { return nil },
		func(string) string { return "" })
	// An empty key abandons the run, which reports the resumable cancel
	// message (non-nil) rather than silently succeeding.
	if code == 0 {
		t.Fatalf("abandoning after a rejected key must exit non-zero: code=%d err=%v", code, err)
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
	// Piped answers in wizard order: key with a trailing newline (the raw
	// pipe line), empty model, no GitHub, empty identity (defaults), apply.
	input := "test-key\n\nn\n\n\n\ny\n"
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
	// Wizard order: key, empty model, no GitHub, empty name, empty email
	// (both default), apply.
	input := "test-key\n\nn\n\n\n\ny\n"
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
	// Wizard order: key, model, no GitHub, name, email, then cancel at apply.
	input := "test-key\nsome-model\nn\n\n\nn\n"
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
