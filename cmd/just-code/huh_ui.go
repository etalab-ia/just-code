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
	// Local copies the form binds to; written back to answers on success.
	root := answers.Root
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
	}
	if !answers.MCPsSet {
		if pc, err := justcode.DiscoverProject(answers.Root); err == nil {
			if manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root)); err == nil {
				mcps = append([]string(nil), manifest.MCPConnectors...)
			}
		}
	}

	var catalogue []justcode.ProjectSkill
	var catalogueErr error
	if !opts.Set["skills"] {
		catalogue, catalogueErr = projectSkillCatalogueFn(context.Background())
		if catalogueErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: the pinned project skills catalogue is unavailable: %v\n", catalogueErr)
		}
	}

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

	rootGroup := huh.NewGroup(
		huh.NewInput().
			Title("Project root").
			Description("Directory the manifest and sandbox config live in.").
			Value(&root),
	).Title("Project").
		WithHideFunc(func() bool { return opts.Set["root"] })

	runtimeGroup := huh.NewGroup(
		huh.NewSelect[string]().
			Title("Runtime").
			Description("Where the guest comes from ('just-code doctor' checks the selected one).").
			Options(
				huh.NewOption("microsandbox — the sealed microVM (default)", string(justcode.RuntimeMicrosandbox)),
				huh.NewOption("tart — a full VM that mounts the project", string(justcode.RuntimeTart)),
				huh.NewOption("agent-vm — the managed tart VM", string(justcode.RuntimeAgentVM)),
			).
			Value(&runtime),
		huh.NewSelect[string]().
			Title("Sharing mode").
			Description("Where the agent and its credentials run.").
			Options(
				huh.NewOption("full — inside the guest; your machine is only a terminal", string(justcode.IsolationFull)),
				huh.NewOption("backend — the TUI runs here and attaches to the guest", string(justcode.IsolationBackend)),
			).
			Value(&isolation),
	).Title("Runtime").
		WithHideFunc(func() bool { return opts.Set["runtime"] && opts.Set["isolation"] })

	modelGroup := huh.NewGroup(
		huh.NewInput().
			Title("Model").
			Description("Leave empty for the built-in default ('just-code models' lists the catalogue).").
			Value(&model),
		huh.NewInput().
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
			Value(&cpusInput),
		huh.NewInput().
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
			Value(&memoryInput),
	).Title("Model and resources").
		WithHideFunc(func() bool { return opts.Set["model"] && opts.Set["cpus"] && opts.Set["memory-mb"] })

	credentialGroup := huh.NewGroup(
		huh.NewInput().
			Title("Credential reference").
			Description("The global Albert credential is used unless you name another (P08 reference).").
			Validate(func(s string) error {
				if strings.HasPrefix(strings.TrimSpace(s), "sk-") {
					return fmt.Errorf("a reference names a stored credential, not a raw key")
				}
				return nil
			}).
			Value(&credentialRef),
		huh.NewConfirm().
			Title("Approve the GitHub workflow for this project?").
			Description("The Microsandbox guest can push branches and open draft PRs to this origin. It uses the stored GitHub credential through the secret proxy; approval is local to this host. 'just-code bindings revoke github' disables the grant.").
			Value(&githubWorkflow),
	).Title("Credentials").
		WithHideFunc(func() bool { return opts.Set["credential-ref"] && opts.Set["github"] })

	skillsGroup := huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Project skills").
			Description("Selected artifacts are pinned and installed inside the Microsandbox guest. Type / to filter.").
			Filterable(true).
			Options(skillOptions...).
			Value(&skills),
	).Title("Skills").
		WithHideFunc(func() bool { return opts.Set["skills"] })

	mcpGroup := huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("MCPs").
			Description("Curated remote services and optional guest-local browser tools.").
			Filterable(true).
			Options(mcpOptions...).
			Value(&mcps),
		huh.NewSelect[bool]().
			Title("Skill storage").
			Description("Versioned skills are committed with the project; local-only skills stay on this host.").
			Options(
				huh.NewOption("versioned", false),
				huh.NewOption("local-only", true),
			).
			Value(&skillsLocalOnly),
	).Title("MCPs and storage").
		WithHideFunc(func() bool { return opts.Set["mcps"] && opts.Set["skills-storage"] })

	form := huh.NewForm(rootGroup, runtimeGroup, modelGroup, credentialGroup, skillsGroup, mcpGroup)
	if err := form.Run(); err != nil {
		return answers, err
	}

	answers.Root = strings.TrimSpace(root)
	answers.Runtime = justcode.Runtime(runtime)
	answers.Isolation = justcode.Isolation(isolation)
	answers.Model = strings.TrimSpace(model)
	if n, err := strconv.Atoi(strings.TrimSpace(cpusInput)); err == nil {
		answers.CPUs = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(memoryInput)); err == nil {
		answers.MemoryMB = n
	}
	answers.CredentialRef = strings.TrimSpace(credentialRef)
	answers.GitHubWorkflow = githubWorkflow
	answers.Skills = skills
	answers.SkillsSet = true
	answers.MCPConnectors = mcps
	answers.MCPsSet = true
	answers.SkillsLocalOnly = skillsLocalOnly
	answers.SkillsLocalOnlySet = true
	return answers, nil
}
