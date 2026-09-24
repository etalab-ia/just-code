package justcode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInitForTransfer makes dir a Git worktree with one commit, so the
// transfer source set comes from git's own view (tracked + untracked +
// ignored) rather than a plain walk.
func gitInitForTransfer(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTransferExcludesDotenvByDefault is the core canary property: a .env in
// the host checkout never enters the transfer set, so it can never reach the
// guest.
func TestTransferExcludesDotenvByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, ".env", "ALBERT_API_KEY=canary-value\n")
	writeFile(t, dir, ".env.local", "SECRET=canary2\n")
	// The safe templates are documentation, not secrets.
	writeFile(t, dir, ".env.example", "ALBERT_API_KEY=\n")
	gitInitForTransfer(t, dir)

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	included := map[string]bool{}
	for _, e := range man.Included() {
		included[e.Rel] = true
	}
	if !included["main.go"] {
		t.Fatalf("a source file must cross: %v", man.Included())
	}
	if included[".env"] || included[".env.local"] {
		t.Fatalf("dotenv files must not cross: %v", man.Included())
	}
	if !included[".env.example"] {
		t.Fatalf(".env.example carries no value and must cross: %v", man.Included())
	}
	// The exclusion is reported with its reason, not silently dropped.
	var reason string
	for _, e := range man.Excluded() {
		if e.Rel == ".env" {
			reason = e.Reason
		}
	}
	if !strings.Contains(reason, "dotenv") {
		t.Fatalf(".env exclusion reason = %q", reason)
	}
}

// TestTransferCanaryAfterSync pins the refresh case the plan calls out: a
// secret added to the checkout AFTER the first sync must not cross on the
// next resolution either.
func TestTransferCanaryAfterSync(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.py", "print('hi')\n")
	gitInitForTransfer(t, dir)

	before, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Excluded()) != 0 {
		t.Fatalf("precondition: nothing excluded yet, got %v", before.Excluded())
	}

	// Planted after the initial sync, untracked.
	writeFile(t, dir, ".env", "CANARY=leaked-if-transferred\n")

	after, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after.Included() {
		if e.Rel == ".env" {
			t.Fatal("a secret planted after the first sync crossed on the next resolution")
		}
	}
}

// TestTransferExcludesIgnoredAndSymlinks pins the two non-obvious classes:
// git-ignored files (where local .env files live) and symlinks (which can
// resolve outside the resolved set). Both are reported, not skipped.
func TestTransferExcludesIgnoredAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "node_modules/\n*.log\n")
	writeFile(t, dir, "keep.txt", "keep\n")
	writeFile(t, dir, "debug.log", "log\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "module\n")
	gitInitForTransfer(t, dir)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]TransferEntry{}
	for _, e := range man.Entries {
		state[e.Rel] = e
	}
	if !state["keep.txt"].Included {
		t.Fatal("a tracked file must cross")
	}
	if state["debug.log"].Included {
		t.Fatal("an ignored file must not cross")
	}
	if !strings.Contains(state["debug.log"].Reason, "git-ignored") {
		t.Fatalf("ignored reason = %q", state["debug.log"].Reason)
	}
	if state["link"].Included {
		t.Fatal("a symlink must not cross")
	}
}

// TestTransferOptInIsPerFileAndRecorded pins the "no blanket include" rule:
// an explicit decision re-includes exactly one path, and the decision is
// persisted so it survives the next resolution.
func TestTransferOptInIsPerFileAndRecorded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.go", "package main\n")
	writeFile(t, dir, ".env", "X=1\n")
	writeFile(t, dir, ".env.production", "Y=2\n")
	gitInitForTransfer(t, dir)

	store := TransferOptInStore{Path: filepath.Join(t.TempDir(), "transfer-optin.json"), FS: DefaultFS}
	if err := store.Allow(".env", "dotenv file (default-deny)"); err != nil {
		t.Fatal(err)
	}
	records, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := records[".env"]; !ok {
		t.Fatalf("the decision was not recorded: %v", records)
	}

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{OptIn: map[string]bool{".env": true}})
	if err != nil {
		t.Fatal(err)
	}
	included := map[string]TransferEntry{}
	for _, e := range man.Included() {
		included[e.Rel] = e
	}
	if _, ok := included[".env"]; !ok {
		t.Fatal("the re-included file must cross")
	}
	if !included[".env"].OptIn {
		t.Fatal("the entry must be flagged as an explicit decision")
	}
	// The other dotenv file is untouched: decisions are per file.
	if _, ok := included[".env.production"]; ok {
		t.Fatal("a per-file decision must not re-include other excluded files")
	}
}

// TestTransferArchiveContainsOnlyIncludedFiles pins the structural guarantee:
// the payload is built from the resolved set, so a refused path cannot appear
// in it even if it exists on disk.
func TestTransferArchiveContainsOnlyIncludedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/main.go", "package main\n")
	writeFile(t, dir, "src/nested/deep.txt", "deep\n")
	writeFile(t, dir, ".env", "CANARY=do-not-transfer\n")
	gitInitForTransfer(t, dir)

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildTransferArchive(man)
	if err != nil {
		t.Fatal(err)
	}
	names := archiveNames(t, payload)
	for _, want := range []string{"src/main.go", "src/nested/deep.txt"} {
		if !names[want] {
			t.Fatalf("archive is missing %s: %v", want, names)
		}
	}
	if names[".env"] {
		t.Fatal("the archive must not carry a file the filter refused")
	}
	// The canary value must not appear in the payload bytes at all.
	if bytes.Contains(payload, []byte("CANARY=do-not-transfer")) {
		t.Fatal("the canary value is present in the transfer payload")
	}
}

// TestTransferArchiveIsDeterministic pins reproducibility: the same input
// produces the same bytes, so a refresh is comparable and tests are stable.
func TestTransferArchiveIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b/c.txt", "c\n")
	gitInitForTransfer(t, dir)
	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildTransferArchive(man)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildTransferArchive(man)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two builds of the same manifest differ")
	}
}

// TestTransferNonGitRootUsesSnapshot pins the explicit non-Git decision: the
// root is snapshotted through the same filter rather than rejected, so a
// non-Git project still reaches a usable guest workspace.
func TestTransferNonGitRootUsesSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "notes.md", "hello\n")
	writeFile(t, dir, ".env", "SECRET=canary\n")

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if man.GitManaged {
		t.Fatal("a directory without a repository must not be reported as Git-managed")
	}
	included := map[string]bool{}
	for _, e := range man.Included() {
		included[e.Rel] = true
	}
	if !included["notes.md"] {
		t.Fatalf("the snapshot must carry the project files: %v", man.Included())
	}
	if included[".env"] {
		t.Fatal("the filter applies to a non-Git root too")
	}
}

// TestTransferRejectsPathsOutsideTheProject pins the guard on recorded
// re-inclusions: a decision can only name a file inside the project.
func TestTransferRejectsPathsOutsideTheProject(t *testing.T) {
	for _, in := range []string{"", ".", "..", "../secret", "/etc/passwd", "a/../../b"} {
		if got := NormalizeTransferPath(in); got != "" {
			t.Fatalf("NormalizeTransferPath(%q) = %q, want empty", in, got)
		}
	}
	if got := NormalizeTransferPath("./src/main.go"); got != "src/main.go" {
		t.Fatalf("NormalizeTransferPath = %q", got)
	}
}

func archiveNames(t *testing.T, payload []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			names[hdr.Name] = true
		}
	}
	return names
}

// fakeGitleaks installs a stub `gitleaks` on PATH that emits report, so the
// finding→path mapping is exercised without depending on the real tool's
// presence (and without depending on its current path convention).
func fakeGitleaks(t *testing.T, report string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'REPORT'\n" + report + "\nREPORT\n"
	if err := os.WriteFile(filepath.Join(dir, "gitleaks"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestTransferFindingPathMappingHandlesBothConventions pins the mapping for a
// gitleaks report whose File is relative (current versions) and absolute
// (observed in other builds). Either way the flagged file must be excluded:
// dropping the finding would let it cross.
func TestTransferFindingPathMappingHandlesBothConventions(t *testing.T) {
	for _, tc := range []struct {
		name string
		file func(root string) string
	}{
		{"relative", func(string) string { return "src/config.txt" }},
		{"absolute", func(root string) string { return filepath.Join(root, "src", "config.txt") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "src/config.txt", "password: \"hunter2correcthorsebatterystaple12345\"\n")
			writeFile(t, dir, "src/other.go", "package main\n")
			gitInitForTransfer(t, dir)
			fakeGitleaks(t, fmt.Sprintf(`[{"RuleID":"generic-api-key","File":%q,"StartLine":1,"Match":"REDACTED"}]`, tc.file(dir)))

			man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(man.UnmappedFindings) != 0 {
				t.Fatalf("a mappable finding must not be reported as unmapped: %v", man.UnmappedFindings)
			}
			for _, e := range man.Included() {
				if e.Rel == "src/config.txt" {
					t.Fatalf("a flagged file must not cross: %v", man.Included())
				}
			}
			var reason string
			for _, e := range man.Excluded() {
				if e.Rel == "src/config.txt" {
					reason = e.Reason
				}
			}
			if !strings.Contains(reason, "gitleaks") || !strings.Contains(reason, "generic-api-key") {
				t.Fatalf("exclusion reason = %q", reason)
			}
			// An unflagged sibling still crosses: the filter is precise.
			var crossed bool
			for _, e := range man.Included() {
				if e.Rel == "src/other.go" {
					crossed = true
				}
			}
			if !crossed {
				t.Fatalf("an unflagged file must still cross: %v", man.Included())
			}
		})
	}
}

// TestTransferRefusesUnattributableFinding pins the fail-closed rule: a
// finding whose path cannot be matched to a candidate blocks the transfer
// instead of being dropped.
func TestTransferRefusesUnattributableFinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	gitInitForTransfer(t, dir)
	// A path outside the source root cannot be attributed to any candidate.
	fakeGitleaks(t, `[{"RuleID":"generic-api-key","File":"/elsewhere/other/config.txt","StartLine":1,"Match":"REDACTED"}]`)

	man, err := ResolveTransferSet(context.Background(), dir, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(man.UnmappedFindings) == 0 {
		t.Fatal("an unattributable finding must be recorded")
	}
	client := &fakeMSBClient{execCaptureResults: []fakeMSBExecCaptureResult{{stdout: "no"}}}
	m := newTestMicrosandbox(t, client)
	m.cfg.WorkspaceDir = dir
	if err := m.ProvisionGuestWorkspace(context.Background(), SyncOptions{Print: func(string) {}}); err == nil {
		t.Fatal("provisioning must refuse while a finding cannot be attributed")
	}
	if len(client.written) != 0 {
		t.Fatal("nothing may be written when the filter cannot decide")
	}
}
