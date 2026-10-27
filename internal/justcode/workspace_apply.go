package justcode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Guest → host change delivery as a reviewed, selected write (issue #173).
//
// ExportGuestChanges (workspace_sealed.go) hands back a patch the user must
// apply by hand. This file implements the one-command path: read what the
// agent changed inside the sealed guest, show it, and write the chosen files
// back into the host transfer source — never staging, never committing, and
// never overwriting a file the host changed since the transfer.
//
// The safety property is the mirror of the transfer filter: content crosses
// guest → host only as parsed, classified, per-file decisions. Three verdicts
// exist per file:
//
//   - apply: the host still byte-matches the transferred snapshot, so writing
//     the agent's version is a fast-forward of known content;
//   - conflict: the host diverged (edited, missing, or already has a file the
//     agent created), so the file is never written — the patch is kept instead;
//   - excluded: the reverse-direction filter refuses the file for the same
//     reasons the forward filter would (secrets, dotenv, git metadata, ignored
//     or oversized files), so agent work on those paths stays in the guest.

// msbGuestWorkspace is defined in microsandbox.go; the guest working tree the
// diff is read from.

// GuestChangeStatus classifies one file's change kind inside the guest.
type GuestChangeStatus string

const (
	GuestChangeModified GuestChangeStatus = "M"
	GuestChangeAdded    GuestChangeStatus = "A"
	GuestChangeDeleted  GuestChangeStatus = "D"
)

// GuestChange is one parsed section of the guest's diff against the snapshot
// the host transferred.
type GuestChange struct {
	// Rel is the slash-separated path relative to the guest workspace.
	Rel string
	// Status is M (modified), A (added) or D (deleted).
	Status GuestChangeStatus
	// PreHash and PostHash are the full git blob hashes of the file before
	// and after the agent's work (all zeros for the missing side).
	PreHash  string
	PostHash string
	// OldMode and NewMode are the git file modes as printed by the diff
	// (informational; the write uses the mode carried by the tarball).
	OldMode string
	NewMode string
	// Binary marks a file git cannot render as text.
	Binary bool
	// AddCount and DelCount are the hunk line counts (+ and −).
	AddCount int
	DelCount int
	// Diff is the verbatim diff section, appliable with git apply.
	Diff string
}

// WorkspaceApplyVerdict is what just-code will do with one guest change.
type WorkspaceApplyVerdict string

const (
	// WorkspaceApplyOK: the file can be written into the host folder.
	WorkspaceApplyOK WorkspaceApplyVerdict = "apply"
	// WorkspaceApplyConflict: the host folder diverged from the snapshot,
	// so the file is never written.
	WorkspaceApplyConflict WorkspaceApplyVerdict = "conflict"
	// WorkspaceApplyExcluded: the reverse-direction filter refuses the file.
	WorkspaceApplyExcluded WorkspaceApplyVerdict = "excluded"
)

// WorkspaceApplyConflict names why a file could not be applied.
type WorkspaceApplyConflictKind string

const (
	// WorkspaceConflictHostModified: the host file's bytes differ from the
	// transferred snapshot (edited, or replaced by something else).
	WorkspaceConflictHostModified WorkspaceApplyConflictKind = "host-modified"
	// WorkspaceConflictHostMissing: the guest modified or deleted a file
	// that no longer exists in the host folder.
	WorkspaceConflictHostMissing WorkspaceApplyConflictKind = "host-missing"
	// WorkspaceConflictHostExists: the agent created a file that already
	// exists in the host folder.
	WorkspaceConflictHostExists WorkspaceApplyConflictKind = "host-exists"
)

// WorkspaceApplyFile is one guest change with its verdict and, for applicable
// files, the exact bytes to write.
type WorkspaceApplyFile struct {
	Change GuestChange
	// Verdict is apply, conflict or excluded.
	Verdict WorkspaceApplyVerdict
	// Conflict is set when Verdict is conflict.
	Conflict WorkspaceApplyConflictKind
	// Exclusion is set when Verdict is excluded; ExclusionReason carries
	// the internal detail for logs and tests.
	Exclusion       TransferExclusion
	ExclusionReason string
	// Content is the postimage for files that will be written (nil for
	// deletions and non-applicable files).
	Content []byte
	// Mode is the guest permission bits to give the written file.
	Mode fs.FileMode
}

// WorkspaceApplyPlan is the full, read-only result of inspecting the guest:
// everything the agent changed, with a verdict per file, before any byte is
// written to the host.
type WorkspaceApplyPlan struct {
	// Root is the host directory writes land in: the transfer source
	// (cfg.WorkspaceDir), which is where the snapshot came from. It is not
	// necessarily the project root.
	Root string
	// SnapshotCommit is the guest commit the changes were measured against.
	SnapshotCommit string
	// Files lists every changed file, in the diff's (path-sorted) order.
	Files []WorkspaceApplyFile
	// SecretsUnscanned reports that the secret detector was unavailable
	// when the incoming content was checked.
	SecretsUnscanned bool
}

// Applicable reports the files whose verdict allows a write.
func (p *WorkspaceApplyPlan) Applicable() []WorkspaceApplyFile {
	var out []WorkspaceApplyFile
	for _, f := range p.Files {
		if f.Verdict == WorkspaceApplyOK {
			out = append(out, f)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Diff parsing
// ---------------------------------------------------------------------------

// parseGitDiff parses the output of
// git -c core.quotePath=false diff --no-renames --full-index
//
//	--src-prefix=a/ --dst-prefix=b/ <commit>
//
// into one GuestChange per section. Anything it cannot attribute to a file
// path is an error: the caller must fail closed rather than guess which file
// a section belongs to.
func parseGitDiff(diff string) ([]GuestChange, error) {
	lines := strings.Split(diff, "\n")
	var out []GuestChange
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if !strings.HasPrefix(line, "diff --git ") {
			return nil, fmt.Errorf("cannot parse the guest diff: unexpected line %d: %q", i+1, line)
		}
		rel, err := splitDiffGitPath(line)
		if err != nil {
			return nil, err
		}
		start := i
		i++
		var c GuestChange
		c.Rel = rel
		// Header block: everything git prints before the first hunk.
		for i < len(lines) {
			l := lines[i]
			switch {
			case strings.HasPrefix(l, "old mode "):
				c.OldMode = strings.TrimSpace(strings.TrimPrefix(l, "old mode "))
			case strings.HasPrefix(l, "new mode "):
				c.NewMode = strings.TrimSpace(strings.TrimPrefix(l, "new mode "))
			case strings.HasPrefix(l, "new file mode "):
				c.Status = GuestChangeAdded
				c.NewMode = strings.TrimSpace(strings.TrimPrefix(l, "new file mode "))
			case strings.HasPrefix(l, "deleted file mode "):
				c.Status = GuestChangeDeleted
				c.OldMode = strings.TrimSpace(strings.TrimPrefix(l, "deleted file mode "))
			case strings.HasPrefix(l, "index "):
				pre, post, mode, err := parseDiffIndexLine(l)
				if err != nil {
					return nil, err
				}
				c.PreHash, c.PostHash = pre, post
				if mode != "" {
					if c.OldMode == "" {
						c.OldMode = mode
					}
					if c.NewMode == "" {
						c.NewMode = mode
					}
				}
			case strings.HasPrefix(l, "Binary files "):
				c.Binary = true
			case strings.HasPrefix(l, "GIT binary patch"):
				c.Binary = true
			case strings.HasPrefix(l, "similarity index "), strings.HasPrefix(l, "dissimilarity index "),
				strings.HasPrefix(l, "rename from "), strings.HasPrefix(l, "rename to "),
				strings.HasPrefix(l, "copy from "), strings.HasPrefix(l, "copy to "),
				strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "):
				// --no-renames makes the rename/copy headers unreachable;
				// the ---/+++ paths are redundant with the section header.
			default:
				goto headersDone
			}
			i++
		}
	headersDone:
		if c.Status == "" {
			c.Status = GuestChangeModified
		}
		// Hunks, bounded by the line counts in each @@ header so file
		// content that looks like diff syntax cannot end a section early.
		for i < len(lines) && strings.HasPrefix(lines[i], "@@ ") {
			oldCount, newCount, err := parseHunkHeader(lines[i])
			if err != nil {
				return nil, err
			}
			i++
			for (oldCount > 0 || newCount > 0) && i < len(lines) {
				l := lines[i]
				switch {
				case strings.HasPrefix(l, "\\"):
					// "\ No newline at end of file": consumes nothing.
				case strings.HasPrefix(l, "-"):
					oldCount--
					c.DelCount++
				case strings.HasPrefix(l, "+"):
					newCount--
					c.AddCount++
				default:
					oldCount--
					newCount--
				}
				i++
			}
			if oldCount > 0 || newCount > 0 {
				return nil, fmt.Errorf("cannot parse the guest diff: the hunk for %s is truncated", rel)
			}
			// "\ No newline at end of file" trails the line it
			// annotates, including the hunk's last one.
			for i < len(lines) && strings.HasPrefix(lines[i], "\\") {
				i++
			}
		}
		if i < len(lines) && !strings.HasPrefix(lines[i], "diff --git ") && strings.TrimSpace(lines[i]) != "" {
			return nil, fmt.Errorf("cannot parse the guest diff: unexpected content after the change to %s: %q", rel, lines[i])
		}
		c.Diff = strings.Join(lines[start:i], "\n") + "\n"
		out = append(out, c)
	}
	return out, nil
}

// parseDiffIndexLine reads "index <pre>..<post>[ <mode>]".
func parseDiffIndexLine(line string) (pre, post, mode string, err error) {
	rest := strings.TrimPrefix(line, "index ")
	mode = ""
	if sp := strings.LastIndex(rest, " "); sp >= 0 {
		mode = strings.TrimSpace(rest[sp+1:])
		rest = rest[:sp]
	}
	pre, post, ok := strings.Cut(rest, "..")
	if !ok || pre == "" || post == "" {
		return "", "", "", fmt.Errorf("cannot parse the guest diff: malformed index line %q", line)
	}
	return pre, post, mode, nil
}

// parseHunkHeader reads "@@ -a[,b] +c[,d] @@ ..." and returns the old and new
// line counts the hunk must contain.
func parseHunkHeader(line string) (oldCount, newCount int, err error) {
	rest := strings.TrimPrefix(line, "@@ ")
	end := strings.Index(rest, " @@")
	if end < 0 {
		return 0, 0, fmt.Errorf("cannot parse the guest diff: malformed hunk header %q", line)
	}
	rest = rest[:end]
	fields := strings.Fields(rest)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		return 0, 0, fmt.Errorf("cannot parse the guest diff: malformed hunk header %q", line)
	}
	oldCount, err = hunkRangeCount(fields[0])
	if err != nil {
		return 0, 0, err
	}
	newCount, err = hunkRangeCount(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return oldCount, newCount, nil
}

// hunkRangeCount reads the count of "-a,b" / "+a" (count defaults to 1).
func hunkRangeCount(field string) (int, error) {
	spec := field[1:]
	if comma := strings.Index(spec, ","); comma >= 0 {
		count, err := strconv.Atoi(spec[comma+1:])
		if err != nil || count < 0 {
			return 0, fmt.Errorf("cannot parse the guest diff: malformed hunk range %q", field)
		}
		return count, nil
	}
	return 1, nil
}

// splitDiffGitPath extracts the file path from
// "diff --git a/<path> b/<path>" (unquoted) or
// "diff --git \"a/<path>\" \"b/<path>\"" (C-quoted, when the path contains
// quotes, backslashes or control bytes even with core.quotePath=false).
//
// The path cannot be taken from the ---/+++ lines: those carry a trailing tab
// when the path contains spaces, and /dev/null for adds and deletes. The two
// prefixes in the section header are the authority. --no-renames guarantees
// both sides name the same path.
func splitDiffGitPath(line string) (string, error) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if strings.HasPrefix(rest, "\"") {
		end := -1
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\\' {
				i++
				continue
			}
			if rest[i] == '"' {
				end = i
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("cannot parse the guest diff: malformed section header %q", line)
		}
		first := rest[:end+1]
		second := strings.TrimSpace(rest[end+1:])
		firstPath, ok := unquoteGitPath(first)
		secondPath, ok2 := unquoteGitPath(second)
		if !ok || !ok2 || !strings.HasPrefix(firstPath, "a/") || !strings.HasPrefix(secondPath, "b/") || firstPath[2:] != secondPath[2:] {
			return "", fmt.Errorf("cannot parse the guest diff: the section header names two different paths: %q", line)
		}
		return firstPath[2:], nil
	}
	// Unquoted: rest is "a/<path> b/<path>". The split point is the latest
	// " b/" whose suffix equals the a/-prefixed path; only the true split
	// can satisfy that equality, so scanning from the end is unambiguous.
	for i := len(rest) - 1; i >= 0; i-- {
		if !strings.HasPrefix(rest[i:], " b/") {
			continue
		}
		if rest[i+3:] == rest[2:i] {
			return rest[2:i], nil
		}
	}
	return "", fmt.Errorf("cannot parse the guest diff: malformed section header %q", line)
}

// unquoteGitPath reverses git's C-style path quoting (the same escapes as
// git's quote_c_style: \n, \t, \\, \", octal bytes).
func unquoteGitPath(p string) (string, bool) {
	if len(p) < 2 || p[0] != '"' || p[len(p)-1] != '"' {
		return p, true
	}
	body := p[1 : len(p)-1]
	var b []byte
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b = append(b, c)
			continue
		}
		i++
		if i >= len(body) {
			return "", false
		}
		switch body[i] {
		case 'a':
			b = append(b, '\a')
		case 'b':
			b = append(b, '\b')
		case 'f':
			b = append(b, '\f')
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'v':
			b = append(b, '\v')
		case '\\', '"':
			b = append(b, body[i])
		default:
			if body[i] < '0' || body[i] > '7' || i+2 >= len(body) {
				return "", false
			}
			v := 0
			for j := 0; j < 3; j++ {
				d := body[i+j]
				if d < '0' || d > '7' {
					return "", false
				}
				v = v*8 + int(d-'0')
			}
			b = append(b, byte(v))
			i += 2
		}
	}
	return string(b), true
}

// gitBlobHash computes the git blob hash of data ("blob <size>\0" + data),
// which is what the diff's index line prints with --full-index. It is how the
// host preimage is compared against the snapshot: byte-exact, not hunk-local.
func gitBlobHash(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d", len(data))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// maxPreimageBytes bounds how much of a host file the apply reads to verify
// its preimage. No file larger than the transfer cap can ever have crossed
// into the guest — the size-cap exclusion is structural, not overridable by a
// per-file decision — so the snapshot preimage is never bigger than the cap.
// A host file beyond it therefore cannot be the preimage a snapshot hash
// describes: it is a conflict decided from the size alone, and a giant host
// edit cannot push the apply into reading it whole.
const maxPreimageBytes = DefaultTransferMaxFileBytes

// gitBlobHashFile computes the git blob hash of the regular file behind the
// open descriptor, streaming it through a fixed buffer so a file near the
// bound never sits whole in memory. It reports false when the file is not a
// regular file, is larger than maxPreimageBytes (it cannot be the preimage
// the hash describes), fails to read, or changes size while being read.
func gitBlobHashFile(file *os.File) (string, bool) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPreimageBytes {
		return "", false
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d", info.Size())
	_, _ = h.Write([]byte{0})
	buf := make([]byte, 1<<20)
	var total int64
	for total < info.Size() {
		n, err := file.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
			total += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
	}
	// The size must still be what it was when the hashing started: a file
	// that grew or shrank under the read is not the bytes just hashed.
	cur, err := file.Stat()
	if err != nil || cur.Size() != info.Size() || total != info.Size() {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// ---------------------------------------------------------------------------
// The last-transfer record
// ---------------------------------------------------------------------------

// lastTransferSchemaVersion is the schema of last-transfer.json. A record with
// an unknown schema is not trusted: applying on top of a misread snapshot is
// exactly the overwrite the conflict rules exist to prevent.
const lastTransferSchemaVersion = 1

// guestSnapshotCommitMessage is the commit message ensureGuestRepository uses
// for every snapshot. It doubles as the search key when the recorded commit
// hash cannot be used.
const guestSnapshotCommitMessage = "just-code: initial guest snapshot from the host working tree"

// LastTransferExclusion is one path the transfer filter refused, as recorded at
// the last successful sync.
type LastTransferExclusion struct {
	Rel       string            `json:"rel"`
	Exclusion TransferExclusion `json:"exclusion"`
}

// LastTransferRecord is the host-side memory of the most recent host → guest
// transfer: the guest commit it produced (the snapshot the agent's work is
// measured against) and the paths the filter refused.
type LastTransferRecord struct {
	SchemaVersion  int                     `json:"schemaVersion"`
	RecordedAt     string                  `json:"recordedAt"`
	SnapshotCommit string                  `json:"snapshotCommit,omitempty"`
	Excluded       []LastTransferExclusion `json:"excluded,omitempty"`
}

// LastTransferPath is where the record lives: host state, keyed by instance.
func LastTransferPath(stateDir, instance string) string {
	return filepath.Join(InstanceStateDir(stateDir, instance), "last-transfer.json")
}

// LastTransferStore loads and saves the record.
type LastTransferStore struct {
	Path string
	FS   FS
}

// Load returns the record. A missing record is an error, not a silent zero
// value: without it, the exclusions of the transfer that built the guest's
// snapshot are unknowable, and applying without them could bring back a file
// the filter refused at sync time. Instances created before records were
// written, and transfers whose record could not be saved, land here and are
// told how to get the guest's work without the record.
func (s LastTransferStore) Load() (LastTransferRecord, error) {
	data, err := s.FS.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LastTransferRecord{}, fmt.Errorf("no last-transfer record at %s: this instance was created before transfer records were written, or its last record could not be saved. "+
				"The agent's work is safe in the guest: retrieve it with 'just-code workspace export', or re-run 'just-code workspace sync' to write a fresh record, then retry", s.Path)
		}
		return LastTransferRecord{}, err
	}
	var rec LastTransferRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return LastTransferRecord{}, fmt.Errorf("%s is not valid JSON: %w", s.Path, err)
	}
	if rec.SchemaVersion != lastTransferSchemaVersion {
		return LastTransferRecord{}, fmt.Errorf("%s records schema version %d, expected %d", s.Path, rec.SchemaVersion, lastTransferSchemaVersion)
	}
	return rec, nil
}

// Save writes the record atomically.
func (s LastTransferStore) Save(rec LastTransferRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.FS, s.Path, append(data, '\n'), 0o600)
}

// Invalidate removes the record. After it, apply refuses on the missing
// record instead of trusting one whose baseline and exclusions may no longer
// describe the guest. A record that is already absent is already invalid, so
// that is not an error; any other removal failure is, because a record that
// cannot be removed cannot be kept honest either.
func (s LastTransferStore) Invalidate() error {
	if err := s.FS.Remove(s.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// newLastTransferStore is the constructor the transfer flow uses for the
// record; tests replace it to exercise a store whose removal or save fails.
var newLastTransferStore = func(stateDir, instance string) LastTransferStore {
	return LastTransferStore{Path: LastTransferPath(stateDir, instance), FS: DefaultFS}
}

// recordLastTransfer persists the transfer's outcome: the snapshot commit the
// guest just received, and the paths the filter refused. It never fails the
// transfer (the files have already crossed). The previous record was already
// invalidated before the transfer's first guest write (see transferIntoGuest),
// so this save is the only writer this run: a failed save leaves no record at
// all — and apply refuses without one — instead of trusting exclusions from an
// earlier transfer.
func (m *MicrosandboxRuntime) recordLastTransfer(ctx context.Context, manifest TransferManifest) {
	store := newLastTransferStore(m.stateDirOrDefault(), m.InstanceName())
	rec := LastTransferRecord{
		SchemaVersion: lastTransferSchemaVersion,
		RecordedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	for _, entry := range manifest.Entries {
		if entry.Included || entry.Exclusion == TransferExclusionNone {
			continue
		}
		rec.Excluded = append(rec.Excluded, LastTransferExclusion{Rel: entry.Rel, Exclusion: entry.Exclusion})
	}
	// The snapshot commit is read from the guest right after the snapshot
	// commit: HEAD is the commit ensureGuestRepository just made.
	stdout, _, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+" && git rev-parse HEAD")
	if err != nil || code != 0 {
		fmt.Fprintf(os.Stderr, "Warning: cannot record the guest snapshot commit for %s; 'just-code workspace apply' will refuse until the next sync records one — the agent's work stays safe in the guest and 'just-code workspace export' still works\n", m.InstanceName())
		return
	}
	rec.SnapshotCommit = strings.TrimSpace(stdout)
	if err := store.Save(rec); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot save the last-transfer record for %s (%v); 'just-code workspace apply' will refuse until the next sync records one — the agent's work stays safe in the guest and 'just-code workspace export' still works\n", m.InstanceName(), err)
	}
}

// ---------------------------------------------------------------------------
// Reading the guest's changes
// ---------------------------------------------------------------------------

// PlanGuestApply inspects the guest and classifies every change, without
// writing anything to the host. The caller shows the plan, takes the user's
// per-file choices, then hands the chosen files to ApplyGuestFiles.
func (m *MicrosandboxRuntime) PlanGuestApply(ctx context.Context) (*WorkspaceApplyPlan, error) {
	// The reads below mutate the guest repository (git add -A -N, exactly
	// like export); on a bind-mounted instance that repository is the
	// user's own checkout.
	if err := m.requireSealedWorkspace(ctx); err != nil {
		return nil, err
	}
	provisioned, err := m.guestWorkspaceProvisioned(ctx)
	if err != nil {
		return nil, err
	}
	if !provisioned {
		return nil, fmt.Errorf("the guest workspace of %s has no snapshot yet, so there is no baseline to measure the agent's work against; start the project first ('just-code start')", m.InstanceName())
	}
	changes, baseline, err := m.guestChanges(ctx)
	if err != nil {
		return nil, err
	}
	plan := &WorkspaceApplyPlan{
		Root:           m.cfg.WorkspaceDir,
		SnapshotCommit: baseline,
	}
	if len(changes) == 0 {
		return plan, nil
	}
	// The current host filter: the same rules that decide what crosses
	// forward decide what may cross back. Recorded re-inclusions apply in
	// both directions, and an unattributable finding still refuses.
	optIn := m.mergeRecordedOptIn(nil)
	manifest, err := ResolveTransferSet(ctx, m.cfg.WorkspaceDir, TransferOptions{OptIn: optIn})
	if err != nil {
		return nil, err
	}
	if len(manifest.UnmappedFindings) > 0 {
		return nil, fmt.Errorf("the secret detector reported %d finding(s) whose paths could not be matched to a file in %s, so the reverse filter cannot tell which candidate to exclude: "+
			"inspect 'just-code workspace status' and resolve the unmatched findings before retrying",
			len(manifest.UnmappedFindings), manifest.Root)
	}
	files, err := m.classifyGuestChanges(ctx, changes, manifest)
	if err != nil {
		return nil, err
	}
	// Fetch the incoming content (postimages) for the applicable files,
	// enforcing the size cap and the secret scan on the way.
	if err := m.attachIncomingContent(ctx, plan, files); err != nil {
		return nil, err
	}
	plan.Files = files
	return plan, nil
}

// guestChanges reads and parses the guest's diff against the snapshot the
// host transferred, returning the changes and the baseline commit used.
func (m *MicrosandboxRuntime) guestChanges(ctx context.Context) ([]GuestChange, string, error) {
	rec, err := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}.Load()
	if err != nil {
		return nil, "", fmt.Errorf("cannot read the last-transfer record for %s: %w", m.InstanceName(), err)
	}
	baseline, err := m.guestSnapshotCommit(ctx, rec)
	if err != nil {
		return nil, "", err
	}
	diff, err := m.guestDiffText(ctx, baseline)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(diff) == "" {
		return nil, baseline, nil
	}
	changes, err := parseGitDiff(diff)
	if err != nil {
		return nil, "", err
	}
	// A mode-only change carries no index line, so no blob hashes; they
	// are resolved from the baseline tree. Everything else must already
	// have its hashes: an unprovable preimage or postimage refuses the
	// whole plan rather than weakening the verification.
	if err := m.resolveModeOnlyHashes(ctx, baseline, changes); err != nil {
		return nil, "", err
	}
	if err := verifyGuestChangeHashes(changes); err != nil {
		return nil, "", err
	}
	return changes, baseline, nil
}

// resolveModeOnlyHashes fills the blob hashes of mode-only changes (same
// content, different mode: the diff has no index line for them). The blob is
// read from the baseline tree in one batched call, and both the preimage and
// the postimage hash are set to it: the host preimage can then be verified
// byte-exactly like any other change, and the fetched content must match it.
func (m *MicrosandboxRuntime) resolveModeOnlyHashes(ctx context.Context, baseline string, changes []GuestChange) error {
	var need []string
	for _, c := range changes {
		if c.Status == GuestChangeModified && c.PreHash == "" && c.PostHash == "" {
			need = append(need, c.Rel)
		}
	}
	if len(need) == 0 {
		return nil
	}
	var b strings.Builder
	// --literal-pathspecs keeps glob characters in paths literal; -z keeps
	// the entries NUL-terminated and the paths unquoted. The paths stay
	// slash-separated: the guest is Linux even on a Windows host.
	b.WriteString(guestCd() + guestGitFragment(guestLsTreeArgs(baseline, need)))
	stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(), b.String())
	if err != nil {
		return err
	}
	if code != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = "git ls-tree failed in the guest workspace"
		}
		return fmt.Errorf("the guest could not resolve the unchanged content of %d mode-only change(s): %s", len(need), detail)
	}
	blobs := parseGitLsTree(stdout)
	for i := range changes {
		c := &changes[i]
		if c.Status != GuestChangeModified || c.PreHash != "" || c.PostHash != "" {
			continue
		}
		hash, ok := blobs[c.Rel]
		if !ok {
			return fmt.Errorf("cannot resolve the blob of %s at the transferred snapshot; the machine may have changed while applying — retry 'just-code workspace apply'", c.Rel)
		}
		c.PreHash = hash
		c.PostHash = hash
	}
	return nil
}

// parseGitLsTree reads the NUL-terminated entries of git ls-tree -z, keeping
// only blob entries: <mode> <type> <hash>\t<path>.
func parseGitLsTree(stdout string) map[string]string {
	blobs := map[string]string{}
	for _, entry := range strings.Split(stdout, "\x00") {
		entry = strings.TrimSuffix(entry, "\n")
		tab := strings.IndexByte(entry, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(entry[:tab])
		path := entry[tab+1:]
		if len(fields) != 3 || fields[1] != "blob" || path == "" {
			continue
		}
		blobs[path] = fields[2]
	}
	return blobs
}

// verifyGuestChangeHashes is the fail-closed backstop: every change that will
// be verified against host or guest bytes must carry the hash to verify
// against. A change without one would silently skip the byte comparison —
// exactly what must never happen — so it refuses the whole plan.
func verifyGuestChangeHashes(changes []GuestChange) error {
	for _, c := range changes {
		switch c.Status {
		case GuestChangeAdded:
			if c.PostHash == "" {
				return fmt.Errorf("the guest diff gave no postimage hash for the added file %s, so its content cannot be verified; nothing was written — retry 'just-code workspace apply'", c.Rel)
			}
		case GuestChangeDeleted:
			if c.PreHash == "" {
				return fmt.Errorf("the guest diff gave no preimage hash for the deleted file %s, so the host copy cannot be verified before deletion; nothing was written — retry 'just-code workspace apply'", c.Rel)
			}
		default:
			if c.PreHash == "" || c.PostHash == "" {
				return fmt.Errorf("the guest diff gave no blob hash for the modified file %s, so neither the host preimage nor the incoming content can be verified; nothing was written — retry 'just-code workspace apply'", c.Rel)
			}
		}
	}
	return nil
}

// guestSnapshotCommit resolves the commit the agent's work must be measured
// against: the one the last transfer recorded, verified to still exist in the
// guest. There is deliberately no history search fallback — a commit found by
// message could belong to a different transfer round than the one that
// carried the recorded exclusions, and measuring against a guessed baseline
// could misattribute the agent's work. A record that cannot be verified
// refuses the apply with instructions that keep the guest's work reachable.
func (m *MicrosandboxRuntime) guestSnapshotCommit(ctx context.Context, rec LastTransferRecord) (string, error) {
	if rec.SnapshotCommit == "" {
		return "", fmt.Errorf("the last-transfer record for %s names no snapshot commit; "+
			"re-run 'just-code workspace sync' to record the current snapshot, or retrieve the agent's work with 'just-code workspace export', then retry", m.InstanceName())
	}
	stdout, _, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
		"cd "+shellQuote(msbGuestWorkspace)+" && git cat-file -e "+shellQuote(rec.SnapshotCommit+"^{commit}")+" && echo yes || echo no")
	if err != nil {
		return "", err
	}
	if code != 0 || strings.TrimSpace(stdout) != "yes" {
		return "", fmt.Errorf("the snapshot commit recorded for %s is absent from the guest repository: the record is stale, the guest was rebuilt without a new sync; "+
			"re-run 'just-code workspace sync' to record the current snapshot, or retrieve the agent's work with 'just-code workspace export', then retry", m.InstanceName())
	}
	return rec.SnapshotCommit, nil
}

// ---------------------------------------------------------------------------
// Guest command construction
// ---------------------------------------------------------------------------

// guestCd is the prefix every guest command starts with: the workspace the
// commands below operate on.
func guestCd() string {
	return "cd " + shellQuote(msbGuestWorkspace) + " && "
}

// guestGitFragment quotes a git argv into a shell fragment. The paths embedded
// in args are slash-separated and are used verbatim: the guest is Linux even
// when the host is Windows, so a guest path must never pass through
// filepath.FromSlash — on a Windows host it would rewrite a/b into a\b and
// point the guest at the wrong file.
func guestGitFragment(args []string) string {
	var b strings.Builder
	b.WriteString("git")
	for _, a := range args {
		b.WriteString(" " + shellQuote(a))
	}
	return b.String()
}

// guestDiffArgs builds the git argv of the diff that measures the guest's
// work against the snapshot baseline. binary adds --binary for literal
// payloads; rels limits the diff to exactly those slash-separated paths.
//
// --no-ext-diff and --no-textconv pin the output to git's internal diff
// machinery: the guest's git configuration is agent-controlled, and
// diff.external or a textconv driver selected through .gitattributes could
// display content that is not the file's bytes. The displayed diff is what
// the user reviews before anything is written, so it must be the real content
// whatever the guest is configured to.
func guestDiffArgs(baseline string, binary bool, rels []string) []string {
	args := []string{"--literal-pathspecs", "-c", "core.quotePath=false", "diff",
		"--no-ext-diff", "--no-textconv", "--no-renames", "--full-index"}
	if binary {
		args = append(args, "--binary")
	}
	args = append(args, "--src-prefix=a/", "--dst-prefix=b/", baseline)
	if len(rels) > 0 {
		args = append(args, "--")
		args = append(args, rels...)
	}
	return args
}

// guestLsTreeArgs builds the git argv listing the baseline tree's blob entries
// for rels, the mode-only hash resolution.
func guestLsTreeArgs(baseline string, rels []string) []string {
	return append([]string{"--literal-pathspecs", "-c", "core.quotePath=false", "ls-tree", "-z", baseline, "--"}, rels...)
}

// guestStatArgs builds the stat argv reporting the byte sizes of rels.
func guestStatArgs(rels []string) []string {
	return append([]string{"-c", "%s", "--"}, rels...)
}

// guestStatCommand quotes the size probe into a guest command; rels stay
// slash-separated (see guestGitFragment).
func guestStatCommand(rels []string) string {
	var b strings.Builder
	b.WriteString(guestCd() + "stat")
	for _, a := range guestStatArgs(rels) {
		b.WriteString(" " + shellQuote(a))
	}
	return b.String()
}

// guestTarArgs builds the tar argv streaming the named paths' content.
func guestTarArgs(rels []string) []string {
	return append([]string{"-czf", "-", "--"}, rels...)
}

// guestTarCommand quotes the content read into a guest command; rels stay
// slash-separated (see guestGitFragment).
func guestTarCommand(rels []string) string {
	var b strings.Builder
	b.WriteString(guestCd() + "tar")
	for _, a := range guestTarArgs(rels) {
		b.WriteString(" " + shellQuote(a))
	}
	return b.String()
}

// guestDiffText produces the guest's diff from the snapshot to the working
// tree. It measures against the snapshot commit, not HEAD: the agent may
// commit its work inside the guest, and measuring from HEAD would silently
// drop everything it committed.
func (m *MicrosandboxRuntime) guestDiffText(ctx context.Context, baseline string) (string, error) {
	// -N makes untracked files appear in the diff; --no-renames keeps every
	// section about exactly one path; --full-index gives complete blob
	// hashes so the host preimage can be verified byte-exactly; the
	// explicit prefixes and core.quotePath=false keep the paths literal;
	// --no-ext-diff and --no-textconv keep the diff git's own whatever the
	// guest is configured to (see guestDiffArgs).
	cmd := guestCd() + "git add -A -N && " + guestGitFragment(guestDiffArgs(baseline, false, nil))
	stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(), cmd)
	if err != nil {
		return "", err
	}
	if code != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = "git diff failed in the guest workspace"
		}
		return "", fmt.Errorf("the guest could not produce a diff (exit %d): %s", code, detail)
	}
	return stdout, nil
}

// ---------------------------------------------------------------------------
// Classification
// ---------------------------------------------------------------------------

// classifyGuestChanges turns parsed changes into verdicts. It never reads
// content: the exclusion and conflict rules are decided from the sync record,
// the current manifest, the diff metadata and the host's preimage bytes.
func (m *MicrosandboxRuntime) classifyGuestChanges(ctx context.Context, changes []GuestChange, manifest TransferManifest) ([]WorkspaceApplyFile, error) {
	rec, err := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}.Load()
	if err != nil {
		return nil, fmt.Errorf("cannot read the last-transfer record for %s: %w", m.InstanceName(), err)
	}
	recordedExcluded := map[string]TransferExclusion{}
	for _, ex := range rec.Excluded {
		recordedExcluded[ex.Rel] = ex.Exclusion
	}
	entries := map[string]TransferEntry{}
	for _, entry := range manifest.Entries {
		entries[entry.Rel] = entry
	}
	// Paths absent from the current manifest (the host file disappeared
	// or was created after the last sync) are evaluated against the path
	// rules directly; git-ignore status is checked in one batch.
	var absent []string
	for _, c := range changes {
		if _, ok := entries[NormalizeTransferPath(c.Rel)]; !ok {
			absent = append(absent, NormalizeTransferPath(c.Rel))
		}
	}
	ignored, err := gitCheckIgnoredBatch(ctx, manifest.Root, absent)
	if err != nil {
		return nil, err
	}
	files := make([]WorkspaceApplyFile, 0, len(changes))
	for _, c := range changes {
		files = append(files, m.classifyOneGuestChange(c, recordedExcluded, entries, ignored))
	}
	return files, nil
}

// classifyOneGuestChange assigns one change's verdict.
func (m *MicrosandboxRuntime) classifyOneGuestChange(c GuestChange, recordedExcluded map[string]TransferExclusion, entries map[string]TransferEntry, ignored map[string]bool) WorkspaceApplyFile {
	f := WorkspaceApplyFile{Change: c}
	rel := NormalizeTransferPath(c.Rel)
	excluded := func(kind TransferExclusion, reason string) WorkspaceApplyFile {
		f.Verdict = WorkspaceApplyExcluded
		f.Exclusion = kind
		f.ExclusionReason = reason
		return f
	}
	conflict := func(kind WorkspaceApplyConflictKind) WorkspaceApplyFile {
		f.Verdict = WorkspaceApplyConflict
		f.Conflict = kind
		return f
	}
	if rel == "" {
		return excluded(TransferExclusionIrregular, "the diff path is not a usable relative path")
	}
	// A typechange to a link or gitlink is the same refusal as on the way
	// in: writing a symlink would point somewhere unreviewed, and a
	// gitlink is not content.
	if strings.HasPrefix(c.NewMode, "120") || strings.HasPrefix(c.OldMode, "120") {
		return excluded(TransferExclusionSymlink, "the change turns the file into a symlink")
	}
	if strings.HasPrefix(c.NewMode, "160") || strings.HasPrefix(c.OldMode, "160") {
		return excluded(TransferExclusionSubmodule, "the change turns the file into a gitlink")
	}
	// Refused at the sync that created this snapshot: the file never
	// crossed, so the agent's "modification" is work on a path the filter
	// refuses even when the host file has since changed or disappeared.
	if kind, ok := recordedExcluded[rel]; ok {
		return excluded(kind, "excluded by the transfer filter at the last sync")
	}
	entry, inManifest := entries[rel]
	if inManifest {
		if !entry.Included {
			return excluded(entry.Exclusion, "excluded by the current transfer filter")
		}
	} else {
		// Absent from the current manifest: the host file is gone, or
		// the path was never enumerated. The rules are evaluated per
		// path so a vanished host file cannot smuggle agent content
		// back in under a name the filter would refuse.
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			return excluded(TransferExclusionGitMetadata, "inside the .git directory")
		}
		if isDotenvName(path.Base(rel)) {
			return excluded(TransferExclusionDotenv, "dotenv-named file")
		}
		if hasSymlinkComponent(m.cfg.WorkspaceDir, rel) {
			return excluded(TransferExclusionSymlink, "the host path is or passes through a symlink")
		}
		if ignored[rel] {
			return excluded(TransferExclusionGitIgnored, "ignored by git")
		}
	}
	// Conflict rules: the host must still byte-match the snapshot the
	// agent started from. Anything else is a conflict, never an overwrite.
	hostPath := filepath.Join(m.cfg.WorkspaceDir, filepath.FromSlash(rel))
	switch c.Status {
	case GuestChangeAdded:
		if _, err := os.Lstat(hostPath); err == nil {
			return conflict(WorkspaceConflictHostExists)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return conflict(WorkspaceConflictHostExists)
		}
	default:
		// Modified or deleted: the preimage must be present and
		// byte-identical to the transferred snapshot. The read is
		// bounded and streamed: a host file larger than the transfer
		// cap cannot be the transferred preimage, so it is a conflict
		// decided from the size, never a multi-gigabyte read.
		info, err := os.Lstat(hostPath)
		if errors.Is(err, fs.ErrNotExist) {
			return conflict(WorkspaceConflictHostMissing)
		}
		if err != nil {
			return conflict(WorkspaceConflictHostModified)
		}
		if !info.Mode().IsRegular() || info.Size() > maxPreimageBytes {
			return conflict(WorkspaceConflictHostModified)
		}
		file, err := os.Open(hostPath)
		if err != nil {
			return conflict(WorkspaceConflictHostModified)
		}
		hash, ok := gitBlobHashFile(file)
		file.Close()
		if !ok || (c.PreHash != "" && hash != c.PreHash) {
			return conflict(WorkspaceConflictHostModified)
		}
	}
	f.Verdict = WorkspaceApplyOK
	return f
}

// hasSymlinkComponent reports whether rel, resolved under root, is a symlink
// or passes through one. Content is never written through a link: the write
// would land wherever the link points, outside the reviewed tree.
func hasSymlinkComponent(root, rel string) bool {
	if root == "" {
		return false
	}
	parts := strings.Split(rel, "/")
	cur := root
	for _, p := range parts {
		cur = filepath.Join(cur, p)
		if info, err := os.Lstat(cur); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// gitCheckIgnoredBatch reports which of rels git ignores under root, in one
// git invocation. It is only consulted for paths the current manifest does
// not list (the manifest already carries the ignore verdict for existing
// files). Without git, or outside a repository, nothing is reported ignored —
// the same degradation the forward filter has without git.
func gitCheckIgnoredBatch(ctx context.Context, root string, rels []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(rels) == 0 {
		return ignored, nil
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return ignored, nil
	}
	if !gitWorktreeRootExists(root) {
		return ignored, nil
	}
	cmd := exec.CommandContext(ctx, git, "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(rels, "\x00"))
	stdout := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		// Exit code 1 means "no path is ignored"; anything else is a
		// real failure, and an unreadable ignore verdict must not
		// silently turn into "not ignored".
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() > 1 {
			return nil, fmt.Errorf("cannot evaluate git ignore rules under %s: %w", root, err)
		}
	}
	for _, p := range strings.Split(stdout.String(), "\x00") {
		if p != "" {
			ignored[filepath.ToSlash(filepath.Clean(p))] = true
		}
	}
	return ignored, nil
}

// ---------------------------------------------------------------------------
// Incoming content: fetch, size cap, hash check, secret scan
// ---------------------------------------------------------------------------

// attachIncomingContent fetches the postimage of every applicable non-deleted
// file from the guest, enforces the per-file size cap, verifies each file
// still hashes to what the diff reported (a mismatch means the guest changed
// between the diff and the read — abort rather than write stale content), and
// runs the secret detector over the incoming bytes.
func (m *MicrosandboxRuntime) attachIncomingContent(ctx context.Context, plan *WorkspaceApplyPlan, files []WorkspaceApplyFile) error {
	var fetch []string
	for _, f := range files {
		if f.Verdict == WorkspaceApplyOK && f.Change.Status != GuestChangeDeleted {
			fetch = append(fetch, f.Change.Rel)
		}
	}
	if len(fetch) == 0 {
		return nil
	}
	// The size cap is checked before the tarball is read, so an oversized
	// file never transits the host's memory.
	sizes, err := m.guestFileSizes(ctx, fetch)
	if err != nil {
		return err
	}
	var capped []string
	for i, rel := range fetch {
		if sizes[i] > DefaultTransferMaxFileBytes {
			for j := range files {
				if files[j].Change.Rel == rel {
					files[j].Verdict = WorkspaceApplyExcluded
					files[j].Exclusion = TransferExclusionSizeCap
					files[j].ExclusionReason = fmt.Sprintf("the incoming file is larger than the %s transfer cap", FormatByteSize(DefaultTransferMaxFileBytes))
				}
			}
			capped = append(capped, rel)
		}
	}
	fetch = removePaths(fetch, capped)
	if len(fetch) == 0 {
		return nil
	}
	payload, err := m.guestReadFiles(ctx, fetch)
	if err != nil {
		return err
	}
	contents, modes, err := extractTransferTarball(payload, fetch)
	if err != nil {
		return err
	}
	for i := range files {
		f := &files[i]
		if f.Verdict != WorkspaceApplyOK || f.Change.Status == GuestChangeDeleted {
			continue
		}
		data, ok := contents[f.Change.Rel]
		if !ok {
			return fmt.Errorf("the guest did not return the content of %s; the machine may have changed while applying — retry", f.Change.Rel)
		}
		if f.Change.PostHash != "" && gitBlobHash(data) != f.Change.PostHash {
			return fmt.Errorf("the content of %s changed inside the guest between the diff and the read; nothing was written — retry 'just-code workspace apply'", f.Change.Rel)
		}
		f.Content = data
		f.Mode = modes[f.Change.Rel]
	}
	// The secret scan runs on the incoming bytes, not on the diff text:
	// the diff can split or elide the very line that carries a secret.
	flagged, missing, err := scanIncomingContents(ctx, files)
	if err != nil {
		return err
	}
	plan.SecretsUnscanned = missing
	for i := range files {
		if rule, ok := flagged[files[i].Change.Rel]; ok {
			files[i].Verdict = WorkspaceApplyExcluded
			files[i].Exclusion = TransferExclusionSecret
			files[i].ExclusionReason = "the secret detector flagged the incoming content"
			if rule != "" {
				files[i].ExclusionReason = fmt.Sprintf("the secret detector flagged the incoming content (%s)", rule)
			}
		}
	}
	return nil
}

// removePaths drops the capped paths from the fetch list.
func removePaths(all, drop []string) []string {
	dropped := map[string]bool{}
	for _, p := range drop {
		dropped[p] = true
	}
	out := make([]string, 0, len(all))
	for _, p := range all {
		if !dropped[p] {
			out = append(out, p)
		}
	}
	return out
}

// guestFileSizes asks the guest for the byte sizes of rels, one line per path
// in the same order.
func (m *MicrosandboxRuntime) guestFileSizes(ctx context.Context, rels []string) ([]int64, error) {
	stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(), guestStatCommand(rels))
	if err != nil {
		return nil, err
	}
	if code != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = "stat failed in the guest workspace"
		}
		return nil, fmt.Errorf("the guest could not report the incoming file sizes: %s", detail)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != len(rels) {
		return nil, fmt.Errorf("the guest reported %d size(s) for %d file(s); the machine may have changed while applying — retry", len(lines), len(rels))
	}
	sizes := make([]int64, len(lines))
	for i, l := range lines {
		n, err := strconv.ParseInt(strings.TrimSpace(l), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("the guest reported a size that is not a number (%q)", l)
		}
		sizes[i] = n
	}
	return sizes, nil
}

// guestReadFiles returns a tarball of the named paths' current content from
// the guest. The tarball carries the modes, and is binary-safe.
func (m *MicrosandboxRuntime) guestReadFiles(ctx context.Context, rels []string) ([]byte, error) {
	stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(), guestTarCommand(rels))
	if err != nil {
		return nil, err
	}
	if code != 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = "tar failed in the guest workspace"
		}
		return nil, fmt.Errorf("the guest could not read the changed files: %s", detail)
	}
	return []byte(stdout), nil
}

// extractTransferTarball unpacks the guest's tarball, requiring every wanted
// path to be present as a regular file. A symlink or unexpected entry is an
// error: the content to write must be exactly what was asked for.
func extractTransferTarball(payload []byte, want []string) (map[string][]byte, map[string]fs.FileMode, error) {
	contents := map[string][]byte{}
	modes := map[string]fs.FileMode{}
	wanted := map[string]bool{}
	for _, rel := range want {
		wanted[rel] = true
	}
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, nil, fmt.Errorf("the guest's file payload is not a readable tarball: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the guest's file payload is not a readable tarball: %w", err)
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		// bsdtar archives carry a global header entry GNU tar does not
		// emit; it carries no file content.
		if name == "pax_global_header" {
			continue
		}
		if !wanted[name] {
			return nil, nil, fmt.Errorf("the guest returned an unexpected file (%s); the machine may have changed while applying — retry", name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return nil, nil, fmt.Errorf("the guest returned %s as something other than a regular file; it is not applied", name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, fmt.Errorf("reading %s from the guest payload failed: %w", name, err)
		}
		contents[name] = data
		modes[name] = hdr.FileInfo().Mode().Perm()
	}
	for _, rel := range want {
		if _, ok := contents[rel]; !ok {
			return nil, nil, fmt.Errorf("the guest did not return the content of %s; the machine may have changed while applying — retry", rel)
		}
	}
	return contents, modes, nil
}

// scanIncomingContents materializes the incoming postimages in a temporary
// directory and runs the secret detector over them, mirroring the forward
// transfer's scan. It returns the flagged paths (with the rule) and whether
// the detector was unavailable.
func scanIncomingContents(ctx context.Context, files []WorkspaceApplyFile) (map[string]string, bool, error) {
	flagged := map[string]string{}
	var candidates []WorkspaceApplyFile
	for _, f := range files {
		if f.Verdict == WorkspaceApplyOK && f.Content != nil {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return flagged, false, nil
	}
	tmp, err := os.MkdirTemp("", "just-code-apply-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(tmp)
	for _, f := range candidates {
		hostPath := filepath.Join(tmp, filepath.FromSlash(f.Change.Rel))
		if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(hostPath, f.Content, 0o644); err != nil {
			return nil, false, err
		}
	}
	probe, err := probeGitleaks(ctx, tmp)
	if err != nil {
		return nil, false, err
	}
	if probe.missing {
		return flagged, true, nil
	}
	for _, finding := range probe.findings {
		rel := findingRelPath(tmp, finding.File)
		if rel == "" {
			continue
		}
		if _, seen := flagged[rel]; !seen {
			flagged[rel] = finding.RuleID
		}
	}
	return flagged, false, nil
}

// ---------------------------------------------------------------------------
// Writing the chosen files
// ---------------------------------------------------------------------------

// ApplyGuestFiles writes the chosen changes into the host transfer source.
// Each file's preimage is re-validated immediately before its write, so a
// change made while the user sat at the prompt demotes to a conflict instead
// of being overwritten. It never runs git: nothing is staged, nothing is
// committed, and the user reviews and commits when ready.
//
// Every host access is anchored at the root directory (os.Root): a path
// component swapped for a symlink can never redirect a write or a deletion
// outside the workspace, because the anchored operations refuse any
// resolution that would escape the root. On top of that anchor:
//
//   - each component of the path is checked and a symlink anywhere on it
//     demotes the file to a conflict — content is never written or removed
//     through a link, even one pointing back inside the workspace;
//   - the preimage is read through a held O_NOFOLLOW descriptor, so a link
//     swapped in as the final component between the check and the open is
//     refused by the open itself;
//   - a write never truncates the live host file. The content is written to a
//     fresh temporary file in the same directory, mode-set and synced, and
//     only then swapped in atomically — a rename for a modification, a hard
//     link for an addition. A failure at ANY point (write error, disk full,
//     crash) leaves the host file exactly as it was;
//   - an addition is linked in with link(2), which fails with EEXIST if
//     anything already occupies the path: a file that appeared since the
//     plan is a conflict, never an overwrite, with no check-then-act window;
//   - before the swap, the path is re-checked to still name the exact inode
//     whose bytes were just verified (os.SameFile against the held
//     descriptor), so a modification or deletion cannot act on an entry that
//     was replaced between the verification and the act.
//
// The symlink races, re-evaluated: rename(2) and link(2) never follow the
// final path component, so an entry swapped to a symlink in the act's path is
// replaced (the link's target is untouched) rather than written through —
// bounded to an in-workspace entry, never an escape. The residual race is an
// intermediate directory component swapped for a symlink pointing INSIDE the
// workspace in the instant between the component check and the open: it would
// be followed, landing the write at a different in-workspace path than
// reviewed. It cannot escape the workspace, and any error fails closed
// (conflict or failure, never an unverified write).
//
// The returned conflicts include files demoted at write time, in addition to
// the plan's conflicts the caller chose to include.
func ApplyGuestFiles(root string, files []WorkspaceApplyFile) (applied, failed []string, conflicts []WorkspaceApplyFile) {
	rooted, err := os.OpenRoot(root)
	if err != nil {
		// Without the anchor nothing can be written safely; every
		// applicable file fails rather than falling back to unanchored
		// paths.
		for _, f := range files {
			if f.Verdict == WorkspaceApplyOK {
				failed = append(failed, NormalizeTransferPath(f.Change.Rel))
			}
		}
		return nil, failed, nil
	}
	defer rooted.Close()
	result := &hostApplyResult{}
	for _, f := range files {
		if f.Verdict != WorkspaceApplyOK {
			continue
		}
		rel := NormalizeTransferPath(f.Change.Rel)
		if rel == "" {
			continue
		}
		if f.Change.Status == GuestChangeDeleted {
			result.applyHostDeletion(rooted, rel, f)
			continue
		}
		result.applyHostWrite(rooted, rel, f)
	}
	return result.applied, result.failed, result.conflicts
}

// hostApplyResult accumulates one ApplyGuestFiles run.
type hostApplyResult struct {
	applied   []string
	failed    []string
	conflicts []WorkspaceApplyFile
}

// applyHostDeletion removes a file whose preimage still byte-matches the
// snapshot. The preimage is read through a held no-follow descriptor, and the
// path is re-checked to still name that exact inode right before the removal,
// so a parent swapped for a symlink — even one whose target holds matching
// content — is a conflict, never a deletion outside the workspace.
func (r *hostApplyResult) applyHostDeletion(rooted *os.Root, rel string, f WorkspaceApplyFile) {
	if rootedHasSymlinkComponent(rooted, rel) {
		r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
		return
	}
	file, err := rooted.OpenFile(rel, os.O_RDONLY|hostOpenNoFollow, 0)
	if err != nil {
		// Missing, or not openable as a regular file: none of it is a
		// provably identical preimage, so none of it is removed.
		if errors.Is(err, fs.ErrNotExist) {
			r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostMissing))
			return
		}
		r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
		return
	}
	defer file.Close()
	hash, ok := gitBlobHashFile(file)
	if !ok || f.Change.PreHash == "" || hash != f.Change.PreHash {
		r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
		return
	}
	if !rootedEntryStillNames(rooted, rel, file) {
		r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
		return
	}
	if err := rooted.Remove(rel); err != nil {
		r.failed = append(r.failed, rel)
		return
	}
	r.applied = append(r.applied, rel)
}

// applyHostWrite writes an added or modified file through a same-directory
// temporary: the content is fully written, mode-set and synced to a fresh
// file, and only then swapped in atomically. The preimage of a modification
// is verified through a held no-follow descriptor and re-checked by inode
// identity right before the swap; an addition is linked in, which fails if
// anything already occupies the path. Nothing that appeared or changed since
// the plan is ever overwritten, and a failure at any point leaves the host
// file exactly as it was.
func (r *hostApplyResult) applyHostWrite(rooted *os.Root, rel string, f WorkspaceApplyFile) {
	if rootedHasSymlinkComponent(rooted, rel) {
		r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
		return
	}
	if err := rooted.MkdirAll(path.Dir(rel), 0o755); err != nil {
		r.failed = append(r.failed, rel)
		return
	}
	var held *os.File
	if f.Change.Status != GuestChangeAdded {
		file, err := rooted.OpenFile(rel, os.O_RDONLY|hostOpenNoFollow, 0)
		if err != nil {
			// Unreadable, missing, or not a regular file: none of it is
			// a provably identical preimage, so none of it is written.
			if errors.Is(err, fs.ErrNotExist) {
				r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostMissing))
				return
			}
			r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
			return
		}
		defer file.Close()
		held = file
		hash, ok := gitBlobHashFile(held)
		if !ok || f.Change.PreHash == "" || hash != f.Change.PreHash {
			r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
			return
		}
	}
	// The temporary lives in the target's directory: same filesystem, so
	// the swap is always valid, and a partial write never touches the
	// host file.
	temp, err := writeHostTemp(rooted, path.Dir(rel), f)
	if err != nil {
		r.failed = append(r.failed, rel)
		return
	}
	discardTemp := func() { _ = rooted.Remove(temp) }
	if f.Change.Status == GuestChangeAdded {
		// link(2) is the atomic "create only if absent": it fails with
		// EEXIST when anything occupies the path — a regular file, a
		// symlink, anything — so a file that appeared since the plan is
		// a conflict, never an overwrite.
		if err := rooted.Link(temp, rel); err != nil {
			discardTemp()
			if errors.Is(err, fs.ErrExist) {
				r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostExists))
				return
			}
			r.failed = append(r.failed, rel)
			return
		}
		// The content is in place; the temporary is only a second name
		// for it now. A failure to drop that name leaves a hidden stray,
		// never a wrong file.
		_ = rooted.Remove(temp)
		r.applied = append(r.applied, rel)
		return
	}
	// Modification: the path must still name the inode whose bytes were
	// verified, then the fully written temporary replaces it atomically.
	// The old content is gone only once the new content is entirely on
	// disk.
	if !rootedEntryStillNames(rooted, rel, held) {
		discardTemp()
		r.conflicts = append(r.conflicts, demoteToConflict(f, WorkspaceConflictHostModified))
		return
	}
	if err := rooted.Rename(temp, rel); err != nil {
		discardTemp()
		r.failed = append(r.failed, rel)
		return
	}
	r.applied = append(r.applied, rel)
}

// writeHostTemp writes the file's content to a fresh, exclusive temporary in
// dir, sets its final mode through the descriptor, and syncs it: when this
// returns, the content is fully on disk and the host file has not been
// touched.
func writeHostTemp(rooted *os.Root, dir string, f WorkspaceApplyFile) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		name := path.Join(dir, ".just-code-apply-"+randomTempSuffix())
		file, err := rooted.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := file.Write(f.Content); err != nil {
			file.Close()
			_ = rooted.Remove(name)
			return "", err
		}
		if err := file.Chmod(hostWritePerm(f.Mode)); err != nil {
			file.Close()
			_ = rooted.Remove(name)
			return "", err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			_ = rooted.Remove(name)
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = rooted.Remove(name)
			return "", err
		}
		return name, nil
	}
	return "", fs.ErrExist
}

// randomTempSuffix is a random hexadecimal suffix for the write temporary.
func randomTempSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; a fixed
		// suffix still works (the exclusive create retries).
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// rootedEntryStillNames reports whether the path rel still names the inode
// behind the held descriptor: the last identity check before an atomic
// replacement or removal, so an entry swapped after its bytes were verified
// is a conflict rather than an act on the wrong file.
func rootedEntryStillNames(rooted *os.Root, rel string, held *os.File) bool {
	entry, err := rooted.Lstat(rel)
	if err != nil || !entry.Mode().IsRegular() {
		return false
	}
	cur, err := held.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(entry, cur)
}

// hostWritePerm is the creation mode for a new file.
func hostWritePerm(mode fs.FileMode) fs.FileMode {
	if mode == 0 {
		return 0o644
	}
	return mode.Perm()
}

// rootedHasSymlinkComponent reports whether any component of rel — including
// the final one — is a symlink, resolved through the anchor.
func rootedHasSymlinkComponent(rooted *os.Root, rel string) bool {
	cur := ""
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." {
			continue
		}
		cur = path.Join(cur, part)
		if info, err := rooted.Lstat(cur); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// demoteToConflict returns a copy of f marked as the given conflict, for
// files whose preimage stopped matching between planning and writing.
func demoteToConflict(f WorkspaceApplyFile, kind WorkspaceApplyConflictKind) WorkspaceApplyFile {
	f.Verdict = WorkspaceApplyConflict
	f.Conflict = kind
	f.Content = nil
	return f
}

// ---------------------------------------------------------------------------
// Saving the unapplied patch
// ---------------------------------------------------------------------------

// SaveUnappliedPatch writes the diff sections of the given (conflicted) files
// into host state for manual application, returning the path. Binary files
// need their literal payload, so the guest is asked for a --binary diff of
// exactly those paths; when that read fails the whole save is an error — the
// text sections of a binary conflict are not applicable, and claiming a patch
// was saved while the binary files are missing from it would point the user
// at a file that cannot restore their conflicts.
func (m *MicrosandboxRuntime) SaveUnappliedPatch(ctx context.Context, baseline string, files []WorkspaceApplyFile) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	needBinary := false
	for _, f := range files {
		if f.Change.Binary {
			needBinary = true
		}
	}
	if needBinary {
		if baseline == "" {
			return "", fmt.Errorf("cannot save the unapplied patch: a binary conflict needs its literal payload and the snapshot baseline is unknown; no patch was written — the conflicts are still listed above and 'just-code workspace export' remains available")
		}
		var rels []string
		for _, f := range files {
			rels = append(rels, f.Change.Rel)
		}
		stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(),
			guestCd()+guestGitFragment(guestDiffArgs(baseline, true, rels)))
		if err != nil {
			return "", fmt.Errorf("cannot save the unapplied patch: reading the binary conflicts from the guest failed (%v); no patch was written — the conflicts are still listed above and 'just-code workspace export' remains available", err)
		}
		if code != 0 {
			detail := strings.TrimSpace(stderr)
			if detail == "" {
				detail = fmt.Sprintf("the guest diff exited %d", code)
			}
			return "", fmt.Errorf("cannot save the unapplied patch: reading the binary conflicts from the guest failed (%s); no patch was written — the conflicts are still listed above and 'just-code workspace export' remains available", detail)
		}
		if strings.TrimSpace(stdout) == "" {
			return "", fmt.Errorf("cannot save the unapplied patch: the guest returned an empty diff for the binary conflicts; no patch was written — the conflicts are still listed above and 'just-code workspace export' remains available")
		}
		var patch bytes.Buffer
		patch.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			patch.WriteString("\n")
		}
		return m.writeUnappliedPatch(patch.Bytes())
	}
	var patch bytes.Buffer
	for _, f := range files {
		if f.Change.Diff == "" {
			continue
		}
		patch.WriteString(f.Change.Diff)
	}
	return m.writeUnappliedPatch(patch.Bytes())
}

// writeUnappliedPatch writes the patch under the instance's state directory
// with a timestamped name, and returns its path.
func (m *MicrosandboxRuntime) writeUnappliedPatch(data []byte) (string, error) {
	dir := InstanceStateDir(m.stateDirOrDefault(), m.InstanceName())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(dir, "unapplied-"+time.Now().UTC().Format("20060102T150405")+".patch")
	if err := os.WriteFile(outPath, data, 0o600); err != nil {
		return "", err
	}
	return outPath, nil
}
