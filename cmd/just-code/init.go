package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// initCmd implements `just-code init` (P12b): the minimal project setup. The
// decisions and the files they produce live in internal/justcode/initwizard.go;
// this file renders the questions and owns the non-TTY contract.
//
//	init                                  # interactive, every question defaulted
//	init --root <dir> --runtime tart ...  # non-interactive, all inputs on the command line
//	init --replace                        # allow replacing an existing manifest
//	init --yes                            # accept the review without asking
//
// Non-TTY contract (P12 acceptance): with no terminal, the command never
// waits. It fails and names exactly which inputs are missing, so a script can
// supply them on the next run.
func initCmd(args []string) (int, error) {
	// Help wins wherever it appears, and needs no terminal.
	for _, a := range args {
		if a == "--help" || a == "-h" {
			initUsage()
			return 0, nil
		}
	}
	opts, err := parseInitArgs(args)
	if err != nil {
		return 2, err
	}
	in := bufio.NewReader(os.Stdin)
	return initRun(opts, in, isTTY())
}

// initOptions is the flag surface, and doubles as the answers already supplied
// on the command line.
type initOptions struct {
	Root            string
	Runtime         string
	Isolation       string
	Model           string
	CPUs            int
	MemoryMB        int
	CredentialRef   string
	GitHub          bool
	Skills          []string
	MCPConnectors   []string
	SkillsLocalOnly bool
	Replace         bool
	Yes             bool
	// FromLaunch records that the launch flow is driving the setup, which only
	// changes the closing message.
	FromLaunch bool
	// Set records which fields came from the command line, so the interactive
	// flow only asks for what is missing and the non-TTY flow knows what to
	// report.
	Set map[string]bool
}

func parseInitArgs(args []string) (initOptions, error) {
	opts := initOptions{Set: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			// A following flag is not a value: `init --root --yes` must be
			// reported as a missing value rather than as a root named
			// "--yes", which would fail later as an incomprehensible path.
			if strings.HasPrefix(args[i+1], "-") {
				return "", fmt.Errorf("%s needs a value, got the flag %q", a, args[i+1])
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--replace":
			opts.Replace = true
		case a == "--yes" || a == "-y":
			opts.Yes = true
		case a == "--github":
			opts.GitHub, opts.Set["github"] = true, true
		case a == "--skill":
			v, err := value()
			if err != nil {
				return opts, err
			}
			if opts.Set["clear-skills"] {
				return opts, fmt.Errorf("--skill and --clear-skills cannot be used together")
			}
			opts.Skills = append(opts.Skills, v)
			opts.Set["skills"] = true
		case a == "--clear-skills":
			if opts.Set["skills"] {
				return opts, fmt.Errorf("--skill and --clear-skills cannot be used together")
			}
			opts.Set["clear-skills"], opts.Set["skills"] = true, true
		case a == "--mcp":
			v, err := value()
			if err != nil {
				return opts, err
			}
			if opts.Set["clear-mcps"] {
				return opts, fmt.Errorf("--mcp and --clear-mcps cannot be used together")
			}
			opts.MCPConnectors = append(opts.MCPConnectors, v)
			opts.Set["mcps"] = true
		case a == "--clear-mcps":
			if opts.Set["mcps"] {
				return opts, fmt.Errorf("--mcp and --clear-mcps cannot be used together")
			}
			opts.Set["clear-mcps"], opts.Set["mcps"] = true, true
		case a == "--local-only-skills" || a == "--versioned-skills":
			if opts.Set["skills-storage"] {
				return opts, fmt.Errorf("choose only one skill storage mode")
			}
			opts.Set["skills-storage"] = true
			opts.SkillsLocalOnly = a == "--local-only-skills"
		case a == "--root":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.Root, opts.Set["root"] = v, true
		case a == "--runtime":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.Runtime, opts.Set["runtime"] = v, true
		case a == "--isolation":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.Isolation, opts.Set["isolation"] = v, true
		case a == "--model":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.Model, opts.Set["model"] = v, true
		case a == "--credential-ref":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.CredentialRef, opts.Set["credential-ref"] = v, true
		case a == "--cpus":
			v, err := value()
			if err != nil {
				return opts, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return opts, fmt.Errorf("--cpus must be a whole number, got %q", v)
			}
			opts.CPUs, opts.Set["cpus"] = n, true
		case a == "--memory-mb":
			v, err := value()
			if err != nil {
				return opts, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return opts, fmt.Errorf("--memory-mb must be a whole number, got %q", v)
			}
			opts.MemoryMB, opts.Set["memory-mb"] = n, true
		default:
			return opts, fmt.Errorf("unknown init option %q (see 'just-code help')", a)
		}
	}
	return opts, nil
}

// initRun is the testable core: the reader and the TTY verdict are passed in,
// so the non-TTY contract can be exercised without a terminal.
func initRun(opts initOptions, in *bufio.Reader, tty bool) (int, error) {
	answers := justcode.InitAnswers{
		Root:               opts.Root,
		Runtime:            justcode.Runtime(opts.Runtime),
		Isolation:          justcode.Isolation(opts.Isolation),
		Model:              opts.Model,
		CPUs:               opts.CPUs,
		MemoryMB:           opts.MemoryMB,
		CredentialRef:      opts.CredentialRef,
		GitHubWorkflow:     opts.GitHub,
		Skills:             opts.Skills,
		MCPConnectors:      opts.MCPConnectors,
		MCPsSet:            opts.Set["mcps"],
		SkillsSet:          opts.Set["skills"],
		SkillsLocalOnly:    opts.SkillsLocalOnly,
		SkillsLocalOnlySet: opts.Set["skills-storage"],
	}
	if answers.Root == "" {
		if cwd, err := os.Getwd(); err == nil {
			answers.Root = cwd
		}
	}
	// The root is the one answer that cannot be defaulted by the engine from
	// nothing, and in a script it is the most likely thing to be wrong.
	if !tty && !opts.Set["root"] {
		return 1, fmt.Errorf("no terminal available: name the project explicitly, e.g. 'just-code init --root <dir> --yes'.\n" +
			"Every question can be answered by a flag: --runtime, --isolation, --model, --cpus, --memory-mb, --credential-ref, --github, --skill, --clear-skills, --mcp, --clear-mcps, --local-only-skills, --versioned-skills, --replace, --yes")
	}
	answers = seedInitAnswersFromManifest(opts, answers)

	// The catalogue check reads the credential reference of the project being
	// configured, not of whatever directory the caller happens to run from,
	// and it resolves the root the same way the engine will (a path inside a
	// worktree refers to the worktree's manifest).
	wizard := justcode.InitWizard{ValidateModel: func(model string) (string, error) {
		root := answers.Root
		if pc, err := justcode.DiscoverProject(root); err == nil {
			root = pc.Root
		}
		return catalogueModelWarningFn(root, model)
	}}
	// Flags are validated before any question: a typo on the command line must
	// not send the user through six prompts to learn about it.
	if err := validateInitOptions(opts); err != nil {
		return 2, err
	}
	var plan justcode.InitPlan
	var err error
	for {
		if tty && !opts.Yes {
			if wizardHuhFn() {
				answers, err = askInitQuestionsHuh(opts, answers)
				if err != nil {
					return 1, err
				}
			} else {
				answers, err = askInitQuestions(in, opts, answers)
				if err != nil {
					return 1, err
				}
			}
		}
		answers = withBrowserResourceGuidance(answers)
		if answers.GitHubWorkflow {
			remote, err := githubInitPreflightFn(answers.Root)
			if err != nil {
				return 1, err
			}
			answers.GitHubRemote = remote
		}
		plan, err = wizard.Plan(answers)
		if err != nil {
			return 1, err
		}
		fmt.Print(justcode.FormatInitReview(plan))
		if plan.ExistingManifest != nil && !opts.Replace {
			return 1, fmt.Errorf("%s already exists; re-run with --replace to overwrite it, or edit it directly", plan.ManifestPath)
		}
		if !tty || opts.Yes {
			break
		}
		if wizardHuhFn() {
			choice, err := huhApplyChoice()
			if err != nil {
				return 1, fmt.Errorf("no answer; nothing was written")
			}
			switch choice {
			case "apply":
			case "cancel":
				return 1, fmt.Errorf("cancelled; nothing was written")
			case "edit":
				fmt.Println("Reopening setup choices; blank answers keep the current selection.")
				continue
			}
			break
		}
		answer, ok := promptLine(in, "\nApply? [Y/n/edit]: ")
		if !ok {
			return 1, fmt.Errorf("no answer; nothing was written")
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			break
		case "n", "no":
			return 1, fmt.Errorf("cancelled; nothing was written")
		case "e", "edit", "back":
			fmt.Println("Reopening setup choices; blank answers keep the current selection.")
			continue
		default:
			return 1, fmt.Errorf("enter yes, no, or edit")
		}
		break
	}
	revokeContext7 := plan.ExistingManifest != nil && containsConnector(plan.ExistingManifest.MCPConnectors, "context7") && !containsConnector(plan.Answers.MCPConnectors, "context7")
	context7Revoked, context7Live := false, false
	if revokeContext7 {
		instance := justcode.InstanceName(plan.Answers.Root, filepath.Base(plan.Answers.Root))
		path := justcode.BindingApprovalsPath(mcpBindingStateDirFn(), instance)
		if err := justcode.RevokeBindingApproval(justcode.DefaultFS, path, justcode.CredentialContext7); err != nil {
			return 1, fmt.Errorf("Context7 credential approval could not be revoked; project selection was not changed: %w", err)
		}
		// The revoke is attempted regardless of the recorded runtime: the
		// recorded runtime can lag a runtime switch, and a preserved sandbox
		// from the previous runtime would keep its proxy registration (and
		// the guest's enabled connector) until its next boot. A nonexistent
		// instance is revoked=false, nil, so the call is safe when no
		// Microsandbox instance was ever created. When the control plane
		// cannot be reached to verify the instance, the deselection proceeds
		// with a warning: the host approval is already revoked (the primary
		// authorization), a stale registration in a preserved sandbox is
		// inert without it, and the next reconcile strips it because absence
		// is authoritative in the binding refresh.
		context7Revoked, context7Live, err = revokeMCPBindingFn(context.Background(), instance, justcode.CredentialContext7)
		if err != nil {
			if errors.Is(err, justcode.ErrLookupFailed) {
				fmt.Fprintf(os.Stderr, "Warning: could not verify the Microsandbox instance %s to remove the Context7 registration (%v); the host approval is revoked and the stale registration will be removed at the next start.\n", instance, err)
				context7Revoked, context7Live = false, false
				err = nil
			} else {
				return 1, fmt.Errorf("Context7 host approval was revoked, but the Microsandbox credential could not be revoked; project selection was not changed: %w", err)
			}
		}
	}
	if err := wizard.Apply(plan, opts.Replace); err != nil {
		if revokeContext7 {
			return 1, fmt.Errorf("project configuration was not written; Context7 authorization was revoked: %w", err)
		}
		return 1, err
	}
	if revokeContext7 {
		if context7Revoked && context7Live {
			fmt.Println("Context7 credential approval revoked and removed from the running guest.")
		} else if context7Revoked {
			fmt.Println("Context7 credential approval revoked and removed from the stopped guest.")
		} else {
			fmt.Println("Context7 credential approval revoked on this host.")
		}
	}
	if plan.Answers.GitHubWorkflow {
		if err := approveGitHubForProjectFn(plan.Answers.Root); err != nil {
			return 1, fmt.Errorf("project configuration was written, but GitHub approval was not recorded; run 'just-code bindings approve github': %w", err)
		}
		fmt.Println("GitHub guest workflow approved on this host; the approval is not stored in project files.")
	}
	// The trailer is advice for the standalone command. When the launch flow
	// drives the setup, the launch is already continuing: telling the user to
	// run it would describe a step the code does not need.
	if opts.FromLaunch {
		fmt.Println("\nConfiguration written. Continuing the launch...")
	} else {
		fmt.Println("\nNext: run 'just-code' in this directory to start the agent in its sealed workspace.")
	}
	return 0, nil
}

func withBrowserResourceGuidance(answers justcode.InitAnswers) justcode.InitAnswers {
	if !justcode.HasBrowserMCPSelection(answers.MCPConnectors) {
		answers.BrowserResourceGuidance = ""
		return answers
	}
	// Recompute on every review pass: the user may have changed CPU or
	// memory settings in the edit form since the previous guidance was made.
	host, err := detectHostResourcesFn()
	answers.BrowserResourceGuidance = justcode.BrowserResourceGuidance(host, answers.CPUs, answers.MemoryMB, err)
	return answers
}

func seedInitAnswersFromManifest(opts initOptions, answers justcode.InitAnswers) justcode.InitAnswers {
	project, err := justcode.DiscoverProject(answers.Root)
	if err != nil {
		return answers
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(project.Root))
	if err != nil {
		return answers
	}
	if !opts.Set["runtime"] {
		answers.Runtime = justcode.Runtime(manifest.Runtime)
	}
	if !opts.Set["isolation"] {
		answers.Isolation = justcode.Isolation(manifest.Isolation)
	}
	if !opts.Set["model"] {
		answers.Model = manifest.Model
	}
	if !opts.Set["cpus"] {
		answers.CPUs = manifest.CPUs
	}
	if !opts.Set["memory-mb"] {
		answers.MemoryMB = manifest.MemoryMB
	}
	if !opts.Set["credential-ref"] {
		answers.CredentialRef = manifest.CredentialRef
	}
	if !opts.Set["mcps"] {
		answers.MCPConnectors = append([]string(nil), manifest.MCPConnectors...)
	}
	if !opts.Set["skills-storage"] {
		answers.SkillsLocalOnly = manifest.SkillsLocalOnly
	}
	return answers
}

func containsConnector(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

var projectSkillCatalogueFn = justcode.ProjectSkillCatalogue
var detectHostResourcesFn = justcode.DetectHostResources

func currentProjectSkillSelection(root string) ([]string, error) {
	project, err := justcode.DiscoverProject(root)
	if err != nil {
		return nil, err
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(project.Root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !manifest.SkillsLocalOnly {
		return append([]string(nil), manifest.Skills...), nil
	}
	path, err := justcode.LocalSkillSelectionsPath(justcode.DefaultStateDir(), project.InstanceName())
	if err != nil {
		return nil, err
	}
	selection, err := justcode.ReadLocalSkillSelections(justcode.DefaultFS, path)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), selection.Skills...), nil
}

// validateInitOptions rejects a bad flag value before any prompt runs. The
// engine validates everything again; this is only about failing early with the
// message a command-line user expects.
func validateInitOptions(opts initOptions) error {
	if opts.Set["skills"] {
		if err := justcode.ValidateProjectSkillIDs(opts.Skills); err != nil {
			return err
		}
	}
	if opts.Set["runtime"] {
		if _, err := justcode.ResolveRuntime(opts.Runtime, ""); err != nil {
			return err
		}
	}
	if opts.Set["isolation"] {
		if _, err := justcode.ResolveIsolation(opts.Isolation, ""); err != nil {
			return err
		}
	}
	// An explicit zero is not "use the default": the flag being present means
	// the caller wants to set the value, and 0 is outside the supported range.
	if opts.Set["cpus"] && (opts.CPUs < 1 || opts.CPUs > justcode.MaxSandboxCPUs) {
		return fmt.Errorf("--cpus must be 1 to %d, got %d", justcode.MaxSandboxCPUs, opts.CPUs)
	}
	if opts.Set["memory-mb"] && (opts.MemoryMB < 1 || opts.MemoryMB > justcode.MaxSandboxMemoryMB) {
		return fmt.Errorf("--memory-mb must be 1 to %d, got %d", justcode.MaxSandboxMemoryMB, opts.MemoryMB)
	}
	if opts.Set["mcps"] {
		if _, err := justcode.ValidateMCPConnectorIDs(opts.MCPConnectors); err != nil {
			return err
		}
	}
	return nil
}

// askInitQuestions prompts only for the decisions the command line did not
// supply, showing the value that will be used if the answer is empty — which
// is the flag's value when one was given, not the engine default.
func askInitQuestions(in *bufio.Reader, opts initOptions, answers justcode.InitAnswers) (justcode.InitAnswers, error) {
	fmt.Println("just-code init — project configuration")
	fmt.Println()
	if !opts.Set["root"] {
		currentRoot := answers.Root
		fmt.Printf("Project root [%s]:\n", answers.Root)
		if answer, ok := promptLine(in, "  root: "); ok && strings.TrimSpace(answer) != "" {
			answers.Root = strings.TrimSpace(answer)
			if answers.Root != currentRoot {
				answers = seedInitAnswersFromManifest(opts, answers)
			}
		}
	}

	if !opts.Set["runtime"] {
		fmt.Println()
		fmt.Println("Runtime — where the guest comes from ('just-code doctor' checks the selected one):")
		fmt.Println("  microsandbox (default): the sealed microVM")
		fmt.Println("  tart / agent-vm: a full VM that mounts the project (see the review)")
		current := answers.Runtime
		if current == "" {
			current = justcode.RuntimeMicrosandbox
		}
		if answer, ok := promptLine(in, "  runtime ["+string(current)+"] (type `default` for microsandbox): "); ok && strings.TrimSpace(answer) != "" {
			if strings.EqualFold(strings.TrimSpace(answer), "default") {
				answers.Runtime = justcode.RuntimeMicrosandbox
			} else {
				answers.Runtime = justcode.Runtime(strings.TrimSpace(answer))
			}
		}
	}

	if !opts.Set["isolation"] {
		fmt.Println()
		fmt.Println("Sharing mode — where the agent and its credentials run:")
		fmt.Println("  full (default): inside the guest; your machine is only a terminal")
		fmt.Println("  backend: the TUI runs here and attaches to the guest")
		// Nothing has supplied this value at this point (the branch requires
		// the flag to be absent), so the effective default is the engine's.
		current := answers.Isolation
		if current == "" {
			current = justcode.IsolationFull
		}
		if answer, ok := promptLine(in, "  isolation ["+string(current)+"] (type `default` for full): "); ok && strings.TrimSpace(answer) != "" {
			if strings.EqualFold(strings.TrimSpace(answer), "default") {
				answers.Isolation = justcode.IsolationFull
			} else {
				answers.Isolation = justcode.Isolation(strings.TrimSpace(answer))
			}
		}
	}

	if !opts.Set["model"] {
		fmt.Println()
		current := answers.Model
		if current == "" {
			current = "built-in default"
		}
		fmt.Println("Model — enter `default` to use the built-in model ('just-code models' lists the catalogue):")
		if answer, ok := promptLine(in, "  model ["+current+"]: "); ok && strings.TrimSpace(answer) != "" {
			if strings.EqualFold(strings.TrimSpace(answer), "default") {
				answers.Model = ""
			} else {
				answers.Model = strings.TrimSpace(answer)
			}
		}
	}

	if !opts.Set["cpus"] || !opts.Set["memory-mb"] {
		fmt.Println()
		fmt.Printf("Guest resources [%d CPUs, %d MiB]:\n", resolvedOrDefault(answers.CPUs, justcode.DefaultSandboxCPUs), resolvedOrDefault(answers.MemoryMB, justcode.DefaultSandboxMemoryMB))
		if !opts.Set["cpus"] {
			if answer, ok := promptLine(in, "  cpus (`default` resets): "); ok && strings.TrimSpace(answer) != "" {
				if strings.EqualFold(strings.TrimSpace(answer), "default") {
					answers.CPUs = 0
				} else {
					n, err := strconv.Atoi(strings.TrimSpace(answer))
					if err != nil {
						return answers, fmt.Errorf("cpus must be a whole number, got %q", answer)
					}
					if n < 1 || n > justcode.MaxSandboxCPUs {
						return answers, fmt.Errorf("cpus must be 1 to %d, got %d", justcode.MaxSandboxCPUs, n)
					}
					answers.CPUs = n
				}
			}
		}
		if !opts.Set["memory-mb"] {
			if answer, ok := promptLine(in, "  memory MiB (`default` resets): "); ok && strings.TrimSpace(answer) != "" {
				if strings.EqualFold(strings.TrimSpace(answer), "default") {
					answers.MemoryMB = 0
				} else {
					n, err := strconv.Atoi(strings.TrimSpace(answer))
					if err != nil {
						return answers, fmt.Errorf("memory must be a whole number of MiB, got %q", answer)
					}
					if n < 1 || n > justcode.MaxSandboxMemoryMB {
						return answers, fmt.Errorf("memory must be 1 to %d MiB, got %d", justcode.MaxSandboxMemoryMB, n)
					}
					answers.MemoryMB = n
				}
			}
		}
	}

	if !opts.Set["credential-ref"] {
		fmt.Println()
		fmt.Println("Credential — the global Albert credential is used unless you name another (P08 reference):")
		current := answers.CredentialRef
		if current == "" {
			current = "global Albert credential"
		}
		if answer, ok := promptLine(in, "  reference ["+current+"] (`default` resets): "); ok && strings.TrimSpace(answer) != "" {
			if strings.EqualFold(strings.TrimSpace(answer), "default") {
				answers.CredentialRef = ""
			} else {
				answers.CredentialRef = strings.TrimSpace(answer)
			}
		}
	}
	if !opts.Set["github"] {
		fmt.Println()
		fmt.Println("GitHub guest workflow (optional): the Microsandbox guest can push branches and open draft PRs to this origin.")
		fmt.Println("It uses the stored GitHub credential through the secret proxy; approval is local to this host and not shared with clones.")
		fmt.Println("Existing approvals remain unchanged; 'just-code bindings revoke github' disables the grant.")
		prompt := "Approve the GitHub workflow for this project? [y/N]: "
		if answers.GitHubWorkflow {
			prompt = "Approve the GitHub workflow for this project? [Y/n]: "
		}
		if answer, ok := promptLine(in, prompt); ok && strings.TrimSpace(answer) != "" {
			answers.GitHubWorkflow = strings.EqualFold(strings.TrimSpace(answer), "y")
		}
	}
	if !opts.Set["skills"] {
		fmt.Println("\nProject skills — selected artifacts are pinned and installed inside the Microsandbox guest.")
		catalogue, err := projectSkillCatalogueFn(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: the pinned project skills catalogue is unavailable: %v\n", err)
		} else {
			printProjectSkills(catalogue)
		}
		if !answers.SkillsSet {
			current, err := currentProjectSkillSelection(answers.Root)
			if err != nil {
				return answers, fmt.Errorf("read current project skills: %w", err)
			}
			answers.Skills = current
		}
	skillInput:
		for {
			current := "none"
			if len(answers.Skills) > 0 {
				current = strings.Join(answers.Skills, ",")
			}
			answer, ok := promptLine(in, "  skills ["+current+"] (IDs, `search <term>`, `list`, `none`; Enter keeps): ")
			if !ok || strings.TrimSpace(answer) == "" {
				break
			}
			value := strings.TrimSpace(answer)
			lower := strings.ToLower(value)
			switch {
			case lower == "none":
				answers.Skills = nil
				answers.SkillsSet = true
				break skillInput
			case lower == "list":
				printProjectSkills(catalogue)
			case strings.HasPrefix(lower, "search "):
				matches := justcode.SearchProjectSkills(catalogue, strings.TrimSpace(value[len("search "):]))
				if len(matches) == 0 {
					fmt.Println("  No matching skills.")
					continue
				}
				printProjectSkills(matches)
			default:
				answers.Skills = nil
				for _, id := range strings.Split(value, ",") {
					answers.Skills = append(answers.Skills, strings.TrimSpace(id))
				}
				answers.SkillsSet = true
				break skillInput
			}
		}
	}
	if !opts.Set["mcps"] {
		fmt.Println("\nMCPs — curated remote services and optional guest-local browser tools:")
		for _, choice := range justcode.MCPSelections() {
			fmt.Printf("  %-16s %s\n", choice.ID, choice.Description)
		}
		current := append([]string(nil), answers.MCPConnectors...)
		if !answers.MCPsSet {
			current = nil
			if pc, err := justcode.DiscoverProject(answers.Root); err == nil {
				if manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root)); err == nil {
					current = manifest.MCPConnectors
				}
			}
			answers.MCPConnectors = append([]string(nil), current...)
		}
		currentLabel := strings.Join(current, ",")
		if currentLabel == "" {
			currentLabel = "none"
		}
		answer, ok := promptLine(in, "  MCP IDs ["+currentLabel+"] (comma-separated; Enter keeps, `none` clears): ")
		if ok && strings.TrimSpace(answer) != "" {
			answers.MCPsSet = true
			answers.MCPConnectors = nil
			if !strings.EqualFold(strings.TrimSpace(answer), "none") {
				for _, id := range strings.Split(answer, ",") {
					answers.MCPConnectors = append(answers.MCPConnectors, strings.TrimSpace(id))
				}
			}
		}
	}
	if justcode.HasBrowserMCPSelection(answers.MCPConnectors) {
		host, detectErr := detectHostResourcesFn()
		answers.BrowserResourceGuidance = justcode.BrowserResourceGuidance(host, answers.CPUs, answers.MemoryMB, detectErr)
		fmt.Println("\n" + answers.BrowserResourceGuidance)
	}
	if !opts.Set["skills-storage"] {
		localOnly := answers.SkillsLocalOnly
		if !answers.SkillsLocalOnlySet {
			if pc, err := justcode.DiscoverProject(answers.Root); err == nil {
				if manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root)); err == nil {
					localOnly = manifest.SkillsLocalOnly
				}
			}
		}
		defaultStorage := "versioned"
		if localOnly {
			defaultStorage = "local-only"
		}
		answer, ok := promptLine(in, "  skill storage (versioned or local-only) ["+defaultStorage+"]: ")
		answers.SkillsLocalOnly = localOnly
		if ok && strings.TrimSpace(answer) != "" {
			switch strings.ToLower(strings.TrimSpace(answer)) {
			case "versioned":
				answers.SkillsLocalOnly = false
			case "local-only":
				answers.SkillsLocalOnly = true
			default:
				return answers, fmt.Errorf("skill storage must be versioned or local-only")
			}
		}
		answers.SkillsLocalOnlySet = true
	}
	return answers, nil
}

func printProjectSkills(skills []justcode.ProjectSkill) {
	for _, skill := range skills {
		label := skill.ID
		if skill.Experimental {
			label += " (EXPERIMENTAL; review before adopting)"
		}
		fmt.Printf("  %-42s %s\n", label, skill.Description)
	}
}

// githubInitPreflightFn resolves the stored token and the host origin before
// the project manifest is written. Tests replace this seam so they do not
// inherit a real keychain or repository remote.
var githubInitPreflightFn = func(root string) (justcode.GitHubRemote, error) {
	if _, err := justcode.ReadStoredCredential(context.Background(), justcode.CredentialGithub); err != nil {
		return justcode.GitHubRemote{}, fmt.Errorf("GitHub workflow requires a stored GitHub credential; add one with 'just-code auth add github': %w", err)
	}
	remote, err := justcode.GitHubOriginForProject(context.Background(), root)
	if err != nil {
		return justcode.GitHubRemote{}, fmt.Errorf("GitHub workflow requires a GitHub.com origin remote: %w", err)
	}
	return remote, nil
}

func approveGitHubForProject(root string) error {
	pc, err := justcode.DiscoverProject(root)
	if err != nil {
		return err
	}
	path := justcode.BindingApprovalsPath(justcode.DefaultStateDir(), pc.InstanceName())
	return justcode.ApproveBinding(justcode.DefaultFS, path, justcode.CredentialGithub)
}

var approveGitHubForProjectFn = approveGitHubForProject
var revokeMCPBindingFn = justcode.RevokeInstanceBinding
var mcpBindingStateDirFn = justcode.DefaultStateDir

// resolvedOrDefault renders the value a reader can reason about: what will
// actually be used, rather than a zero meaning "unset".
func resolvedOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// catalogueModelWarning validates a model against the P10 catalogue through the
// same cache the other commands use. An unreachable catalogue is a warning, not
// a refusal: the model is still recorded and 'just-code models' re-checks it.
// catalogueModelWarningFn is the catalogue validator the flow uses. It is a
// package variable so a test can exercise the flow without an Albert
// credential and without a network: the real validator needs both, and a test
// that silently depends on them fails on the machine that has them.
var catalogueModelWarningFn = catalogueModelWarningForRoot

func catalogueModelWarningForRoot(projectRoot, model string) (string, error) {
	apiKey, err := resolveAlbertKeyForCatalogueAt(projectRoot)
	if err != nil {
		return "the model was recorded unverified: no Albert credential is available to check the catalogue", nil
	}
	cache := justcode.CatalogueCache{
		FS:  justcode.DefaultFS,
		Dir: justcode.DefaultStateDir(),
		Fetch: func(ctx context.Context) (justcode.Catalogue, error) {
			return justcode.FetchCatalogue(ctx, nil, apiKey, justcode.AlbertCatalogueURL())
		},
	}
	catalogue, _, err := cache.Load(context.Background())
	if err != nil {
		return "the model was recorded unverified: the catalogue could not be reached", nil
	}
	if _, ok := catalogue.Find(model); !ok {
		// A listed miss is the user's answer being wrong, not a validation
		// outage: stop so the answer can change.
		return "", fmt.Errorf("the model %q is not in the Albert catalogue; run 'just-code models' for the valid ids", model)
	}
	return "", nil
}

// initUsage is the init-specific help, kept next to the flags it documents so
// the two cannot drift.
func initUsage() {
	fmt.Print(`Usage: just-code init [options]

Configure this project: where the agent runs, which model it uses, how much
machine it gets, which remote MCPs it may use, and which credential it may use. The result is
.just-code/project.json (plus lock.json), read by the launch path.

Options:
  --root <dir>            project directory (default: the current directory;
                          a Git worktree root is used when the directory is
                          inside one)
  --runtime <name>        microsandbox (default), tart or agent-vm
  --isolation <level>     full (default) or backend
  --model <id>            OpenCode model; empty keeps the built-in default
  --cpus <n>              guest CPUs (1-255; default 2)
  --memory-mb <n>         guest memory in MiB (default 4096)
  --credential-ref <ref>  project credential reference (default: the global one)
  --github                enable the protected GitHub guest workflow
  --skill <id>            select an exact catalogue skill (repeatable; e.g. official/rgaa)
  --clear-skills          remove all managed project skills
  --mcp <id>              select data-gouv or context7 (repeatable)
  --clear-mcps            remove all managed remote MCPs
  --local-only-skills     keep skill selection and pins in host state, not the checkout
  --versioned-skills      store skill selection and pins in .just-code/
  --replace               allow replacing an existing manifest
  --yes, -y               accept the review without asking

With no terminal, the command never waits: it fails and names the inputs it is
missing, so a script can supply them on the next run.
`)
}

// isLaunchAction reports whether an action builds a guest, which is what makes
// the project configuration relevant.
func isLaunchAction(action string) bool {
	switch action {
	case "attach", "start", "restart", "recreate":
		return true
	default:
		return false
	}
}

// offerProjectInit runs the project setup when the launch is about to happen
// with no project configuration at all.
//
// The point is not to nag: it is that a zero-flag launch would otherwise
// choose the runtime, the isolation, the model and the guest sizing silently,
// and the user would have no moment at which those choices were visible. With
// no terminal there is nobody to ask, so it fails and names the command that
// answers everything — never a prompt that cannot be answered.
//
// It returns configured=true when the launch may proceed (a configuration
// exists, or the offer was accepted and written).
func offerProjectInit(projectRoot string, parsed parsedArgs) (configured bool, code int, err error) {
	return offerProjectInitWithReader(projectRoot, parsed, nil, isTTY())
}

// offerProjectInitWithReader is the testable core: the reader and the TTY
// verdict are supplied so the offer can be exercised without a terminal.
func offerProjectInitWithReader(projectRoot string, parsed parsedArgs, given *bufio.Reader, tty bool) (configured bool, code int, err error) {
	if _, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(projectRoot)); err == nil {
		return true, 0, nil
	} else if !os.IsNotExist(err) {
		// A present-but-unreadable manifest must not be silently replaced —
		// and it must not be silently ignored either. The launch readers
		// tolerate a broken manifest (so a read-only command keeps working),
		// which means nothing downstream will say so: this is the one place
		// holding the error, so it has to say it.
		fmt.Fprintf(os.Stderr, "Warning: %s cannot be read (%v); the launch will ignore the settings it records.\n"+
			"         Fix or remove it, or run 'just-code init --replace' to write it again.\n",
			justcode.ProjectManifestPath(projectRoot), err)
		return true, 0, nil
	}
	// An explicit flag or variable means the caller already made the choices
	// this offer would collect, so there is nothing to ask about.
	// The environment counts: `RUNTIME=tart just-code start` in CI is a caller
	// who has answered, and prompting someone who cannot answer would turn a
	// working launch into a failure. An empty exported value is not a choice
	// (it selects nothing), which mirrors how the isolation resolution treats
	// it.
	if parsed.runtime != "" || parsed.isolation != "" ||
		strings.TrimSpace(os.Getenv("RUNTIME")) != "" || strings.TrimSpace(os.Getenv("ISOLATION")) != "" {
		return true, 0, nil
	}
	if !tty {
		return false, 1, fmt.Errorf("this project has no configuration (%s) and no terminal is available to ask for one.\n"+
			"Run 'just-code init --root %q --yes' to accept the defaults, or 'just-code init' in a terminal to choose",
			justcode.ProjectManifestPath(projectRoot), projectRoot)
	}

	fmt.Printf("This project has no just-code configuration yet (%s).\n", justcode.ProjectManifestPath(projectRoot))
	// One reader for both steps: a line the user typed ahead (or a piped
	// answer list) must not be split between two buffers.
	in := given
	if in == nil {
		in = bufio.NewReader(os.Stdin)
	}
	if tty && wizardHuhFn() {
		proceed, err := huhConfirm("Configure this project now?", "This project has no just-code configuration yet. The wizard writes the project manifest.", true)
		if err != nil {
			return false, 1, fmt.Errorf("no project configuration: nothing was written. Run 'just-code init' when you want to configure it")
		}
		if !proceed {
			return false, 1, fmt.Errorf("no project configuration: nothing was written. Run 'just-code init' when you want to configure it")
		}
		code, err = initRun(initOptions{Root: projectRoot, FromLaunch: true, Set: map[string]bool{"root": true}}, nil, true)
		if err != nil || code != 0 {
			return false, code, err
		}
		return true, 0, nil
	}
	answer, ok := promptLine(in, "Configure it now? [Y/n]: ")
	if ok && strings.EqualFold(strings.TrimSpace(answer), "n") {
		return false, 1, fmt.Errorf("no project configuration: nothing was written. Run 'just-code init' when you want to configure it")
	}
	code, err = initRun(initOptions{Root: projectRoot, FromLaunch: true, Set: map[string]bool{"root": true}}, in, true)
	if err != nil || code != 0 {
		return false, code, err
	}
	return true, 0, nil
}
