package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/etalab-ia/just-code/internal/justcode"
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
var wizardHuhFn = func() bool { return isTTY() && stdoutIsTTY() }

// stdoutIsTTY reports whether standard output is a terminal, using the same
// character-device test as isTTY.
func stdoutIsTTY() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// huhSecret reads one hidden secret through a huh form. It returns
// ErrUserAborted when the user cancels.
func huhSecret(title, description string) (string, error) {
	var value string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Description(description).
				EchoMode(huh.EchoModePassword).
				Value(&value),
		),
	).Run()
	if err != nil {
		return "", err
	}
	return value, nil
}

// huhConfirm asks one yes/no question. It returns ErrUserAborted when the
// user cancels.
func huhConfirm(title, description string, defaultYes bool) (bool, error) {
	var value bool = defaultYes
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Description(description).
				Value(&value),
		),
	).Run()
	if err != nil {
		return false, err
	}
	return value, nil
}

// huhApplyChoice asks the three-way review question as a huh Select:
// apply, edit (reopen the form with the current answers as defaults), or
// cancel. It returns ErrUserAborted when the user cancels.
func huhApplyChoice() (string, error) {
	choice := "apply"
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Apply this configuration?").
				Options(
					huh.NewOption("Apply — write the manifest and lockfile", "apply"),
					huh.NewOption("Edit — reopen the questions with the current answers", "edit"),
					huh.NewOption("Cancel — write nothing", "cancel"),
				).
				Value(&choice),
		),
	).Run()
	if err != nil {
		return "", err
	}
	return choice, nil
}

// askInitQuestionsHuh renders the init wizard as one huh form. Groups the
// command line did not supply are hidden (WithHideFunc on opts.Set), so a
// partial `just-code init --runtime tart` form shows only the unanswered
// questions, matching the line path's skip semantics.
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
		cpusInput := strconv.Itoa(resolvedOrDefault(answers.CPUs, justcode.DefaultSandboxCPUs))
		memoryInput := strconv.Itoa(resolvedOrDefault(answers.MemoryMB, justcode.DefaultSandboxMemoryMB))
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
		if !answers.MCPsSet {
			if pc, err := justcode.DiscoverProject(answers.Root); err == nil {
				if manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root)); err == nil {
					mcps = append([]string(nil), manifest.MCPConnectors...)
					answers.MCPConnectors = append([]string(nil), manifest.MCPConnectors...)
				}
			}
		}

		rootGroup := huh.NewGroup(
			huh.NewInput().
				Title("Project root").
				Description(fmt.Sprintf("Directory the manifest and sandbox config live in. Leave empty to keep %s.", answers.Root)).
				Placeholder(answers.Root).
				Value(&root),
		).Title("Project").
			WithHideFunc(func() bool { return opts.Set["root"] })

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
		if !opts.Set["cpus"] {
			resourceFields = append(resourceFields, huh.NewInput().
				Title("Guest CPUs").
				Description(fmt.Sprintf("1 to %d; the default is %d.", justcode.MaxSandboxCPUs, justcode.DefaultSandboxCPUs)).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return nil
					}
					n, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || n < 1 || n > justcode.MaxSandboxCPUs {
						return fmt.Errorf("cpus must be 1 to %d", justcode.MaxSandboxCPUs)
					}
					return nil
				}).
				Value(&cpusInput))
		}
		if !opts.Set["memory-mb"] {
			resourceFields = append(resourceFields, huh.NewInput().
				Title("Guest memory (MiB)").
				Description(fmt.Sprintf("1 to %d; the default is %d.", justcode.MaxSandboxMemoryMB, justcode.DefaultSandboxMemoryMB)).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return nil
					}
					n, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || n < 1 || n > justcode.MaxSandboxMemoryMB {
						return fmt.Errorf("memory must be 1 to %d MiB", justcode.MaxSandboxMemoryMB)
					}
					return nil
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

		skillsGroup := huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Project skills").
				Description("Selected artifacts are pinned and installed inside the Microsandbox guest. Type / to filter.").
				Filterable(true).
				Options(skillOptions...).
				Value(&skills),
		).Title("Skills").
			WithHideFunc(func() bool { return opts.Set["skills"] || skipSkills })

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

		groups := []*huh.Group{rootGroup}
		for _, g := range []*huh.Group{runtimeGroup, resourceGroup, credentialGroup, skillsGroup, mcpGroup} {
			if g != nil {
				groups = append(groups, g)
			}
		}
		form := huh.NewForm(groups...)
		if err := form.Run(); err != nil {
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
		if cpusInput != strconv.Itoa(resolvedOrDefault(answers.CPUs, justcode.DefaultSandboxCPUs)) {
			answers.CPUs = parsePositiveInt(cpusInput)
		}
		if memoryInput != strconv.Itoa(resolvedOrDefault(answers.MemoryMB, justcode.DefaultSandboxMemoryMB)) {
			answers.MemoryMB = parsePositiveInt(memoryInput)
		}
		answers.CredentialRef = strings.TrimSpace(credentialRef)
		answers.GitHubWorkflow = githubWorkflow
		// Match the line wizard's "Enter keeps" semantics: a form
		// submission only marks the value explicit when it differs from
		// the seeded value, so accepting the defaults preserves the
		// project's existing skill pins and storage mode.
		if !skipSkills {
			answers.SkillsSet = answers.SkillsSet || !equalStringSlices(answers.Skills, skills)
			answers.Skills = skills
		}
		if !opts.Set["mcps"] {
			answers.MCPsSet = answers.MCPsSet || !equalStringSlices(answers.MCPConnectors, mcps)
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

// shouldSkipSkillsField reports whether the skills MultiSelect must be
// omitted from the form: either the flag supplied the selection, or the
// catalogue fetch failed and an empty option list would clobber the
// project's existing skills on submit.
func shouldSkipSkillsField(opts initOptions, catalogueErr error) bool {
	return opts.Set["skills"] || catalogueErr != nil
}
