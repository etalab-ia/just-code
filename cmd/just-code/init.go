package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/etalab-ia/just-code/internal/justcode"
	"github.com/mattn/go-runewidth"
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
	if err := validateInitOptions(opts); err != nil {
		return 2, err
	}
	tty := initTTYFn()
	if tty && !opts.Yes && !initStdoutTTYFn() {
		return 1, fmt.Errorf("interactive init requires stdout to be a terminal; stdout is redirected or points at %s", os.DevNull)
	}
	if tty && !opts.Yes {
		opts = initOptionsWithDisplayedRoot(opts)
		// Keep the preflight offer's reader from consuming input intended for
		// setup's own raw-terminal intro. A one-byte buffer avoids read-ahead.
		in := bufio.NewReaderSize(os.Stdin, 1)
		proceed, code, err := ensureInitSetup(in)
		if err != nil || !proceed {
			return code, err
		}
		if !printInitIntro(opts.Root, in) {
			return 1, fmt.Errorf("cancelled; nothing was written")
		}
		return initRun(opts, in, tty)
	}
	return initRun(opts, bufio.NewReader(os.Stdin), tty)
}

func initOptionsWithDisplayedRoot(opts initOptions) initOptions {
	if opts.Root == "" {
		opts.Root, _ = os.Getwd()
	}
	if opts.Set == nil {
		opts.Set = make(map[string]bool)
	}
	opts.Set["root"] = true
	return opts
}

var initAlbertCredentialCheckFn = func() error {
	_, err := resolveAlbertKeyForDefaultCatalogue()
	return err
}

var runInitSetupFn = setupRunCmd

var initTTYFn = isTTY

var initStdoutTTYFn = stdoutIsTTY

func initSetupNeeded() (bool, error) {
	// Global setup owns the account credential and default settings. A
	// project's credentialRef is not evidence that global setup is missing.
	err := initAlbertCredentialCheckFn()
	if errors.Is(err, justcode.ErrCredentialNotFound) {
		return true, nil
	}
	// A store/keychain failure does not prove setup is missing. Leave the
	// existing init path available rather than converting an uncertain probe
	// into a new blocker.
	return false, nil
}

func ensureInitSetup(in *bufio.Reader) (bool, int, error) {
	needed, err := initSetupNeeded()
	if err != nil || !needed {
		return true, 0, err
	}
	answer, ok := promptLine(in, "La configuration initiale n'a pas été faite. Lancer 'just-code setup' d'abord ? [Y/n]: ")
	if !ok || strings.EqualFold(strings.TrimSpace(answer), "n") {
		fmt.Println("Vous pourrez lancer 'just-code setup' avant de configurer ce projet.")
		return false, 0, nil
	}
	code, err := runInitSetupFn(false)
	if err != nil || code != 0 {
		return false, code, err
	}
	return true, 0, nil
}

func printInitIntro(root string, in *bufio.Reader) bool {
	if root == "" {
		root, _ = os.Getwd()
	}
	project := filepath.Base(filepath.Clean(root))
	if project == "." || project == string(filepath.Separator) || project == "" {
		project = root
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		if rel, err := filepath.Rel(home, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			root = filepath.Join("~", rel)
		}
	}
	fmt.Printf(`Configuration du projet : %s
Dossier : %s

just-code va préparer une machine isolée pour ce projet.
Par défaut, Microsandbox travaille sur une copie filtrée : vos fichiers .env
et les fichiers ignorés par git ne sont pas copiés.
Avec Tart et agent-vm, le dossier du projet est monté dans la machine :
l'agent peut lire et modifier les fichiers qui s'y trouvent.

3 étapes :
  1. Skills (facultatif) : des savoir-faire pour l'agent (sécurité, accessibilité…).
  2. MCP (facultatif) : des services que l'agent peut interroger (data.gouv.fr, documentation…).
  3. GitHub (facultatif) : autoriser l'agent à proposer ses modifications sur ce dépôt.

Les réglages techniques (ressources, isolation…) ont des valeurs par défaut,
modifiables depuis le récapitulatif.

Rien n'est enregistré avant le récapitulatif final.
Appuyez sur Entrée pour commencer, ou sur Ctrl+C pour quitter.
`, project, root)
	_, ok := promptLine(in, "")
	return ok
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
	FromLaunch   bool
	hostCPUs     int
	hostMemoryMB int
	maxCPUs      int
	maxMemoryMB  int
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
			n, err := justcode.ParseMemorySize(v)
			if err != nil {
				return opts, fmt.Errorf("--memory-mb: %w", err)
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
	wizardHostResources := justcode.HostResources{}
	wizard := justcode.InitWizard{
		HostResources: &wizardHostResources,
		ResolveSkills: resolveInitProjectSkillsFn,
		VerifySkills:  verifyInitProjectSkillsFn,
		ValidateModel: func(model string) (string, error) {
			root := answers.Root
			if pc, err := justcode.DiscoverProject(root); err == nil {
				root = pc.Root
			}
			return catalogueModelWarningFn(root, model)
		},
	}
	// Flags are validated before any question: a typo on the command line must
	// not send the user through six prompts to learn about it.
	if err := validateInitOptions(opts); err != nil {
		return 2, err
	}
	hostResources, hostErr := detectHostResourcesFn()
	if hostErr != nil {
		return 1, fmt.Errorf("cannot determine host CPU and memory capacity; cannot enforce the guest resource limit: %v", hostErr)
	}
	if hostResources.CPUs <= 0 || hostResources.MemoryMB <= 0 {
		return 1, fmt.Errorf("host resource detection returned incomplete CPU or memory capacity; cannot enforce the guest resource limit")
	}
	wizardHostResources = hostResources
	opts.hostCPUs = hostResources.CPUs
	opts.hostMemoryMB = hostResources.MemoryMB
	opts.maxCPUs = justcode.MaxGuestCPUs(hostResources.CPUs)
	opts.maxMemoryMB = justcode.MaxGuestMemoryMB(hostResources.MemoryMB)
	if opts.maxCPUs < 1 {
		return 1, fmt.Errorf("host resource detection reported no logical CPUs available for the guest")
	}
	if opts.maxMemoryMB < 1 {
		return 1, fmt.Errorf("host memory is too small to allocate guest memory")
	}
	if opts.Set["cpus"] && opts.CPUs > opts.maxCPUs {
		return 2, fmt.Errorf("--cpus %d exceeds detected host capacity of %d logical CPUs", opts.CPUs, opts.maxCPUs)
	}
	if opts.Set["memory-mb"] && opts.MemoryMB > opts.maxMemoryMB {
		return 2, fmt.Errorf("--memory-mb %s exceeds detected host capacity of %s", justcode.FormatMemorySize(opts.MemoryMB), justcode.FormatMemorySize(opts.maxMemoryMB))
	}
	if !opts.Set["cpus"] && opts.maxCPUs == 1 {
		if answers.CPUs > opts.maxCPUs {
			answers.CPUs = 0
		}
		if tty && !opts.Yes {
			fmt.Println("Only one whole guest CPU is available; selected 1 CPU automatically.")
		}
	}
	var plan justcode.InitPlan
	var err error
	// askQuestions gates the default-path questions. The review's "edit"
	// choice reopens them; the "Réglages avancés" choice (#170) edits only
	// the expert settings and returns straight to the review.
	askQuestions := true
	for {
		useHuhReview := tty && !opts.Yes && wizardHuhFn()
		if tty && !opts.Yes && askQuestions {
			if useHuhReview {
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
		if !opts.Set["cpus"] && opts.maxCPUs == 1 && answers.CPUs > opts.maxCPUs {
			// The only valid whole-core choice is automatic; discard an
			// oversized legacy manifest value so init --replace can repair it.
			answers.CPUs = 0
		}
		answers = withBrowserResourceGuidance(answers)
		guestCPUs := answers.CPUs
		if guestCPUs <= 0 {
			guestCPUs = justcode.RecommendedDefaultGuestCPUs(hostResources.CPUs)
		}
		guestMemoryMB := answers.MemoryMB
		if guestMemoryMB <= 0 {
			guestMemoryMB = justcode.RecommendedDefaultGuestMemoryMB(hostResources.MemoryMB)
		}
		if err := justcode.ValidateGuestResources(hostResources, guestCPUs, guestMemoryMB); err != nil {
			return 1, err
		}
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
		review := justcode.FormatInitReview(plan)
		if plan.ExistingManifest != nil && !opts.Replace {
			fmt.Print(review)
			return 1, fmt.Errorf("%s already exists; re-run with --replace to overwrite it, or edit it directly", plan.ManifestPath)
		}
		if !useHuhReview {
			fmt.Print(review)
		}
		if !tty && !opts.Yes {
			resourceWarnings := justcode.GuestResourceWarnings(hostResources, plan.GuestCPUs, plan.GuestMemoryMB)
			if len(resourceWarnings) > 0 {
				return 1, fmt.Errorf("non-interactive setup with high resource allocation requires explicit confirmation; rerun with --yes")
			}
		}
		if !tty || opts.Yes {
			break
		}
		if useHuhReview {
			choice, err := huhApplyChoice(plan)
			if err != nil {
				return 1, fmt.Errorf("no answer; nothing was written")
			}
			switch choice {
			case "apply":
			case "cancel":
				return 1, fmt.Errorf("cancelled; nothing was written")
			case "edit":
				fmt.Println("Reopening the setup questions with the current answers.")
				askQuestions = true
				continue
			case "advanced":
				askQuestions = false
				updated, advErr := askInitAdvancedHuh(opts, answers)
				if advErr != nil {
					return 1, advErr
				}
				answers = updated
				continue
			}
			break
		}
		answer, ok := promptLine(in, "\nApply? [Y/n/edit/advanced]: ")
		if !ok {
			return 1, fmt.Errorf("no answer; nothing was written")
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "y", "yes":
			break
		case "n", "no":
			return 1, fmt.Errorf("cancelled; nothing was written")
		case "e", "edit", "back":
			fmt.Println("Reopening the setup questions; blank answers keep current values.")
			askQuestions = true
			continue
		case "a", "advanced":
			askQuestions = false
			answers, err = askInitAdvancedLine(in, opts, answers)
			if err != nil {
				return 1, err
			}
			continue
		default:
			return 1, fmt.Errorf("enter yes, no, edit, or advanced")
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
	guestCPUs, guestMemoryMB := answers.CPUs, answers.MemoryMB
	if err == nil {
		if guestCPUs <= 0 {
			guestCPUs = justcode.RecommendedDefaultGuestCPUs(host.CPUs)
		}
		if guestMemoryMB <= 0 {
			guestMemoryMB = justcode.RecommendedDefaultGuestMemoryMB(host.MemoryMB)
		}
	}
	answers.BrowserResourceGuidance = justcode.BrowserResourceGuidance(host, guestCPUs, guestMemoryMB, err)
	return answers
}

func seedInitAnswersFromManifest(opts initOptions, answers justcode.InitAnswers) justcode.InitAnswers {
	// Clear previous-project values first. If the new root has no manifest,
	// this leaves each non-flag answer at the built-in default instead of
	// carrying settings across projects. Flags remain the caller's explicit
	// choices.
	if !opts.Set["runtime"] {
		answers.Runtime = ""
	}
	if !opts.Set["isolation"] {
		answers.Isolation = ""
	}
	if !opts.Set["model"] {
		answers.Model = ""
	}
	if !opts.Set["cpus"] {
		answers.CPUs = 0
	}
	if !opts.Set["memory-mb"] {
		answers.MemoryMB = 0
	}
	if !opts.Set["credential-ref"] {
		answers.CredentialRef = ""
	}
	if !opts.Set["github"] {
		answers.GitHubWorkflow = false
	}
	answers.GitHubRemote = justcode.GitHubRemote{}
	answers.BrowserResourceGuidance = ""
	if !opts.Set["mcps"] {
		answers.MCPConnectors = nil
		answers.MCPsSet = false
	}
	if !opts.Set["skills"] {
		answers.Skills = nil
		answers.SkillsSet = false
	}
	if !opts.Set["skills-storage"] {
		answers.SkillsLocalOnly = false
		answers.SkillsLocalOnlySet = false
	}
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
var resolveInitProjectSkillsFn = justcode.ResolveProjectSkills
var verifyInitProjectSkillsFn = func(ids []string, locks map[string]justcode.SkillLock) error {
	_, err := justcode.LoadLockedSkillPackages(ids, locks)
	return err
}
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
		if _, err := justcode.ValidateMCPNewSelection(opts.MCPConnectors); err != nil {
			return err
		}
	}
	return nil
}

// askInitQuestions prompts only for the decisions the command line did not
// supply, showing the value that will be used if the answer is empty — which
// is the flag's value when one was given, not the engine default. The expert
// settings (runtime, isolation, resources, credential reference) are not
// asked here: they live behind the review's "Réglages avancés" entry (#170).
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

	if !opts.Set["github"] {
		// Resolve the origin and stored credential before offering the
		// choice: a project without a usable GitHub.com origin cannot approve
		// the workflow, and refusing only after the last question threw away
		// every answer (#155). The post-question preflight still gates the
		// explicit --github flag path.
		remote, preflightErr := githubInitPreflightFn(answers.Root)
		if preflightErr != nil {
			fmt.Println()
			fmt.Println("GitHub guest workflow (optional) — unavailable for this project:")
			fmt.Println("  " + preflightErr.Error())
			fmt.Println("Existing approvals remain unchanged; fix the above and re-run 'just-code init' to enable it.")
			answers.GitHubWorkflow = false
			answers.GitHubRemote = justcode.GitHubRemote{}
		} else {
			answers.GitHubRemote = remote
			fmt.Println()
			fmt.Println("GitHub guest workflow (optional): the Microsandbox guest can push branches and open draft PRs to this origin.")
			fmt.Printf("Origin: %s\n", remote.Repo)
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
	}
	if !opts.Set["skills"] {
		catalogue, catalogueErr := projectSkillCatalogueFn(context.Background())
		if catalogueErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: the pinned project skills catalogue is unavailable: %v\n", catalogueErr)
		}
		if !answers.SkillsSet {
			current, err := currentProjectSkillSelection(answers.Root)
			if err != nil {
				return answers, fmt.Errorf("read current project skills: %w", err)
			}
			answers.Skills = current
			if catalogueErr == nil && len(current) == 0 && !projectManifestExists(answers.Root) && initSkillInCatalogue(catalogue, "official/securite-developpement") {
				answers.Skills = []string{"official/securite-developpement"}
				answers.SkillsSet = true
			}
		}
		if catalogueErr == nil {
			for {
				printInitProjectSkills(answers.Skills, catalogue, initSkillsStepPrefix(opts))
				current := strings.Join(initSkillNames(answers.Skills), ",")
				if current == "" {
					current = "aucune"
				}
				answer, ok := promptLine(in, fmt.Sprintf("  Skills sélectionnées (%d) [%s] (noms séparés par des virgules; Entrée garde, `none` efface): ", len(answers.Skills), current))
				if ok && strings.TrimSpace(answer) != "" {
					value := strings.TrimSpace(answer)
					if strings.EqualFold(value, "none") {
						answers.Skills = nil
						answers.SkillsSet = true
					} else {
						selected, err := parseInitSkillSelection(value)
						if err != nil {
							fmt.Printf("  %v. Corrigez votre sélection et réessayez.\n", err)
							continue
						}
						selected = append(selected, retainedInitSkillIDs(answers.Skills, initSkillOptions(catalogue))...)
						answers.Skills = selected
						answers.SkillsSet = true
					}
				}
				if len(answers.Skills) > 0 {
					break
				}
				proceed, err := confirmNoInitSkills(in)
				if err != nil {
					return answers, err
				}
				if proceed {
					answers.SkillsSet = true
					break
				}
			}
			// Skills absent from the catalogue cannot be resolved again:
			// record them so Plan preserves their existing pins verbatim.
			answers.RetainedSkills = retainedInitSkillIDs(answers.Skills, initSkillOptions(catalogue))
		} else {
			fmt.Println("La sélection actuelle est conservée. Relancez `just-code init` lorsque le catalogue est accessible pour modifier les skills.")
		}
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
		defaultStorage := "Avec le projet"
		if localOnly {
			defaultStorage = "Sur ce poste uniquement"
		}
		fmt.Println("Où enregistrer les skills ?")
		fmt.Println("  Avec le projet : elles sont versionnées, un coéquipier qui clone le projet les retrouve.")
		fmt.Println("  Sur ce poste uniquement : rien n'est ajouté au dépôt.")
		answer, ok := promptLine(in, "  stockage ["+defaultStorage+"]: ")
		answers.SkillsLocalOnly = localOnly
		if ok && strings.TrimSpace(answer) != "" {
			switch strings.ToLower(strings.TrimSpace(answer)) {
			case "projet", "avec le projet", "versioned":
				answers.SkillsLocalOnly = false
			case "poste", "sur ce poste uniquement", "local-only":
				answers.SkillsLocalOnly = true
			default:
				return answers, fmt.Errorf("storage must be 'projet' or 'poste'")
			}
		}
		answers.SkillsLocalOnlySet = true
	}
	if !opts.Set["mcps"] {
		fmt.Printf("\n%s\n", justcode.MCPStepTitle)
		fmt.Println("Un MCP donne à l'agent l'accès à un service ou à un outil.")
		current := append([]string(nil), answers.MCPConnectors...)
		haveManifest := false
		if !answers.MCPsSet {
			current = nil
			if pc, err := justcode.DiscoverProject(answers.Root); err == nil {
				if manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root)); err == nil {
					current = manifest.MCPConnectors
					haveManifest = true
				}
			}
		}
		if len(current) == 0 && !haveManifest && !answers.MCPsSet {
			current = append([]string(nil), justcode.DefaultFreshMCPIDs...)
		}
		answers.MCPConnectors = append([]string(nil), current...)
		for {
			for _, choice := range justcode.MCPSelections() {
				marker := "[ ]"
				if containsString(current, choice.ID) {
					marker = "[x]"
				}
				fmt.Printf("  %s %-14s %s\n", marker, choice.ID, choice.Description)
			}
			currentLabel := strings.Join(current, ",")
			if currentLabel == "" {
				currentLabel = "none"
			}
			answer, ok := promptLine(in, "  MCP IDs ["+currentLabel+"] (IDs séparés par des virgules ; Entrée garde la sélection, `none` efface tout) : ")
			if !ok {
				break
			}
			trimmed := strings.TrimSpace(answer)
			if trimmed == "" {
				break
			}
			if strings.EqualFold(trimmed, "none") {
				if !promptNoMCPConfirmation(in) {
					continue
				}
				answers.MCPsSet = true
				answers.MCPConnectors = nil
				current = nil
				break
			}
			var ids []string
			for _, id := range strings.Split(trimmed, ",") {
				ids = append(ids, strings.TrimSpace(id))
			}
			validated, err := justcode.ValidateMCPNewSelection(ids)
			if err != nil {
				fmt.Printf("  %s\n", err)
				continue
			}
			answers.MCPsSet = true
			answers.MCPConnectors = validated
			current = validated
			break
		}
		for _, id := range answers.MCPConnectors {
			if reason, removed := justcode.RemovedMCPIDs[id]; removed {
				fmt.Printf("  %s : %s.\n", id, reason)
			}
		}
		if justcode.HasBrowserMCPSelection(answers.MCPConnectors) {
			fmt.Println(justcode.BrowserDownloadNote)
		}
	}
	if justcode.HasBrowserMCPSelection(answers.MCPConnectors) {
		host, detectErr := detectHostResourcesFn()
		answers.BrowserResourceGuidance = justcode.BrowserResourceGuidance(host, answers.CPUs, answers.MemoryMB, detectErr)
		fmt.Println("\n" + answers.BrowserResourceGuidance)
	}
	return answers, nil
}

// askInitAdvancedLine is the line-wizard half of the "Réglages avancés"
// screen (#170): runtime, isolation and guest resources, reached from the
// review. A bad value re-asks the question instead of aborting, so a typo
// never loses the answers already given. Blank keeps the current value;
// `default` resets to the built-in default.
func askInitAdvancedLine(in *bufio.Reader, opts initOptions, answers justcode.InitAnswers) (justcode.InitAnswers, error) {
	fmt.Println()
	fmt.Println("Réglages avancés")
	fmt.Println("Ces valeurs conviennent à la plupart des projets. Modifiez-les seulement si besoin.")
	fmt.Println("Agent (harnais) : OpenCode (recommandé) — Pi n'est pas encore pris en charge.")
	fmt.Println("Petit modèle : même modèle que le modèle principal — sert aux titres et aux résumés de session.")

	if !opts.Set["runtime"] {
		fmt.Println()
		fmt.Println("Type de machine isolée :")
		fmt.Println("  microsandbox : l'agent travaille sur une copie filtrée du projet (.env et secrets exclus) ; démarrage rapide")
		fmt.Println("  tart : VM macOS complète ; votre dossier est partagé avec la VM")
		fmt.Println("  agent-vm : VM Lima ; votre dossier est partagé avec la VM")
		current := answers.Runtime
		if current == "" {
			current = justcode.RuntimeMicrosandbox
		}
		for {
			answer, ok := promptLine(in, "  machine isolée ["+string(current)+"] (`default` revient à microsandbox) : ")
			trimmed := strings.TrimSpace(answer)
			if !ok || trimmed == "" {
				break
			}
			if strings.EqualFold(trimmed, "default") {
				answers.Runtime = justcode.RuntimeMicrosandbox
				break
			}
			rt, rtErr := justcode.ResolveRuntime(trimmed, "")
			if rtErr != nil {
				fmt.Printf("  %s\n", rtErr)
				continue
			}
			if answers.GitHubWorkflow && rt != justcode.RuntimeMicrosandbox {
				fmt.Println("  Le workflow GitHub du projet nécessite Microsandbox.")
				continue
			}
			answers.Runtime = rt
			break
		}
	}

	if !opts.Set["isolation"] {
		fmt.Println()
		fmt.Println("Mode d'isolation :")
		fmt.Println("  complet : l'agent et son interface tournent dans la machine isolée")
		fmt.Println("  partiel : l'interface tourne sur votre poste, l'agent dans la machine isolée")
		current := answers.Isolation
		if current == "" {
			current = justcode.IsolationFull
		}
		for {
			answer, ok := promptLine(in, "  isolation ["+isolationDisplayName(current)+"] (`default` revient à complet) : ")
			if !ok || strings.TrimSpace(answer) == "" {
				break
			}
			switch strings.ToLower(strings.TrimSpace(answer)) {
			case "default", "défaut":
				answers.Isolation = justcode.IsolationFull
			case "complet", "full":
				answers.Isolation = justcode.IsolationFull
			case "partiel", "backend":
				answers.Isolation = justcode.IsolationBackend
			default:
				fmt.Println("  Choisissez complet ou partiel.")
				continue
			}
			break
		}
	}

	if !opts.Set["cpus"] {
		for {
			answer, ok := promptLine(in, fmt.Sprintf("  processeurs [%d] (maximum sur ce poste : %d ; `default` revient à la valeur par défaut) : ",
				resolvedOrDefault(answers.CPUs, justcode.RecommendedDefaultGuestCPUs(opts.hostCPUs)), opts.maxCPUs))
			trimmed := strings.TrimSpace(answer)
			if !ok || trimmed == "" {
				break
			}
			if strings.EqualFold(trimmed, "default") {
				answers.CPUs = 0
				break
			}
			n, convErr := strconv.Atoi(trimmed)
			if convErr != nil {
				fmt.Println("  Entrez un nombre entier.")
				continue
			}
			if n < 1 {
				fmt.Println("  Au moins 1 processeur.")
				continue
			}
			if n > opts.maxCPUs {
				fmt.Printf("  Maximum sur ce poste : %d processeurs.\n", opts.maxCPUs)
				continue
			}
			answers.CPUs = n
			break
		}
	}

	if !opts.Set["memory-mb"] {
		for {
			answer, ok := promptLine(in, fmt.Sprintf("  mémoire [%s] (6 Go conseillés avec chrome-devtools ; `default` revient à la valeur par défaut) : ",
				justcode.FormatMemorySize(resolvedOrDefault(answers.MemoryMB, justcode.RecommendedDefaultGuestMemoryMB(opts.hostMemoryMB)))))
			trimmed := strings.TrimSpace(answer)
			if !ok || trimmed == "" {
				break
			}
			if strings.EqualFold(trimmed, "default") {
				answers.MemoryMB = 0
				break
			}
			n, convErr := justcode.ParseMemorySize(trimmed)
			if convErr != nil {
				fmt.Println("  Format attendu : 4096, 4G ou 2.5G.")
				continue
			}
			if n < 1 {
				fmt.Println("  Au moins 1 MiB.")
				continue
			}
			if n > opts.maxMemoryMB {
				fmt.Printf("  Maximum sur ce poste : %s.\n", justcode.FormatMemorySize(opts.maxMemoryMB))
				continue
			}
			answers.MemoryMB = n
			break
		}
	}

	if notice := initRuntimeNotice(effectiveInitRuntime(answers)); notice != "" {
		fmt.Println(notice)
	}
	if !opts.Set["model"] {
		for {
			answer, ok := promptLine(in, "  modèle principal ["+initModelDisplayName(answers.Model)+"] ('défaut' reprend le modèle de setup ; Entrée conserve le choix) : ")
			if !ok || strings.TrimSpace(answer) == "" {
				break
			}
			warning, err := validateInitModelInput(answers.Root, answer)
			if err != nil {
				fmt.Printf("  %s\n", err)
				continue
			}
			answers.Model = normalizeInitModelInput(answer)
			if warning != "" {
				fmt.Println("  " + warning)
			}
			break
		}
	}
	return answers, nil
}

// isolationDisplayName renders the isolation mode in the wizard's wording.
func isolationDisplayName(iso justcode.Isolation) string {
	if iso == justcode.IsolationBackend {
		return "partiel"
	}
	return "complet"
}

// promptNoMCPConfirmation asks the zero-selection confirmation and returns
// true when the user keeps an empty MCP set. It reads through the wizard's
// own buffered reader: wrapping `in` in a fresh bufio.Reader here would
// prefetch and discard the answers queued behind this one.
func promptNoMCPConfirmation(in *bufio.Reader) bool {
	fmt.Print("Aucun MCP sélectionné. Continuer sans MCP ? (O/n) ")
	answer, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "" || answer == "o" || answer == "oui" || answer == "y" || answer == "yes"
}

func printProjectSkills(skills []justcode.ProjectSkill) {
	for _, skill := range skills {
		label := skill.ID
		if skill.Experimental {
			label += " [experimental]"
		}
		desc := strings.Join(strings.Fields(skill.Description), " ")
		fmt.Println(truncateLine(fmt.Sprintf("  %-36s %s", label, desc), 78))
	}
}

func printInitProjectSkills(selected []string, catalogue []justcode.ProjectSkill, stepPrefix string) {
	selectedSet := make(map[string]bool, len(selected))
	for _, id := range selected {
		selectedSet[id] = true
	}
	fmt.Println("\n" + initSkillsTitle(len(selected), stepPrefix))
	fmt.Println("Une skill apporte à l'agent un savoir-faire de l'État : règles, références, exemples.")
	fmt.Println("Il s'en sert quand votre demande s'y rapporte.")
	for _, id := range initSkillOrder {
		check := " "
		if selectedSet[id] {
			check = "x"
		}
		name := strings.TrimPrefix(id, "official/")
		line := fmt.Sprintf("  [%s] %s %s", check, name, initSkillDescriptions[id])
		if ansi.StringWidth(line) > 80 {
			fmt.Printf("  [%s] %s\n      %s\n", check, name, initSkillDescriptions[id])
		} else {
			fmt.Println(line)
		}
	}
	fmt.Println("Saisissez un ou plusieurs noms séparés par des virgules. Entrée garde la sélection.")
	fmt.Println(initSkillsCountLabel(len(selected)))
	retained := retainedInitSkillIDs(selected, initSkillOptions(catalogue))
	if len(retained) > 0 {
		fmt.Printf("Skills déjà configurées hors de la liste conservées si vous saisissez de nouveaux noms : %s. `none` efface tout.\n",
			strings.Join(initSkillNames(retained), ", "))
	}
}

func initSkillsCountLabel(count int) string {
	if count == 1 {
		return "1 skill sélectionnée."
	}
	return fmt.Sprintf("%d skills sélectionnées.", count)
}

func initSkillNames(ids []string) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.HasPrefix(id, "official/") {
			names = append(names, strings.TrimPrefix(id, "official/"))
		} else {
			names = append(names, id)
		}
	}
	return names
}

func parseInitSkillSelection(input string) ([]string, error) {
	byName := make(map[string]string, len(initSkillOrder))
	for _, id := range initSkillOrder {
		byName[strings.TrimPrefix(id, "official/")] = id
		byName[id] = id
	}
	var selected []string
	seen := make(map[string]bool)
	for _, name := range strings.Split(input, ",") {
		name = strings.TrimSpace(name)
		id, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown skill %q; choose one of the six listed names", name)
		}
		if !seen[id] {
			selected = append(selected, id)
			seen[id] = true
		}
	}
	return selected, nil
}

func confirmNoInitSkills(in *bufio.Reader) (bool, error) {
	fmt.Println("Aucune skill sélectionnée. Continuer sans skill ? (O/n)")
	answer, ok := promptLine(in, "  ")
	if !ok {
		return false, errors.New("no answer for the empty skill selection confirmation")
	}
	answer = strings.TrimSpace(answer)
	return answer == "" || strings.EqualFold(answer, "o") || strings.EqualFold(answer, "oui") || strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

func projectManifestExists(root string) bool {
	pc, err := justcode.DiscoverProject(root)
	if err != nil {
		return false
	}
	_, err = justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(pc.Root))
	return err == nil
}

// truncateLine caps the composed line at n terminal cells, so one catalogue
// entry never wraps on an 80-column terminal. Width is measured in display
// cells (a CJK or emoji rune occupies two), and truncation stays on rune
// boundaries, so wide characters are never split mid-sequence.
func truncateLine(s string, n int) string {
	if ansi.StringWidth(s) <= n {
		return s
	}
	var b strings.Builder
	width := 0
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if width+w > n-1 {
			break
		}
		b.WriteRune(r)
		width += w
	}
	b.WriteString("…")
	return b.String()
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
  --cpus <n>              guest CPUs (max detected host capacity)
  --memory-mb <size>      guest memory in MiB or GiB (e.g. 4096, 4G, 2.5G; max detected host RAM)
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
	if given == nil && !stdoutIsTTY() {
		return false, 1, fmt.Errorf("interactive project setup requires stdout to be a terminal; stdout is redirected or points at %s", os.DevNull)
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
