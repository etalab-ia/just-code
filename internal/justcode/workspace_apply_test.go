package justcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// sealedFakeMSB returns a fake client whose instance passes
// requireSealedWorkspace: it exists with guest-owned /workspace storage.
func sealedFakeMSB() *fakeMSBClient {
	return &fakeMSBClient{exists: true, owned: boolPtr(true)}
}

// applyTestRuntime binds a sealed fake client to a runtime with a test
// state directory, so record stores land in t.TempDir().
func applyTestRuntime(t *testing.T, client *fakeMSBClient) (*MicrosandboxRuntime, string) {
	t.Helper()
	stateDir := t.TempDir()
	m := NewMicrosandboxRuntimeForInstance(Config{}, "test-instance")
	m.Client = client
	m.StateDir = stateDir
	return m, stateDir
}

func TestParseNumstatZ(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []guestNumstat
	}{
		{
			name: "one text change",
			out:  "12\t3\tsrc/app.js",
			want: []guestNumstat{{rel: "src/app.js", additions: 12, deletions: 3}},
		},
		{
			name: "two changes separated by NUL",
			out:  "84\t0\tsrc/snake.js\x002\t0\tREADME.md",
			want: []guestNumstat{
				{rel: "src/snake.js", additions: 84},
				{rel: "README.md", additions: 2},
			},
		},
		{
			name: "binary file reports dash counts",
			out:  "-\t-\tlogo.png",
			want: []guestNumstat{{rel: "logo.png", binary: true}},
		},
		{
			name: "path containing a tab splits on the first two only",
			out:  "1\t1\tweird\tname.txt",
			want: []guestNumstat{{rel: "weird\tname.txt", additions: 1, deletions: 1}},
		},
		{name: "empty output", out: "", want: nil},
		{name: "trailing NUL", out: "1\t0\ta.txt\x00", want: []guestNumstat{{rel: "a.txt", additions: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseNumstatZ(tc.out)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseNumstatZ(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
		})
	}
}

// applyNumstatTwo is the `git diff --numstat -z` payload for two text
// changes: src/app.js modified (12 added, 3 deleted) and a new README.md
// (84 added), in that order.
const applyNumstatTwo = "12\t3\tsrc/app.js\x0084\t0\tREADME.md\x00"

// applyDiffTwoSections is the `git diff` payload matching applyNumstatTwo:
// one modified file then one new file.
const applyDiffTwoSections = "diff --git a/src/app.js b/src/app.js\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/src/app.js\n" +
	"+++ b/src/app.js\n" +
	"@@ -1,2 +1,3 @@\n" +
	" line\n" +
	"+new\n" +
	" tail\n" +
	"diff --git a/README.md b/README.md\n" +
	"new file mode 100644\n" +
	"index 0000000..3333333\n" +
	"--- /dev/null\n" +
	"+++ b/README.md\n" +
	"@@ -0,0 +1,2 @@\n" +
	"+one\n" +
	"+two\n"

// applyFirstSection is the src/app.js section alone, as splitDiffSections
// must cut it out of applyDiffTwoSections.
const applyFirstSection = "diff --git a/src/app.js b/src/app.js\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/src/app.js\n" +
	"+++ b/src/app.js\n" +
	"@@ -1,2 +1,3 @@\n" +
	" line\n" +
	"+new\n" +
	" tail\n"

// applySecondSection is the README.md section alone.
const applySecondSection = "diff --git a/README.md b/README.md\n" +
	"new file mode 100644\n" +
	"index 0000000..3333333\n" +
	"--- /dev/null\n" +
	"+++ b/README.md\n" +
	"@@ -0,0 +1,2 @@\n" +
	"+one\n" +
	"+two\n"

func TestSplitDiffSections(t *testing.T) {
	if got := splitDiffSections(""); got != nil {
		t.Fatalf("empty diff = %v, want nil", got)
	}
	if got := splitDiffSections("not a diff"); got != nil {
		t.Fatalf("diff without a section header = %v, want nil", got)
	}
	one := splitDiffSections(applyFirstSection)
	if len(one) != 1 || one[0] != applyFirstSection {
		t.Fatalf("single section = %q", one)
	}
	got := splitDiffSections(applyDiffTwoSections)
	if len(got) != 2 {
		t.Fatalf("two sections = %d, want 2", len(got))
	}
	if got[0] != applyFirstSection {
		t.Fatalf("first section = %q, want %q", got[0], applyFirstSection)
	}
	if got[1] != applySecondSection {
		t.Fatalf("second section = %q, want %q", got[1], applySecondSection)
	}
}

func TestGuestSectionStatus(t *testing.T) {
	cases := []struct {
		name    string
		section string
		want    byte
	}{
		{"modified file", applyFirstSection, 'M'},
		{"new file", applySecondSection, 'A'},
		{
			"deleted file",
			"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\nindex 111..000\n--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n",
			'D',
		},
		{
			"binary patch",
			"diff --git a/logo.png b/logo.png\nindex 111..222 100644\nGIT binary patch\nliteral 4\ndata 4\n",
			'M',
		},
		{
			"header only falls back to modified",
			"diff --git a/x.txt b/x.txt\n",
			'M',
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guestSectionStatus(tc.section); got != tc.want {
				t.Fatalf("guestSectionStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPatchContentSize(t *testing.T) {
	cases := []struct {
		name    string
		section string
		want    int64
	}{
		{"empty", "", 0},
		{"one added line", applyFirstSection, 4},
		{"two added lines", applySecondSection, 8},
		{
			"binary literal size",
			"diff --git a/b.bin b/b.bin\nindex 111..222 100644\nGIT binary patch\nliteral 4096\ndata 8\n",
			4096,
		},
		{
			"binary delta size",
			"diff --git a/b.bin b/b.bin\nindex 111..222 100644\nGIT binary patch\ndelta 2048\ndata 8\n",
			2048,
		},
		{"no hunk", "diff --git a/x.txt b/x.txt\nindex 111..222 100644\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := patchContentSize(tc.section); got != tc.want {
				t.Fatalf("patchContentSize = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGuestChangeDigest(t *testing.T) {
	base := GuestChange{Rel: "src/app.js", Patch: []byte(applyFirstSection)}
	same := GuestChange{Rel: "src/app.js", Patch: []byte(applyFirstSection)}
	if GuestChangeDigest(base) != GuestChangeDigest(same) {
		t.Fatal("digest of the same change differs between calls")
	}
	if GuestChangeDigest(base) == GuestChangeDigest(GuestChange{Rel: "other.js", Patch: base.Patch}) {
		t.Fatal("digest ignores the path")
	}
	if GuestChangeDigest(base) == GuestChangeDigest(GuestChange{Rel: base.Rel, Patch: []byte(applySecondSection)}) {
		t.Fatal("digest ignores the patch bytes")
	}
}

func TestApplyRecordStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "applied-guest-changes.json")
	store := ApplyRecordStore{Path: path}

	if got, err := store.Load(); err != nil || len(got) != 0 {
		t.Fatalf("Load on a missing record = (%v, %v), want empty and no error", got, err)
	}
	if err := store.Record("src/app.js", "digest-one"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := store.Record("README.md", "digest-two"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{"src/app.js": "digest-one", "README.md": "digest-two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %v, want %v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %v, want 0600", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "applied-changes record") {
		t.Fatalf("Load on corrupt JSON = %v, want an applied-changes record error", err)
	}
}

func TestGuestPushedBranch(t *testing.T) {
	t.Run("never created", func(t *testing.T) {
		client := sealedFakeMSB()
		client.execCaptureResults = []fakeMSBExecCaptureResult{{code: 1}}
		m, _ := applyTestRuntime(t, client)
		branch, prURL, pushed, err := m.GuestPushedBranch(context.Background())
		if err != nil || pushed || branch != "" || prURL != "" {
			t.Fatalf("GuestPushedBranch = (%q, %q, %v, %v), want empty and not pushed", branch, prURL, pushed, err)
		}
	})
	t.Run("created but never pushed", func(t *testing.T) {
		client := sealedFakeMSB()
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "letta/fix\n"}, // git config --get
			{code: 1},               // rev-parse: no remote ref
		}
		m, _ := applyTestRuntime(t, client)
		branch, prURL, pushed, err := m.GuestPushedBranch(context.Background())
		if err != nil || pushed || branch != "letta/fix" || prURL != "" {
			t.Fatalf("GuestPushedBranch = (%q, %q, %v, %v), want the branch, not pushed", branch, prURL, pushed, err)
		}
	})
	t.Run("pushed with a draft pull request", func(t *testing.T) {
		client := sealedFakeMSB()
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "letta/fix\n"},
			{stdout: "refs/remotes/origin/letta/fix\n"},
			{stdout: `[{"url":"https://github.com/owner/repo/pull/9"}]`},
		}
		m, _ := applyTestRuntime(t, client)
		branch, prURL, pushed, err := m.GuestPushedBranch(context.Background())
		if err != nil || !pushed || branch != "letta/fix" || prURL != "https://github.com/owner/repo/pull/9" {
			t.Fatalf("GuestPushedBranch = (%q, %q, %v, %v), want pushed with the pull request", branch, prURL, pushed, err)
		}
	})
	t.Run("pushed without a usable gh", func(t *testing.T) {
		client := sealedFakeMSB()
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "letta/fix\n"},
			{stdout: "refs/remotes/origin/letta/fix\n"},
			{code: 1}, // gh pr list fails; the branch still counts as pushed
		}
		m, _ := applyTestRuntime(t, client)
		branch, prURL, pushed, err := m.GuestPushedBranch(context.Background())
		if err != nil || !pushed || branch != "letta/fix" || prURL != "" {
			t.Fatalf("GuestPushedBranch = (%q, %q, %v, %v), want pushed without a pull request", branch, prURL, pushed, err)
		}
	})
}

func TestCollectGuestChangesPairsNumstatWithDiff(t *testing.T) {
	client := sealedFakeMSB()
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"},           // provisioned probe
		{stdout: applyNumstatTwo}, // numstat
		{stdout: applyDiffTwoSections},
	}
	m, _ := applyTestRuntime(t, client)
	changes, err := m.CollectGuestChanges(context.Background())
	if err != nil {
		t.Fatalf("CollectGuestChanges: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(changes), changes)
	}
	appJS, readme := changes[0], changes[1]
	if appJS.Rel != "src/app.js" || appJS.Status != 'M' || appJS.Additions != 12 || appJS.Deletions != 3 || appJS.Binary {
		t.Fatalf("src/app.js change = %+v", appJS)
	}
	if readme.Rel != "README.md" || readme.Status != 'A' || readme.Additions != 84 || readme.Deletions != 0 {
		t.Fatalf("README.md change = %+v", readme)
	}
	if string(appJS.Patch) != applyFirstSection {
		t.Fatalf("src/app.js patch = %q, want %q", appJS.Patch, applyFirstSection)
	}
	if string(readme.Patch) != applySecondSection {
		t.Fatalf("README.md patch = %q, want %q", readme.Patch, applySecondSection)
	}
	if n := countCalls(client, "execcapture"); n != 3 {
		t.Fatalf("guest ran %d captured commands, want the probe, numstat and diff", n)
	}
}

func TestCollectGuestChangesUnprovisionedGuestHasNoWork(t *testing.T) {
	client := sealedFakeMSB()
	client.execCaptureResults = []fakeMSBExecCaptureResult{{stdout: "no"}}
	m, _ := applyTestRuntime(t, client)
	changes, err := m.CollectGuestChanges(context.Background())
	if err != nil {
		t.Fatalf("CollectGuestChanges: %v", err)
	}
	if changes != nil {
		t.Fatalf("got %+v, want nil: an unprovisioned guest holds no work", changes)
	}
	if n := countCalls(client, "execcapture"); n != 1 {
		t.Fatalf("guest ran %d captured commands, want exactly the provisioned probe", n)
	}
}

func TestCollectGuestChangesFailsClosedOnCountMismatch(t *testing.T) {
	client := sealedFakeMSB()
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"},
		{stdout: applyNumstatTwo},   // two files listed
		{stdout: applyFirstSection}, // but a one-file patch
	}
	m, _ := applyTestRuntime(t, client)
	_, err := m.CollectGuestChanges(context.Background())
	if err == nil || !strings.Contains(err.Error(), "changed while it was being read") {
		t.Fatalf("error = %v, want the fail-closed mismatch message", err)
	}
}

func TestCollectGuestChangesReportsGuestFailures(t *testing.T) {
	t.Run("numstat fails", func(t *testing.T) {
		client := sealedFakeMSB()
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"},
			{code: 128, stderr: "fatal: not a git repository"},
		}
		m, _ := applyTestRuntime(t, client)
		_, err := m.CollectGuestChanges(context.Background())
		if err == nil || !strings.Contains(err.Error(), "could not list its changes") {
			t.Fatalf("error = %v, want the numstat failure", err)
		}
	})
	t.Run("diff fails", func(t *testing.T) {
		client := sealedFakeMSB()
		client.execCaptureResults = []fakeMSBExecCaptureResult{
			{stdout: "yes"},
			{stdout: ""}, // numstat: clean tree
			{code: 128, stderr: "fatal: bad object HEAD"},
		}
		m, _ := applyTestRuntime(t, client)
		_, err := m.CollectGuestChanges(context.Background())
		if err == nil || !strings.Contains(err.Error(), "could not produce a diff") {
			t.Fatalf("error = %v, want the diff failure", err)
		}
	})
}

func TestCollectGuestChangesRefusesUnsealedWorkspace(t *testing.T) {
	cases := []struct {
		name   string
		client *fakeMSBClient
		want   string
	}{
		{
			"missing instance",
			&fakeMSBClient{exists: false},
			"does not exist yet",
		},
		{
			"bind-mounted workspace",
			&fakeMSBClient{exists: true, mount: "/home/user/proj"},
			"mounted from the host",
		},
		{
			"unowned storage",
			&fakeMSBClient{exists: true, owned: boolPtr(false)},
			"guest-owned storage",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := applyTestRuntime(t, tc.client)
			_, err := m.CollectGuestChanges(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if n := countCalls(tc.client, "execcapture"); n != 0 {
				t.Fatalf("an unsealed guest ran %d captured commands, want none", n)
			}
		})
	}
}

func TestApplyPolicyCheck(t *testing.T) {
	man := TransferManifest{
		Entries: []TransferEntry{
			{Rel: "src/app.js", Included: true},
			{Rel: "big.bin", Included: false, Reason: "above the per-file transfer cap", Exclusion: TransferExclusionSizeCap},
			{Rel: "link", Included: false, Exclusion: TransferExclusionSymlink},
		},
	}
	optIn := map[string]bool{".env.local": true, ".git/config": true}
	p := newApplyPolicy(t.TempDir(), man, optIn)
	ctx := context.Background()
	cases := []struct {
		name string
		rel  string
		want string
	}{
		{"git metadata is structural", ".git/config", "Git metadata never crosses the isolation boundary"},
		{"git directory itself", ".git", "Git metadata never crosses the isolation boundary"},
		{"opt-in re-includes a dotenv file", ".env.local", ""},
		{"dotenv is default-deny", ".env", "dotenv file (default-deny)"},
		{"manifest entry that crossed", "src/app.js", ""},
		{"manifest entry refused with a reason", "big.bin", "above the per-file transfer cap"},
		{"manifest entry refused by class", "link", "symlink"},
		{"unknown path is allowed", "src/new-file.ts", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.check(ctx, GuestChange{Rel: tc.rel}); got != tc.want {
				t.Fatalf("check(%q) = %q, want %q", tc.rel, got, tc.want)
			}
		})
	}
}

func TestApplyPolicyCheckRefusesGitIgnoredAgentPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitInitForTransfer(t, root)
	writeFile(t, root, ".gitignore", "node_modules/\n")
	runGitTest(t, root, "add", ".gitignore")
	runGitTest(t, root, "commit", "-q", "-m", "gitignore")
	p := newApplyPolicy(root, TransferManifest{GitManaged: true}, nil)
	ctx := context.Background()

	if got := p.check(ctx, GuestChange{Rel: "node_modules/derived.js"}); got != "git-ignored (local or derived state)" {
		t.Fatalf("ignored path: check = %q, want the git-ignored refusal", got)
	}
	if got := p.check(ctx, GuestChange{Rel: "src/new-file.ts"}); got != "" {
		t.Fatalf("tracked path: check = %q, want allowed", got)
	}
}

func TestApplyPolicyCheckGitIgnoredProbeIsBestEffort(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A git-managed manifest against a root that is not a repository: the
	// probe cannot run, and the refusal must not turn into a blanket block.
	p := newApplyPolicy(t.TempDir(), TransferManifest{GitManaged: true}, nil)
	if got := p.check(context.Background(), GuestChange{Rel: "node_modules/derived.js"}); got != "" {
		t.Fatalf("check = %q, want allowed: the probe is a refinement, not a gate", got)
	}
}

func TestApplyPolicyCheckRefusesAboveTheCap(t *testing.T) {
	p := newApplyPolicy(t.TempDir(), TransferManifest{}, nil)
	ctx := context.Background()
	header := "diff --git a/big.bin b/big.bin\nindex 111..222 100644\nGIT binary patch\nliteral "
	over := header + strconv.FormatInt(DefaultTransferMaxFileBytes+1, 10) + "\ndata 4\n"
	if got := p.check(ctx, GuestChange{Rel: "big.bin", Patch: []byte(over)}); got != "above the per-file transfer cap" {
		t.Fatalf("check over the cap = %q, want the cap refusal", got)
	}
	at := header + strconv.FormatInt(DefaultTransferMaxFileBytes, 10) + "\ndata 4\n"
	if got := p.check(ctx, GuestChange{Rel: "big.bin", Patch: []byte(at)}); got != "" {
		t.Fatalf("check exactly at the cap = %q, want allowed", got)
	}
}

func TestApplyGuestPatchesAppliesAndConflicts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitInitForTransfer(t, root)
	writeFile(t, root, "a.txt", "old\n")
	writeFile(t, root, "keep.txt", "stay\n")
	runGitTest(t, root, "add", "-A")
	runGitTest(t, root, "commit", "-q", "-m", "base")

	ctx := context.Background()
	if applied, conflicted, err := ApplyGuestPatches(ctx, root, nil); err != nil || applied != nil || conflicted != nil {
		t.Fatalf("empty set = (%v, %v, %v), want nothing and no error", applied, conflicted, err)
	}

	good := GuestChange{Rel: "a.txt", Patch: []byte("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n")}
	bad := GuestChange{Rel: "keep.txt", Patch: []byte("diff --git a/keep.txt b/keep.txt\n--- a/keep.txt\n+++ b/keep.txt\n@@ -1 +1 @@\n-not-the-content\n+new\n")}
	applied, conflicted, err := ApplyGuestPatches(ctx, root, []GuestChange{good, bad})
	if err != nil {
		t.Fatalf("ApplyGuestPatches: %v", err)
	}
	if len(applied) != 1 || applied[0].Rel != "a.txt" {
		t.Fatalf("applied = %+v, want exactly a.txt", applied)
	}
	if len(conflicted) != 1 || conflicted[0].Rel != "keep.txt" {
		t.Fatalf("conflicted = %+v, want exactly keep.txt", conflicted)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "new\n" {
		t.Fatalf("a.txt = %q, want the patch applied", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "keep.txt")); string(got) != "stay\n" {
		t.Fatalf("keep.txt = %q, want the host content untouched", got)
	}
}

func TestApplyGuestPatchesConflictDoesNotOverwriteHostWork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitInitForTransfer(t, root)
	writeFile(t, root, "a.txt", "old\n")
	runGitTest(t, root, "add", "-A")
	runGitTest(t, root, "commit", "-q", "-m", "base")
	// The host file moves on after the copy, without committing.
	writeFile(t, root, "a.txt", "host\n")

	guest := GuestChange{Rel: "a.txt", Patch: []byte("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+guest\n")}
	applied, conflicted, err := ApplyGuestPatches(context.Background(), root, []GuestChange{guest})
	if err != nil {
		t.Fatalf("ApplyGuestPatches: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %+v, want none", applied)
	}
	if len(conflicted) != 1 || conflicted[0].Rel != "a.txt" {
		t.Fatalf("conflicted = %+v, want exactly a.txt", conflicted)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "host\n" {
		t.Fatalf("a.txt = %q, want the host work preserved", got)
	}
}

func TestPreserveUnappliedPatches(t *testing.T) {
	stateDir := t.TempDir()
	if path, err := PreserveUnappliedPatches(stateDir, "inst", nil); err != nil || path != "" {
		t.Fatalf("empty set = (%q, %v), want no file and no error", path, err)
	}
	first := GuestChange{Rel: "a.txt", Patch: []byte("diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-old\n+new")}
	second := GuestChange{Rel: "b.txt", Patch: []byte("diff --git a/b.txt b/b.txt\n@@ -1 +1 @@\n-x\n+y\n")}
	path, err := PreserveUnappliedPatches(stateDir, "inst", []GuestChange{first, second})
	if err != nil {
		t.Fatalf("PreserveUnappliedPatches: %v", err)
	}
	if path != filepath.Join(InstanceStateDir(stateDir, "inst"), "unapplied-guest-changes.patch") {
		t.Fatalf("path = %q, want the instance state file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The first patch lacks its trailing newline and gains one; the second
	// already ends with one and must not gain a second.
	want := string(first.Patch) + "\n" + string(second.Patch)
	if string(data) != want {
		t.Fatalf("patch file = %q, want %q", data, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("patch file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSortGuestChanges(t *testing.T) {
	changes := []GuestChange{{Rel: "b.txt"}, {Rel: "a.txt"}, {Rel: "c.txt"}}
	SortGuestChanges(changes)
	if changes[0].Rel != "a.txt" || changes[1].Rel != "b.txt" || changes[2].Rel != "c.txt" {
		t.Fatalf("sorted = %v", changes)
	}
}

func TestUnrecoveredGuestChangesSkipsRecorded(t *testing.T) {
	client := sealedFakeMSB()
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"},
		{stdout: applyNumstatTwo},
		{stdout: applyDiffTwoSections},
	}
	m, stateDir := applyTestRuntime(t, client)
	recorded := GuestChange{Rel: "src/app.js", Patch: []byte(applyFirstSection)}
	store := ApplyRecordStore{Path: ApplyRecordPath(stateDir, m.InstanceName())}
	if err := store.Record(recorded.Rel, GuestChangeDigest(recorded)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	out, err := UnrecoveredGuestChanges(context.Background(), m)
	if err != nil {
		t.Fatalf("UnrecoveredGuestChanges: %v", err)
	}
	if len(out) != 1 || out[0].Rel != "README.md" {
		t.Fatalf("unrecovered = %+v, want exactly README.md", out)
	}
}

func TestPrepareApplyReviewMarksRecoveredAndRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/app.js", "old\n")
	writeFile(t, root, ".env", "")
	writeFile(t, root, "README.md", "base\n")
	client := sealedFakeMSB()
	envSection := "diff --git a/.env b/.env\nindex 111..222 100644\n--- a/.env\n+++ b/.env\n@@ -1 +0,0 @@\n-old\n"
	readmeSection := "diff --git a/README.md b/README.md\nindex 111..222 100644\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-old\n+new\n"
	client.execCaptureResults = []fakeMSBExecCaptureResult{
		{stdout: "yes"},
		{stdout: "0\t1\t.env\x001\t1\tREADME.md\x001\t1\tsrc/app.js\x00"},
		{stdout: envSection + readmeSection + applyFirstSection},
	}
	m, stateDir := applyTestRuntime(t, client)
	recorded := GuestChange{Rel: "src/app.js", Patch: []byte(applyFirstSection)}
	store := ApplyRecordStore{Path: ApplyRecordPath(stateDir, m.InstanceName())}
	if err := store.Record(recorded.Rel, GuestChangeDigest(recorded)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	review, err := PrepareApplyReview(context.Background(), m, root, nil)
	if err != nil {
		t.Fatalf("PrepareApplyReview: %v", err)
	}
	if len(review.Changes) != 3 {
		t.Fatalf("changes = %+v, want the three guest files", review.Changes)
	}
	if !review.Recovered["src/app.js"] {
		t.Fatalf("recovered = %v, want src/app.js", review.Recovered)
	}
	if got := review.Refused[".env"]; got != "dotenv file (default-deny)" {
		t.Fatalf(".env refusal = %q, want the dotenv default-deny", got)
	}
	applicable := review.Applicable()
	if len(applicable) != 1 || applicable[0].Rel != "README.md" {
		t.Fatalf("applicable = %+v, want exactly README.md", applicable)
	}
}
