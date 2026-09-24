package justcode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The host->guest transfer boundary (P22). The sealed workspace has no bind
// mount, so project content reaches the guest only through this file: it
// resolves a concrete, reviewable file set from the host checkout, applies a
// default-deny filter, and only then builds the archive that is written into
// the guest. There is deliberately no path that copies a directory tree
// wholesale — a wholesale copy could not enforce the filter, and a filter
// that can be bypassed is the original leak with extra steps.
//
// The excluded classes, all by default:
//   - VCS metadata (.git): the guest repo is created in the guest.
//   - symlinks: a link can resolve outside the resolved set.
//   - dotenv-named files beyond the safe .example/.sample suffixes.
//   - files gitleaks flags.
//   - git-ignored files: that is where local .env files and derived state
//     live. They are still *evaluated* so a secret in one is reported by
//     path rather than silently skipped.
//   - files above the per-file cap.
//
// A user may re-include a specific excluded file, per file, through a
// recorded decision (TransferOptInStore). There is no "include everything".

// DefaultTransferMaxFileBytes bounds one transferred file. The transfer
// writes through the sandbox filesystem API, so a single enormous file would
// hold the whole payload in memory.
const DefaultTransferMaxFileBytes int64 = 64 << 20

// TransferOptions tunes one resolution.
type TransferOptions struct {
	// OptIn lists relative paths the user explicitly re-included after the
	// filter excluded them. Each is a recorded per-file decision.
	OptIn map[string]bool
	// MaxFileBytes bounds a single transferred file; zero uses
	// DefaultTransferMaxFileBytes.
	MaxFileBytes int64
}

// TransferEntry is one candidate path in the manifest.
type TransferEntry struct {
	// Rel is the slash-separated path relative to the root.
	Rel string
	// Size is the file size in bytes, from Lstat.
	Size int64
	// Included reports whether the file crosses into the guest.
	Included bool
	// OptIn reports that inclusion came from an explicit per-file decision
	// rather than the default policy.
	OptIn bool
	// Reason explains the decision, for the review screen.
	Reason string
	// Mode is the file's permission bits, applied on extraction.
	Mode fs.FileMode
}

// TransferManifest is the reviewable outcome of one resolution.
type TransferManifest struct {
	// Root is the absolute, symlink-resolved host root.
	Root string
	// Entries covers every candidate, included or not, sorted by path.
	Entries []TransferEntry
	// GitManaged reports whether candidates came from git's own view of the
	// working tree (tracked + untracked + ignored, minus .git internals).
	GitManaged bool
	// GitleaksMissing records that gitleaks is not installed, so the
	// detection half of the filter did not run. The dotenv half still did.
	GitleaksMissing bool
	// Warnings are non-fatal resolution notes (an unreadable path, a
	// skipped directory).
	Warnings []string
	// UnmappedFindings lists gitleaks findings whose reported path could not
	// be attributed to a candidate. They cannot be matched to a file, so the
	// transfer must refuse rather than proceed without knowing what was
	// flagged (fail closed: see UnmappedFindingsError).
	UnmappedFindings []string
}

// Included returns the entries that cross into the guest.
func (m TransferManifest) Included() []TransferEntry {
	var out []TransferEntry
	for _, e := range m.Entries {
		if e.Included {
			out = append(out, e)
		}
	}
	return out
}

// CandidateCount is how many paths the resolution considered, included or
// not. It distinguishes "the source directory is empty" from "every candidate
// was filtered out", which callers must report differently: the first is a
// legitimately empty project, the second is a filter decision the user has to
// see.
func (m TransferManifest) CandidateCount() int { return len(m.Entries) }

// Excluded returns the entries the filter refused.
func (m TransferManifest) Excluded() []TransferEntry {
	var out []TransferEntry
	for _, e := range m.Entries {
		if !e.Included {
			out = append(out, e)
		}
	}
	return out
}

// TotalBytes is the transfer size of the included set.
func (m TransferManifest) TotalBytes() int64 {
	var total int64
	for _, e := range m.Included() {
		total += e.Size
	}
	return total
}

// Summary renders the manifest for the review screen: what crosses, and what
// the filter refused with the reason.
func (m TransferManifest) Summary() string {
	var b strings.Builder
	included := m.Included()
	excluded := m.Excluded()
	fmt.Fprintf(&b, "Transfer into the sealed guest: %d file(s), %s\n", len(included), FormatByteSize(m.TotalBytes()))
	if m.GitManaged {
		b.WriteString("Source set: the Git working tree (tracked, untracked, and ignored files; .git metadata is not transferred).\n")
	} else {
		b.WriteString("Source set: the directory tree (no Git repository at this root).\n")
	}
	if m.GitleaksMissing {
		b.WriteString("Warning: gitleaks is not installed, so secret detection did not run; the dotenv name filter still applied.\n")
	}
	if len(m.UnmappedFindings) > 0 {
		b.WriteString("Warning: these gitleaks findings could not be attributed to a file; the transfer is refused while they are unresolved:\n")
		for _, f := range m.UnmappedFindings {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	for _, w := range m.Warnings {
		fmt.Fprintf(&b, "Note: %s\n", w)
	}
	if len(excluded) > 0 {
		b.WriteString("\nExcluded by the default-deny filter:\n")
		for _, e := range excluded {
			fmt.Fprintf(&b, "  %s (%s)\n", e.Rel, e.Reason)
		}
	}
	return b.String()
}

// ResolveTransferSet resolves the host file set for one transfer. It reads
// the host checkout only; nothing crosses into the guest here.
func ResolveTransferSet(ctx context.Context, root string, opts TransferOptions) (TransferManifest, error) {
	man := TransferManifest{Root: root}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = DefaultTransferMaxFileBytes
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return man, err
	}
	// A symlinked root is common; resolve it so candidate paths are stable.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if _, err := os.Stat(abs); err != nil {
		return man, err
	}
	man.Root = abs

	// Secret detection runs over the whole working tree; findings are then
	// mapped onto candidates so a flagged file is refused by path.
	findingsByPath := map[string][]gitleaksFinding{}
	probe, err := probeGitleaks(ctx, abs)
	if err != nil {
		return man, err
	}
	man.GitleaksMissing = probe.missing
	for _, f := range probe.findings {
		rel := findingRelPath(abs, f.File)
		if rel == "" {
			man.UnmappedFindings = append(man.UnmappedFindings, fmt.Sprintf("%s:%d (%s)", f.File, f.Line, f.RuleID))
			continue
		}
		findingsByPath[rel] = append(findingsByPath[rel], f)
	}

	type candidate struct {
		rel     string
		ignored bool
	}
	var candidates []candidate
	if gitAvailable() && gitWorktreeRootExists(abs) {
		man.GitManaged = true
		tracked, err := gitListFiles(ctx, abs, "--cached", "--others", "--exclude-standard")
		if err != nil {
			return man, err
		}
		ignored, err := gitListFiles(ctx, abs, "--others", "--ignored", "--exclude-standard")
		if err != nil {
			return man, err
		}
		for _, rel := range tracked {
			candidates = append(candidates, candidate{rel: rel})
		}
		for _, rel := range ignored {
			candidates = append(candidates, candidate{rel: rel, ignored: true})
		}
	} else {
		// No Git view to enumerate: walk the tree, skipping .git internals.
		err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable path is a resolution note, not a failure:
				// the file simply does not cross.
				man.Warnings = append(man.Warnings, fmt.Sprintf("skipped unreadable path %s: %v", relTo(abs, path), err))
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if path == abs {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			candidates = append(candidates, candidate{rel: relTo(abs, path)})
			return nil
		})
		if err != nil {
			return man, err
		}
	}

	seen := map[string]bool{}
	for _, c := range candidates {
		if c.rel == "" || seen[c.rel] {
			continue
		}
		seen[c.rel] = true
		entry, err := classifyTransferCandidate(abs, c.rel, c.ignored, findingsByPath[c.rel], opts)
		if err != nil {
			man.Warnings = append(man.Warnings, fmt.Sprintf("skipped %s: %v", c.rel, err))
			continue
		}
		man.Entries = append(man.Entries, entry)
	}
	sort.Slice(man.Entries, func(i, j int) bool { return man.Entries[i].Rel < man.Entries[j].Rel })
	return man, nil
}

// classifyTransferCandidate applies the default-deny policy to one path. The
// order of the checks decides the reported reason: a dotenv file that is also
// git-ignored is reported as a dotenv exclusion, because that is the
// security-relevant fact.
func classifyTransferCandidate(root, rel string, ignored bool, findings []gitleaksFinding, opts TransferOptions) (TransferEntry, error) {
	entry := TransferEntry{Rel: rel}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		return entry, err
	}
	if info.IsDir() {
		// Directories are recreated by the archive's own parent entries.
		return entry, fmt.Errorf("directory entry")
	}
	entry.Size = info.Size()
	entry.Mode = info.Mode().Perm()

	optIn := opts.OptIn[rel]

	exclude := func(reason string) (TransferEntry, error) {
		if optIn {
			entry.Included = true
			entry.OptIn = true
			entry.Reason = "re-included by an explicit per-file decision; filter rule was: " + reason
			return entry, nil
		}
		entry.Reason = reason
		return entry, nil
	}

	name := filepath.Base(rel)
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return exclude("symlink; not transferred into the sealed guest")
	case name == ".git" || strings.HasPrefix(rel, ".git/"):
		return exclude("Git metadata; the guest repository is created inside the guest")
	case isDotenvName(name):
		return exclude("dotenv file (default-deny)")
	case len(findings) > 0:
		return exclude(fmt.Sprintf("gitleaks: %s", findings[0].RuleID))
	case ignored:
		return exclude("git-ignored (local or derived state); re-include explicitly if the guest needs it")
	case info.Size() > opts.MaxFileBytes:
		return exclude(fmt.Sprintf("above the per-file transfer cap (%s)", FormatByteSize(opts.MaxFileBytes)))
	}
	entry.Included = true
	entry.Reason = "included"
	return entry, nil
}

// gitListFiles runs `git ls-files -z` with the given selector flags and
// returns slash-separated paths relative to the root.
func gitListFiles(ctx context.Context, root string, flags ...string) ([]string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	args := append([]string{"-C", root, "ls-files", "-z"}, flags...)
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(p))
	}
	return paths, nil
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitleaksProbe is one secret-detection run: either findings, or the fact
// that gitleaks is not installed (in which case the dotenv name filter still
// applies and the caller warns).
type gitleaksProbe struct {
	findings []gitleaksFinding
	missing  bool
}

// probeGitleaks is the detection seam. It is a variable so tests can supply
// findings directly: driving it through a stub executable on PATH is not
// portable (a shell script is not executable on Windows, and a resolved path
// differs from an unresolved one on macOS), and the mapping logic under test
// is the code that consumes the findings, not the tool invocation.
var probeGitleaks = func(ctx context.Context, dir string) (gitleaksProbe, error) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		return gitleaksProbe{missing: true}, nil
	}
	findings, err := runGitleaksFindings(ctx, dir)
	if err != nil {
		return gitleaksProbe{}, err
	}
	return gitleaksProbe{findings: findings}, nil
}

// findingRelPath maps a gitleaks report path onto the manifest's relative
// form. Current gitleaks versions report the path relative to --source, but
// an absolute path has been observed in other builds; both are normalized
// here so the mapping cannot silently miss. A path that cannot be attributed
// returns "" and the caller fails closed: dropping a finding would let the
// flagged file cross.
//
// An absolute path is relativized against both the root as given and its
// symlink-resolved form, because the two sides can sit on different views of
// the same tree (macOS /var -> /private/var): comparing raw strings would
// refuse a transfer gitleaks attributed correctly.
func findingRelPath(root, reported string) string {
	if strings.TrimSpace(reported) == "" {
		return ""
	}
	p := filepath.FromSlash(reported)
	if !filepath.IsAbs(p) {
		return NormalizeTransferPath(filepath.ToSlash(p))
	}
	for _, base := range distinctPaths(root) {
		if rel := relativize(base, p); rel != "" {
			return rel
		}
	}
	// The report may name a resolved path while the root is not (or the
	// reverse): canonicalize the report and retry.
	if canon, err := filepath.EvalSymlinks(p); err == nil {
		for _, base := range distinctPaths(root) {
			if rel := relativize(base, canon); rel != "" {
				return rel
			}
		}
	}
	return ""
}

// distinctPaths returns the path as given plus its symlink-resolved form,
// skipping duplicates.
func distinctPaths(p string) []string {
	out := []string{p}
	if canon, err := filepath.EvalSymlinks(p); err == nil && canon != p {
		out = append(out, canon)
	}
	return out
}

// relativize returns the project-relative form of abs under base, or "" when
// abs is not inside base.
func relativize(base, abs string) string {
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return ""
	}
	return NormalizeTransferPath(filepath.ToSlash(rel))
}

// FormatByteSize renders a byte count for the review screen. The justcode
// package cannot use the CLI's helper in cmd/just-code, so the sealed-transfer
// messages format here.
func FormatByteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// NormalizeTransferPath canonicalizes a user-supplied path into the
// manifest's form: relative to the project root, slash-separated, cleaned. A
// path that is empty, absolute, or escapes the root returns "", so a
// re-inclusion decision can never be recorded against a file outside the
// project.
func NormalizeTransferPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "" || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") ||
		strings.Contains(cleaned, ":") {
		return ""
	}
	return cleaned
}

// BuildTransferArchive builds the deterministic tar.gz payload for a manifest.
// Only included entries are added: the archive is built from the resolved
// set, so a path the filter refused cannot appear in the payload.
func BuildTransferArchive(man TransferManifest) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// A fixed timestamp keeps the archive deterministic for a given input,
	// which keeps refresh behavior reproducible and tests meaningful.
	epoch := time.Unix(0, 0).UTC()
	dirsWritten := map[string]bool{}

	for _, e := range man.Included() {
		// Parent directories, so extraction needs no mkdir pass.
		parts := strings.Split(e.Rel, "/")
		for i := 1; i < len(parts); i++ {
			dir := strings.Join(parts[:i], "/")
			if dirsWritten[dir] {
				continue
			}
			dirsWritten[dir] = true
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     dir + "/",
				Mode:     0o755,
				ModTime:  epoch,
			}); err != nil {
				return nil, err
			}
		}
		data, err := os.ReadFile(filepath.Join(man.Root, filepath.FromSlash(e.Rel)))
		if err != nil {
			return nil, err
		}
		mode := int64(e.Mode.Perm())
		if mode == 0 {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     e.Rel,
			Mode:     mode,
			Size:     int64(len(data)),
			ModTime:  epoch,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TransferOptInRecord is one recorded re-inclusion decision.
type TransferOptInRecord struct {
	// Rel is the path relative to the project root.
	Rel string `json:"rel"`
	// Reason is the filter rule the user overrode.
	Reason string `json:"reason,omitempty"`
	// RecordedAt is when the decision was made (RFC3339, UTC).
	RecordedAt string `json:"recordedAt"`
}

// TransferOptInStore persists the per-file re-inclusion decisions so a
// decision survives across syncs instead of being re-asked every time. The
// file is host state, never inside the project.
type TransferOptInStore struct {
	Path string
	FS   FS
}

type transferOptInFile struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Paths         []TransferOptInRecord `json:"paths"`
}

const transferOptInSchemaVersion = 1

// Load returns the recorded decisions keyed by relative path.
func (s TransferOptInStore) Load() (map[string]TransferOptInRecord, error) {
	out := map[string]TransferOptInRecord{}
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
	var raw transferOptInFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("transfer opt-in file %s: %w", s.Path, err)
	}
	for _, r := range raw.Paths {
		if r.Rel != "" {
			out[r.Rel] = r
		}
	}
	return out, nil
}

// Allow records a per-file re-inclusion.
func (s TransferOptInStore) Allow(rel, reason string) error {
	return s.mutate(func(records map[string]TransferOptInRecord) {
		records[rel] = TransferOptInRecord{Rel: rel, Reason: reason, RecordedAt: time.Now().UTC().Format(time.RFC3339)}
	})
}

// Revoke drops a recorded re-inclusion, returning the file to the default
// policy.
func (s TransferOptInStore) Revoke(rel string) error {
	return s.mutate(func(records map[string]TransferOptInRecord) {
		delete(records, rel)
	})
}

func (s TransferOptInStore) mutate(change func(map[string]TransferOptInRecord)) error {
	fsys := s.FS
	if fsys == nil {
		fsys = DefaultFS
	}
	records, err := s.Load()
	if err != nil {
		return err
	}
	change(records)
	file := transferOptInFile{SchemaVersion: transferOptInSchemaVersion}
	for _, r := range records {
		file.Paths = append(file.Paths, r)
	}
	sort.Slice(file.Paths, func(i, j int) bool { return file.Paths[i].Rel < file.Paths[j].Rel })
	if err := fsys.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(fsys, s.Path, append(data, '\n'), 0o600)
}
