package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

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
	input := root + "\n\n\n\n\n\n\n" // root, then empty answers, then apply
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
