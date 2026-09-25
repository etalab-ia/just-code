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
// What the setup collects is deliberately small: where the project is, how the
// agent is isolated, which model it uses, how much machine it gets, which
// credential it may use, and whether the project state is versioned. Anything
// that belongs to a later chantier (skills, connectors, browser MCPs) is not
// asked here, because a question whose answer nothing reads is worse than no
// question.

// InitAnswers is one set of project-setup decisions.
type InitAnswers struct {
	// Root is the canonical project root. It is required and must exist.
	Root string
	// Name is the readable project name used for instance naming (P05). Empty
	// means "derive from the root".
	Name string
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
	// Storage selects where the project state lives: StorageVersioned (the
	// default, inside the checkout) or StorageLocal (host state, for a
	// repository the user does not want to carry .just-code).
	Storage string
}

// Storage backends for the project state.
const (
	// StorageVersioned keeps the manifest in the checkout, so the whole team
	// shares the same project settings.
	StorageVersioned = "versioned"
	// StorageLocal keeps it in host state, for a checkout that must not carry
	// just-code files.
	StorageLocal = "local"
)

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
	// ManifestPath and LockPath are the files Apply will write. With
	// StorageLocal they point into host state instead of the checkout.
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

	root := strings.TrimSpace(answers.Root)
	if root == "" {
		return plan, fmt.Errorf("the project root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return plan, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return plan, fmt.Errorf("project root %s: %w", abs, err)
	}
	if !info.IsDir() {
		return plan, fmt.Errorf("project root %s is not a directory", abs)
	}
	plan.Answers.Root = abs

	name := strings.TrimSpace(answers.Name)
	if name == "" {
		name = filepath.Base(abs)
	}
	if safeFileName(name) == "" {
		return plan, fmt.Errorf("the project name %q cannot be used in an instance name", name)
	}
	plan.Answers.Name = name

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

	switch answers.Storage {
	case "", StorageVersioned:
		plan.Answers.Storage = StorageVersioned
		plan.ManifestPath = ProjectManifestPath(abs)
		plan.LockPath = ProjectLockPath(abs)
	case StorageLocal:
		// The host-local location is derived from HOME by the same helper the
		// resolver documents, so the writer and any future reader agree.
		dir, err := LocalProjectStateDir(abs)
		if err != nil {
			return plan, err
		}
		plan.ManifestPath = filepath.Join(dir, "project.json")
		plan.LockPath = filepath.Join(dir, "lock.json")
	default:
		return plan, fmt.Errorf("storage must be %q or %q, got %q", StorageVersioned, StorageLocal, answers.Storage)
	}

	if existing, err := ReadProjectManifest(fs, plan.ManifestPath); err == nil {
		plan.ExistingManifest = &existing
	} else if !os.IsNotExist(err) {
		// A present-but-unreadable manifest is a real problem: the setup must
		// not present a review that quietly ignores it.
		return plan, err
	}
	if plan.Answers.Storage == StorageVersioned {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
			plan.Warnings = append(plan.Warnings, "this directory is not a Git repository: the manifest in .just-code/ will not be versioned with the project")
		}
	}
	return plan, nil
}

// FormatInitReview renders the confirmation screen: every decision the user
// is about to commit, plus what the guest will actually receive.
func FormatInitReview(plan InitPlan) string {
	var b strings.Builder
	a := plan.Answers
	b.WriteString("Review the project setup:\n")
	fmt.Fprintf(&b, "  project      %s (%s)\n", a.Name, a.Root)
	fmt.Fprintf(&b, "  sharing      %s runtime, isolation %s\n", a.Runtime, a.Isolation)
	if a.Isolation == IsolationFull {
		b.WriteString("               the agent, its TUI and its credentials run inside the guest\n")
	} else {
		b.WriteString("               the agent runs inside the guest and the TUI attaches from this machine\n")
	}
	if a.Model != "" {
		fmt.Fprintf(&b, "  model        %s\n", a.Model)
	} else {
		b.WriteString("  model        built-in default (change later with 'just-code models')\n")
	}
	fmt.Fprintf(&b, "  resources    %d CPUs, %d MiB (fixed when the guest is created)\n", resolvedCPUs(a.CPUs), resolvedMemoryMB(a.MemoryMB))
	if a.CredentialRef != "" {
		fmt.Fprintf(&b, "  credential   reference %q (the value stays in the host credential store)\n", a.CredentialRef)
	} else {
		b.WriteString("  credential   the global Albert credential\n")
	}
	fmt.Fprintf(&b, "  state        %s\n", DescribeStorage(a.Storage))
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

// DescribeStorage renders the storage choice for the review.
func DescribeStorage(storage string) string {
	if storage == StorageLocal {
		return "host state only (nothing is added to the checkout)"
	}
	return "versioned in .just-code/ (shared with whoever clones the repository)"
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
	if plan.ExistingManifest != nil && !replace {
		return fmt.Errorf("%s already exists; re-run with the replace option, or edit it directly", plan.ManifestPath)
	}
	// The manifest is secret-free by construction: a credential is referenced
	// by name, never written here. It also carries no host-absolute path, so a
	// teammate who clones the repository gets the same project identity.
	pm := ProjectManifest{
		Project:       plan.Answers.Name,
		Runtime:       string(plan.Answers.Runtime),
		Isolation:     string(plan.Answers.Isolation),
		Model:         plan.Answers.Model,
		CPUs:          plan.Answers.CPUs,
		MemoryMB:      plan.Answers.MemoryMB,
		CredentialRef: plan.Answers.CredentialRef,
		Storage:       plan.Answers.Storage,
	}
	if err := fs.MkdirAll(filepath.Dir(plan.ManifestPath), 0o755); err != nil {
		return err
	}
	if err := WriteProjectManifest(fs, plan.ManifestPath, pm); err != nil {
		return err
	}
	// The lockfile is written even though nothing is pinned yet: it is where
	// the skills and images of later chantiers record their resolved
	// revisions, and an empty lock is the honest "nothing pinned" state.
	if err := WriteLockfile(fs, plan.LockPath, Lockfile{Entries: map[string]string{}}); err != nil {
		return err
	}
	w.print(fmt.Sprintf("Wrote %s\nWrote %s\n", plan.ManifestPath, plan.LockPath))
	return nil
}
