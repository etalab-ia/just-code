package justcode

import (
	"context"
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
// else: where the project is, how the agent is isolated, which model and
// curated remote MCPs it uses, how much machine it gets, and which credential
// it may use. A question whose answer nothing reads is worse than no question:
// instance naming derives from the root, and the versioned location is the
// only supported storage mode.

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
	// GitHubWorkflow records the user's project-init choice to approve the
	// optional GitHub binding on this host. It is not written to the manifest:
	// binding approval is a local trust decision (P09).
	GitHubWorkflow bool
	// GitHubRemote is the sanitized origin shown in the review. It is derived
	// from the host Git config and is not persisted by InitWizard.
	GitHubRemote GitHubRemote
	// Skills are catalogue IDs selected by this init invocation. SkillsSet
	// distinguishes an unchanged empty answer from an explicit deselection.
	Skills    []string
	SkillsSet bool
	// SkillsLocalOnly stores selections and pins outside the checkout. Its
	// boolean has a separate presence bit so a rerun preserves current mode.
	SkillsLocalOnly    bool
	SkillsLocalOnlySet bool
	// MCPConnectors is the selected curated remote connector set. MCPsSet
	// distinguishes an unchanged empty answer from explicit deselection.
	MCPConnectors []string
	MCPsSet       bool
}

// InitWizard validates project setup answers and writes what they imply.
// The seams keep every external dependency out of the flow: the model
// catalogue, the file system, and the review screen.
type InitWizard struct {
	FS FS
	// StateDir contains host-local per-project settings used only when the
	// user explicitly selects local-only skill storage.
	StateDir string
	// ValidateModel checks a model against the Albert catalogue (P10). Nil
	// skips validation; a rejection is an error (the answer must change), an
	// unreachable catalogue is a warning (the model is still recorded).
	ValidateModel func(model string) (warning string, err error)
	// Print receives the review screen. Nil means stdout.
	Print func(string)
	// ResolveSkills pins and caches selected catalogue entries. Nil selects
	// the production pinned catalogue; tests inject fixture content.
	ResolveSkills func(context.Context, []string) ([]ProjectSkill, map[string]SkillLock, error)
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

func (w InitWizard) stateDir() string {
	if w.StateDir != "" {
		return w.StateDir
	}
	return DefaultStateDir()
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
	Warnings            []string
	SkillEntries        []ProjectSkill
	SkillLocks          map[string]SkillLock
	LocalSkillsPath     string
	InstructionsPath    string
	InstructionsBefore  string
	InstructionsAfter   string
	InstructionsChanged bool
}

type managedFileMutation struct {
	path  string
	mode  os.FileMode
	write func() error
}

type managedFileSnapshot struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

func applyManagedFileMutations(fs FS, mutations []managedFileMutation) error {
	snapshots := make([]managedFileSnapshot, len(mutations))
	for i, mutation := range mutations {
		snapshot := managedFileSnapshot{path: mutation.path, mode: mutation.mode}
		if info, err := os.Lstat(mutation.path); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s is a symlink; refusing to replace managed data", mutation.path)
			}
			if info.Mode().IsRegular() {
				snapshot.mode = info.Mode().Perm()
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := fs.ReadFile(mutation.path)
		if err == nil {
			snapshot.data, snapshot.exists = data, true
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("snapshot %s before Apply: %w", mutation.path, err)
		}
		snapshots[i] = snapshot
	}
	for i, mutation := range mutations {
		if err := mutation.write(); err != nil {
			var rollbackErrors []string
			for j := i - 1; j >= 0; j-- {
				snapshot := snapshots[j]
				if snapshot.exists {
					if rollbackErr := atomicWrite(fs, snapshot.path, snapshot.data, snapshot.mode); rollbackErr != nil {
						rollbackErrors = append(rollbackErrors, fmt.Sprintf("restore %s: %v", snapshot.path, rollbackErr))
					}
				} else if rollbackErr := fs.Remove(snapshot.path); rollbackErr != nil && !os.IsNotExist(rollbackErr) {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("remove %s: %v", snapshot.path, rollbackErr))
				}
			}
			if len(rollbackErrors) > 0 {
				return fmt.Errorf("Apply failed (%v) and rollback was incomplete: %s", err, strings.Join(rollbackErrors, "; "))
			}
			return err
		}
	}
	return nil
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
	// Warn only when the user named a directory that is genuinely INSIDE the
	// worktree: the same directory spelled differently (macOS /var for
	// /private/var, a Windows 8.3 short name) is not a surprise worth a
	// warning, and warning about it would be noise on every launch.
	// The absolute form comes first: a relative path never prefix-matches the
	// absolute root, so a relative nested root would lose the notice.
	if abs, err := filepath.Abs(strings.TrimSpace(answers.Root)); err == nil {
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			if resolved != pc.Root && strings.HasPrefix(resolved, pc.Root+string(filepath.Separator)) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("the project root is the Git worktree root %s (not %s): its manifest covers the whole worktree", pc.Root, resolved))
			}
		}
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
	if answers.GitHubWorkflow {
		if rt != RuntimeMicrosandbox {
			return plan, fmt.Errorf("the protected GitHub guest workflow requires the microsandbox runtime")
		}
		remote, err := ParseGitHubRemote(answers.GitHubRemote.URL)
		if err != nil || remote != answers.GitHubRemote {
			return plan, fmt.Errorf("the GitHub guest workflow requires a GitHub.com origin remote")
		}
		plan.Answers.GitHubRemote = remote
	}

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

	// A rerun without skill flags preserves the prior selection and pin. An
	// explicit selection resolves now so the review can show the exact source
	// revision and content digest before Apply changes project files.
	selected := []string(nil)
	localOnly := false
	oldLocalOnly := false
	var lock Lockfile
	if plan.ExistingManifest != nil {
		if !answers.MCPsSet {
			plan.Answers.MCPConnectors = append([]string(nil), plan.ExistingManifest.MCPConnectors...)
		}
		selected = append(selected, plan.ExistingManifest.Skills...)
		oldLocalOnly = plan.ExistingManifest.SkillsLocalOnly
		localOnly = oldLocalOnly
		if oldLocalOnly {
			plan.LocalSkillsPath, err = LocalSkillSelectionsPath(w.stateDir(), pc.InstanceName())
			if err != nil {
				return plan, err
			}
			local, readErr := ReadLocalSkillSelections(fs, plan.LocalSkillsPath)
			if readErr != nil {
				return plan, readErr
			}
			selected = append([]string(nil), local.Skills...)
			plan.SkillLocks = local.Pins
		} else if len(plan.ExistingManifest.Skills) > 0 && !answers.SkillsSet {
			lock, err = ReadLockfile(fs, plan.LockPath)
			if err != nil {
				return plan, err
			}
			plan.SkillLocks = lock.Skills
		}
	}
	if answers.MCPsSet {
		connectors, err := ValidateMCPConnectorIDs(answers.MCPConnectors)
		if err != nil {
			return plan, err
		}
		plan.Answers.MCPConnectors = connectors
	}
	if answers.SkillsLocalOnlySet {
		localOnly = answers.SkillsLocalOnly
	}
	if answers.SkillsSet {
		selected = append([]string(nil), answers.Skills...)
		if len(selected) > 0 {
			if err := validateProjectSkillRuntime(plan.Answers.Runtime, selected); err != nil {
				return plan, err
			}
			resolve := w.ResolveSkills
			if resolve == nil {
				resolve = ResolveProjectSkills
			}
			plan.SkillEntries, plan.SkillLocks, err = resolve(context.Background(), selected)
			if err != nil {
				return plan, err
			}
		} else {
			plan.SkillLocks = map[string]SkillLock{}
		}
	}
	plan.Answers.Skills = selected
	plan.Answers.SkillsSet = answers.SkillsSet
	plan.Answers.SkillsLocalOnly = localOnly
	plan.Answers.SkillsLocalOnlySet = answers.SkillsLocalOnlySet
	if err := validateProjectSkillRuntime(plan.Answers.Runtime, selected); err != nil {
		return plan, err
	}
	if len(selected) > 0 && len(plan.SkillLocks) == 0 {
		return plan, fmt.Errorf("the selected skills have no pinned lock data")
	}
	if len(selected) > 0 {
		if _, err := LoadLockedSkillPackages(selected, plan.SkillLocks); err != nil {
			return plan, err
		}
	}
	if localOnly {
		if plan.LocalSkillsPath == "" {
			plan.LocalSkillsPath, err = LocalSkillSelectionsPath(w.stateDir(), pc.InstanceName())
			if err != nil {
				return plan, err
			}
		}
	}
	wasVersionedWithSkills := plan.ExistingManifest != nil && !oldLocalOnly && len(plan.ExistingManifest.Skills) > 0
	ownsVersionedSkillState := plan.ExistingManifest != nil && plan.ExistingManifest.SchemaVersion >= projectManifestSchemaVersion && (!oldLocalOnly || !localOnly)
	if len(selected) > 0 || wasVersionedWithSkills || ownsVersionedSkillState {
		plan.InstructionsPath = filepath.Join(pc.Root, "AGENTS.md")
		if info, statErr := os.Lstat(plan.InstructionsPath); statErr == nil {
			if !info.Mode().IsRegular() {
				return plan, fmt.Errorf("%s is not a regular file; refusing to change managed instructions", plan.InstructionsPath)
			}
			data, readErr := fs.ReadFile(plan.InstructionsPath)
			if readErr != nil {
				return plan, readErr
			}
			plan.InstructionsBefore = string(data)
		} else if !os.IsNotExist(statErr) {
			return plan, statErr
		}
		instructionsSkills := selected
		instructionsLocks := plan.SkillLocks
		if localOnly {
			instructionsSkills = nil
			instructionsLocks = nil
		}
		plan.InstructionsAfter, err = MergeManagedInstructions(plan.InstructionsBefore, instructionsSkills, instructionsLocks)
		if err != nil {
			return plan, err
		}
		plan.InstructionsChanged = plan.InstructionsBefore != plan.InstructionsAfter
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
	if a.GitHubWorkflow {
		fmt.Fprintf(&b, "  GitHub       guest workflow for %s\n", a.GitHubRemote.Repo)
		b.WriteString("               GitHub token approval is host-local and is not written to the project\n")
	} else {
		b.WriteString("  GitHub       no new approval; existing host-local approvals remain unchanged\n")
	}
	if len(a.MCPConnectors) == 0 {
		b.WriteString("  MCPs         none selected\n")
	} else {
		fmt.Fprintf(&b, "  MCPs         %s\n", strings.Join(a.MCPConnectors, ", "))
		for _, connector := range MCPConnectors() {
			if containsMCPConnector(a.MCPConnectors, connector.ID) {
				fmt.Fprintf(&b, "               %s -> %s (%s; auth: %s)\n", connector.Name, connector.Endpoint, connector.Transport, connector.Credential)
			}
		}
	}
	if len(a.Skills) == 0 {
		b.WriteString("  skills       no managed project skills selected\n")
	} else {
		storage := "versioned in .just-code/lock.json"
		if a.SkillsLocalOnly {
			storage = "host-local; not shared with repository clones"
		}
		fmt.Fprintf(&b, "  skills       %s\n", storage)
		for _, id := range a.Skills {
			lock := plan.SkillLocks[id]
			fmt.Fprintf(&b, "               %s at %s (sha256:%s)\n", id, lock.Revision, lock.SHA256[:12])
		}
		b.WriteString("               selected skills are instruction artifacts, not a security boundary\n")
		if a.SkillsLocalOnly {
			b.WriteString("               local-only selection does not add managed rules to AGENTS.md\n")
		}
	}
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
	if plan.InstructionsChanged {
		before, _ := managedSkillZone(plan.InstructionsBefore)
		after, _ := managedSkillZone(plan.InstructionsAfter)
		b.WriteString("\nAGENTS.md managed-zone diff (all text outside this block is preserved byte-for-byte):\n")
		b.WriteString("If the workspace filter marks AGENTS.md ignored or secret-like, it will not reach the guest without a per-file allow decision.\n")
		if before == "" {
			b.WriteString("  current: no managed skills block\n")
		} else {
			fmt.Fprintf(&b, "--- current ---\n%s", ensureReviewNewline(before))
		}
		if after == "" {
			b.WriteString("+++ proposed: remove the managed block\n")
		} else {
			fmt.Fprintf(&b, "+++ proposed +++\n%s", ensureReviewNewline(after))
		}
	}
	return b.String()
}

func ensureReviewNewline(s string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
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
	if len(pm.Skills) > 0 {
		add("skills", strings.Join(pm.Skills, ", "))
	}
	if pm.SkillsLocalOnly {
		add("skillStorage", "local-only")
	}
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
	// Validate the lock before writing any project file: a malformed or
	// unreadable lock must not leave a new manifest beside discarded pins.
	kept, err := ReadLockfile(fs, plan.LockPath)
	if err != nil {
		return err
	}
	instructionsChanged := false
	instructionsAfter := plan.InstructionsAfter
	if plan.InstructionsPath != "" {
		if info, err := os.Lstat(plan.InstructionsPath); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file; refusing to change managed instructions", plan.InstructionsPath)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		current, err := fs.ReadFile(plan.InstructionsPath)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		ids, locks := plan.Answers.Skills, plan.SkillLocks
		if plan.Answers.SkillsLocalOnly {
			ids, locks = nil, nil
		}
		instructionsAfter, err = MergeManagedInstructions(string(current), ids, locks)
		if err != nil {
			return err
		}
		instructionsChanged = string(current) != instructionsAfter
	}
	var localSelection LocalSkillSelections
	if plan.Answers.SkillsLocalOnly {
		if plan.LocalSkillsPath == "" {
			return fmt.Errorf("local-only skill state path was not resolved")
		}
		localSelection = LocalSkillSelections{Skills: append([]string(nil), plan.Answers.Skills...), Pins: cloneSkillLocks(plan.SkillLocks)}
	}
	// The manifest is secret-free by construction: a credential is referenced
	// by name, never written here. It also carries no host-absolute path, so a
	// teammate who clones the repository gets the same project identity.
	pm := ProjectManifest{
		Runtime:         string(plan.Answers.Runtime),
		Isolation:       string(plan.Answers.Isolation),
		Model:           plan.Answers.Model,
		CPUs:            plan.Answers.CPUs,
		MemoryMB:        plan.Answers.MemoryMB,
		CredentialRef:   plan.Answers.CredentialRef,
		SkillsLocalOnly: plan.Answers.SkillsLocalOnly,
		MCPConnectors:   append([]string(nil), plan.Answers.MCPConnectors...),
	}
	if !plan.Answers.SkillsLocalOnly {
		pm.Skills = append([]string(nil), plan.Answers.Skills...)
	}
	// The lockfile is written even though nothing is pinned yet: it is where
	// the skills and images of later chantiers record their resolved
	// revisions, and an empty lock is the honest "nothing pinned" state. An
	// existing lock is preserved: replacing the manifest is not a reason to
	// discard pins someone else committed.
	// A lock that cannot be read is NOT replaced with an empty one: that would
	// discard whatever pins it held, silently, which is the same refusal the
	// manifest gets above. A missing lock reads as the zero lock, no error.
	lock := Lockfile{Entries: map[string]string{}}
	if len(kept.Entries) > 0 {
		lock.Entries = kept.Entries
	}
	if !plan.Answers.SkillsLocalOnly && len(plan.Answers.Skills) > 0 {
		lock.Skills = cloneSkillLocks(plan.SkillLocks)
	}
	mutations := make([]managedFileMutation, 0, 4)
	if plan.Answers.SkillsLocalOnly {
		mutations = append(mutations, managedFileMutation{
			path: plan.LocalSkillsPath, mode: 0o600,
			write: func() error { return WriteLocalSkillSelections(fs, plan.LocalSkillsPath, localSelection) },
		})
	}
	mutations = append(mutations,
		managedFileMutation{path: plan.ManifestPath, mode: 0o644, write: func() error { return WriteProjectManifest(fs, plan.ManifestPath, pm) }},
		managedFileMutation{path: plan.LockPath, mode: 0o644, write: func() error { return WriteLockfile(fs, plan.LockPath, lock) }},
	)
	if instructionsChanged {
		mutations = append(mutations, managedFileMutation{
			path: plan.InstructionsPath, mode: 0o644,
			write: func() error { return atomicWrite(fs, plan.InstructionsPath, []byte(instructionsAfter), 0o644) },
		})
	}
	if err := applyManagedFileMutations(fs, mutations); err != nil {
		return err
	}
	w.print(fmt.Sprintf("Wrote %s\nWrote %s\n", plan.ManifestPath, plan.LockPath))
	if instructionsChanged {
		w.print(fmt.Sprintf("Updated %s\n", plan.InstructionsPath))
	}
	if plan.Answers.SkillsLocalOnly && len(plan.Answers.Skills) > 0 {
		w.print("Saved host-local project skills outside the checkout.\n")
	}
	return nil
}
