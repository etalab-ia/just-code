package justcode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The minimal project setup (P12b). This file owns the decisions and the
// files they produce; the CLI layer owns the prompts. Splitting them is what
// makes the flow testable without a terminal, and it is the shape P11's global
// setup already uses (setupwizard.go).
//
// What the setup collects is exactly what the launch path applies, and nothing
// else: where the project is, how the agent is isolated, which model it uses,
// how much machine it gets, and which credential it may use. A question whose
// answer nothing reads is worse than no question — so there is no name
// question (instance naming derives from the root, P05) and no storage
// question (the versioned location is the only one with readers). Skills,
// connectors and browser MCPs arrive with their own chantiers.

// InitAnswers is one set of project-setup decisions.
type InitAnswers struct {
	// Root is the project directory. It is required and must exist; it is
	// canonicalized through the same discovery the launch path uses, so the
	// file this writes is the file the launch reads (a Git worktree root, not
	// a subdirectory of one).
	Root string
	// Runtime and Isolation are the agent placement choices. Empty means the
	// built-in default (Microsandbox, full) rather than "unset in the file".
	Runtime   Runtime
	Isolation Isolation
	// Model is the OpenCode model id. Empty keeps the built-in default.
	Model string
	// CPUs and MemoryMB size the guest. Zero means the built-in default.
	CPUs     int
	MemoryMB int
	// CredentialRef names the credential this project may use (a P08
	// reference). Empty means the global default resolution applies.
	CredentialRef string
}

// InitWizard validates project setup answers and writes what they imply.
// The seams keep every external dependency out of the flow: the model
// catalogue, the file system, and the review screen.
type InitWizard struct {
	FS FS
	// ValidateModel checks a model against the Albert catalogue (P10). Nil
	// skips validation; a rejection is an error (the answer must change), an
	// unreachable catalogue is a warning (the model is still recorded).
	ValidateModel func(model string) (warning string, err error)
	// Print receives the review screen. Nil means stdout.
	Print func(string)
}

// Note on the FS seam: it carries the manifest and lockfile. Path validation
// (does the root exist, is it a directory, is it inside a worktree) uses the
// real filesystem, because those are host paths the user is asserting about
// rather than content this component owns.

func (w InitWizard) fs() FS {
	if w.FS != nil {
		return w.FS
	}
	return DefaultFS
}

func (w InitWizard) print(msg string) {
	if w.Print != nil {
		w.Print(msg)
		return
	}
	fmt.Print(msg)
}

// InitPlan is a validated set of answers: what will be written, and what the
// user should know before confirming.
type InitPlan struct {
	Answers InitAnswers
	// ManifestPath and LockPath are the files Apply will write, in the
	// checkout. They are the same paths the launch path reads.
	ManifestPath string
	LockPath     string
	// ExistingManifest is the manifest already on disk, if any. Apply refuses
	// to replace it unless it is told to, so a re-run cannot silently discard
	// decisions someone else committed.
	ExistingManifest *ProjectManifest
	// Warnings are non-fatal notes shown in the review.
	Warnings []string
}

// Plan validates the answers and resolves what they imply. It reads only the
// existing manifest; nothing is written here, so a user can see the review and
// walk away.
func (w InitWizard) Plan(answers InitAnswers) (InitPlan, error) {
	plan := InitPlan{Answers: answers}
	fs := w.fs()

	given := strings.TrimSpace(answers.Root)
	if given == "" {
		return plan, fmt.Errorf("the project root is required")
	}
	// The same discovery the launch path uses, so the two cannot disagree: a
	// directory inside a Git worktree resolves to the worktree root, and
	// writing a manifest anywhere else would be a manifest nothing reads.
	pc, err := DiscoverProject(given)
	if err != nil {
		return plan, fmt.Errorf("project root %s: %w", given, err)
	}
	info, err := os.Stat(pc.Root)
	if err != nil {
		return plan, fmt.Errorf("project root %s: %w", given, err)
	}
	if !info.IsDir() {
		return plan, fmt.Errorf("project root %s is not a directory", pc.Root)
	}
	plan.Answers.Root = pc.Root
	if abs, err := filepath.Abs(given); err == nil && filepath.Clean(abs) != pc.Root {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("the project root is the Git worktree root %s (not %s): its manifest covers the whole worktree", pc.Root, filepath.Clean(abs)))
	}

	runtimeName := answers.Runtime
	if runtimeName == "" {
		runtimeName = RuntimeMicrosandbox
	}
	rt, err := parseRuntimeName(string(runtimeName))
	if err != nil {
		return plan, err
	}
	plan.Answers.Runtime = rt

	iso := answers.Isolation
	if iso == "" {
		iso = IsolationFull
	}
	iso, err = ResolveIsolation(string(iso), "")
	if err != nil {
		return plan, err
	}
	plan.Answers.Isolation = iso

	if model := strings.TrimSpace(answers.Model); model != "" {
		plan.Answers.Model = model
		if w.ValidateModel != nil {
			warning, err := w.ValidateModel(model)
			if err != nil {
				return plan, err
			}
			if warning != "" {
				plan.Warnings = append(plan.Warnings, warning)
			}
		}
	}

	if answers.CPUs != 0 {
		if answers.CPUs < 0 || answers.CPUs > MaxSandboxCPUs {
			return plan, fmt.Errorf("CPUs must be 1 to %d, got %d", MaxSandboxCPUs, answers.CPUs)
		}
	}
	if answers.MemoryMB != 0 {
		if answers.MemoryMB < 0 || answers.MemoryMB > MaxSandboxMemoryMB {
			return plan, fmt.Errorf("memory must be 1 to %d MiB, got %d", MaxSandboxMemoryMB, answers.MemoryMB)
		}
	}

	plan.ManifestPath = ProjectManifestPath(pc.Root)
	plan.LockPath = ProjectLockPath(pc.Root)

	if existing, err := ReadProjectManifest(fs, plan.ManifestPath); err == nil {
		plan.ExistingManifest = &existing
	} else if !os.IsNotExist(err) {
		// A present-but-unreadable manifest is a real problem: the setup must
		// not present a review that quietly ignores it.
		return plan, err
	}
	if !pc.IsGit {
		plan.Warnings = append(plan.Warnings, "this directory is not a Git repository: the manifest in .just-code/ will not be versioned with the project")
	}
	return plan, nil
}

// FormatInitReview renders the confirmation screen: every decision the user
// is about to commit, plus what the guest will actually receive.
func FormatInitReview(plan InitPlan) string {
	var b strings.Builder
	a := plan.Answers
	b.WriteString("Review the project setup:\n")
	fmt.Fprintf(&b, "  project      %s\n", a.Root)
	fmt.Fprintf(&b, "  sharing      %s runtime, isolation %s\n", a.Runtime, a.Isolation)
	if a.Isolation == IsolationFull {
		b.WriteString("               the agent, its TUI and its credentials run inside the guest\n")
	} else {
		b.WriteString("               the agent runs inside the guest and the TUI attaches from this machine\n")
	}
	if a.Model != "" {
		fmt.Fprintf(&b, "  model        %s\n", a.Model)
	} else {
		b.WriteString("  model        the built-in default\n")
	}
	fmt.Fprintf(&b, "  resources    %d CPUs, %d MiB (fixed when the guest is created)\n", resolvedCPUs(a.CPUs), resolvedMemoryMB(a.MemoryMB))
	if a.CredentialRef != "" {
		fmt.Fprintf(&b, "  credential   reference %q\n", a.CredentialRef)
	} else {
		b.WriteString("  credential   the global Albert credential\n")
	}
	// No claim is made here about where the credential VALUE lives: the
	// manifest records a reference, and what each runtime then does with the
	// secret differs (Microsandbox proxies it, Tart and agent-vm hand it to
	// the guest). Promising "it stays on the host" would be false for two of
	// the three.
	b.WriteString("               (the manifest records the reference, never a value)\n")
	b.WriteString("  state        versioned in .just-code/ (shared with whoever clones the repository)\n")
	fmt.Fprintf(&b, "  manifest     %s\n", plan.ManifestPath)
	if a.Runtime == RuntimeMicrosandbox {
		b.WriteString("\nThe guest receives a filtered copy of the project: .env files, ignored files,\n" +
			"symlinks and anything gitleaks flags are excluded by default ('just-code workspace status').\n")
	} else {
		b.WriteString("\nThis runtime MOUNTS the project into the guest: everything in it is readable there,\n" +
			"including any secret it holds (a .env file blocks the start).\n")
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(&b, "\nWarning: %s\n", warning)
	}
	if plan.ExistingManifest != nil {
		b.WriteString("\nA project manifest already exists and would be REPLACED:\n")
		b.WriteString(formatManifestSummary(*plan.ExistingManifest))
	}
	return b.String()
}

func resolvedCPUs(cpus int) int {
	if cpus == 0 {
		return DefaultSandboxCPUs
	}
	return cpus
}

func resolvedMemoryMB(memory int) int {
	if memory == 0 {
		return DefaultSandboxMemoryMB
	}
	return memory
}

// formatManifestSummary lists the settings an existing manifest records, so
// the review shows what would be lost rather than only that something exists.
func formatManifestSummary(pm ProjectManifest) string {
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, fmt.Sprintf("    %-12s %s", label, value))
		}
	}
	add("project", pm.Project)
	add("runtime", pm.Runtime)
	add("isolation", pm.Isolation)
	add("model", pm.Model)
	if pm.CPUs > 0 {
		add("cpus", fmt.Sprintf("%d", pm.CPUs))
	}
	if pm.MemoryMB > 0 {
		add("memory", fmt.Sprintf("%d MiB", pm.MemoryMB))
	}
	add("credential", pm.CredentialRef)
	add("storage", pm.Storage)
	if len(lines) == 0 {
		return "    (the existing manifest sets nothing)\n"
	}
	return strings.Join(lines, "\n") + "\n"
}

// Apply writes the reviewed plan. It refuses to replace an existing manifest
// unless replace is set: overwriting decisions that were committed on purpose
// is not something a re-run should do quietly.
func (w InitWizard) Apply(plan InitPlan, replace bool) error {
	fs := w.fs()
	// Re-read rather than trusting the plan's snapshot: a manifest created
	// between Plan and Apply must not be overwritten without consent, and the
	// guarantee would be only as strong as the snapshot.
	_, readErr := ReadProjectManifest(fs, plan.ManifestPath)
	if readErr == nil && !replace {
		return fmt.Errorf("%s already exists; re-run with the replace option, or edit it directly", plan.ManifestPath)
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		// An unreadable manifest must not be silently replaced either.
		return readErr
	}
	// The manifest is secret-free by construction: a credential is referenced
	// by name, never written here. It also carries no host-absolute path, so a
	// teammate who clones the repository gets the same project identity.
	pm := ProjectManifest{
		Runtime:       string(plan.Answers.Runtime),
		Isolation:     string(plan.Answers.Isolation),
		Model:         plan.Answers.Model,
		CPUs:          plan.Answers.CPUs,
		MemoryMB:      plan.Answers.MemoryMB,
		CredentialRef: plan.Answers.CredentialRef,
	}
	if err := fs.MkdirAll(filepath.Dir(plan.ManifestPath), 0o755); err != nil {
		return err
	}
	if err := WriteProjectManifest(fs, plan.ManifestPath, pm); err != nil {
		return err
	}
	// The lockfile is written even though nothing is pinned yet: it is where
	// the skills and images of later chantiers record their resolved
	// revisions, and an empty lock is the honest "nothing pinned" state. An
	// existing lock is preserved: replacing the manifest is not a reason to
	// discard pins someone else committed.
	lock := Lockfile{Entries: map[string]string{}}
	if kept, err := ReadLockfile(fs, plan.LockPath); err == nil && len(kept.Entries) > 0 {
		lock.Entries = kept.Entries
	}
	if err := WriteLockfile(fs, plan.LockPath, lock); err != nil {
		return err
	}
	w.print(fmt.Sprintf("Wrote %s\nWrote %s\n", plan.ManifestPath, plan.LockPath))
	return nil
}
