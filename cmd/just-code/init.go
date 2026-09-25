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

	wizard := justcode.InitWizard{ValidateModel: catalogueModelWarning}
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

// askInitQuestions prompts for the decisions the command line did not supply,
// offering the engine's defaults so an empty answer is always meaningful.
func askInitQuestions(in *bufio.Reader, opts initOptions, answers justcode.InitAnswers) (justcode.InitAnswers, error) {
	fmt.Println("just-code init — project configuration")
	fmt.Println()
	fmt.Printf("Project root [%s]:\n", answers.Root)
	if answer, ok := promptLine(in, "  root: "); ok && strings.TrimSpace(answer) != "" {
		answers.Root = strings.TrimSpace(answer)
	}

	fmt.Println()
	fmt.Println("Sharing mode — where the agent and its credentials run:")
	fmt.Println("  full (default): inside the guest; your machine is only a terminal")
	fmt.Println("  backend: the TUI runs here and attaches to the guest")
	if answer, ok := promptLine(in, "  isolation [full]: "); ok && strings.TrimSpace(answer) != "" {
		answers.Isolation = justcode.Isolation(strings.TrimSpace(answer))
	}

	fmt.Println()
	fmt.Println("Model — leave empty for the built-in default ('just-code models' lists the catalogue):")
	if answer, ok := promptLine(in, "  model: "); ok && strings.TrimSpace(answer) != "" {
		answers.Model = strings.TrimSpace(answer)
	}

	fmt.Println()
	fmt.Printf("Guest resources [%d CPUs, %d MiB]:\n", justcode.DefaultSandboxCPUs, justcode.DefaultSandboxMemoryMB)
	if answer, ok := promptLine(in, "  cpus: "); ok && strings.TrimSpace(answer) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(answer))
		if err != nil {
			return answers, fmt.Errorf("cpus must be a whole number, got %q", answer)
		}
		answers.CPUs = n
	}
	if answer, ok := promptLine(in, "  memory MiB: "); ok && strings.TrimSpace(answer) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(answer))
		if err != nil {
			return answers, fmt.Errorf("memory must be a whole number of MiB, got %q", answer)
		}
		answers.MemoryMB = n
	}

	fmt.Println()
	fmt.Println("Credential — the global Albert credential is used unless you name another (P08 reference):")
	if answer, ok := promptLine(in, "  reference: "); ok && strings.TrimSpace(answer) != "" {
		answers.CredentialRef = strings.TrimSpace(answer)
	}
	return answers, nil
}

// catalogueModelWarning validates a model against the P10 catalogue through the
// same cache the other commands use. An unreachable catalogue is a warning, not
// a refusal: the model is still recorded and 'just-code models' re-checks it.
func catalogueModelWarning(model string) (string, error) {
	apiKey, err := resolveAlbertKeyForCatalogue()
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
