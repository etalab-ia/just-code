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

// setupCmd implements `just-code setup` (P11): the global setup wizard.
// Stages: preflight (read-only, fatal blockers first — no credential prompt
// before a fatal virtualization failure) -> Albert credential (masked,
// validated; rejection vs network failure distinguished) -> optional GitHub
// credential -> editable git identity -> model selection -> review ->
// apply (settings + managed runtime). An interrupted setup resumes at the
// unfinished stage via the host-state journal.
//
//	setup              # interactive wizard
//	setup doctor       # read-only diagnosis, no prompts, no writes
//	setup --fallback   # use the consented file credential store
//	setup --no-color   # plain terminal output
//
// The plain line-based flow is the retained rendering: it works on every
// terminal, survives pasting, degrades to non-TTY, and needs no external
// renderer dependency. A maintained TUI renderer can replace the rendering
// layer later without touching the engine (internal/justcode/setupwizard.go).
func setupCmd(args []string) (int, error) {
	sub := ""
	fallback, noColor := false, false
	for _, a := range args {
		switch a {
		case "doctor":
			sub = "doctor"
		case "--fallback":
			fallback = true
		case "--no-color":
			noColor = true
		default:
			return 2, fmt.Errorf("Unknown setup argument: %s (expected doctor, --fallback or --no-color)", a)
		}
	}
	_ = noColor // the plain renderer emits no color; the flag is accepted for forward compatibility
	if sub == "doctor" {
		return setupDoctorCmd()
	}
	return setupRunCmd(fallback)
}

// setupDoctorCmd prints the read-only diagnosis.
func setupDoctorCmd() (int, error) {
	ctx := context.Background()
	p := justcode.SetupPreflighter{}
	res := p.RunPreflight(ctx)
	fmt.Printf("Platform: %s\n", res.Platform)
	fmt.Printf("Virtualization: %s\n", yn(res.VirtualizationOK))
	if res.VirtualizationDetail != "" {
		for _, line := range strings.Split(strings.TrimSpace(res.VirtualizationDetail), "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
	if res.DiskFreeBytes >= 0 {
		fmt.Printf("Disk free: %s (%s)\n", humanBytes(res.DiskFreeBytes), yn(res.DiskOK))
	}
	if res.DiskDetail != "" {
		fmt.Printf("Disk free: probe failed (%s)\n", res.DiskDetail)
	}
	fmt.Printf("Managed runtime: %s\n", yn(res.RuntimeInstalled))
	if res.Fatal {
		fmt.Println("Result: this host cannot run just-code's managed runtime. Fix the failures above and re-run.")
		return 1, nil
	}
	fmt.Println("Result: ready. Run 'just-code setup' to configure credentials and install the runtime.")
	return 0, nil
}

func yn(b bool) string {
	if b {
		return "ok"
	}
	return "FAILED"
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// setupRunCmd runs the interactive wizard. Every prompt reads one line;
// empty + Ctrl-D (EOF) cancels the current stage, and "cancel" at the first
// prompt of the flow exits with the journal preserved for a later resume.
func setupRunCmd(fallback bool) (int, error) {
	ctx := context.Background()
	stateDir := justcode.DefaultStateDir()
	fs := justcode.DefaultFS
	in := bufio.NewReader(os.Stdin)

	j, err := justcode.ReadSetupJournal(fs, stateDir)
	if err != nil {
		return 1, err
	}
	if j.Stage == justcode.StagePreflight && len(j.CredentialKinds) == 0 && j.GitName == "" {
		// Fresh start: preflight first, so a fatal blocker surfaces before
		// any prompt.
		p := justcode.SetupPreflighter{}
		res := p.RunPreflight(ctx)
		j.Preflight = res
		if res.Fatal {
			fmt.Println("This host cannot run just-code's managed runtime:")
			printPreflight(res)
			_ = justcode.WriteSetupJournal(fs, stateDir, j)
			return 1, fmt.Errorf("setup stopped: fix the failures above, then re-run 'just-code setup'")
		}
		j.Stage = justcode.StageCredential
		if err := justcode.WriteSetupJournal(fs, stateDir, j); err != nil {
			return 1, err
		}
	} else {
		fmt.Println("Resuming setup where it stopped (credentials already stored are kept).")
	}

	w := justcode.SetupWizard{
		FS:       fs,
		StateDir: stateDir,
		Store: func(ctx context.Context) (justcode.SetupStore, error) {
			return authStore(fallback)
		},
		ValidateAlbert: func(ctx context.Context, key string) error {
			probe := justcode.ValidateAlbertKey(ctx, nil, key)
			switch {
			case probe.Rejected:
				return justcode.RejectedCredentialError{Detail: probe.Detail}
			case probe.Unreachable:
				return fmt.Errorf("could not reach the Albert endpoint: %s", probe.Detail)
			}
			return nil
		},
	}

	// Stage: Albert credential (skipped when already stored this run).
	if !justcode.ContainsCredentialKind(j.CredentialKinds, "albert") {
		fmt.Println("just-code setup — global configuration")
		fmt.Println()
		key, ok := promptSecret(in, "Enter your Albert API key (input hidden where the terminal supports it; empty to cancel): ")
		if !ok || key == "" {
			return 1, fmt.Errorf("%s", cancelMsg(stateDir))
		}
		probe, err := w.StoreCredential(ctx, justcode.CredentialAlbert, key)
		if err != nil {
			var rejected justcode.RejectedCredentialError
			if probe.Rejected || strings.Contains(err.Error(), "rejected") {
				_ = rejected
				fmt.Fprintf(os.Stderr, "The key was rejected (%s). Re-run 'just-code setup' and re-enter it.\n", probe.Detail)
				return 1, nil
			}
			return 1, err
		}
		if probe.Unreachable {
			fmt.Fprintf(os.Stderr, "Warning: the Albert endpoint could not be reached (%s); the key was stored without validation. 'just-code models' will confirm it when the network recovers.\n", probe.Detail)
		} else {
			fmt.Println("Albert credential validated and stored.")
		}
		j.CredentialKinds = append(j.CredentialKinds, "albert")
	} else {
		fmt.Println("Albert credential: already stored.")
	}

	// Stage: optional GitHub credential. Skipping is penalty-free.
	if !justcode.ContainsCredentialKind(j.CredentialKinds, "github") {
		fmt.Println()
		fmt.Println("GitHub credential (optional — used by the guest GitHub workflow; can be added later with 'just-code auth add github').")
		answer, _ := promptLine(in, "Add a GitHub token now? [y/N]: ")
		if strings.EqualFold(strings.TrimSpace(answer), "y") {
			token, ok := promptSecret(in, "Enter the GitHub token (input hidden; empty to skip): ")
			if ok && token != "" {
				if _, err := w.StoreCredential(ctx, justcode.CredentialGithub, token); err != nil {
					return 1, err
				}
				j.CredentialKinds = append(j.CredentialKinds, "github")
				fmt.Println("GitHub credential stored. It is NOT activated for any project; per-project approval comes with the GitHub workflow (bindings approve github).")
			} else {
				fmt.Println("Skipped GitHub.")
			}
		} else {
			fmt.Println("Skipped GitHub.")
		}
	}

	// Stage: git identity (editable, defaults offered).
	if j.GitName == "" || j.GitEmail == "" {
		fmt.Println()
		fmt.Println("Git identity for guest commits:")
		name, _ := promptLine(in, "Name [Albert Code Agent]: ")
		if strings.TrimSpace(name) == "" {
			name = "Albert Code Agent"
		}
		email, _ := promptLine(in, "Email [albert-code@noreply.etalab.gouv.fr]: ")
		if strings.TrimSpace(email) == "" {
			email = "albert-code@noreply.etalab.gouv.fr"
		}
		if err := w.SaveIdentity(strings.TrimSpace(name), strings.TrimSpace(email)); err != nil {
			return 1, err
		}
		j.GitName, j.GitEmail = name, email
	}

	// Stage: model selection (catalogue-validated when reachable).
	model := j.DefaultModel
	{
		fmt.Println()
		fmt.Println("Default model (leave empty for albert/deepseek-v4-flash; 'just-code models' lists the catalogue):")
		chosen, _ := promptLine(in, "Model: ")
		chosen = strings.TrimSpace(chosen)
		if chosen != "" && chosen != model {
			model = chosen
		}
	}
	if err := w.SaveSettings(model); err != nil {
		return 1, err
	}

	// Review.
	fmt.Println()
	fmt.Println("Review:")
	fmt.Printf("  Albert credential: stored (%s store)\n", storeLabel(fallback))
	if justcode.ContainsCredentialKind(j.CredentialKinds, "github") {
		fmt.Println("  GitHub credential: stored (inactive until a project approves the binding)")
	} else {
		fmt.Println("  GitHub credential: not stored (skippable, add later)")
	}
	fmt.Printf("  Git identity: %s <%s>\n", strings.TrimSpace(j.GitName), strings.TrimSpace(j.GitEmail))
	if model != "" {
		fmt.Printf("  Default model: %s\n", model)
	} else {
		fmt.Println("  Default model: albert/deepseek-v4-flash (built-in)")
	}
	fmt.Println("  Managed runtime: will be downloaded and verified (or reused if trusted)")
	answer, _ := promptLine(in, "Apply? [Y/n]: ")
	if strings.EqualFold(strings.TrimSpace(answer), "n") {
		return 1, fmt.Errorf("%s", cancelMsg(stateDir))
	}

	// Apply: settings are already written; install the runtime.
	fmt.Println()
	fmt.Println("Installing the managed Microsandbox runtime (downloaded and digest-verified)...")
	if err := w.InstallRuntime(ctx); err != nil {
		// Journal preserved: the retry skips the credential prompts.
		return 1, fmt.Errorf("runtime installation failed: %v (re-run 'just-code setup' to resume; the credential prompts will be skipped)", err)
	}
	fmt.Println()
	fmt.Println("Setup complete. Run 'just-code' from a project directory to start the backend and attach the TUI.")
	return 0, nil
}

func storeLabel(fallback bool) string {
	if fallback {
		return "file"
	}
	return "native"
}

func cancelMsg(stateDir string) string {
	return fmt.Sprintf("setup cancelled; run 'just-code setup' to resume from where it stopped (journal: %s)", justcode.SetupJournalPath(stateDir))
}

func printPreflight(res justcode.SetupPreflight) {
	fmt.Printf("  Platform: %s\n", res.Platform)
	if !res.VirtualizationOK {
		fmt.Printf("  Virtualization: %s\n", res.VirtualizationDetail)
	}
	if !res.DiskOK && res.DiskFreeBytes >= 0 {
		fmt.Printf("  Disk free: %s (need at least 1 GiB)\n", humanBytes(res.DiskFreeBytes))
	}
}

// promptLine reads one line, reporting EOF as not-ok.
func promptLine(in *bufio.Reader, prompt string) (string, bool) {
	fmt.Print(prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return line, true
}

// promptSecret reads a secret with echo disabled where supported.
func promptSecret(in *bufio.Reader, prompt string) (string, bool) {
	fmt.Print(prompt)
	if isTTY() {
		value, err := readPassword()
		fmt.Println()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(value), true
	}
	return promptLine(in, "")
}
