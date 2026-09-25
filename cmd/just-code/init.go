package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
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
	Root          string
	Runtime       string
	Isolation     string
	Model         string
	CPUs          int
	MemoryMB      int
	CredentialRef string
	Replace       bool
	Yes           bool
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
		Root:          opts.Root,
		Runtime:       justcode.Runtime(opts.Runtime),
		Isolation:     justcode.Isolation(opts.Isolation),
		Model:         opts.Model,
		CPUs:          opts.CPUs,
		MemoryMB:      opts.MemoryMB,
		CredentialRef: opts.CredentialRef,
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
			"Every question can be answered by a flag: --runtime, --isolation, --model, --cpus, --memory-mb, --credential-ref, --replace, --yes")
	}

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
	if tty && !opts.Yes {
		var err error
		answers, err = askInitQuestions(in, opts, answers)
		if err != nil {
			return 1, err
		}
	}

	plan, err := wizard.Plan(answers)
	if err != nil {
		return 1, err
	}
	fmt.Print(justcode.FormatInitReview(plan))
	if plan.ExistingManifest != nil && !opts.Replace {
		return 1, fmt.Errorf("%s already exists; re-run with --replace to overwrite it, or edit it directly", plan.ManifestPath)
	}
	if tty && !opts.Yes {
		answer, ok := promptLine(in, "\nApply? [Y/n]: ")
		if !ok {
			return 1, fmt.Errorf("no answer; nothing was written")
		}
		if strings.EqualFold(strings.TrimSpace(answer), "n") {
			return 1, fmt.Errorf("cancelled; nothing was written")
		}
	}
	if err := wizard.Apply(plan, opts.Replace); err != nil {
		return 1, err
	}
	fmt.Println("\nNext: run 'just-code' in this directory to start the agent in its sealed workspace.")
	return 0, nil
}

// validateInitOptions rejects a bad flag value before any prompt runs. The
// engine validates everything again; this is only about failing early with the
// message a command-line user expects.
func validateInitOptions(opts initOptions) error {
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
	return nil
}

// askInitQuestions prompts only for the decisions the command line did not
// supply, showing the value that will be used if the answer is empty — which
// is the flag's value when one was given, not the engine default.
func askInitQuestions(in *bufio.Reader, opts initOptions, answers justcode.InitAnswers) (justcode.InitAnswers, error) {
	fmt.Println("just-code init — project configuration")
	fmt.Println()
	if !opts.Set["root"] {
		fmt.Printf("Project root [%s]:\n", answers.Root)
		if answer, ok := promptLine(in, "  root: "); ok && strings.TrimSpace(answer) != "" {
			answers.Root = strings.TrimSpace(answer)
		}
	}

	if !opts.Set["runtime"] {
		fmt.Println()
		fmt.Println("Runtime — where the guest comes from ('just-code doctor' checks the selected one):")
		fmt.Println("  microsandbox (default): the sealed microVM")
		fmt.Println("  tart / agent-vm: a full VM that mounts the project (see the review)")
		if answer, ok := promptLine(in, "  runtime [microsandbox]: "); ok && strings.TrimSpace(answer) != "" {
			answers.Runtime = justcode.Runtime(strings.TrimSpace(answer))
		}
	}

	if !opts.Set["isolation"] {
		fmt.Println()
		fmt.Println("Sharing mode — where the agent and its credentials run:")
		fmt.Println("  full (default): inside the guest; your machine is only a terminal")
		fmt.Println("  backend: the TUI runs here and attaches to the guest")
		// Nothing has supplied this value at this point (the branch requires
		// the flag to be absent), so the effective default is the engine's.
		fmt.Printf("  isolation [%s]: ", justcode.IsolationFull)
		if answer, ok := promptLine(in, ""); ok && strings.TrimSpace(answer) != "" {
			answers.Isolation = justcode.Isolation(strings.TrimSpace(answer))
		}
	}

	if !opts.Set["model"] {
		fmt.Println()
		fmt.Println("Model — leave empty for the built-in default ('just-code models' lists the catalogue):")
		if answer, ok := promptLine(in, "  model: "); ok && strings.TrimSpace(answer) != "" {
			answers.Model = strings.TrimSpace(answer)
		}
	}

	if !opts.Set["cpus"] || !opts.Set["memory-mb"] {
		fmt.Println()
		fmt.Printf("Guest resources [%d CPUs, %d MiB]:\n", resolvedOrDefault(answers.CPUs, justcode.DefaultSandboxCPUs), resolvedOrDefault(answers.MemoryMB, justcode.DefaultSandboxMemoryMB))
		if !opts.Set["cpus"] {
			if answer, ok := promptLine(in, "  cpus: "); ok && strings.TrimSpace(answer) != "" {
				n, err := strconv.Atoi(strings.TrimSpace(answer))
				if err != nil {
					return answers, fmt.Errorf("cpus must be a whole number, got %q", answer)
				}
				// Symmetry with --cpus: a typed 0 is out of range, not a
				// secret way to mean "default" (leave the answer empty).
				if n < 1 || n > justcode.MaxSandboxCPUs {
					return answers, fmt.Errorf("cpus must be 1 to %d, got %d", justcode.MaxSandboxCPUs, n)
				}
				answers.CPUs = n
			}
		}
		if !opts.Set["memory-mb"] {
			if answer, ok := promptLine(in, "  memory MiB: "); ok && strings.TrimSpace(answer) != "" {
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

	if !opts.Set["credential-ref"] {
		fmt.Println()
		fmt.Println("Credential — the global Albert credential is used unless you name another (P08 reference):")
		if answer, ok := promptLine(in, "  reference: "); ok && strings.TrimSpace(answer) != "" {
			answers.CredentialRef = strings.TrimSpace(answer)
		}
	}
	return answers, nil
}

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
machine it gets, and which credential it may use. The result is
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
		// A present-but-unreadable manifest is reported by the paths that own
		// that decision; it must not be silently replaced here.
		return true, 0, nil
	}
	// An explicit flag or variable means the caller already made the choices
	// this offer would collect, so there is nothing to ask about.
	if parsed.runtime != "" || parsed.isolation != "" {
		return true, 0, nil
	}
	if !tty {
		return false, 1, fmt.Errorf("this project has no configuration (%s) and no terminal is available to ask for one.\n"+
			"Run 'just-code init --root %s --yes' to accept the defaults, or 'just-code init' in a terminal to choose",
			justcode.ProjectManifestPath(projectRoot), projectRoot)
	}

	fmt.Printf("This project has no just-code configuration yet (%s).\n", justcode.ProjectManifestPath(projectRoot))
	// One reader for both steps: a line the user typed ahead (or a piped
	// answer list) must not be split between two buffers.
	in := given
	if in == nil {
		in = bufio.NewReader(os.Stdin)
	}
	answer, ok := promptLine(in, "Configure it now? [Y/n]: ")
	if ok && strings.EqualFold(strings.TrimSpace(answer), "n") {
		return false, 1, fmt.Errorf("no project configuration: nothing was written. Run 'just-code init' when you want to configure it")
	}
	code, err = initRun(initOptions{Root: projectRoot, Set: map[string]bool{"root": true}}, in, true)
	if err != nil || code != 0 {
		return false, code, err
	}
	return true, 0, nil
}
