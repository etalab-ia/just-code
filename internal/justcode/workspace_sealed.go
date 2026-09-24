package justcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sealed-workspace provisioning, refresh, and change delivery (P22).
//
// The guest owns its working tree. Content crosses host->guest only through
// ResolveTransferSet + BuildTransferArchive (transfer.go), written with the
// sandbox filesystem API; changes come back only as a reviewed diff. Nothing
// here mounts the host checkout, and nothing copies a host directory
// wholesale.

// msbTransferArchivePath is where the transfer payload is staged inside the
// guest before extraction. It is removed after a successful extraction.
const msbTransferArchivePath = "/tmp/just-code-workspace-transfer.tar.gz"

// TransferOptInPath is the host-side record of per-file re-inclusions for an
// instance. It lives in host state, never in the project.
func TransferOptInPath(stateDir, instance string) string {
	return filepath.Join(InstanceStateDir(stateDir, instance), "transfer-optin.json")
}

// SyncOptions tunes a workspace provisioning or refresh.
type SyncOptions struct {
	// Force allows a refresh over guest-local uncommitted work, which is
	// otherwise refused so nothing is overwritten silently.
	Force bool
	// OptIn carries the recorded per-file re-inclusions (see
	// TransferOptInStore).
	OptIn map[string]bool
	// Print receives the review screen. Nil means stdout.
	Print func(string)
}

func (o SyncOptions) print(msg string) {
	if o.Print != nil {
		o.Print(msg)
		return
	}
	fmt.Print(msg)
}

// WorkspaceManifest resolves the transfer set for this project without
// sending anything, so a caller can show the user what would cross.
func (m *MicrosandboxRuntime) WorkspaceManifest(ctx context.Context, opts SyncOptions) (TransferManifest, error) {
	return ResolveTransferSet(ctx, m.cfg.WorkspaceDir, TransferOptions{OptIn: opts.OptIn})
}

// ProvisionGuestWorkspace performs the initial filtered transfer into a
// freshly created sandbox and creates the guest repository. It is idempotent:
// a guest that already carries a repository is left alone (use
// SyncGuestWorkspace to refresh).
func (m *MicrosandboxRuntime) ProvisionGuestWorkspace(ctx context.Context, opts SyncOptions) error {
	provisioned, err := m.guestWorkspaceProvisioned(ctx)
	if err != nil {
		return err
	}
	if provisioned {
		return nil
	}
	return m.transferIntoGuest(ctx, opts, "Provisioning the sealed guest workspace")
}

// SyncGuestWorkspace refreshes the guest working tree from the host checkout.
//
// Guest-local uncommitted work is not overwritten silently: the refresh
// refuses unless opts.Force is set, and reports what it found. (Extraction is
// additive by nature — it does not delete files the guest added — but a file
// the guest edited and the host also changed would be clobbered, which is the
// case worth stopping for.)
func (m *MicrosandboxRuntime) SyncGuestWorkspace(ctx context.Context, opts SyncOptions) error {
	if !opts.Force {
		dirty, err := m.guestWorkingTreeDirty(ctx)
		if err != nil {
			return err
		}
		if dirty != "" {
			return fmt.Errorf("the guest workspace has uncommitted changes, so a refresh could overwrite them; "+
				"export them first ('just-code workspace export') or pass --force to refresh anyway:\n%s", dirty)
		}
	}
	return m.transferIntoGuest(ctx, opts, "Refreshing the sealed guest workspace")
}

// transferIntoGuest resolves, filters, archives, and writes the transfer set.
func (m *MicrosandboxRuntime) transferIntoGuest(ctx context.Context, opts SyncOptions, headline string) error {
	manifest, err := m.WorkspaceManifest(ctx, opts)
	if err != nil {
		return err
	}
	opts.print(manifest.Summary())
	// Fail closed on an unattributable finding: a flagged file that cannot be
	// named cannot be excluded, and proceeding would mean guessing that it is
	// harmless.
	if len(manifest.UnmappedFindings) > 0 {
		return fmt.Errorf("gitleaks reported %d finding(s) whose paths could not be matched to a file in %s, so the filter cannot tell which candidate to exclude: "+
			"resolve the report by hand ('gitleaks detect --no-git --source %s'), then retry",
			len(manifest.UnmappedFindings), manifest.Root, manifest.Root)
	}
	included := manifest.Included()
	if len(included) == 0 {
		if manifest.CandidateCount() == 0 {
			// Nothing to send: the source directory holds no files at all.
			// That is an empty project, not a filter decision, so the guest
			// still gets a repository it can work in.
			opts.print(fmt.Sprintf("The transfer source %s holds no files; the guest workspace will be an empty repository.\n", manifest.Root))
		} else {
			return fmt.Errorf("the transfer filter excluded all %d candidate file(s) in %s, so the guest would start with an empty workspace; "+
				"re-include what the project needs with 'just-code workspace allow <path>' (see 'just-code workspace status' for the reasons)",
				manifest.CandidateCount(), manifest.Root)
		}
	}
	payload, err := BuildTransferArchive(manifest)
	if err != nil {
		return err
	}
	opts.print(fmt.Sprintf("%s: writing %s to the guest...\n", headline, FormatByteSize(int64(len(payload)))))
	if err := m.Client.WriteFile(ctx, m.InstanceName(), msbTransferArchivePath, payload); err != nil {
		return fmt.Errorf("writing the transfer payload into the guest failed: %w", err)
	}
	// Extraction is the only thing that creates content in /workspace, and it
	// consumes the payload built from the filtered set above.
	script := fmt.Sprintf("mkdir -p %s && tar -xzf %s -C %s && rm -f %s",
		shellQuote(msbGuestWorkspace), shellQuote(msbTransferArchivePath), shellQuote(msbGuestWorkspace), shellQuote(msbTransferArchivePath))
	if err := m.guestShell(ctx, script); err != nil {
		return fmt.Errorf("extracting the transfer payload inside the guest failed: %w", err)
	}
	if err := m.ensureGuestRepository(ctx); err != nil {
		return err
	}
	opts.print(fmt.Sprintf("Guest workspace ready: %d file(s) in %s.\n", len(included), msbGuestWorkspace))
	return nil
}

// ensureGuestRepository makes the guest workspace a Git repository with one
// commit, so guest changes can always be delivered as a reviewed diff —
// including for a project root that is not a Git repository on the host.
//
// The host's history is deliberately NOT transferred. A git bundle would
// carry committed content the transfer filter cannot inspect, and the filter
// is the boundary; a snapshot that is filtered file by file is the property
// worth keeping. The host origin URL is recorded as a remote for later
// branch/PR delivery (P13).
func (m *MicrosandboxRuntime) ensureGuestRepository(ctx context.Context) error {
	steps := []string{
		// -q and an explicit identity keep the command independent of the
		// guest's global configuration, which the user may have changed.
		"cd " + shellQuote(msbGuestWorkspace),
		"git rev-parse --git-dir >/dev/null 2>&1 || git init -q",
		"git add -A",
		"git -c user.name=" + shellQuote(m.gitIdentityName()) + " -c user.email=" + shellQuote(m.gitIdentityEmail()) +
			" commit -q -m " + shellQuote("just-code: initial guest snapshot from the host working tree") + " --allow-empty",
	}
	return m.guestShell(ctx, strings.Join(steps, " && "))
}

// gitIdentityName and gitIdentityEmail resolve the guest commit identity from
// the P11 user settings, falling back to the documented defaults.
func (m *MicrosandboxRuntime) gitIdentityName() string {
	name, _ := guestGitIdentity(m.guestGitConfig())
	return name
}

func (m *MicrosandboxRuntime) gitIdentityEmail() string {
	_, email := guestGitIdentity(m.guestGitConfig())
	return email
}

// guestGitConfig reads the host-side identity records onto a guest config.
func (m *MicrosandboxRuntime) guestGitConfig() GuestConfig {
	name, email := hostGitIdentity()
	return GuestConfig{GitName: name, GitEmail: email}
}

// guestWorkspaceProvisioned reports whether the guest already has a workspace
// repository.
func (m *MicrosandboxRuntime) guestWorkspaceProvisioned(ctx context.Context) (bool, error) {
	stdout, _, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"test -d "+shellQuote(msbGuestWorkspace+"/.git")+" && echo yes || echo no")
	if err != nil {
		return false, err
	}
	if code != 0 {
		return false, fmt.Errorf("cannot determine whether the guest workspace is provisioned")
	}
	return strings.TrimSpace(stdout) == "yes", nil
}

// guestWorkingTreeDirty returns the guest's porcelain status, or "" when the
// tree is clean.
func (m *MicrosandboxRuntime) guestWorkingTreeDirty(ctx context.Context) (string, error) {
	stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+" && git status --porcelain")
	if err != nil {
		return "", err
	}
	if code != 0 {
		// No repository yet is not "dirty": the first transfer is free to run.
		return "", nil
	}
	_ = stderr
	return strings.TrimSpace(stdout), nil
}

// guestShell runs a command in the guest, surfacing stderr on failure.
func (m *MicrosandboxRuntime) guestShell(ctx context.Context, command string) error {
	code, stderr, err := m.Client.Exec(ctx, m.InstanceName(), command)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("guest command exited %d: %s", code, strings.TrimSpace(stderr))
	}
	return nil
}

// ExportGuestChanges produces a reviewable patch of the guest's work and
// writes it to the host. It is the change-delivery path for a project without
// an enabled GitHub grant; with one, P13 adds direct branch/PR delivery.
//
// The patch is written outside the project by default (host state), so
// reviewing it does not itself dirty the checkout.
func (m *MicrosandboxRuntime) ExportGuestChanges(ctx context.Context, outPath string) (string, error) {
	// `git add -A -N` marks untracked files intent-to-add so they appear in
	// the diff; without it, new files would be invisible in the export.
	cmd := "cd " + shellQuote(msbGuestWorkspace) + " && git add -A -N && git diff HEAD"
	stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(), cmd)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("the guest could not produce a diff (exit %d): %s", code, strings.TrimSpace(stderr))
	}
	if strings.TrimSpace(stdout) == "" {
		return "", nil
	}
	if outPath == "" {
		outPath = filepath.Join(InstanceStateDir(DefaultStateDir(), m.InstanceName()), "guest-changes.patch")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, []byte(stdout), 0o600); err != nil {
		return "", err
	}
	return outPath, nil
}
