package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	lipgloss "charm.land/lipgloss/v2"
	lipglosstable "charm.land/lipgloss/v2/table"
	"github.com/etalab-ia/just-code/internal/justcode"
	"golang.org/x/term"
)

// huh_ui.go renders the interactive surfaces with charmbracelet/huh forms
// (issue #133). The line-based prompts in setup.go/init.go remain the
// non-interactive contract: they answer from a pipe, a script, or a test
// with the same semantics. The routing seam is wizardHuhFn — tests stub it
// to false so the scripted-line tests keep exercising the line path, the
// same pattern as stdinIsTTYFn.

// wizardHuhFn decides which wizard surface runs. Production answers whether
// both stdin and stdout are terminals; a piped stdin (CI, scripts, tests)
// must keep the line path. Tests stub it to false.
var wizardHuhFn = func() bool { return !wizardNoColor && isTTY() && stdoutIsTTY() }

// doctorRendererFn decides whether the assembled diagnostics use the styled
// TUI table. It deliberately does not depend on stdin: a diagnostic must be
// readable when the command is run non-interactively while stdout is a real
// terminal. Tests stub it to force the plain path.
var doctorRendererFn = func() bool { return !wizardNoColor && stdoutIsTTY() }

// wizardNoColor is set by setup --no-color: the huh renderer styles its
// output, so the flag keeps the plain line renderer even on a terminal.
var wizardNoColor bool

// huhRun runs a form with its renderer bound to stdout. The routing check
// (wizardHuhFn) requires stdout to be a terminal, but huh's default
// renderer writes to stderr; binding the renderer to stdout keeps the
// check and the output on the same stream, so `2>file` cannot hide the
// form while it waits for input.
func huhRun(form *huh.Form) error {
	return form.WithProgramOptions(tea.WithOutput(os.Stdout)).Run()
}

// stdoutIsTTY reports whether standard output is a terminal, using the same
// character-device test as isTTY.
func stdoutIsTTY() bool {
	return isTTYFile(os.Stdout)
}

// huhSecret reads one hidden secret through a huh form. It returns
// ErrUserAborted when the user cancels.
func huhSecret(title, description string) (string, error) {
	var value string
	err := huhRun(huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Description(description).
				EchoMode(huh.EchoModePassword).
				Value(&value),
		),
	))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// huhConfirm asks one yes/no question. It returns ErrUserAborted when the
// user cancels.
func huhConfirm(title, description string, defaultYes bool) (bool, error) {
	var value bool = defaultYes
	err := huhRun(huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Description(description).
				Value(&value),
		),
	))
	if err != nil {
		return false, err
	}
	return value, nil
}

// huhApplyChoice presents the configuration table and the three-way review
// question in one huh form: apply, edit or cancel.
func huhApplyChoice(plan justcode.InitPlan) (string, error) {
	choice := "apply"
	err := huhRun(huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Configuration recap").
				Description(huhReviewDescription(plan)),
			huh.NewSelect[string]().
				Title("Apply this configuration?").
				Options(
					huh.NewOption("Apply — write the manifest and lockfile", "apply"),
					huh.NewOption("Edit — reopen the questions with the current answers", "edit"),
					huh.NewOption("Cancel — write nothing", "cancel"),
				).
				Value(&choice),
		),
	))
	if err != nil {
		return "", err
	}
	return choice, nil
}

func parseGuestCPUsInput(value string, maximum int) (guestCPUs int, useDefault bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "default") {
		return 0, true, nil
	}
	guestCPUs, err = strconv.Atoi(value)
	if err != nil || guestCPUs < 1 || guestCPUs > maximum {
		return 0, false, fmt.Errorf("cpus must be 1 to %d, a blank value or 'default' resets", maximum)
	}
	return guestCPUs, false, nil
}

func parseGuestMemoryInput(value string, maximum int) (memoryMB int, useDefault bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "default") {
		return 0, true, nil
	}
	memoryMB, err = justcode.ParseMemorySize(value)
	if err != nil {
		return 0, false, err
	}
	if memoryMB < 1 || memoryMB > maximum {
		return 0, false, fmt.Errorf("memory must be 1 to %s; a blank value or 'default' resets", justcode.FormatMemorySize(maximum))
	}
	return memoryMB, false, nil
}

func huhReviewDescription(plan justcode.InitPlan) string {
	a := plan.Answers
	t := lipglosstable.New().
		Headers("Setting", "Selection").
		Width(huhReviewTableWidth()).
		StyleFunc(func(row, column int) lipgloss.Style {
			if row == lipglosstable.HeaderRow || column == 0 {
				return lipgloss.NewStyle().Bold(true)
			}
			return lipgloss.NewStyle()
		})
	t.Row("Project", a.Root)
	t.Row("Runtime", string(a.Runtime))
	t.Row("Isolation", string(a.Isolation))
	model := a.Model
	if model == "" {
		model = "Built-in default"
	}
	t.Row("Model", model)
	t.Row("Resources", fmt.Sprintf("%d CPUs · %s", plan.GuestCPUs, justcode.FormatMemorySize(plan.GuestMemoryMB)))
	credential := "Global Albert credential (reference only; no secret value is written)"
	if a.CredentialRef != "" {
		credential = fmt.Sprintf("Reference: %s (value is not written)", a.CredentialRef)
	}
	t.Row("Credential", credential)
	github := "No new approval; existing host-local approvals remain unchanged"
	if a.GitHubWorkflow {
		github = fmt.Sprintf("Guest workflow for %s (approval stays host-local)", a.GitHubRemote.Repo)
	}
	t.Row("GitHub", github)
	if len(a.MCPConnectors) == 0 {
		t.Row("MCPs", "None selected")
	} else {
		for _, choice := range justcode.MCPSelections() {
			if !slices.Contains(a.MCPConnectors, choice.ID) {
				continue
			}
			detail := choice.Description
			if !choice.Local {
				for _, connector := range justcode.MCPConnectors() {
					if connector.ID == choice.ID {
						detail = fmt.Sprintf("%s · %s · auth: %s", connector.Endpoint, connector.Transport, connector.Credential)
						break
					}
				}
			}
			t.Row("MCP · "+choice.ID, detail)
		}
	}
	if len(a.Skills) == 0 {
		t.Row("Skills", "None selected")
	} else {
		for _, id := range a.Skills {
			detail := id
			if lock, ok := plan.SkillLocks[id]; ok {
				digest := lock.SHA256
				if len(digest) > 12 {
					digest = digest[:12]
				}
				detail = fmt.Sprintf("%s · revision %s · sha256:%s", id, lock.Revision, digest)
			}
			t.Row("Skill", detail)
		}
	}
	storage := "Versioned in .just-code/"
	if a.SkillsLocalOnly {
		storage = "Host-local; not shared with repository clones"
	}
	t.Row("Skill storage", storage)
	t.Row("Manifest", plan.ManifestPath)
	t.Row("Lockfile", plan.LockPath)
	if plan.LocalSkillsPath != "" {
		t.Row("Local skills", plan.LocalSkillsPath)
	}
	workspace := "Filtered project copy; .env, ignored files and symlinks are excluded by default"
	if a.Runtime != justcode.RuntimeMicrosandbox {
		workspace = "Project mounted into the guest; its files are readable there (.env blocks launch)"
	}
	t.Row("Workspace", workspace)
	t.Row("Apply", "Does not boot or restart a VM; managed MCP changes apply on next launch")
	if plan.ExistingManifest != nil {
		t.Row("Existing manifest", "Will be replaced by this configuration")
	}
	if plan.InstructionsChanged {
		t.Row("AGENTS.md", "Managed skills section changes; unrelated text is preserved")
	}
	if a.BrowserResourceGuidance != "" {
		t.Row("Browser profile", a.BrowserResourceGuidance)
	}
	description := t.Render()
	if len(plan.Warnings) > 0 {
		description += "\n\nWarnings\n"
		for _, warning := range plan.Warnings {
			description += "• " + warning + "\n"
		}
	}
	return escapeHuhNoteMarkup(description)
}

func huhReviewTableWidth() int {
	width := 80
	if detected, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && detected > 0 {
		width = detected - 8
	}
	if width < 48 {
		return 48
	}
	if width > 112 {
		return 112
	}
	return width
}

// huhDiagnosticsDescription renders the assembled support report as styled
// sections. Status is a label, not color alone: the same text survives in the
// accessible/no-color renderer and when a user pastes the output.
func huhDiagnosticsDescription(report justcode.DiagnosticsReport) string {
	var b strings.Builder
	for _, section := range report.Sections {
		t := lipglosstable.New().
			Headers("Check", "Status", "Value").
			Width(huhReviewTableWidth()).
			StyleFunc(func(row, column int) lipgloss.Style {
				if row == lipglosstable.HeaderRow || column == 0 {
					return lipgloss.NewStyle().Bold(true)
				}
				return lipgloss.NewStyle()
			})
		for _, item := range section.Items {
			value := item.Value
			if item.Detail != "" {
				value += " — " + item.Detail
			}
			t.Row(item.Name, string(item.Status), value)
		}
		b.WriteString(section.Title + "\n" + t.String() + "\n")
	}
	return escapeHuhNoteMarkup(strings.TrimRight(b.String(), "\n"))
}

// Note descriptions support inline markdown; escape those markers in table
// data without changing the plain accessible output used by TERM=dumb.
func escapeHuhNoteMarkup(value string) string {
	if os.Getenv("TERM") == "dumb" {
		return value
	}
	return strings.NewReplacer("\\", "\\\\", "_", "\\_", "*", "\\*", "`", "\\`").Replace(value)
}

// askInitQuestionsHuh renders the init wizard as one huh form. Groups the
// command line did not supply are omitted structurally (never added to the
// form), so a partial `just-code init --runtime tart` form shows only the
// unanswered questions, matching the line path's skip semantics — and the
// omission also holds in huh's accessible runner, which ignores hide
// predicates.
func askInitQuestionsHuh(opts initOptions, answers justcode.InitAnswers) (justcode.InitAnswers, error) {
	// The catalogue and curated MCP list do not depend on the project
	// root, so they are fetched once outside the reseed loop below.
	var catalogue []justcode.ProjectSkill
	var catalogueErr error
	if !opts.Set["skills"] {
		catalogue, catalogueErr = projectSkillCatalogueFn(context.Background())
		if catalogueErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: the pinned project skills catalogue is unavailable: %v\n", catalogueErr)
		}
	}

	// A failed catalogue must not clear an existing selection: huh rebuilds
	// a MultiSelect's value from its options on submit, so an empty option
	// list would wipe skills the project already pins. Skip the field
	// instead and leave the answers untouched.
	skipSkills := shouldSkipSkillsField(opts, catalogueErr)

	skillOptions := make([]huh.Option[string], 0, len(catalogue))
	for _, skill := range catalogue {
		label := skill.ID
		if skill.Experimental {
			label += " [experimental]"
		}
		skillOptions = append(skillOptions, huh.NewOption(label, skill.ID))
	}
	mcpOptions := make([]huh.Option[string], 0)
	for _, choice := range justcode.MCPSelections() {
		mcpOptions = append(mcpOptions, huh.NewOption(choice.ID+" — "+choice.Description, choice.ID))
	}

	// The form runs in a loop: when the user changes the project root, the
	// answers are reseeded from the target project's manifest (matching
	// the line wizard) and the form reopens with the reseeded defaults.
	for {
		// Local copies the form binds to; written back to answers on success.
		// The root field starts empty each pass: an empty submission keeps the
		// current root, matching the line wizard's default-in-prompt behavior.
		root := ""
		runtime := string(answers.Runtime)
		if runtime == "" {
			runtime = string(justcode.RuntimeMicrosandbox)
		}
		isolation := string(answers.Isolation)
		if isolation == "" {
			isolation = string(justcode.IsolationFull)
		}
		model := answers.Model
		cpusInput := strconv.Itoa(resolvedOrDefault(answers.CPUs, justcode.RecommendedDefaultGuestCPUs(opts.hostCPUs)))
		memoryInput := justcode.FormatMemorySize(resolvedOrDefault(answers.MemoryMB, justcode.RecommendedDefaultGuestMemoryMB(opts.hostMemoryMB)))
		credentialRef := answers.CredentialRef
		githubWorkflow := answers.GitHubWorkflow
		skills := append([]string(nil), answers.Skills...)
		mcps := append([]string(nil), answers.MCPConnectors...)
		skillsLocalOnly := answers.SkillsLocalOnly

		if !answers.SkillsSet {
			current, err := currentProjectSkillSelection(answers.Root)
			if err != nil {
				return answers, fmt.Errorf("read current project skills: %w", err)
			}
			skills = current
			// The comparison baseline below must match what the form
			// actually shows: an unchanged submission must not look
			// like a new selection and re-resolve existing pins.
			answers.Skills = append([]string(nil), current...)
		}
		skillOptionsPass := retainedSkillOptions(skillOptions, catalogue, skills)
		if !answers.MCPsSet {
			if pc, err := justcode.DiscoverProject(answers.Root); err == nil {
				if manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root)); err == nil {
					mcps = append([]string(nil), manifest.MCPConnectors...)
					answers.MCPConnectors = append([]string(nil), manifest.MCPConnectors...)
				}
			}
		}

		var rootGroup *huh.Group
		if !opts.Set["root"] {
			rootGroup = huh.NewGroup(
				huh.NewInput().
					Title("Project root").
					Description(fmt.Sprintf("Directory the manifest and sandbox config live in. Leave empty to keep %s.", answers.Root)).
					Placeholder(answers.Root).
					Value(&root),
			).Title("Project")
		}

		runtimeFields := make([]huh.Field, 0, 2)
		if !opts.Set["runtime"] {
			runtimeFields = append(runtimeFields, huh.NewSelect[string]().
				Title("Runtime").
				Description("Where the guest comes from ('just-code doctor' checks the selected one).").
				Options(
					huh.NewOption("microsandbox — the sealed microVM (default)", string(justcode.RuntimeMicrosandbox)),
					huh.NewOption("tart — a full VM that mounts the project", string(justcode.RuntimeTart)),
					huh.NewOption("agent-vm — the managed tart VM", string(justcode.RuntimeAgentVM)),
				).
				Value(&runtime))
		}
		if !opts.Set["isolation"] {
			runtimeFields = append(runtimeFields, huh.NewSelect[string]().
				Title("Sharing mode").
				Description("Where the agent and its credentials run.").
				Options(
					huh.NewOption("full — inside the guest; your machine is only a terminal", string(justcode.IsolationFull)),
					huh.NewOption("backend — the TUI runs here and attaches to the guest", string(justcode.IsolationBackend)),
				).
				Value(&isolation))
		}
		var runtimeGroup *huh.Group
		if len(runtimeFields) > 0 {
			runtimeGroup = huh.NewGroup(runtimeFields...).Title("Runtime")
		}

		resourceFields := make([]huh.Field, 0, 3)
		if !opts.Set["model"] {
			resourceFields = append(resourceFields, huh.NewInput().
				Title("Model").
				Description("Leave empty for the built-in default ('just-code models' lists the catalogue).").
				Value(&model))
		}
		if shouldAskGuestCPUs(opts) {
			resourceFields = append(resourceFields, huh.NewInput().
				Title("Guest CPUs").
				Description(fmt.Sprintf("1 to %d CPUs; blank or 'default' restores the implicit default. The host has %d logical CPUs.", opts.maxCPUs, opts.hostCPUs)).
				Validate(func(s string) error {
					_, _, err := parseGuestCPUsInput(s, opts.maxCPUs)
					return err
				}).
				Value(&cpusInput))
		}
		if !opts.Set["memory-mb"] {
			resourceFields = append(resourceFields, huh.NewInput().
				Title("Guest memory").
				Description(fmt.Sprintf("Enter MiB or GiB (e.g. 4G or 2.5G); max %s. Blank or 'default' restores the implicit default.", justcode.FormatMemorySize(opts.maxMemoryMB))).
				Validate(func(s string) error {
					_, _, err := parseGuestMemoryInput(s, opts.maxMemoryMB)
					return err
				}).
				Value(&memoryInput))
		}
		var resourceGroup *huh.Group
		if len(resourceFields) > 0 {
			resourceGroup = huh.NewGroup(resourceFields...).Title("Model and resources")
		}

		credentialFields := make([]huh.Field, 0, 2)
		if !opts.Set["credential-ref"] {
			credentialFields = append(credentialFields, huh.NewInput().
				Title("Credential reference").
				Description("The global Albert credential is used unless you name another (P08 reference).").
				Validate(func(s string) error {
					if strings.HasPrefix(strings.TrimSpace(s), "sk-") {
						return fmt.Errorf("a reference names a stored credential, not a raw key")
					}
					return nil
				}).
				Value(&credentialRef))
		}
		if !opts.Set["github"] {
			credentialFields = append(credentialFields, huh.NewConfirm().
				Title("Approve the GitHub workflow for this project?").
				Description("The Microsandbox guest can push branches and open draft PRs to this origin. It uses the stored GitHub credential through the secret proxy; approval is local to this host. 'just-code bindings revoke github' disables the grant.").
				Value(&githubWorkflow))
		}
		var credentialGroup *huh.Group
		if len(credentialFields) > 0 {
			credentialGroup = huh.NewGroup(credentialFields...).Title("Credentials")
		}

		var skillsGroup *huh.Group
		if !opts.Set["skills"] && !skipSkills {
			skillsGroup = huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Project skills").
					Description("Selected artifacts are pinned and installed inside the Microsandbox guest. Type / to filter.").
					Filterable(true).
					Options(skillOptionsPass...).
					Value(&skills),
			).Title("Skills")
		}

		mcpFields := make([]huh.Field, 0, 2)
		if !opts.Set["mcps"] {
			mcpFields = append(mcpFields, huh.NewMultiSelect[string]().
				Title("MCPs").
				Description("Curated remote services and optional guest-local browser tools.").
				Filterable(true).
				Options(mcpOptions...).
				Value(&mcps))
		}
		if !opts.Set["skills-storage"] {
			mcpFields = append(mcpFields, huh.NewSelect[bool]().
				Title("Skill storage").
				Description("Versioned skills are committed with the project; local-only skills stay on this host.").
				Options(
					huh.NewOption("versioned", false),
					huh.NewOption("local-only", true),
				).
				Value(&skillsLocalOnly))
		}
		var mcpGroup *huh.Group
		if len(mcpFields) > 0 {
			mcpGroup = huh.NewGroup(mcpFields...).Title("MCPs and storage")
		}

		// Groups are omitted structurally rather than via WithHideFunc:
		// huh's accessible runner (TERM=dumb) iterates every group and
		// never evaluates hide predicates, so a hidden group would still
		// prompt there and its submitted value would override the flag.
		var groups []*huh.Group
		for _, g := range []*huh.Group{rootGroup, runtimeGroup, resourceGroup, credentialGroup, skillsGroup, mcpGroup} {
			if g != nil {
				groups = append(groups, g)
			}
		}
		if err := huhRun(huh.NewForm(groups...)); err != nil {
			return answers, err
		}

		// Write the form's answers back; a root change re-seeds every other
		// answer from the target project's manifest (matching the line
		// wizard) and reopens the form with the reseeded defaults.
		newRoot := strings.TrimSpace(root)
		if newRoot == "" {
			newRoot = answers.Root
		}
		rootChanged := newRoot != answers.Root
		if rootChanged {
			answers.Root = newRoot
			answers = seedInitAnswersFromManifest(opts, answers)
			// Flags keep their explicit selection across a root change;
			// only interactively seeded values are reseeded from the new
			// project's manifest.
			if !opts.Set["skills"] {
				answers.SkillsSet = false
			}
			if !opts.Set["mcps"] {
				answers.MCPsSet = false
			}
			continue
		}
		answers.Root = newRoot
		answers.Runtime = justcode.Runtime(runtime)
		answers.Isolation = justcode.Isolation(isolation)
		answers.Model = strings.TrimSpace(model)
		// An accepted default keeps the implicit (zero) value so the
		// project keeps inheriting built-in default changes; only an
		// edited input becomes an explicit number.
		if shouldAskGuestCPUs(opts) {
			cpus, useDefault, err := parseGuestCPUsInput(cpusInput, opts.maxCPUs)
			if err != nil {
				return answers, err
			}
			if useDefault {
				answers.CPUs = 0
			} else if cpus != resolvedOrDefault(answers.CPUs, justcode.RecommendedDefaultGuestCPUs(opts.hostCPUs)) {
				answers.CPUs = cpus
			}
		}
		memoryMB, useDefault, err := parseGuestMemoryInput(memoryInput, opts.maxMemoryMB)
		if err != nil {
			return answers, fmt.Errorf("memory: %w", err)
		}
		if useDefault {
			answers.MemoryMB = 0
		} else if memoryMB != resolvedOrDefault(answers.MemoryMB, justcode.RecommendedDefaultGuestMemoryMB(opts.hostMemoryMB)) {
			answers.MemoryMB = memoryMB
		}
		answers.CredentialRef = strings.TrimSpace(credentialRef)
		answers.GitHubWorkflow = githubWorkflow
		// Match the line wizard's "Enter keeps" semantics: a form
		// submission only marks the value explicit when it differs from
		// the seeded value, so accepting the defaults preserves the
		// project's existing skill pins and storage mode. huh rebuilds a
		// MultiSelect's value in its own option order, so compare as
		// sets: a reordered-but-identical selection is not a change.
		if !skipSkills {
			answers.SkillsSet = answers.SkillsSet || !equalStringSets(answers.Skills, skills)
			answers.Skills = skills
		}
		if !opts.Set["mcps"] {
			answers.MCPsSet = answers.MCPsSet || !equalStringSets(answers.MCPConnectors, mcps)
			answers.MCPConnectors = mcps
		}
		if !opts.Set["skills-storage"] {
			answers.SkillsLocalOnlySet = answers.SkillsLocalOnlySet || answers.SkillsLocalOnly != skillsLocalOnly
			answers.SkillsLocalOnly = skillsLocalOnly
		}
		return answers, nil
	}
}

func parsePositiveInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalStringSets reports whether two slices hold the same strings
// regardless of order, without mutating its inputs.
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string(nil), a...)
	sortedB := append([]string(nil), b...)
	slices.Sort(sortedA)
	slices.Sort(sortedB)
	return slices.Equal(sortedA, sortedB)
}

// shouldSkipSkillsField reports whether the skills MultiSelect must be
// omitted from the form: either the flag supplied the selection, or the
// catalogue fetch failed and an empty option list would clobber the
// project's existing skills on submit.
func shouldSkipSkillsField(opts initOptions, catalogueErr error) bool {
	return opts.Set["skills"] || catalogueErr != nil
}

// retainedSkillOptions appends the project's pinned skill IDs that are
// absent from today's catalogue as retained options. A MultiSelect
// rebuilds its value from its options on submit, so an ID missing from
// the current options would be silently dropped; a retained option keeps
// the pin unless the user explicitly deselects it.
func retainedSkillOptions(options []huh.Option[string], catalogue []justcode.ProjectSkill, selected []string) []huh.Option[string] {
	retained := append([]huh.Option[string](nil), options...)
	for _, id := range selected {
		known := false
		for _, skill := range catalogue {
			if skill.ID == id {
				known = true
				break
			}
		}
		if !known {
			retained = append(retained, huh.NewOption(id+" (pinned at an older revision)", id))
		}
	}
	return retained
}
