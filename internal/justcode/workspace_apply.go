package justcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GuestChange is one file's slice of the agent's uncommitted work in the
// isolated machine: enough to list it on a screen, and the exact patch bytes
// to apply it on its own (issue #173).
type GuestChange struct {
	// Rel is the file's slash-separated path relative to the project root.
	Rel string
	// Status is 'M' (modified), 'A' (added) or 'D' (deleted).
	Status byte
	// Additions and Deletions are the numstat line counts. A binary file has
	// no line counts; Binary reports that case instead.
	Additions int
	Deletions int
	// Binary marks a GIT binary patch.
	Binary bool
	// Patch is this file's own patch, appliable on its own with git apply.
	Patch []byte
}

// GuestChangeDigest fingerprints one change (path + patch bytes) so a later
// run can tell whether this exact change was already applied to the host
// folder. The path is part of the digest so identical content under a new
// name still counts as new work.
func GuestChangeDigest(c GuestChange) string {
	h := sha256.New()
	h.Write([]byte(c.Rel))
	h.Write([]byte{0})
	h.Write(c.Patch)
	return hex.EncodeToString(h.Sum(nil))
}

// CollectGuestChanges reads the agent's uncommitted work in the isolated
// machine as one change per file. It is the read half of `workspace apply`
// (issue #173) and of the exit screen's recovery block (issue #174).
//
// The paths and line counts come from `git diff --numstat -z`, which is
// unambiguous even for paths with spaces or quotes; the per-file patches come
// from the same diff without --numstat, split on its `diff --git` boundaries.
// The two reads are paired by position and verified to agree in count, so a
// guest that changed between the two reads fails closed instead of pairing
// the wrong patch with the wrong path.
func (m *MicrosandboxRuntime) CollectGuestChanges(ctx context.Context) ([]GuestChange, error) {
	// `git add -A -N` below mutates the repository it runs in; on a
	// bind-mounted instance that repository is the user's own checkout.
	if err := m.requireSealedWorkspace(ctx); err != nil {
		return nil, err
	}
	// A guest that was never provisioned holds no work to recover. Reading
	// its missing repository as an error would present an empty exit screen
	// as a failure instead of the normal "no changes" case.
	provisioned, err := m.guestWorkspaceProvisioned(ctx)
	if err != nil {
		return nil, err
	}
	if !provisioned {
		return nil, nil
	}
	// -N (intent-to-add) makes new files visible in the diff; --no-renames
	// keeps one section per path so numstat and diff pair one-to-one.
	const base = " && git add -A -N && git diff HEAD --binary --no-renames"
	numstatOut, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+base+" --numstat -z")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("the guest could not list its changes (exit %d): %s", code, strings.TrimSpace(stderr))
	}
	stats := parseNumstatZ(numstatOut)
	diffOut, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+base)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("the guest could not produce a diff (exit %d): %s", code, strings.TrimSpace(stderr))
	}
	if len(stats) == 0 && strings.TrimSpace(diffOut) == "" {
		return nil, nil
	}
	sections := splitDiffSections(diffOut)
	if len(sections) != len(stats) {
		return nil, fmt.Errorf("the guest workspace changed while it was being read (%d files listed, %d in the patch); run the command again", len(stats), len(sections))
	}
	changes := make([]GuestChange, 0, len(sections))
	for i, section := range sections {
		c := GuestChange{
			Rel:       stats[i].rel,
			Additions: stats[i].additions,
			Deletions: stats[i].deletions,
			Binary:    stats[i].binary,
			Patch:     []byte(section),
		}
		c.Status = guestSectionStatus(section)
		changes = append(changes, c)
	}
	return changes, nil
}

// guestNumstat is one --numstat -z entry.
type guestNumstat struct {
	rel       string
	additions int
	deletions int
	binary    bool
}

// parseNumstatZ parses `git diff --numstat -z` output. Entries are
// `added\tdeleted\tpath` separated by NUL; a binary file reports `-` counts.
// A path may itself contain tabs, so only the first two tabs split fields.
func parseNumstatZ(out string) []guestNumstat {
	var stats []guestNumstat
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		tab1 := strings.IndexByte(entry, '\t')
		if tab1 < 0 {
			continue
		}
		tab2 := strings.IndexByte(entry[tab1+1:], '\t')
		if tab2 < 0 {
			continue
		}
		addField := entry[:tab1]
		delField := entry[tab1+1 : tab1+1+tab2]
		s := guestNumstat{rel: entry[tab1+1+tab2+1:]}
		if addField == "-" || delField == "-" {
			s.binary = true
		} else {
			s.additions, _ = strconv.Atoi(addField)
			s.deletions, _ = strconv.Atoi(delField)
		}
		stats = append(stats, s)
	}
	return stats
}

// splitDiffSections splits a `git diff` payload into one section per file,
// each starting at its `diff --git` line. It returns nil when the payload is
// empty or does not start with a section header.
func splitDiffSections(diff string) []string {
	if !strings.HasPrefix(diff, "diff --git ") {
		return nil
	}
	var sections []string
	for {
		next := strings.Index(diff[1:], "\ndiff --git ")
		if next < 0 {
			sections = append(sections, diff)
			return sections
		}
		cut := next + 1
		// The newline before the next header terminates the previous
		// section's last line, so it stays with that section.
		sections = append(sections, diff[:cut+1])
		diff = diff[cut+1:]
	}
}

// guestSectionStatus classifies a diff section as added, deleted or modified
// from its header lines, before the first hunk.
func guestSectionStatus(section string) byte {
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			return 'M'
		case strings.HasPrefix(line, "new file mode "):
			return 'A'
		case strings.HasPrefix(line, "deleted file mode "):
			return 'D'
		case strings.HasPrefix(line, "Binary files "):
			return 'M'
		case strings.HasPrefix(line, "GIT binary patch"):
			return 'M'
		}
	}
	return 'M'
}

// patchContentSize estimates how much content a patch would write, for the
// per-file transfer cap: the added lines of a text patch, or the size stated
// by a binary patch's literal/delta header.
func patchContentSize(section string) int64 {
	if strings.Contains(section, "\nGIT binary patch\n") {
		for _, line := range strings.Split(section, "\n") {
			if n, ok := patchBinarySize(line, "literal "); ok {
				return n
			}
			if n, ok := patchBinarySize(line, "delta "); ok {
				return n
			}
		}
		return 0
	}
	var total int64
	inHunk := false
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			total += int64(len(line)) // +1 for the newline the diff omits
		}
	}
	return total
}

func patchBinarySize(line, prefix string) (int64, bool) {
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(line[len(prefix):]), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// GuestPushedBranch reports whether the agent's work branch was pushed from
// the isolated machine, and the draft pull request it opened, for the exit
// screen (issue #174). A branch that was never created or never pushed is
// reported as not pushed, not as an error.
func (m *MicrosandboxRuntime) GuestPushedBranch(ctx context.Context) (branch, prURL string, pushed bool, err error) {
	stdout, _, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+" && git config --get just-code.github-workflow-branch")
	if err != nil {
		return "", "", false, err
	}
	if code != 0 {
		return "", "", false, nil
	}
	branch = strings.TrimSpace(stdout)
	if branch == "" {
		return "", "", false, nil
	}
	_, _, code, err = m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+" && git rev-parse --verify --quiet "+shellQuote("refs/remotes/origin/"+branch))
	if err != nil {
		return branch, "", false, err
	}
	if code != 0 {
		return branch, "", false, nil
	}
	// The pull request probe is best-effort: a guest without a usable `gh`
	// still pushed its branch, which the exit screen reports on its own.
	out, _, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+" && gh pr list --head "+shellQuote(branch)+" --json url --limit 1")
	if err != nil || code != 0 {
		return branch, "", true, nil
	}
	var prs []struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &prs) != nil || len(prs) == 0 {
		return branch, "", true, nil
	}
	return branch, prs[0].URL, true, nil
}

// ApplyRecordPath is the host path of the applied-changes record for one
// instance: which exact guest changes were already recovered into the host
// folder.
func ApplyRecordPath(stateDir, instance string) string {
	return filepath.Join(InstanceStateDir(stateDir, instance), "applied-guest-changes.json")
}

// ApplyRecordStore records which guest changes were already applied to the
// host folder, so the exit screen does not re-offer recovered work (issue
// #174) and a second apply does not report an already-applied file as a
// conflict (issue #173).
type ApplyRecordStore struct {
	Path string
	FS   FS
}

type appliedGuestChangeFile struct {
	SchemaVersion int               `json:"schemaVersion"`
	Digests       map[string]string `json:"digests"`
}

const appliedGuestChangeSchemaVersion = 1

// Load returns the recorded digests keyed by relative path. A missing record
// is an empty history, not an error.
func (s ApplyRecordStore) Load() (map[string]string, error) {
	out := map[string]string{}
	fsys := s.FS
	if fsys == nil {
		fsys = DefaultFS
	}
	data, err := fsys.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var raw appliedGuestChangeFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("applied-changes record %s: %w", s.Path, err)
	}
	for rel, digest := range raw.Digests {
		if rel != "" && digest != "" {
			out[rel] = digest
		}
	}
	return out, nil
}

// Record marks one guest change as applied.
func (s ApplyRecordStore) Record(rel, digest string) error {
	return s.mutate(func(digests map[string]string) {
		digests[rel] = digest
	})
}

func (s ApplyRecordStore) mutate(change func(map[string]string)) error {
	fsys := s.FS
	if fsys == nil {
		fsys = DefaultFS
	}
	digests, err := s.Load()
	if err != nil {
		return err
	}
	change(digests)
	file := appliedGuestChangeFile{
		SchemaVersion: appliedGuestChangeSchemaVersion,
		Digests:       digests,
	}
	if err := fsys.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(fsys, s.Path, append(data, '\n'), 0o600)
}

// ApplyReview is the classified outcome for the apply screen (issue #173):
// every guest change, which are already recovered, and which the filter
// refuses to bring back.
type ApplyReview struct {
	// Changes is every guest change, in diff order.
	Changes []GuestChange
	// Recovered lists paths whose exact change was already applied to the
	// host folder by a previous run.
	Recovered map[string]bool
	// Refused maps a path to the reason it must not be applied.
	Refused map[string]string
}

// Applicable lists the changes the user may apply: not already recovered and
// not refused.
func (r ApplyReview) Applicable() []GuestChange {
	var out []GuestChange
	for _, c := range r.Changes {
		if r.Recovered[c.Rel] || r.Refused[c.Rel] != "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// PrepareApplyReview collects the guest's changes and classifies each one
// against the applied-changes record and the transfer filter.
func PrepareApplyReview(ctx context.Context, m *MicrosandboxRuntime, root string, optIn map[string]bool) (ApplyReview, error) {
	changes, err := m.CollectGuestChanges(ctx)
	if err != nil {
		return ApplyReview{}, err
	}
	store := ApplyRecordStore{Path: ApplyRecordPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}
	records, err := store.Load()
	if err != nil {
		return ApplyReview{}, err
	}
	man, err := ResolveTransferSet(ctx, root, TransferOptions{OptIn: optIn})
	if err != nil {
		return ApplyReview{}, err
	}
	policy := newApplyPolicy(root, man, optIn)
	review := ApplyReview{
		Changes:   changes,
		Recovered: map[string]bool{},
		Refused:   map[string]string{},
	}
	for _, c := range changes {
		if records[c.Rel] == GuestChangeDigest(c) {
			review.Recovered[c.Rel] = true
			continue
		}
		if reason := policy.check(ctx, c); reason != "" {
			review.Refused[c.Rel] = reason
		}
	}
	return review, nil
}

// UnrecoveredGuestChanges lists the guest's changes not yet applied to the
// host folder, for the exit screen's recovery block (issue #174). Refused
// changes still count as unrecovered: the file was modified and the change
// did not come back, whatever the reason.
func UnrecoveredGuestChanges(ctx context.Context, m *MicrosandboxRuntime) ([]GuestChange, error) {
	changes, err := m.CollectGuestChanges(ctx)
	if err != nil {
		return nil, err
	}
	store := ApplyRecordStore{Path: ApplyRecordPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}
	records, err := store.Load()
	if err != nil {
		return nil, err
	}
	var out []GuestChange
	for _, c := range changes {
		if records[c.Rel] == GuestChangeDigest(c) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// applyPolicy is the per-file decision context for bringing guest changes
// back: the host manifest (what crossed into the guest), the recorded
// per-file re-inclusions, and the host root for the git-ignored probe.
type applyPolicy struct {
	root       string
	gitManaged bool
	entries    map[string]TransferEntry
	optIn      map[string]bool
}

func newApplyPolicy(root string, man TransferManifest, optIn map[string]bool) applyPolicy {
	entries := make(map[string]TransferEntry, len(man.Entries))
	for _, e := range man.Entries {
		entries[e.Rel] = e
	}
	return applyPolicy{
		root:       root,
		gitManaged: man.GitManaged,
		entries:    entries,
		optIn:      optIn,
	}
}

// check returns the refusal reason for one change, or "" when it may be
// applied to the host folder. The rules mirror the host-to-guest transfer
// filter for the reverse direction: a path the copy would not carry — Git
// metadata, a dotenv file, a secret the filter flagged, git-ignored state —
// must not cross back either.
func (p applyPolicy) check(ctx context.Context, c GuestChange) string {
	// Structural first: an explicit per-file decision cannot override it,
	// the same way the forward filter refuses to record one.
	if path.Base(c.Rel) == ".git" || strings.HasPrefix(c.Rel, ".git/") {
		return "Git metadata never crosses the isolation boundary"
	}
	// A recorded re-inclusion is the user's explicit call that this path may
	// cross the boundary; it holds in both directions.
	if p.optIn[c.Rel] {
		return ""
	}
	if isDotenvName(path.Base(c.Rel)) {
		return "dotenv file (default-deny)"
	}
	if entry, ok := p.entries[c.Rel]; ok {
		if entry.Included {
			return ""
		}
		if entry.Reason != "" {
			return entry.Reason
		}
		return string(entry.Exclusion)
	}
	// The agent created this path after the copy: it has no manifest entry.
	// Host git-ignored state must not come back either — the filter refuses
	// to copy it in, so writing it out would create files the user's own
	// gitignore calls derived state.
	if p.gitIgnored(ctx, c.Rel) {
		return "git-ignored (local or derived state)"
	}
	if patchContentSize(string(c.Patch)) > DefaultTransferMaxFileBytes {
		return "above the per-file transfer cap"
	}
	return ""
}

// gitIgnored reports whether the host's git would ignore this path. It is
// best-effort: without a git-managed host or a git binary, the answer is
// "not ignored", because the probe is a refinement, not a gate.
func (p applyPolicy) gitIgnored(ctx context.Context, rel string) bool {
	if !p.gitManaged {
		return false
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, git, "-C", p.root, "check-ignore", "-q", "--", rel)
	return cmd.Run() == nil // exit 0 = ignored; anything else = not known to be ignored
}

// ApplyGuestPatches writes each change's patch into the host folder with
// git apply. A patch that no longer applies — because the host file moved on
// since the copy — is skipped, never forced (issue #173: a conflict must not
// overwrite a host-modified file).
func ApplyGuestPatches(ctx context.Context, root string, files []GuestChange) (applied, conflicted []GuestChange, err error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, nil, fmt.Errorf("the 'git' CLI is required to apply changes to your folder but was not found on PATH")
	}
	dir, err := os.MkdirTemp("", "just-code-apply-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	for i, c := range files {
		patchPath := filepath.Join(dir, fmt.Sprintf("change-%d.patch", i))
		if err := os.WriteFile(patchPath, c.Patch, 0o600); err != nil {
			return nil, nil, err
		}
		check := exec.CommandContext(ctx, git, "apply", "--check", patchPath)
		check.Dir = root
		if err := check.Run(); err != nil {
			conflicted = append(conflicted, c)
			continue
		}
		apply := exec.CommandContext(ctx, git, "apply", patchPath)
		apply.Dir = root
		if err := apply.Run(); err != nil {
			// The check passed but the apply failed: report the file as
			// unapplied rather than claiming success for a half-written one.
			conflicted = append(conflicted, c)
			continue
		}
		applied = append(applied, c)
	}
	return applied, conflicted, nil
}

// PreserveUnappliedPatches writes the patches of changes that could not be
// applied to the host state, so the user can review and apply them by hand.
// The file is overwritten on each run: it mirrors the current unapplied set,
// not an append-only history.
func PreserveUnappliedPatches(stateDir, instance string, files []GuestChange) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	for _, c := range files {
		buf.Write(c.Patch)
		if !bytes.HasSuffix(c.Patch, []byte("\n")) {
			buf.WriteByte('\n')
		}
	}
	outPath := filepath.Join(InstanceStateDir(stateDir, instance), "unapplied-guest-changes.patch")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return outPath, nil
}

// SortGuestChanges orders changes by path for stable screen output.
func SortGuestChanges(changes []GuestChange) {
	sort.Slice(changes, func(i, j int) bool { return changes[i].Rel < changes[j].Rel })
}
