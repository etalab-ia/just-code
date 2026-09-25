package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// stubCatalogueCheck replaces the catalogue validator for one test. The real
// one needs an Albert credential and a reachable (or cached) catalogue, so a
// test that exercises it fails on a machine that has those — which is exactly
// how this seam came to exist.
func stubCatalogueCheck(t *testing.T, fn func(root, model string) (string, error)) {
	t.Helper()
	orig := catalogueModelWarningFn
	catalogueModelWarningFn = fn
	t.Cleanup(func() { catalogueModelWarningFn = orig })
}

func initTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestParseInitArgs pins the flag surface, including the two numeric options.
func TestParseInitArgs(t *testing.T) {
	opts, err := parseInitArgs([]string{
		"--root", "/tmp/p", "--runtime", "tart", "--isolation", "backend",
		"--model", "albert/x", "--cpus", "4", "--memory-mb", "2048",
		"--credential-ref", "work", "--replace", "--yes",
	})
	if err != nil {
		t.Fatalf("parseInitArgs: %v", err)
	}
	if opts.Root != "/tmp/p" || opts.Runtime != "tart" || opts.Isolation != "backend" ||
		opts.Model != "albert/x" || opts.CPUs != 4 || opts.MemoryMB != 2048 ||
		opts.CredentialRef != "work" || !opts.Replace || !opts.Yes {
		t.Fatalf("options = %+v", opts)
	}
	if !opts.Set["root"] || !opts.Set["cpus"] || !opts.Set["memory-mb"] {
		t.Fatalf("every supplied field must be recorded as set: %v", opts.Set)
	}
	for _, args := range [][]string{{"--cpus", "many"}, {"--memory-mb", "x"}, {"--root"}, {"--bogus"}} {
		if _, err := parseInitArgs(args); err == nil {
			t.Fatalf("%v must be rejected", args)
		}
	}
}

// TestInitNonTTYNamesWhatIsMissing pins the P12 acceptance item: with no
// terminal the command must never wait. It fails and says which input is
// missing, so a script can supply it next time.
func TestInitNonTTYNamesWhatIsMissing(t *testing.T) {
	root := initTestProject(t)
	// Run FROM the project so the "nothing was written" assertion checks the
	// directory a regressed run would actually write to, instead of the test
	// package directory inside the real checkout.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	// No --root: the one answer that cannot be defaulted from nothing.
	code, err := initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader("")), false)
	if code != 1 || err == nil {
		t.Fatalf("a non-TTY run without --root must fail: code=%d err=%v", code, err)
	}
	for _, want := range []string{"no terminal available", "--root", "--yes", "--isolation"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure must mention %q: %v", want, err)
		}
	}
	// And nothing was written.
	if _, err := os.Stat(filepath.Join(root, ".just-code")); !os.IsNotExist(err) {
		t.Fatalf("a failed run must not write anything (stat err = %v)", err)
	}
}

// TestInitNonTTYWithAllInputsWritesTheManifest pins the scriptable path: every
// answer on the command line, no terminal, no prompt.
func TestInitNonTTYWithAllInputsWritesTheManifest(t *testing.T) {
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	root := initTestProject(t)
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{
			Root: root, Isolation: "backend", CPUs: 4, MemoryMB: 2048,
			Replace: true, Yes: true,
			Set: map[string]bool{"root": true, "isolation": true, "cpus": true, "memory-mb": true},
		}, bufio.NewReader(strings.NewReader("")), false)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Review the project setup") {
		t.Fatalf("the review must be printed even without a terminal: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatalf("the manifest must be written and readable: %v", err)
	}
	if pm.CPUs != 4 || pm.MemoryMB != 2048 || pm.Isolation != string(justcode.IsolationBackend) {
		t.Fatalf("manifest = %+v", pm)
	}
}

// TestInitInteractiveUsesDefaultsOnEmptyAnswers pins that pressing enter
// through the questions is a valid path: every question has a default, and the
// engine's defaults are what get written.
func TestInitInteractiveUsesDefaultsOnEmptyAnswers(t *testing.T) {
	root := initTestProject(t)
	input := root + "\n\n\n\n\n\n\n\n" // root, runtime, isolation, model, cpus, memory, credential, apply
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "Sharing mode") || !strings.Contains(out, "Guest resources") {
		t.Fatalf("the questions must be asked: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if pm.Isolation != string(justcode.IsolationFull) {
		t.Fatalf("an empty answer must take the default, got %q", pm.Isolation)
	}
	if pm.CPUs != 0 || pm.MemoryMB != 0 {
		t.Fatalf("the default sizing must be left unset in the manifest, got %d/%d", pm.CPUs, pm.MemoryMB)
	}
}

// TestInitRefusesToReplaceWithoutTheFlag pins the guard at the CLI level: the
// engine refuses too, but the message has to reach the user before anything is
// written.
func TestInitRefusesToReplaceWithoutTheFlag(t *testing.T) {
	root := initTestProject(t)
	if _, err := (justcode.InitWizard{}).Plan(justcode.InitAnswers{Root: root}); err != nil {
		t.Fatal(err)
	}
	// First run creates the manifest.
	if _, err := initRun(initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}},
		bufio.NewReader(strings.NewReader("")), false); err != nil {
		t.Fatalf("first init: %v", err)
	}
	// Second run must refuse without --replace.
	var err error
	var code int
	_ = captureStdout(t, func() {
		code, err = initRun(initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}},
			bufio.NewReader(strings.NewReader("")), false)
	})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("a second run must refuse without --replace: code=%d err=%v", code, err)
	}
	// With --replace it goes through.
	if _, err := initRun(initOptions{Root: root, Yes: true, Replace: true, Set: map[string]bool{"root": true}},
		bufio.NewReader(strings.NewReader("")), false); err != nil {
		t.Fatalf("init --replace: %v", err)
	}
}

// TestInitHelpNeedsNoTerminal pins that the help path is answered without a
// prompt, since it is the one thing a user reaches for when the questions are
// unclear.
func TestInitHelpNeedsNoTerminal(t *testing.T) {
	out := captureStdout(t, func() {
		if code, err := initCmd([]string{"--help"}); code != 0 || err != nil {
			t.Fatalf("init --help: code=%d err=%v", code, err)
		}
	})
	for _, want := range []string{"--root", "--isolation", "--replace", "no terminal"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help must document %q: %q", want, out)
		}
	}
}

// TestIsTTYTreatsDevNullAsNonInteractive pins the script idiom: /dev/null is a
// character device, so a naive check calls it a terminal, and
// `init --yes < /dev/null` would then silently configure whatever directory
// the script happened to run from.
func TestIsTTYTreatsDevNullAsNonInteractive(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()
	if isTTY() {
		t.Fatalf("%s must not be reported as a terminal", os.DevNull)
	}
}

// TestInitSkipsQuestionsAnsweredByFlags pins that the flag surface and the
// prompts are one coherent path: a supplied flag is not re-asked, and the
// displayed default is the value that will actually be used.
func TestInitSkipsQuestionsAnsweredByFlags(t *testing.T) {
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	root := initTestProject(t)
	input := "\n" // only the apply confirmation is left to answer
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{
			Root: root, Runtime: "microsandbox", Isolation: "backend", Model: "albert/x",
			CPUs: 4, MemoryMB: 2048, CredentialRef: "work",
			Set: map[string]bool{"root": true, "runtime": true, "isolation": true, "model": true, "cpus": true, "memory-mb": true, "credential-ref": true},
		}, bufio.NewReader(strings.NewReader(input)), true)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	for _, asked := range []string{"root:", "runtime [", "isolation [", "model:", "cpus:", "memory MiB:", "reference:"} {
		if strings.Contains(out, asked) {
			t.Fatalf("a question already answered by a flag must not be asked (%q): %q", asked, out)
		}
	}
	if !strings.Contains(out, "Apply?") {
		t.Fatalf("the confirmation must still be asked: %q", out)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatal(err)
	}
	// The flag values are what got written: the prompt must not have been able
	// to override them, and no engine default may have leaked in.
	if pm.Isolation != "backend" || pm.CPUs != 4 || pm.MemoryMB != 2048 || pm.CredentialRef != "work" {
		t.Fatalf("manifest = %+v", pm)
	}
}

// TestInitValidatesFlagsBeforeAsking pins that a bad flag value is reported
// immediately: making someone answer six prompts to learn about a typo is a
// needless cost.
func TestInitValidatesFlagsBeforeAsking(t *testing.T) {
	root := initTestProject(t)
	for _, tc := range []struct {
		name string
		opts initOptions
		want string
	}{
		{"bad runtime", initOptions{Root: root, Runtime: "podman", Set: map[string]bool{"root": true, "runtime": true}}, "microsandbox"},
		{"bad isolation", initOptions{Root: root, Isolation: "sometimes", Set: map[string]bool{"root": true, "isolation": true}}, "isolation"},
		{"zero cpus", initOptions{Root: root, CPUs: 0, Set: map[string]bool{"root": true, "cpus": true}}, "--cpus must be 1"},
		{"huge memory", initOptions{Root: root, MemoryMB: justcode.MaxSandboxMemoryMB + 1, Set: map[string]bool{"root": true, "memory-mb": true}}, "--memory-mb must be 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			_ = captureStdout(t, func() {
				_, err = initRun(tc.opts, bufio.NewReader(strings.NewReader("")), true)
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestParseInitArgsRejectsAFlagAsValue pins that `--root --yes` is a missing
// value, not a directory named "--yes".
func TestParseInitArgsRejectsAFlagAsValue(t *testing.T) {
	_, err := parseInitArgs([]string{"--root", "--yes"})
	if err == nil || !strings.Contains(err.Error(), "needs a value") {
		t.Fatalf("error = %v, want a missing-value error", err)
	}
}

// TestInitStopsOnAModelTheCatalogueDoesNotList pins both halves of the model
// rule at the CLI level: a listing that does not contain the model stops the
// setup (the answer must change), while an unreachable catalogue only warns.
func TestInitStopsOnAModelTheCatalogueDoesNotList(t *testing.T) {
	root := initTestProject(t)
	stubCatalogueCheck(t, func(string, string) (string, error) {
		return "", fmt.Errorf("the model %q is not in the Albert catalogue", "albert/gone")
	})
	var err error
	_ = captureStdout(t, func() {
		_, err = initRun(initOptions{Root: root, Model: "albert/gone", Yes: true, Set: map[string]bool{"root": true, "model": true}},
			bufio.NewReader(strings.NewReader("")), false)
	})
	if err == nil || !strings.Contains(err.Error(), "catalogue") {
		t.Fatalf("a model the catalogue does not list must stop the setup: %v", err)
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("a refused model must leave nothing on disk (stat err = %v)", err)
	}

	// Unreachable catalogue: a warning, and the setup proceeds.
	stubCatalogueCheck(t, func(string, string) (string, error) {
		return "the model was recorded unverified: the catalogue could not be reached", nil
	})
	out := captureStdout(t, func() {
		code, err := initRun(initOptions{Root: root, Model: "albert/deepseek-v4-flash", Yes: true, Set: map[string]bool{"root": true, "model": true}},
			bufio.NewReader(strings.NewReader("")), false)
		if code != 0 || err != nil {
			t.Fatalf("initRun: code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "unverified") {
		t.Fatalf("the warning must reach the user: %q", out)
	}
}

// TestInitRejectsATypedZero pins parity with the flag: an empty answer means
// "use the default", so a typed 0 is an out-of-range value rather than a
// second way to say the same thing.
func TestInitRejectsATypedZero(t *testing.T) {
	root := initTestProject(t)
	// root, runtime, isolation, model, cpus(0)
	input := root + "\n\n\n\n0\n"
	var err error
	_ = captureStdout(t, func() {
		_, err = initRun(initOptions{Set: map[string]bool{}}, bufio.NewReader(strings.NewReader(input)), true)
	})
	if err == nil || !strings.Contains(err.Error(), "cpus must be 1 to") {
		t.Fatalf("a typed 0 must be rejected like the flag: %v", err)
	}
}

// TestOfferProjectInitSkipsWhenConfigured pins the first rule: a project that
// already has a configuration is never asked about it, so a normal launch
// takes no new step.
func TestOfferProjectInitSkipsWhenConfigured(t *testing.T) {
	root := initTestProject(t)
	if _, err := (justcode.InitWizard{}).Plan(justcode.InitAnswers{Root: root}); err != nil {
		t.Fatal(err)
	}
	if code, err := initRun(initOptions{Root: root, Yes: true, Set: map[string]bool{"root": true}},
		bufio.NewReader(strings.NewReader("")), false); err != nil || code != 0 {
		t.Fatalf("precondition: init must write a manifest: code=%d err=%v", code, err)
	}
	configured, code, err := offerProjectInit(root, parsedArgs{action: "attach"})
	if !configured || code != 0 || err != nil {
		t.Fatalf("a configured project must proceed: configured=%v code=%d err=%v", configured, code, err)
	}
}

// TestOfferProjectInitWithNoTerminalNamesTheCommand pins the non-TTY contract
// on the launch path: nothing waits, and the failure says exactly what to run.
func TestOfferProjectInitWithNoTerminalNamesTheCommand(t *testing.T) {
	root := initTestProject(t)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()

	configured, code, err := offerProjectInit(root, parsedArgs{action: "attach"})
	if configured {
		t.Fatal("an unconfigured project must not proceed silently without a terminal")
	}
	if code != 1 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	for _, want := range []string{"--root", "--yes", justcode.ProjectManifestPath(root)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure must mention %q: %v", want, err)
		}
	}
	if _, err := os.Stat(justcode.ProjectManifestPath(root)); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written without an answer (stat err = %v)", err)
	}
}

// TestOfferProjectInitRespectsExplicitChoices pins that a launch already
// carrying its own runtime or isolation flags is not asked to configure a
// project: those flags are the answer the offer would collect.
func TestOfferProjectInitRespectsExplicitChoices(t *testing.T) {
	root := initTestProject(t)
	for _, parsed := range []parsedArgs{{action: "start", runtime: "--tart"}, {action: "start", isolation: "backend"}} {
		configured, _, err := offerProjectInit(root, parsed)
		if !configured || err != nil {
			t.Fatalf("an explicit choice must skip the offer: configured=%v err=%v", configured, err)
		}
	}
}

// TestOfferProjectInitAppliesWhatItWrote pins that accepting the offer is not
// cosmetic: the manifest written by the offer is what the launch then applies.
func TestOfferProjectInitAppliesWhatItWrote(t *testing.T) {
	stubCatalogueCheck(t, func(string, string) (string, error) { return "", nil })
	root := initTestProject(t)
	// root, runtime, isolation(backend), model, cpus, memory, credential, apply
	input := "\n\nbackend\n\n\n\n\n\n"
	devNullLike := bufio.NewReader(strings.NewReader(input))

	// Drive the offer with the same answers a user would give.
	var configured bool
	var err error
	_ = captureStdout(t, func() {
		configured, _, err = offerProjectInitWithReader(root, parsedArgs{action: "attach"}, devNullLike, true)
	})
	if err != nil || !configured {
		t.Fatalf("the offer must go through: configured=%v err=%v", configured, err)
	}
	pm, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root))
	if err != nil {
		t.Fatalf("the offer must write the manifest: %v", err)
	}
	if pm.Isolation != "backend" {
		t.Fatalf("isolation = %q, want the answer given to the offer", pm.Isolation)
	}
	runtimeChoice, isolationChoice := projectRuntimeIsolation(root)
	if runtimeChoice != justcode.RuntimeMicrosandbox || isolationChoice != justcode.IsolationBackend {
		t.Fatalf("the launch must resolve what the offer wrote: %q / %q", runtimeChoice, isolationChoice)
	}
}

// TestOfferProjectInitSkipsWhenTheEnvironmentDecides pins that a choice made
// through the documented environment variables counts as an answer: a CI run
// exporting RUNTIME or ISOLATION has no terminal to be asked with, and asking
// anyway would turn a working launch into a failure.
func TestOfferProjectInitSkipsWhenTheEnvironmentDecides(t *testing.T) {
	for _, env := range []string{"RUNTIME", "ISOLATION"} {
		t.Run(env, func(t *testing.T) {
			root := initTestProject(t)
			t.Setenv(env, "microsandbox")
			if env == "ISOLATION" {
				t.Setenv(env, "backend")
			}
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Skipf("cannot open %s: %v", os.DevNull, err)
			}
			defer func() { _ = devNull.Close() }()
			orig := os.Stdin
			os.Stdin = devNull
			defer func() { os.Stdin = orig }()

			configured, code, err := offerProjectInit(root, parsedArgs{action: "start"})
			if !configured || code != 0 || err != nil {
				t.Fatalf("an environment-carried choice must skip the offer: configured=%v code=%d err=%v", configured, code, err)
			}
		})
	}
	// An empty exported value selects nothing, so it is not a choice.
	root := initTestProject(t)
	t.Setenv("RUNTIME", "")
	t.Setenv("ISOLATION", "")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()
	if configured, _, _ := offerProjectInit(root, parsedArgs{action: "start"}); configured {
		t.Fatal("an empty exported value must not silently stand in for a configuration")
	}
}

// TestOfferProjectInitWarnsOnABrokenManifest pins that a manifest the launch
// cannot read is not passed over in silence: the launch readers tolerate the
// error, so this is the only place holding it.
func TestOfferProjectInitWarnsOnABrokenManifest(t *testing.T) {
	root := initTestProject(t)
	if err := os.MkdirAll(filepath.Dir(justcode.ProjectManifestPath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	// A secret-looking field is rejected by the reader, like a broken one.
	if err := os.WriteFile(justcode.ProjectManifestPath(root), []byte(`{"schemaVersion":1,"apiKey":"leak"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var configured bool
	var err error
	stderr := captureStderr(t, func() {
		configured, _, err = offerProjectInit(root, parsedArgs{action: "attach"})
	})
	if err != nil || !configured {
		t.Fatalf("a broken manifest must not block the launch: configured=%v err=%v", configured, err)
	}
	if !strings.Contains(stderr, "cannot be read") || !strings.Contains(stderr, "--replace") {
		t.Fatalf("the user must be told the manifest is ignored and how to fix it: %q", stderr)
	}
	// And the broken file is left alone: the offer must not replace it.
	raw, readErr := os.ReadFile(justcode.ProjectManifestPath(root))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(raw), "leak") {
		t.Fatalf("the offer must not rewrite the manifest: %s", raw)
	}
}

// TestIsLaunchActionPinsTheGuestBuildingSet keeps the one action set the launch
// flow shares from drifting: the offer, the malformed-config gate and the guest
// sizing gate all key off it, and a read-only action must never be gated on a
// configuration the user may have come to inspect because it is broken.
func TestIsLaunchActionPinsTheGuestBuildingSet(t *testing.T) {
	for _, action := range []string{"attach", "start", "restart", "recreate"} {
		if !isLaunchAction(action) {
			t.Fatalf("%q builds a guest and must be a launch action", action)
		}
	}
	for _, action := range []string{"stop", "logs", "shell", "check", "doctor", "clean", "help", "version", "config", "auth", "bindings", "workspace", "init", "trust", "models"} {
		if isLaunchAction(action) {
			t.Fatalf("%q does not build a guest and must not be gated on the project configuration", action)
		}
	}
}
