package justcode

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for `just-code workspace apply` (issue #173). The guest side is driven
// through the fake client; the diff, tarball and stat payloads come from the
// real git/tar/stat binaries wherever the parser or the classification
// consumes them, so the pipeline is pinned against authentic output.

// requireTools skips the test when a tool the fixture drives is missing.
func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

// guestRepoFixture is a real git repository standing in for the guest
// workspace: a committed snapshot (the transfer), then agent-shaped worktree
// changes. Its helpers run the exact commands the runtime runs in the guest.
type guestRepoFixture struct {
	t        *testing.T
	dir      string
	baseline string
}

func newGuestRepoFixture(t *testing.T, base map[string]string) *guestRepoFixture {
	t.Helper()
	requireTools(t, "git")
	dir := t.TempDir()
	for rel, content := range base {
		writeFile(t, dir, rel, content)
	}
	gitInitForTransfer(t, dir)
	g := &guestRepoFixture{t: t, dir: dir}
	g.baseline = g.git("rev-parse", "HEAD")
	return g
}

// git runs a git command in the fixture and returns its stdout.
func (g *guestRepoFixture) git(args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		g.t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// work writes a worktree file, the way the agent would.
func (g *guestRepoFixture) work(rel, content string) {
	g.t.Helper()
	writeFile(g.t, g.dir, rel, content)
}

// remove deletes a worktree file, the way the agent would.
func (g *guestRepoFixture) remove(rel string) {
	g.t.Helper()
	if err := os.Remove(filepath.Join(g.dir, filepath.FromSlash(rel))); err != nil {
		g.t.Fatal(err)
	}
}

// chmod changes a worktree file's mode.
func (g *guestRepoFixture) chmod(rel string, mode fs.FileMode) {
	g.t.Helper()
	if err := os.Chmod(filepath.Join(g.dir, filepath.FromSlash(rel)), mode); err != nil {
		g.t.Fatal(err)
	}
}

// diff produces the guest's diff with the production command.
func (g *guestRepoFixture) diff() string {
	g.t.Helper()
	cmd := exec.Command("git", "-c", "core.quotePath=false", "add", "-A", "-N")
	cmd.Dir = g.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		g.t.Fatalf("git add -A -N: %v: %s", err, out)
	}
	return g.git(guestDiffArgs(g.baseline, false, nil)...)
}

// sizes runs the production stat command for rels, in order.
func (g *guestRepoFixture) sizes(rels []string) string {
	g.t.Helper()
	requireTools(g.t, "stat")
	cmd := exec.Command("stat", guestStatArgs(rels)...)
	cmd.Dir = g.dir
	out, err := cmd.Output()
	if err != nil {
		g.t.Fatalf("stat: %v", err)
	}
	return string(out)
}

// tarball runs the production tar command for rels, in order.
func (g *guestRepoFixture) tarball(rels []string) string {
	g.t.Helper()
	requireTools(g.t, "tar")
	cmd := exec.Command("tar", guestTarArgs(rels)...)
	cmd.Dir = g.dir
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		g.t.Fatalf("tar: %v", err)
	}
	return out.String()
}

// lstree runs the production ls-tree command for rels at the baseline, the
// way mode-only hash resolution does.
func (g *guestRepoFixture) lstree(rels []string) string {
	g.t.Helper()
	cmd := exec.Command("git", guestLsTreeArgs(g.baseline, rels)...)
	cmd.Dir = g.dir
	out, err := cmd.Output()
	if err != nil {
		g.t.Fatalf("git ls-tree: %v", err)
	}
	return string(out)
}

// record writes the last-transfer record the way a successful sync does.
func recordLastTransferForTest(t *testing.T, m *MicrosandboxRuntime, rec LastTransferRecord) {
	t.Helper()
	store := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
}

// planFile returns the plan's entry for rel.
func planFile(t *testing.T, plan *WorkspaceApplyPlan, rel string) WorkspaceApplyFile {
	t.Helper()
	for _, f := range plan.Files {
		if f.Change.Rel == rel {
			return f
		}
	}
	t.Fatalf("no plan entry for %s", rel)
	return WorkspaceApplyFile{}
}

// queuePlanCapture queues the fake's ExecCapture answers for a PlanGuestApply
// run over the fixture: provisioned, baseline verification, diff, then the
// stat/tar reads for the applicable non-deleted files (in plan order, which
// the diff keeps path-sorted).
func queuePlanCapture(t *testing.T, client *fakeMSBClient, g *guestRepoFixture, diff string, fetch []string, extra ...fakeMSBExecCaptureResult) {
	t.Helper()
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"}, // provisioned
		{stdout: "yes"}, // recorded snapshot commit exists
		{stdout: diff},  // the diff
	}
	if len(fetch) > 0 {
		client.execCaptureResults = append(client.execCaptureResults,
			fakeMSBExecCaptureResult{stdout: g.sizes(fetch)},
			fakeMSBExecCaptureResult{stdout: g.tarball(fetch)},
		)
	}
	client.execCaptureResults = append(client.execCaptureResults, extra...)
}

// TestParseGitDiffSections pins the parser on handcrafted sections covering
// every header shape the guest can emit.
func TestParseGitDiffSections(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/src/app.js b/src/app.js",
		"index 8f2a1b0c8f2a1b0c8f2a1b0c8f2a1b0c8f2a1b0c..9c3d4e5f9c3d4e5f9c3d4e5f9c3d4e5f9c3d4e5f 100644",
		"--- a/src/app.js",
		"+++ b/src/app.js",
		"@@ -1,2 +1,3 @@",
		" line1",
		"-line2",
		"+line2",
		"+line3",
		"diff --git a/src/new.js b/src/new.js",
		"new file mode 100644",
		"index 0000000000000000000000000000000000000000..abcabcabcabcabcabcabcabcabcabcabcabcabcab",
		"--- /dev/null",
		"+++ b/src/new.js",
		"@@ -0,0 +1,2 @@",
		"+hello",
		"+world",
		"diff --git a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"index deadbeefdeadbeefdeadbeefdeadbeefdeadbeef..0000000000000000000000000000000000000000",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-bye",
		"\\ No newline at end of file",
		"diff --git a/mode.sh b/mode.sh",
		"old mode 100644",
		"new mode 100755",
		"diff --git a/img.png b/img.png",
		"index 1111111111111111111111111111111111111111..2222222222222222222222222222222222222222 100644",
		"Binary files a/img.png and b/img.png differ",
		"",
	}, "\n")
	changes, err := parseGitDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 5 {
		t.Fatalf("expected 5 sections, got %d", len(changes))
	}
	want := []struct {
		rel    string
		status GuestChangeStatus
		add    int
		del    int
		binary bool
	}{
		{"src/app.js", GuestChangeModified, 2, 1, false},
		{"src/new.js", GuestChangeAdded, 2, 0, false},
		{"gone.txt", GuestChangeDeleted, 0, 1, false},
		{"mode.sh", GuestChangeModified, 0, 0, false},
		{"img.png", GuestChangeModified, 0, 0, true},
	}
	for i, w := range want {
		c := changes[i]
		if c.Rel != w.rel || c.Status != w.status || c.AddCount != w.add || c.DelCount != w.del || c.Binary != w.binary {
			t.Errorf("section %d: got %+v, want rel=%s status=%s add=%d del=%d binary=%v", i, c, w.rel, w.status, w.add, w.del, w.binary)
		}
	}
	if changes[0].PreHash != "8f2a1b0c8f2a1b0c8f2a1b0c8f2a1b0c8f2a1b0c" || changes[0].PostHash != "9c3d4e5f9c3d4e5f9c3d4e5f9c3d4e5f9c3d4e5f" {
		t.Errorf("full-index hashes not parsed: %+v", changes[0])
	}
	if changes[1].Status != GuestChangeAdded || changes[1].PreHash != "0000000000000000000000000000000000000000" {
		t.Errorf("added file section wrong: %+v", changes[1])
	}
	if changes[3].OldMode != "100644" || changes[3].NewMode != "100755" {
		t.Errorf("mode-only section wrong: %+v", changes[3])
	}
	// Every section keeps an appliable copy of its text.
	for _, c := range changes {
		if !strings.HasPrefix(c.Diff, "diff --git ") || !strings.HasSuffix(c.Diff, "\n") {
			t.Errorf("section %s does not keep its verbatim diff: %q", c.Rel, c.Diff)
		}
	}
}

// TestParseGitDiffQuotedPaths pins the C-quoted section header: paths with
// quotes or newlines are quoted even with core.quotePath=false.
func TestParseGitDiffQuotedPaths(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git \"a/weird\\\"name.txt\" \"b/weird\\\"name.txt\"",
		"new file mode 100644",
		"index 0000000000000000000000000000000000000000..1111111111111111111111111111111111111111",
		"--- /dev/null",
		"+++ \"b/weird\\\"name.txt\"",
		"@@ -0,0 +1 @@",
		"+x",
		"diff --git \"a/nl\\nname.txt\" \"b/nl\\nname.txt\"",
		"new file mode 100644",
		"index 0000000000000000000000000000000000000000..2222222222222222222222222222222222222222",
		"--- /dev/null",
		"+++ \"b/nl\\nname.txt\"",
		"@@ -0,0 +1 @@",
		"+y",
		"",
	}, "\n")
	changes, err := parseGitDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(changes))
	}
	if changes[0].Rel != `weird"name.txt` {
		t.Errorf("quoted path with escaped quote: got %q", changes[0].Rel)
	}
	if changes[1].Rel != "nl\nname.txt" {
		t.Errorf("quoted path with newline: got %q", changes[1].Rel)
	}
}

// TestParseGitDiffRejectsGarbage pins the fail-closed side: anything the
// parser cannot attribute to a file is an error, never a guess.
func TestParseGitDiffRejectsGarbage(t *testing.T) {
	cases := map[string]string{
		"not a diff":            "hello\n",
		"truncated hunk":        "diff --git a/x b/x\nindex 1111111111111111111111111111111111111111..2222222222222222222222222222222222222222 100644\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n-a\n",
		"split paths":           "diff --git a/x b/y\n",
		"trailing junk in hunk": "diff --git a/x b/x\nindex 1111111111111111111111111111111111111111..2222222222222222222222222222222222222222 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\nunexplained line\n",
		"malformed index":       "diff --git a/x b/x\nindex notanindex\n",
		"malformed hunk header": "diff --git a/x b/x\nindex 1111111111111111111111111111111111111111..2222222222222222222222222222222222222222 100644\n--- a/x\n+++ b/x\n@@ broken\n",
	}
	for name, diff := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGitDiff(diff); err == nil {
				t.Fatalf("expected an error for %q", name)
			}
		})
	}
}

// TestParseGitDiffMatchesRealGit runs the parser over a real repository
// covering every change kind, including content that looks like diff syntax.
func TestParseGitDiffMatchesRealGit(t *testing.T) {
	requireTools(t, "git")
	base := map[string]string{
		"mod.txt":        "line1\nline2\nline3\n",
		"del.txt":        "kept\n",
		"mode.txt":       "x\n",
		"src/app.js":     "app\n",
		"sp ace.txt":     "with space\n",
		"diffy.txt":      "old\n",
		"src/é.txt":      "unicode\n",
		`weird"name.txt`: "q\n",
	}
	g := newGuestRepoFixture(t, base)
	g.work("mod.txt", "line1\nCHANGED\nline3\nEXTRA\n")
	g.remove("del.txt")
	g.chmod("mode.txt", 0o755)
	g.work("newfile.js", "new file content\n")
	g.work("src/app.js", "app v2\n")
	g.work("bin.dat", "\x00\x01\x02binary\x00")
	g.work("sp ace.txt", "with space v2\n")
	// Content that looks like diff syntax must not end its section early.
	g.work("diffy.txt", "diff --git a/fake b/fake\n@@ -1 +1 @@\n-fake\n+fake\nend\n")
	g.work("src/é.txt", "unicode v2\n")
	g.work(`weird"name.txt`, "q v2\n")

	changes, err := parseGitDiff(g.diff())
	if err != nil {
		t.Fatal(err)
	}
	byRel := map[string]GuestChange{}
	for _, c := range changes {
		if _, dup := byRel[c.Rel]; dup {
			t.Fatalf("duplicate section for %s", c.Rel)
		}
		byRel[c.Rel] = c
	}
	for _, rel := range []string{"bin.dat", "del.txt", "diffy.txt", "mode.txt", "mod.txt", "newfile.js", "sp ace.txt", "src/app.js", "src/é.txt", `weird"name.txt`} {
		if _, ok := byRel[rel]; !ok {
			t.Errorf("missing section for %s", rel)
		}
	}
	if c := byRel["mod.txt"]; c.Status != GuestChangeModified || c.AddCount != 2 || c.DelCount != 1 {
		t.Errorf("mod.txt: %+v", c)
	}
	if c := byRel["newfile.js"]; c.Status != GuestChangeAdded || c.AddCount != 1 || c.DelCount != 0 {
		t.Errorf("newfile.js: %+v", c)
	}
	if c := byRel["del.txt"]; c.Status != GuestChangeDeleted || c.DelCount != 1 {
		t.Errorf("del.txt: %+v", c)
	}
	if c := byRel["bin.dat"]; !c.Binary {
		t.Errorf("bin.dat must parse as binary: %+v", c)
	}
	if c := byRel["mode.txt"]; c.OldMode != "100644" || c.NewMode != "100755" {
		t.Errorf("mode.txt: %+v", c)
	}
	// The diff-syntax-looking content must be counted as content, not split.
	if c := byRel["diffy.txt"]; c.AddCount != 5 || c.DelCount != 1 {
		t.Errorf("diffy.txt content lines miscounted: %+v", c)
	}
	// The postimage hash of a real modification is the blob hash of the
	// file content, which is what the content verification relies on.
	if c := byRel["mod.txt"]; c.PostHash != gitBlobHash([]byte("line1\nCHANGED\nline3\nEXTRA\n")) {
		t.Errorf("mod.txt postimage hash %s is not the content hash", c.PostHash)
	}
	if c := byRel["src/app.js"]; c.PreHash != gitBlobHash([]byte("app\n")) {
		t.Errorf("src/app.js preimage hash %s is not the snapshot hash", c.PreHash)
	}
}

// TestGitBlobHashMatchesRealGit pins the hash against git itself.
func TestGitBlobHashMatchesRealGit(t *testing.T) {
	requireTools(t, "git")
	dir := t.TempDir()
	writeFile(t, dir, "x.txt", "some content\nwith two lines\n")
	cmd := exec.Command("git", "hash-object", "x.txt")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(out))
	if got := gitBlobHash([]byte("some content\nwith two lines\n")); got != want {
		t.Fatalf("gitBlobHash = %s, git hash-object = %s", got, want)
	}
}

// TestPlanGuestApplyVerdicts drives the whole read-only pipeline over a real
// guest diff: every verdict kind must come out exactly once, with content
// attached only to the applicable files.
func TestPlanGuestApplyVerdicts(t *testing.T) {
	base := map[string]string{
		"src/app.js":   "app\n",
		"gone.js":      "gone\n",
		"conflict.js":  "c\n",
		"missing.js":   "m\n",
		"nested/dir/f": "deep\n",
	}
	g := newGuestRepoFixture(t, base)
	// The agent's work.
	g.work("src/app.js", "app v2\n")
	g.work("new.js", "brand new\n")
	// The mode assertion must not depend on the umask of the machine
	// running the test.
	g.chmod("new.js", 0o644)
	g.remove("gone.js")
	g.work("conflict.js", "c2\n")
	g.work("missing.js", "m2\n")
	g.work("exists.js", "e\n")
	g.work("nested/dir/f", "deep v2\n")

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	// The host folder holds the snapshot's content.
	for rel, content := range base {
		writeFile(t, m.cfg.WorkspaceDir, rel, content)
	}
	// The host diverged on two of them.
	writeFile(t, m.cfg.WorkspaceDir, "conflict.js", "host changed\n")
	writeFile(t, m.cfg.WorkspaceDir, "exists.js", "already here\n")
	if err := os.Remove(filepath.Join(m.cfg.WorkspaceDir, "missing.js")); err != nil {
		t.Fatal(err)
	}
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})

	diff := g.diff()
	// Applicable, non-deleted, path-sorted: nested/dir/f, new.js, src/app.js.
	queuePlanCapture(t, client, g, diff, []string{"nested/dir/f", "new.js", "src/app.js"})

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Root != m.cfg.WorkspaceDir {
		t.Errorf("plan.Root = %s, want the transfer source %s", plan.Root, m.cfg.WorkspaceDir)
	}
	if plan.SnapshotCommit != g.baseline {
		t.Errorf("plan.SnapshotCommit = %s, want %s", plan.SnapshotCommit, g.baseline)
	}
	wantVerdicts := map[string]struct {
		verdict  WorkspaceApplyVerdict
		conflict WorkspaceApplyConflictKind
	}{
		"src/app.js":   {WorkspaceApplyOK, ""},
		"new.js":       {WorkspaceApplyOK, ""},
		"gone.js":      {WorkspaceApplyOK, ""},
		"nested/dir/f": {WorkspaceApplyOK, ""},
		"conflict.js":  {WorkspaceApplyConflict, WorkspaceConflictHostModified},
		"missing.js":   {WorkspaceApplyConflict, WorkspaceConflictHostMissing},
		"exists.js":    {WorkspaceApplyConflict, WorkspaceConflictHostExists},
	}
	for rel, want := range wantVerdicts {
		f := planFile(t, plan, rel)
		if f.Verdict != want.verdict || f.Conflict != want.conflict {
			t.Errorf("%s: verdict=%s conflict=%s, want %s/%s", rel, f.Verdict, f.Conflict, want.verdict, want.conflict)
		}
	}
	if len(plan.Files) != len(wantVerdicts) {
		t.Fatalf("plan has %d files, want %d: %+v", len(plan.Files), len(wantVerdicts), plan.Files)
	}
	// Content is attached to exactly the applicable non-deleted files.
	for _, f := range plan.Files {
		switch {
		case f.Verdict == WorkspaceApplyOK && f.Change.Status == GuestChangeDeleted:
			if f.Content != nil {
				t.Errorf("%s: deletions carry no content", f.Change.Rel)
			}
		case f.Verdict == WorkspaceApplyOK:
			if f.Content == nil {
				t.Errorf("%s: applicable file has no content", f.Change.Rel)
			}
		default:
			if f.Content != nil {
				t.Errorf("%s: non-applicable file must not carry content", f.Change.Rel)
			}
		}
	}
	if got := string(planFile(t, plan, "src/app.js").Content); got != "app v2\n" {
		t.Errorf("src/app.js content = %q", got)
	}
	if got := string(planFile(t, plan, "new.js").Content); got != "brand new\n" {
		t.Errorf("new.js content = %q", got)
	}
	if mode := planFile(t, plan, "new.js").Mode; mode.Perm() != 0o644 {
		t.Errorf("new.js mode = %v", mode)
	}
}

// TestPlanGuestApplyNeverOverwritesHostModifiedFile is the safety canary: a
// file the host changed since the transfer is a conflict before the write AND
// stays a conflict at write time; its host content is untouched.
func TestPlanGuestApplyNeverOverwritesHostModifiedFile(t *testing.T) {
	base := map[string]string{"src/app.js": "app\n"}
	g := newGuestRepoFixture(t, base)
	g.work("src/app.js", "app v2\n")

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "src/app.js", "host edited\n")
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	queuePlanCapture(t, client, g, g.diff(), nil)

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := planFile(t, plan, "src/app.js")
	if f.Verdict != WorkspaceApplyConflict || f.Conflict != WorkspaceConflictHostModified {
		t.Fatalf("host-modified file must be a conflict, got %+v", f)
	}
	// Even if a caller passed it to the writer anyway, the writer refuses.
	f.Verdict = WorkspaceApplyOK
	applied, failed, conflicts := ApplyGuestFiles(plan.Root, []WorkspaceApplyFile{f})
	if len(applied) != 0 || len(failed) != 0 || len(conflicts) != 1 {
		t.Fatalf("the writer must demote the stale preimage: applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
	}
	host, err := os.ReadFile(filepath.Join(m.cfg.WorkspaceDir, "src/app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(host) != "host edited\n" {
		t.Fatalf("the host file was overwritten: %q", host)
	}
}

// TestPlanGuestApplyExcludesReverseFilteredPaths pins the reverse-direction
// filter: agent work on paths the transfer would refuse never comes back,
// whether the refusal comes from the sync record, the current manifest, or
// the path rules for files the manifest no longer lists.
func TestPlanGuestApplyExcludesReverseFilteredPaths(t *testing.T) {
	base := map[string]string{
		"tracked.js":  "t\n",
		"gonehost.js": "g\n",
	}
	g := newGuestRepoFixture(t, base)
	// The agent works on paths the forward filter refuses.
	g.work(".env", "ALBERT_API_KEY=secret\n")
	g.work("dist/generated.js", "// derived\n")
	g.work("gonehost.js", "g2\n")
	g.work("tracked.js", "t2\n")

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	// The host folder: a git repository whose ignore rules refuse dist/,
	// the tracked files, and no .env (it never crossed).
	gitInitForTransfer(t, m.cfg.WorkspaceDir)
	writeFile(t, m.cfg.WorkspaceDir, "tracked.js", "t\n")
	writeFile(t, m.cfg.WorkspaceDir, ".gitignore", "dist/\n")
	recordLastTransferForTest(t, m, LastTransferRecord{
		SchemaVersion:  lastTransferSchemaVersion,
		SnapshotCommit: g.baseline,
		Excluded: []LastTransferExclusion{
			{Rel: ".env", Exclusion: TransferExclusionDotenv},
		},
	})
	// gonehost.js existed at sync time but has since disappeared from the
	// host: the agent's modification of it is a conflict (nothing to apply
	// onto), never a write. The host's .env is gone too, so the manifest
	// cannot list it: the dotenv rule must be evaluated from the recorded
	// exclusion alone.
	writeFile(t, m.cfg.WorkspaceDir, "gonehost.js", "g\n")
	if err := os.Remove(filepath.Join(m.cfg.WorkspaceDir, "gonehost.js")); err != nil {
		t.Fatal(err)
	}

	// Fetch list: only tracked.js is applicable and non-deleted.
	queuePlanCapture(t, client, g, g.diff(), []string{"tracked.js"})

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		verdict   WorkspaceApplyVerdict
		exclusion TransferExclusion
		conflict  WorkspaceApplyConflictKind
	}{
		".env":              {WorkspaceApplyExcluded, TransferExclusionDotenv, ""},
		"dist/generated.js": {WorkspaceApplyExcluded, TransferExclusionGitIgnored, ""},
		"gonehost.js":       {WorkspaceApplyConflict, "", WorkspaceConflictHostMissing},
		"tracked.js":        {WorkspaceApplyOK, "", ""},
	}
	for rel, w := range want {
		f := planFile(t, plan, rel)
		if f.Verdict != w.verdict || f.Exclusion != w.exclusion || f.Conflict != w.conflict {
			t.Errorf("%s: verdict=%s exclusion=%s conflict=%s, want %s/%s/%s", rel, f.Verdict, f.Exclusion, f.Conflict, w.verdict, w.exclusion, w.conflict)
		}
	}
	// Nothing the filter refused may be written, even if handed to the writer.
	var all []WorkspaceApplyFile
	for _, f := range plan.Files {
		if f.Verdict == WorkspaceApplyExcluded {
			all = append(all, f)
		}
	}
	applied, failed, conflicts := ApplyGuestFiles(plan.Root, all)
	if len(applied) != 0 || len(failed) != 0 || len(conflicts) != 0 {
		t.Fatalf("excluded files must never be written: applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
	}
	if _, err := os.Lstat(filepath.Join(m.cfg.WorkspaceDir, ".env")); !os.IsNotExist(err) {
		t.Error("an excluded .env must not appear in the host folder")
	}
	if _, err := os.Lstat(filepath.Join(m.cfg.WorkspaceDir, "dist")); !os.IsNotExist(err) {
		t.Error("an excluded ignored path must not appear in the host folder")
	}
}

// TestPlanGuestApplyExcludesSecretFlaggedContent pins the incoming-content
// scan: a postimage the detector flags is excluded, and its bytes never land
// in the host folder.
func TestPlanGuestApplyExcludesSecretFlaggedContent(t *testing.T) {
	base := map[string]string{"ok.js": "ok\n", "creds.js": "c\n"}
	g := newGuestRepoFixture(t, base)
	g.work("ok.js", "ok v2\n")
	g.work("creds.js", "placeholder-not-a-secret\n")

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	for rel, content := range base {
		writeFile(t, m.cfg.WorkspaceDir, rel, content)
	}
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	// The detector seam is shared by the host-side manifest scan and the
	// incoming-content scan; only the incoming scan may flag creds.js, so
	// the host's own copy stays included and the exclusion provably comes
	// from the incoming bytes.
	orig := probeGitleaks
	hostDir := m.cfg.WorkspaceDir
	probeGitleaks = func(_ context.Context, dir string) (gitleaksProbe, error) {
		if dir == hostDir {
			return gitleaksProbe{}, nil
		}
		return gitleaksProbe{findings: []gitleaksFinding{{RuleID: "albert-key", File: "creds.js"}}}, nil
	}
	t.Cleanup(func() { probeGitleaks = orig })
	queuePlanCapture(t, client, g, g.diff(), []string{"creds.js", "ok.js"})

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := planFile(t, plan, "creds.js"); f.Verdict != WorkspaceApplyExcluded || f.Exclusion != TransferExclusionSecret {
		t.Fatalf("flagged content must be excluded, got %+v", f)
	}
	if f := planFile(t, plan, "ok.js"); f.Verdict != WorkspaceApplyOK {
		t.Fatalf("clean content must stay applicable, got %+v", f)
	}
	if plan.SecretsUnscanned {
		t.Error("the scan ran, so SecretsUnscanned must be false")
	}
	// Applying the whole plan writes only the clean file.
	all := plan.Applicable()
	applied, _, _ := ApplyGuestFiles(plan.Root, all)
	if len(applied) != 1 || applied[0] != "ok.js" {
		t.Fatalf("applied = %v, want [ok.js]", applied)
	}
	if _, err := os.Stat(filepath.Join(m.cfg.WorkspaceDir, "creds.js")); err != nil {
		t.Fatal(err)
	}
	creds, err := os.ReadFile(filepath.Join(m.cfg.WorkspaceDir, "creds.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(creds) != "c\n" {
		t.Fatalf("the flagged content was written: %q", creds)
	}
}

// TestPlanGuestApplyReportsUnscannedSecrets pins the honest degradation: when
// the detector is unavailable the plan says so instead of claiming a scan.
func TestPlanGuestApplyReportsUnscannedSecrets(t *testing.T) {
	base := map[string]string{"a.js": "a\n"}
	g := newGuestRepoFixture(t, base)
	g.work("a.js", "a2\n")

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "a.js", "a\n")
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	withNoGitleaks(t)
	queuePlanCapture(t, client, g, g.diff(), []string{"a.js"})

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SecretsUnscanned {
		t.Error("SecretsUnscanned must be true when the detector is missing")
	}
	if f := planFile(t, plan, "a.js"); f.Verdict != WorkspaceApplyOK {
		t.Errorf("a missing detector must not exclude files by itself: %+v", f)
	}
}

// TestPlanGuestApplyExcludesOversizedIncomingFile pins the size cap on the
// reverse direction: an incoming file larger than the transfer cap is
// excluded before its bytes are fetched.
func TestPlanGuestApplyExcludesOversizedIncomingFile(t *testing.T) {
	base := map[string]string{"small.js": "s\n", "big.bin": "b\n"}
	g := newGuestRepoFixture(t, base)
	g.work("small.js", "s2\n")
	// Binary content keeps the diff section small while the file itself
	// crosses the cap.
	big := "\x00" + strings.Repeat("x", int(DefaultTransferMaxFileBytes))
	g.work("big.bin", big)

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "small.js", "s\n")
	writeFile(t, m.cfg.WorkspaceDir, "big.bin", "b\n")
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	// The size probe covers both candidates (the demotion happens on its
	// results); only the small file is then read through the tarball.
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"}, // provisioned
		{stdout: "yes"}, // recorded snapshot commit exists
		{stdout: g.diff()},
		{stdout: g.sizes([]string{"big.bin", "small.js"})},
		{stdout: g.tarball([]string{"small.js"})},
	}

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f := planFile(t, plan, "big.bin"); f.Verdict != WorkspaceApplyExcluded || f.Exclusion != TransferExclusionSizeCap {
		t.Fatalf("oversized incoming file must be excluded, got %+v", f)
	}
	if f := planFile(t, plan, "small.js"); f.Verdict != WorkspaceApplyOK {
		t.Fatalf("small file must stay applicable, got %+v", f)
	}
	// The oversized file's bytes must never transit: the tarball read
	// names only the small file.
	for _, call := range client.calls {
		if strings.Contains(call, "tar -czf") && strings.Contains(call, "big.bin") {
			t.Errorf("the oversized file must not be fetched: %s", call)
		}
	}
}

// TestPlanGuestApplyRefusesWithoutBaseline pins the fail-closed baseline: an
// unprovisioned guest, and a guest whose snapshot cannot be identified, are
// refused rather than measured against a guess.
func TestPlanGuestApplyRefusesWithoutBaseline(t *testing.T) {
	t.Run("unprovisioned", func(t *testing.T) {
		client := &fakeMSBClient{exists: true, execCaptureDefault: &fakeMSBExecCaptureResult{stdout: "no"}}
		m := newTestMicrosandbox(t, client)
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("an unprovisioned guest must be refused")
		}
		if !strings.Contains(err.Error(), "no snapshot yet") {
			t.Fatalf("the refusal must be actionable: %v", err)
		}
	})
	t.Run("no record", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"}, // provisioned
		}
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("a guest without a transfer record must be refused")
		}
		// The refusal must keep the guest's work reachable: it names the
		// export path, and the record's absence is explained rather than
		// silently bypassed by a history search.
		if !strings.Contains(err.Error(), "no last-transfer record") || !strings.Contains(err.Error(), "just-code workspace export") {
			t.Fatalf("the refusal must explain how to get the work: %v", err)
		}
	})
	t.Run("corrupt record", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		path := LastTransferPath(m.stateDirOrDefault(), m.InstanceName())
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("a corrupt record must be refused, not ignored")
		}
	})
	t.Run("recorded commit gone is refused, not searched for", func(t *testing.T) {
		base := map[string]string{"a.js": "a\n"}
		g := newGuestRepoFixture(t, base)
		g.work("a.js", "a2\n")
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		writeFile(t, m.cfg.WorkspaceDir, "a.js", "a\n")
		// The record names a commit the guest no longer has: the record
		// is stale. A history search could find a same-message commit
		// from a different transfer round, with different exclusions —
		// the apply must refuse instead.
		recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: "0123456789012345678901234567890123456789"})
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"}, // provisioned
			{stdout: "no"},  // recorded commit absent
		}
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("a stale record must be refused, not worked around")
		}
		if !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "just-code workspace export") {
			t.Fatalf("the refusal must explain the stale record and the export path: %v", err)
		}
		for _, call := range client.calls {
			if strings.Contains(call, "--grep") {
				t.Errorf("no history search may run: %s", call)
			}
		}
	})
	t.Run("record without a snapshot commit", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion})
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"}, // provisioned
		}
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("a record without a snapshot commit must be refused")
		}
		if !strings.Contains(err.Error(), "names no snapshot commit") {
			t.Fatalf("the refusal must name the cause: %v", err)
		}
	})
}

// TestPlanGuestApplySeesCommittedGuestWork pins the baseline choice: the
// agent's commits inside the guest advance HEAD, and the diff measured
// against the transferred snapshot still reports the committed work.
func TestPlanGuestApplySeesCommittedGuestWork(t *testing.T) {
	base := map[string]string{"a.js": "a\n"}
	g := newGuestRepoFixture(t, base)
	g.work("a.js", "committed work\n")
	// The agent commits its work on top of the snapshot.
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = g.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-c", "user.email=a@b.c", "-c", "user.name=agent", "commit", "-q", "-m", "agent work")
	cmd.Dir = g.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "a.js", "a\n")
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	queuePlanCapture(t, client, g, g.diff(), []string{"a.js"})

	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := planFile(t, plan, "a.js")
	if f.Verdict != WorkspaceApplyOK {
		t.Fatalf("committed work must still be applicable: %+v", f)
	}
	if string(f.Content) != "committed work\n" {
		t.Fatalf("committed work content = %q", f.Content)
	}
}

// TestApplyGuestFilesWritesWithoutCommitting pins the no-commit contract: the
// write changes the working tree only, and a git host shows the changes as
// unstaged, with the commit count untouched.
func TestApplyGuestFilesWritesWithoutCommitting(t *testing.T) {
	requireTools(t, "git")
	root := t.TempDir()
	gitInitForTransfer(t, root)
	writeFile(t, root, "mod.js", "old\n")
	writeFile(t, root, "del.js", "bye\n")
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "commit", "-q", "-m", "base")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	commitsBefore := gitLogCount(t, root)

	files := []WorkspaceApplyFile{
		{Change: GuestChange{Rel: "mod.js", Status: GuestChangeModified, PreHash: gitBlobHash([]byte("old\n"))}, Verdict: WorkspaceApplyOK, Content: []byte("new\n"), Mode: 0o644},
		{Change: GuestChange{Rel: "add.js", Status: GuestChangeAdded}, Verdict: WorkspaceApplyOK, Content: []byte("added\n"), Mode: 0o755},
		{Change: GuestChange{Rel: "del.js", Status: GuestChangeDeleted, PreHash: gitBlobHash([]byte("bye\n"))}, Verdict: WorkspaceApplyOK},
	}
	applied, failed, conflicts := ApplyGuestFiles(root, files)
	if len(applied) != 3 || len(failed) != 0 || len(conflicts) != 0 {
		t.Fatalf("applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "mod.js")); string(got) != "new\n" {
		t.Fatalf("mod.js = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "add.js")); string(got) != "added\n" {
		t.Fatalf("add.js = %q", got)
	}
	if info, err := os.Stat(filepath.Join(root, "add.js")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("add.js mode = %v err=%v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "del.js")); !os.IsNotExist(err) {
		t.Fatal("del.js must be removed")
	}
	if got := gitLogCount(t, root); got != commitsBefore {
		t.Fatalf("commit count changed: before=%d after=%d — apply must never commit", commitsBefore, got)
	}
	status := gitPorcelain(t, root)
	for _, want := range []string{" M mod.js", "?? add.js", " D del.js"} {
		if !strings.Contains(status, want) {
			t.Errorf("git status must show %q as an unstaged change, got:\n%s", want, status)
		}
	}
	for _, staged := range []string{"M  ", "A  ", "D  "} {
		if strings.Contains(status, staged) {
			t.Errorf("nothing may be staged, got:\n%s", status)
		}
	}
}

// TestApplyGuestFilesRevalidatesPreimage pins the TOCTOU guard: a file whose
// preimage stopped matching between the plan and the write is demoted to a
// conflict and its host content is preserved.
func TestApplyGuestFilesRevalidatesPreimage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.js", "old\n")
	// The plan was built when a.js held "old\n"; the host changed since.
	writeFile(t, root, "a.js", "changed on the host\n")
	files := []WorkspaceApplyFile{
		{Change: GuestChange{Rel: "a.js", Status: GuestChangeModified, PreHash: gitBlobHash([]byte("old\n"))}, Verdict: WorkspaceApplyOK, Content: []byte("agent\n"), Mode: 0o644},
	}
	applied, failed, conflicts := ApplyGuestFiles(root, files)
	if len(applied) != 0 || len(failed) != 0 || len(conflicts) != 1 {
		t.Fatalf("applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
	}
	if conflicts[0].Conflict != WorkspaceConflictHostModified {
		t.Fatalf("demotion reason = %s", conflicts[0].Conflict)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.js")); string(got) != "changed on the host\n" {
		t.Fatalf("host content was overwritten: %q", got)
	}
}

// TestApplyGuestFilesRefusesSymlinkEscape pins the symlink rule at write time:
// content is never written through a link, even when the plan said OK.
func TestApplyGuestFilesRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, root, "sub", "")
	if err := os.Remove(filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sub")); err != nil {
		t.Fatal(err)
	}
	files := []WorkspaceApplyFile{
		{Change: GuestChange{Rel: "sub/new.js", Status: GuestChangeAdded}, Verdict: WorkspaceApplyOK, Content: []byte("x\n"), Mode: 0o644},
	}
	applied, _, conflicts := ApplyGuestFiles(root, files)
	if len(applied) != 0 || len(conflicts) != 1 {
		t.Fatalf("a write through a symlink must be refused: applied=%v conflicts=%v", applied, conflicts)
	}
	if _, err := os.Lstat(filepath.Join(outside, "new.js")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written through the symlink")
	}
}

// TestApplyGuestFilesRefusesSwappedParent is the regression for the review
// finding: a parent directory replaced by a symlink between planning and
// writing must demote every operation on paths under it to a conflict —
// including a deletion whose preimage still matches through the link, which
// must never remove the file the link points at.
func TestApplyGuestFilesRefusesSwappedParent(t *testing.T) {
	// swapParent builds a workspace whose sub/ directory held the snapshot
	// content, then replaces sub/ with a symlink to an outside directory
	// holding byte-identical content: the preimage check would pass if it
	// resolved through the link.
	swapParent := func(t *testing.T) (root, outside string) {
		t.Helper()
		root = t.TempDir()
		outside = t.TempDir()
		writeFile(t, root, "sub/del.js", "bye\n")
		writeFile(t, root, "sub/mod.js", "old\n")
		if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, outside, "del.js", "bye\n")
		writeFile(t, outside, "mod.js", "old\n")
		if err := os.Symlink(outside, filepath.Join(root, "sub")); err != nil {
			t.Fatal(err)
		}
		return root, outside
	}
	t.Run("deletion with a matching preimage through the link", func(t *testing.T) {
		root, outside := swapParent(t)
		files := []WorkspaceApplyFile{
			{Change: GuestChange{Rel: "sub/del.js", Status: GuestChangeDeleted, PreHash: gitBlobHash([]byte("bye\n"))}, Verdict: WorkspaceApplyOK},
		}
		applied, failed, conflicts := ApplyGuestFiles(root, files)
		if len(applied) != 0 || len(failed) != 0 || len(conflicts) != 1 {
			t.Fatalf("the deletion must be demoted, not performed: applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
		}
		if _, err := os.Lstat(filepath.Join(outside, "del.js")); err != nil {
			t.Fatalf("the file the swapped parent points at was removed: %v", err)
		}
	})
	t.Run("modification with a matching preimage through the link", func(t *testing.T) {
		root, outside := swapParent(t)
		files := []WorkspaceApplyFile{
			{Change: GuestChange{Rel: "sub/mod.js", Status: GuestChangeModified, PreHash: gitBlobHash([]byte("old\n"))}, Verdict: WorkspaceApplyOK, Content: []byte("agent\n"), Mode: 0o644},
		}
		applied, failed, conflicts := ApplyGuestFiles(root, files)
		if len(applied) != 0 || len(failed) != 0 || len(conflicts) != 1 {
			t.Fatalf("the write must be demoted, not performed: applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
		}
		if got, _ := os.ReadFile(filepath.Join(outside, "mod.js")); string(got) != "old\n" {
			t.Fatalf("content was written through the swapped parent: %q", got)
		}
	})
	t.Run("addition through the link", func(t *testing.T) {
		root, outside := swapParent(t)
		files := []WorkspaceApplyFile{
			{Change: GuestChange{Rel: "sub/new.js", Status: GuestChangeAdded}, Verdict: WorkspaceApplyOK, Content: []byte("agent\n"), Mode: 0o644},
		}
		applied, failed, conflicts := ApplyGuestFiles(root, files)
		if len(applied) != 0 || len(failed) != 0 || len(conflicts) != 1 {
			t.Fatalf("the creation must be demoted, not performed: applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
		}
		if _, err := os.Lstat(filepath.Join(outside, "new.js")); !os.IsNotExist(err) {
			t.Fatal("a file was created through the swapped parent")
		}
	})
}

// TestApplyGuestFilesAnchoredAtRoot pins the anchor itself: writes and
// deletions cannot escape the workspace even when the guest path is crafted
// (the normalization refuses .., and the anchor refuses symlink escapes).
func TestApplyGuestFilesAnchoredAtRoot(t *testing.T) {
	root := t.TempDir()
	// A path with .. is refused by normalization before anything opens.
	files := []WorkspaceApplyFile{
		{Change: GuestChange{Rel: "../escape.js", Status: GuestChangeAdded}, Verdict: WorkspaceApplyOK, Content: []byte("x\n"), Mode: 0o644},
	}
	applied, _, _ := ApplyGuestFiles(root, files)
	if len(applied) != 0 {
		t.Fatalf("a traversal path was applied: %v", applied)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(root), "escape.js")); !os.IsNotExist(err) {
		t.Fatal("a file escaped the workspace")
	}
}

// TestTransferRecordsLastTransfer pins the record's persistence: a successful
// transfer writes the snapshot commit and the filter's exclusions.
func TestTransferRecordsLastTransfer(t *testing.T) {
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "main.go", "package main\n")
	writeFile(t, m.cfg.WorkspaceDir, ".env", "ALBERT_API_KEY=x\n")
	// Not provisioned yet, then the guest reports its HEAD.
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "no"}, // provisioned probe
		{stdout: "abc123abc123abc123abc123abc123abc123abc1"}, // rev-parse HEAD
	}
	if err := m.ProvisionGuestWorkspace(context.Background(), SyncOptions{Print: func(string) {}}); err != nil {
		t.Fatal(err)
	}
	rec, err := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rec.SnapshotCommit != "abc123abc123abc123abc123abc123abc123abc1" {
		t.Errorf("SnapshotCommit = %s", rec.SnapshotCommit)
	}
	found := false
	for _, ex := range rec.Excluded {
		if ex.Rel == ".env" && ex.Exclusion == TransferExclusionDotenv {
			found = true
		}
	}
	if !found {
		t.Errorf("the .env exclusion must be recorded: %+v", rec.Excluded)
	}
	if rec.SchemaVersion != lastTransferSchemaVersion {
		t.Errorf("SchemaVersion = %d", rec.SchemaVersion)
	}
}

// failingSaveFS is an FS whose temporary writes fail, to exercise a record
// save that cannot complete.
type failingSaveFS struct{ FS }

func (failingSaveFS) WriteTemp(dir, base string, data []byte, perm fs.FileMode) (string, error) {
	return "", fmt.Errorf("simulated write failure")
}

// failingRemoveFS is an FS whose removals fail, to exercise a record whose
// invalidation cannot happen.
type failingRemoveFS struct{ FS }

func (failingRemoveFS) Remove(path string) error {
	return fmt.Errorf("simulated removal failure")
}

// TestTransferInvalidatesRecordBeforeWriting pins the ordering: the previous
// transfer's record is invalidated before the first guest write, and a
// removal that fails aborts the transfer with the guest untouched — the old
// record still matches the guest it describes, so it stays and apply keeps
// working.
func TestTransferInvalidatesRecordBeforeWriting(t *testing.T) {
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "main.go", "package main\n")
	// A record from an earlier transfer is on disk.
	recordLastTransferForTest(t, m, LastTransferRecord{
		SchemaVersion:  lastTransferSchemaVersion,
		SnapshotCommit: "oldcommit",
	})
	// Its invalidation fails.
	orig := newLastTransferStore
	newLastTransferStore = func(stateDir, instance string) LastTransferStore {
		return LastTransferStore{Path: LastTransferPath(stateDir, instance), FS: failingRemoveFS{DefaultFS}}
	}
	t.Cleanup(func() { newLastTransferStore = orig })
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "no"}, // provisioned probe: the transfer path runs
	}
	err := m.ProvisionGuestWorkspace(context.Background(), SyncOptions{Print: func(string) {}})
	if err == nil {
		t.Fatal("a transfer whose record invalidation fails must abort")
	}
	if !strings.Contains(err.Error(), "cannot invalidate the last-transfer record") || !strings.Contains(err.Error(), "aborted before anything was written to the guest") {
		t.Fatalf("the refusal must say the guest was not touched: %v", err)
	}
	// The guest was not written to: no payload push, no extraction, no git.
	for _, call := range client.calls {
		if strings.Contains(call, "writefile") || strings.HasPrefix(call, "exec ") {
			t.Errorf("the guest must stay untouched, saw: %s", call)
		}
	}
	// The old record still describes the untouched guest, so it survives
	// and apply still has its baseline.
	store := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}
	rec, err := store.Load()
	if err != nil || rec.SnapshotCommit != "oldcommit" {
		t.Fatalf("the untouched guest's record must stay valid: rec=%+v err=%v", rec, err)
	}
}

// TestTransferFailureLeavesNoStaleRecord pins the mid-transfer contract: once
// the first guest write is about to happen the record is invalid, so a
// failure later in the transfer — here the extraction — leaves no record at
// all, and apply refuses on the missing record rather than trusting one whose
// baseline the half-written guest no longer matches.
func TestTransferFailureLeavesNoStaleRecord(t *testing.T) {
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "main.go", "package main\n")
	recordLastTransferForTest(t, m, LastTransferRecord{
		SchemaVersion:  lastTransferSchemaVersion,
		SnapshotCommit: "oldcommit",
	})
	// The guest refuses the extraction: the transfer fails midway.
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "no"}, // provisioned probe
	}
	client.execResults = []fakeMSBExecResult{{code: 1, stderr: "extraction failed"}}
	err := m.ProvisionGuestWorkspace(context.Background(), SyncOptions{Print: func(string) {}})
	if err == nil {
		t.Fatal("a failed extraction must fail the transfer")
	}
	store := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "no last-transfer record") {
		t.Fatalf("a failed transfer must leave no record: %v", err)
	}
}

// TestTransferSaveFailureLeavesNoStaleRecord pins the trust rule through the
// real flow: when the record of the transfer that just ran cannot be saved,
// the previous record must not survive — it was invalidated before the first
// guest write — and apply must refuse on the missing record rather than trust
// exclusions from an earlier transfer.
func TestTransferSaveFailureLeavesNoStaleRecord(t *testing.T) {
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	writeFile(t, m.cfg.WorkspaceDir, "main.go", "package main\n")
	recordLastTransferForTest(t, m, LastTransferRecord{
		SchemaVersion:  lastTransferSchemaVersion,
		SnapshotCommit: "oldcommit",
		Excluded:       []LastTransferExclusion{{Rel: ".env", Exclusion: TransferExclusionDotenv}},
	})
	// The transfer succeeds, but its record cannot be saved.
	orig := newLastTransferStore
	newLastTransferStore = func(stateDir, instance string) LastTransferStore {
		return LastTransferStore{Path: LastTransferPath(stateDir, instance), FS: failingSaveFS{DefaultFS}}
	}
	t.Cleanup(func() { newLastTransferStore = orig })
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "no"}, // provisioned probe
		{stdout: "abc123abc123abc123abc123abc123abc123abc1"}, // rev-parse HEAD
	}
	if err := m.ProvisionGuestWorkspace(context.Background(), SyncOptions{Print: func(string) {}}); err != nil {
		t.Fatal(err)
	}
	// No record may survive: not the stale one, not a half-written one.
	store := LastTransferStore{Path: LastTransferPath(m.stateDirOrDefault(), m.InstanceName()), FS: DefaultFS}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "no last-transfer record") {
		t.Fatalf("the stale record must not survive a failed save: %v", err)
	}
}

// TestPlanGuestApplyModeOnlyChanges pins the hash rule for mode-only changes:
// the diff carries no index line, so the blob is resolved from the baseline
// tree and verified like any other change — a host file that changed since
// the transfer is a conflict, never an overwrite riding on an empty hash.
func TestPlanGuestApplyModeOnlyChanges(t *testing.T) {
	base := map[string]string{"run.sh": "echo hi\n"}
	g := newGuestRepoFixture(t, base)
	g.chmod("run.sh", 0o755)
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	// The host file changed since the transfer.
	writeFile(t, m.cfg.WorkspaceDir, "run.sh", "echo changed\n")
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"},                        // provisioned
		{stdout: "yes"},                        // recorded snapshot commit exists
		{stdout: g.diff()},                     // the diff
		{stdout: g.lstree([]string{"run.sh"})}, // the baseline blob of the mode-only change
	}
	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := planFile(t, plan, "run.sh")
	if f.Verdict != WorkspaceApplyConflict || f.Conflict != WorkspaceConflictHostModified {
		t.Fatalf("a host-modified preimage must conflict even for a mode-only change: %+v", f)
	}
	if f.Change.PreHash == "" || f.Change.PostHash == "" || f.Change.PreHash != f.Change.PostHash {
		t.Fatalf("the mode-only change must carry the baseline blob hash: %+v", f.Change)
	}
	if f.Change.PreHash != gitBlobHash([]byte("echo hi\n")) {
		t.Fatalf("the resolved hash must be the baseline blob: %s", f.Change.PreHash)
	}
}

// TestPlanGuestApplyModeOnlyChangeApplies pins the happy path of a mode-only
// change: with the host preimage intact, the change applies the new mode
// through the same verified write as any other change, without committing.
func TestPlanGuestApplyModeOnlyChangeApplies(t *testing.T) {
	base := map[string]string{"run.sh": "echo hi\n"}
	g := newGuestRepoFixture(t, base)
	g.chmod("run.sh", 0o755)
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	// The host file still matches the transferred snapshot.
	writeFile(t, m.cfg.WorkspaceDir, "run.sh", "echo hi\n")
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"},                        // provisioned
		{stdout: "yes"},                        // recorded snapshot commit exists
		{stdout: g.diff()},                     // the diff
		{stdout: g.lstree([]string{"run.sh"})}, // the baseline blob
		{stdout: g.sizes([]string{"run.sh"})},  // the size probe
		{stdout: g.tarball([]string{"run.sh"})},
	}
	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := planFile(t, plan, "run.sh")
	if f.Verdict != WorkspaceApplyOK {
		t.Fatalf("an intact preimage must stay applicable: %+v", f)
	}
	if f.Mode.Perm() != 0o755 {
		t.Fatalf("the incoming mode must be the guest's: %v", f.Mode)
	}
	applied, failed, conflicts := ApplyGuestFiles(plan.Root, plan.Files)
	if len(applied) != 1 || len(failed) != 0 || len(conflicts) != 0 {
		t.Fatalf("applied=%v failed=%v conflicts=%v", applied, failed, conflicts)
	}
	info, err := os.Lstat(filepath.Join(plan.Root, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("the mode must be applied: %v", info.Mode())
	}
	if got, _ := os.ReadFile(filepath.Join(plan.Root, "run.sh")); string(got) != "echo hi\n" {
		t.Fatalf("the content must be unchanged: %q", got)
	}
}

// TestPlanGuestApplyRefusesUnverifiableHashes pins the fail-closed backstop:
// a change whose blob hashes cannot be proven refuses the whole plan rather
// than letting the byte verification silently pass.
func TestPlanGuestApplyRefusesUnverifiableHashes(t *testing.T) {
	t.Run("mode-only blob unresolvable", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: "abc123"})
		// A modified section with no index line, and a baseline tree
		// that does not name the path either.
		diff := "diff --git a/x.js b/x.js\n--- a/x.js\n+++ b/x.js\n@@ -1 +1 @@\n-a\n+b\n"
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"}, // provisioned
			{stdout: "yes"}, // recorded snapshot commit exists
			{stdout: diff},  // the diff
			{stdout: ""},    // ls-tree finds nothing
		}
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("an unprovable preimage must refuse the plan")
		}
		if !strings.Contains(err.Error(), "cannot resolve the blob") {
			t.Fatalf("the refusal must name the cause: %v", err)
		}
	})
	t.Run("added file without a postimage hash", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: "abc123"})
		diff := "diff --git a/y.js b/y.js\nnew file mode 100644\n--- /dev/null\n+++ b/y.js\n@@ -0,0 +1 @@\n+new\n"
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"}, // provisioned
			{stdout: "yes"}, // recorded snapshot commit exists
			{stdout: diff},  // the diff
		}
		_, err := m.PlanGuestApply(context.Background())
		if err == nil {
			t.Fatal("an added file without a postimage hash must refuse the plan")
		}
		if !strings.Contains(err.Error(), "no postimage hash") {
			t.Fatalf("the refusal must name the cause: %v", err)
		}
	})
}

// TestGuestDiffResistsGuestDiffConfig pins the integrity of what the user
// reviews: the guest's git configuration is agent-controlled, and both
// diff.external and a textconv driver bound through .gitattributes forge the
// displayed diff by default — git shows the forged bytes, not the file's. The
// production flags must pin the diff to git's internal machinery, so the
// review is always of the real content.
func TestGuestDiffResistsGuestDiffConfig(t *testing.T) {
	base := map[string]string{"a.js": "honest\n"}
	g := newGuestRepoFixture(t, base)
	g.work("a.js", "changed\n")
	plain := []string{"-c", "core.quotePath=false", "diff", "--no-renames", "--full-index", "--src-prefix=a/", "--dst-prefix=b/", g.baseline}
	// Control 1 — an external diff driver replaces the diff body with its
	// own output when the pinning flags are absent.
	script := filepath.Join(t.TempDir(), "forge.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho FORGED-BY-EXTERNAL\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.git("config", "diff.external", script)
	if control := g.git(plain...); !strings.Contains(control, "FORGED-BY-EXTERNAL") {
		t.Fatalf("the control diff must show the forgery the flags prevent, got:\n%s", control)
	}
	g.git("config", "--unset", "diff.external")
	// Control 2 — a textconv driver rewrites the displayed content when
	// the pinning flags are absent.
	g.git("config", "diff.evil.textconv", "sed s/honest/FORGED-BY-TEXTCONV/")
	g.work(".gitattributes", "*.js diff=evil\n")
	if control := g.git(plain...); !strings.Contains(control, "FORGED-BY-TEXTCONV") {
		t.Fatalf("the control diff must show the textconv forgery the flags prevent, got:\n%s", control)
	}
	// Production command, with both forgeries configured: the diff is
	// git's own, and it is the file's real bytes.
	g.git("config", "diff.external", script)
	out := g.diff()
	if strings.Contains(out, "FORGED-BY-EXTERNAL") || strings.Contains(out, "FORGED-BY-TEXTCONV") {
		t.Fatalf("the guest's diff configuration must not forge the displayed diff:\n%s", out)
	}
	if !strings.Contains(out, "-honest") || !strings.Contains(out, "+changed") {
		t.Fatalf("the displayed diff must be the file's real bytes:\n%s", out)
	}
}

// TestGuestCommandsKeepSlashSeparatedPaths pins the guest-path contract: the
// guest is Linux even when the host is Windows, so every guest command must
// carry the apply's slash-separated rels verbatim — filepath.FromSlash on a
// Windows host would rewrite nested/dir/f.js into nested\dir\f.js and point
// the guest at the wrong file. The assertions run on every platform, so a
// Windows host catches a regression the Linux host cannot see.
func TestGuestCommandsKeepSlashSeparatedPaths(t *testing.T) {
	rels := []string{"nested/dir/f.js", "plain.js"}
	commands := map[string]string{
		"diff":          guestCd() + "git add -A -N && " + guestGitFragment(guestDiffArgs("baseline", false, nil)),
		"binary patch":  guestCd() + guestGitFragment(guestDiffArgs("baseline", true, rels)),
		"ls-tree":       guestCd() + guestGitFragment(guestLsTreeArgs("baseline", rels)),
		"stat":          guestStatCommand(rels),
		"tar":           guestTarCommand(rels),
	}
	for name, cmd := range commands {
		if strings.Contains(cmd, `\`) {
			t.Errorf("%s: a guest command must never carry a backslash (the guest is Linux): %s", name, cmd)
		}
		if name == "diff" {
			continue // whole-tree diff: no pathspecs
		}
		if !strings.Contains(cmd, "nested/dir/f.js") || !strings.Contains(cmd, "plain.js") {
			t.Errorf("%s: the slash-separated paths must appear verbatim: %s", name, cmd)
		}
	}
	for _, name := range []string{"diff", "binary patch"} {
		if cmd := commands[name]; !strings.Contains(cmd, "--no-ext-diff") || !strings.Contains(cmd, "--no-textconv") {
			t.Errorf("%s: external diff and textconv must be disabled: %s", name, cmd)
		}
	}
	if !strings.Contains(commands["binary patch"], "--binary") {
		t.Errorf("the binary patch must request literal payloads: %s", commands["binary patch"])
	}
}

// TestPlanGuestApplyRefusesOversizedHostPreimage pins the bounded preimage
// read: no file larger than the transfer cap can ever have crossed into the
// guest (the size-cap exclusion is structural, not overridable), so a host
// file beyond the cap cannot be the preimage — it is a conflict decided from
// the size alone, and a giant host edit can never push the apply into reading
// it whole.
func TestPlanGuestApplyRefusesOversizedHostPreimage(t *testing.T) {
	base := map[string]string{"a.js": "a\n"}
	g := newGuestRepoFixture(t, base)
	g.work("a.js", "a2\n")
	client := &fakeMSBClient{exists: true}
	m := newTestMicrosandbox(t, client)
	recordLastTransferForTest(t, m, LastTransferRecord{SchemaVersion: lastTransferSchemaVersion, SnapshotCommit: g.baseline})
	// The host file grew far beyond the transfer cap (sparse: the size is
	// what matters, the bytes are never read).
	hostFile := filepath.Join(m.cfg.WorkspaceDir, "a.js")
	writeFile(t, m.cfg.WorkspaceDir, "a.js", "a\n")
	if err := os.Truncate(hostFile, maxPreimageBytes+1); err != nil {
		t.Fatal(err)
	}
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"}, // provisioned
		{stdout: "yes"}, // recorded snapshot commit exists
		{stdout: g.diff()},
	}
	plan, err := m.PlanGuestApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f := planFile(t, plan, "a.js")
	if f.Verdict != WorkspaceApplyConflict || f.Conflict != WorkspaceConflictHostModified {
		t.Fatalf("an oversized host preimage must conflict from the size alone: %+v", f)
	}
}

// TestGitBlobHashFileStreamsBounded pins the streaming preimage hash: a
// normal file hashes exactly like the in-memory form, and a file beyond the
// transfer cap is refused without being read.
func TestGitBlobHashFileStreamsBounded(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.js", "hello\n")
	file, err := os.Open(filepath.Join(dir, "a.js"))
	if err != nil {
		t.Fatal(err)
	}
	hash, ok := gitBlobHashFile(file)
	file.Close()
	if !ok || hash != gitBlobHash([]byte("hello\n")) {
		t.Fatalf("hash=%s ok=%v, want the in-memory form's hash", hash, ok)
	}
	// A file larger than the cap cannot be the preimage: refused unread.
	writeFile(t, dir, "big.bin", "x\n")
	big := filepath.Join(dir, "big.bin")
	if err := os.Truncate(big, maxPreimageBytes+1); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(big)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gitBlobHashFile(file); ok {
		t.Fatal("a file beyond the transfer cap must be refused unread")
	}
	file.Close()
}

// TestLastTransferStoreRoundTrip pins the record's own load/save contract.
func TestLastTransferStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := LastTransferStore{Path: filepath.Join(dir, "last-transfer.json"), FS: DefaultFS}
	// A missing record is an error that tells the user how to get the
	// guest's work without it — never a silent zero value whose missing
	// exclusions would weaken the reverse filter.
	_, err := store.Load()
	if err == nil || !strings.Contains(err.Error(), "just-code workspace export") {
		t.Fatalf("a missing record must refuse with instructions, got: %v", err)
	}
	want := LastTransferRecord{
		SchemaVersion:  lastTransferSchemaVersion,
		RecordedAt:     "2026-10-27T00:00:00Z",
		SnapshotCommit: "abc",
		Excluded:       []LastTransferExclusion{{Rel: ".env", Exclusion: TransferExclusionDotenv}},
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotCommit != want.SnapshotCommit || len(got.Excluded) != 1 || got.Excluded[0].Rel != ".env" {
		t.Fatalf("round trip: %+v", got)
	}
	// An unknown schema is refused rather than misread.
	if err := store.Save(LastTransferRecord{SchemaVersion: 99}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("an unknown schema version must be refused")
	}
}

// TestSaveUnappliedPatch pins the patch path: text-only conflicts save the
// captured sections without a second guest call, and binary conflicts
// trigger the --binary re-read.
func TestSaveUnappliedPatch(t *testing.T) {
	textSection := "diff --git a/x.js b/x.js\nindex 1111111111111111111111111111111111111111..2222222222222222222222222222222222222222 100644\n--- a/x.js\n+++ b/x.js\n@@ -1 +1 @@\n-a\n+b\n"
	t.Run("text only", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		files := []WorkspaceApplyFile{
			{Change: GuestChange{Rel: "x.js", Status: GuestChangeModified, Diff: textSection}, Verdict: WorkspaceApplyConflict, Conflict: WorkspaceConflictHostModified},
		}
		var path string
		var err error
		out := captureStdout(t, func() { path, err = m.SaveUnappliedPatch(context.Background(), "baseline", files) })
		if err != nil {
			t.Fatal(err)
		}
		_ = out
		if !strings.HasSuffix(path, ".patch") || !strings.Contains(path, "unapplied-") {
			t.Fatalf("patch path = %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "diff --git a/x.js b/x.js") {
			t.Fatalf("patch content = %q", data)
		}
		if hasCall(client, "execcapture") {
			t.Fatal("text-only conflicts must not trigger a second guest call")
		}
	})
	t.Run("binary triggers the binary re-read", func(t *testing.T) {
		client := &fakeMSBClient{exists: true}
		m := newTestMicrosandbox(t, client)
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "diff --git a/b.bin b/b.bin\nGIT binary patch\n"},
		}
		files := []WorkspaceApplyFile{
			{Change: GuestChange{Rel: "b.bin", Status: GuestChangeModified, Binary: true, Diff: "diff --git a/b.bin b/b.bin\nBinary files differ\n"}, Verdict: WorkspaceApplyConflict, Conflict: WorkspaceConflictHostModified},
		}
		path, err := m.SaveUnappliedPatch(context.Background(), "baseline", files)
		if err != nil {
			t.Fatal(err)
		}
		if !hasCall(client, "execcapture") || !strings.Contains(strings.Join(client.calls, "\n"), "--binary") {
			t.Fatalf("binary conflicts must be re-read with --binary: %v", client.calls)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "GIT binary patch") {
			t.Fatalf("patch content = %q", data)
		}
	})
		t.Run("binary read failure is an error, not a useless patch", func(t *testing.T) {
			client := &fakeMSBClient{exists: true}
			m := newTestMicrosandbox(t, client)
			client.execCaptureResults = []fakeMSBExecCaptureResult{{code: 1, stderr: "the guest diff exploded"}}
			files := []WorkspaceApplyFile{
				{Change: GuestChange{Rel: "b.bin", Status: GuestChangeModified, Binary: true, Diff: "diff --git a/b.bin b/b.bin\nBinary files differ\n"}, Verdict: WorkspaceApplyConflict, Conflict: WorkspaceConflictHostModified},
			}
			path, err := m.SaveUnappliedPatch(context.Background(), "baseline", files)
			if err == nil {
				t.Fatal("a failed binary read must be an error, not a saved text-only patch")
			}
			if path != "" {
				t.Fatalf("no patch path may be claimed: %q", path)
			}
			if !strings.Contains(err.Error(), "no patch was written") {
				t.Fatalf("the error must say no patch was written: %v", err)
			}
		})
		t.Run("empty binary diff is an error", func(t *testing.T) {
			client := &fakeMSBClient{exists: true}
			m := newTestMicrosandbox(t, client)
			client.execCaptureResults = []fakeMSBExecCaptureResult{{stdout: "   "}}
			files := []WorkspaceApplyFile{
				{Change: GuestChange{Rel: "b.bin", Status: GuestChangeModified, Binary: true}, Verdict: WorkspaceApplyConflict, Conflict: WorkspaceConflictHostModified},
			}
			if _, err := m.SaveUnappliedPatch(context.Background(), "baseline", files); err == nil {
				t.Fatal("an empty binary diff must be an error, not a claimed patch")
			}
		})
		t.Run("binary without a baseline is an error", func(t *testing.T) {
			client := &fakeMSBClient{exists: true}
			m := newTestMicrosandbox(t, client)
			files := []WorkspaceApplyFile{
				{Change: GuestChange{Rel: "b.bin", Status: GuestChangeModified, Binary: true}, Verdict: WorkspaceApplyConflict, Conflict: WorkspaceConflictHostModified},
			}
			if _, err := m.SaveUnappliedPatch(context.Background(), "", files); err == nil {
				t.Fatal("a binary conflict without a baseline must be an error")
			}
		})
}

// gitLogCount returns the number of commits reachable from HEAD.
func gitLogCount(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		if c < '0' || c > '9' {
			t.Fatalf("unexpected rev-list output: %q", out)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// gitPorcelain returns git status --porcelain for dir.
func gitPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
