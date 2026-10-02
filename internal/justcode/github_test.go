package justcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseGitHubRemote(t *testing.T) {
	for _, tt := range []struct {
		name     string
		input    string
		wantURL  string
		wantRepo string
		wantErr  bool
	}{
		{name: "https", input: "https://github.com/owner/repo", wantURL: "https://github.com/owner/repo.git", wantRepo: "owner/repo"},
		{name: "https trailing slash", input: "https://github.com/owner/repo.git/", wantURL: "https://github.com/owner/repo.git", wantRepo: "owner/repo"},
		{name: "https strips embedded credentials", input: "https://x-access-token:secret@github.com/owner/repo.git", wantURL: "https://github.com/owner/repo.git", wantRepo: "owner/repo"},
		{name: "ssh", input: "ssh://git@github.com/owner/repo.git", wantURL: "https://github.com/owner/repo.git", wantRepo: "owner/repo"},
		{name: "scp", input: "git@github.com:owner/repo.git", wantURL: "https://github.com/owner/repo.git", wantRepo: "owner/repo"},
		{name: "http is rejected", input: "http://github.com/owner/repo.git", wantErr: true},
		{name: "enterprise host is rejected", input: "https://github.example.net/owner/repo.git", wantErr: true},
		{name: "nested path is rejected", input: "https://github.com/owner/group/repo.git", wantErr: true},
		{name: "query is rejected", input: "https://github.com/owner/repo.git?token=secret", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGitHubRemote(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseGitHubRemote(%q) error = %v, wantErr %t", tt.input, err, tt.wantErr)
			}
			if err == nil && (got.URL != tt.wantURL || got.Repo != tt.wantRepo) {
				t.Fatalf("ParseGitHubRemote(%q) = %#v, want URL %q and repo %q", tt.input, got, tt.wantURL, tt.wantRepo)
			}
			if err == nil && got.URL == tt.input && tt.name == "https strips embedded credentials" {
				t.Fatal("remote credentials must not be returned")
			}
		})
	}
}

func TestGitHubOriginForProjectReadsAndSanitizesOrigin(t *testing.T) {
	root := t.TempDir()
	runGitTest(t, root, "init", "-q")
	runGitTest(t, root, "remote", "add", "origin", "https://x-access-token:secret@github.com/owner/repo.git")

	got, err := GitHubOriginForProject(context.Background(), root)
	if err != nil {
		t.Fatalf("GitHubOriginForProject: %v", err)
	}
	if got.URL != "https://github.com/owner/repo.git" || got.Repo != "owner/repo" {
		t.Fatalf("origin = %#v", got)
	}
}

func TestGitHubOriginForProjectRejectsAmbiguousOrigins(t *testing.T) {
	root := t.TempDir()
	runGitTest(t, root, "init", "-q")
	runGitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	runGitTest(t, root, "remote", "set-url", "--add", "origin", "https://github.com/owner/other.git")
	if _, err := GitHubOriginForProject(context.Background(), root); err == nil {
		t.Fatal("multiple origin URLs must be rejected")
	}
}

func TestGitHubCLIInstallScriptIsVersionAndChecksumPinned(t *testing.T) {
	script := githubCLIInstallScript()
	for _, want := range []string{
		"version=" + githubCLIVersion,
		githubCLIAMD64SHA,
		githubCLIARM64SHA,
		"sha256sum -c -",
		"https://github.com/cli/cli/releases/download/v${version}/${asset}",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("pinned installer is missing %q", want)
		}
	}
	if strings.Contains(script, "sudo ") || strings.Contains(script, "GITHUB_TOKEN") {
		t.Fatal("GitHub CLI installer must not use host privilege escalation or credentials")
	}
}

func TestGitHubBranchNameIsUniqueAndScoped(t *testing.T) {
	first, err := GitHubBranchName("project name/with spaces")
	if err != nil {
		t.Fatal(err)
	}
	second, err := GitHubBranchName("project name/with spaces")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "just-code/") || strings.Contains(first, " ") || strings.Contains(first, "..") {
		t.Fatalf("branch names are not unique, scoped and safe: %q, %q", first, second)
	}
}

func TestGitHubSnapshotBranchScriptKeepsRemoteBaseAndOverlaysFilteredSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("guest branch bootstrap uses POSIX shell commands")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "origin.git")
	cmd := exec.Command("git", "init", "-q", "--bare", remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create remote: %v\n%s", err, out)
	}
	runGitTest(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	base := filepath.Join(root, "base")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, base, "init", "-q")
	runGitTest(t, base, "symbolic-ref", "HEAD", "refs/heads/main")
	runGitTest(t, base, "config", "user.name", "Base")
	runGitTest(t, base, "config", "user.email", "base@example.test")
	if err := os.WriteFile(filepath.Join(base, "shared.txt"), []byte("remote version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "remote-only.txt"), []byte("preserve me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, base, "add", "-f", "-A")
	runGitTest(t, base, "commit", "-qm", "remote base")
	runGitTest(t, base, "remote", "add", "origin", remote)
	runGitTest(t, base, "push", "-u", "origin", "main")
	baseCommit := strings.TrimSpace(gitTestOutput(t, base, "rev-parse", "HEAD"))

	snapshot := filepath.Join(root, "snapshot")
	if err := os.Mkdir(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, snapshot, "init", "-q")
	runGitTest(t, snapshot, "config", "user.name", "Guest")
	runGitTest(t, snapshot, "config", "user.email", "guest@example.test")
	if err := os.WriteFile(filepath.Join(snapshot, "shared.txt"), []byte("filtered host version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "host-only.txt"), []byte("filtered new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, snapshot, "add", "-A")
	runGitTest(t, snapshot, "commit", "-qm", "P22 filtered snapshot")
	runGitTest(t, snapshot, "commit", "--allow-empty", "-qm", "Prior guest history")

	script := githubSnapshotBranchScript(snapshot, GitHubRemote{URL: remote, Repo: "owner/repo"}, "main", "just-code/test", "Albert Code Agent", "agent@example.test")
	ignored := filepath.Join(snapshot, "ignored", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(ignored), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignored, []byte("ignored guest work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runShellTest(script, snapshot); err == nil || !strings.Contains(string(out), "ignored files") {
		t.Fatalf("ignored guest files must block setup: err=%v out=%s", err, out)
	}
	if got, err := os.ReadFile(ignored); err != nil || string(got) != "ignored guest work\n" {
		t.Fatalf("ignored guest work was changed: content=%q err=%v", got, err)
	}
	if err := os.RemoveAll(filepath.Dir(ignored)); err != nil {
		t.Fatal(err)
	}
	dirty := filepath.Join(snapshot, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("uncommitted guest work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runShellTest(script, snapshot); err == nil || !strings.Contains(string(out), "worktree is dirty") {
		t.Fatalf("dirty guest worktree must block setup: err=%v out=%s", err, out)
	}
	if got, err := os.ReadFile(dirty); err != nil || string(got) != "uncommitted guest work\n" {
		t.Fatalf("uncommitted guest work was changed: content=%q err=%v", got, err)
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "HEAD"))
	failingScript := githubSnapshotBranchScript(snapshot, GitHubRemote{URL: remote}, "main", "just-code/test", "", "")
	if out, err := runShellTest(failingScript, snapshot); err == nil {
		t.Fatalf("empty commit identity must fail before publishing the branch: %s", out)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "HEAD")); got != before {
		t.Fatal("failed tree preparation changed guest HEAD")
	}
	if got := gitTestOutput(t, snapshot, "status", "--porcelain"); got != "" {
		t.Fatalf("failed tree preparation changed the live index: %s", got)
	}
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "mv"), []byte("#!/bin/sh\nexit 70\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	interrupted := githubSnapshotBranchScript(snapshot, GitHubRemote{URL: remote}, "main", "just-code/interrupted", "Guest", "guest@example.test")
	cmd = exec.Command("/bin/sh", "-c", interrupted)
	cmd.Dir = snapshot
	cmd.Env = append(os.Environ(), "PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "empty-gitconfig"))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("failed config publication must be surfaced: %s", out)
	}
	if got := gitTestOutput(t, snapshot, "remote"); got != "" {
		t.Fatalf("failed publication exposed a half-configured origin: %s", got)
	}
	if got := gitTestOutput(t, snapshot, "status", "--porcelain"); got != "" {
		t.Fatalf("failed publication left an unresumable dirty workspace: %s", got)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "just-code/interrupted-snapshot-backup")); got != before {
		t.Fatal("interrupted publication lost original guest history")
	}
	preservedBeforeSuccess := strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "HEAD"))
	cmd = exec.Command("/bin/sh", "-c", script)
	cmd.Dir = snapshot
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "empty-gitconfig"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed guest branch: %v\n%s", err, out)
	}

	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "branch", "--show-current")); got != "just-code/test" {
		t.Fatalf("current branch = %q", got)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "HEAD^")); got != baseCommit {
		t.Fatalf("branch parent = %s, want remote base %s", got, baseCommit)
	}
	for path, want := range map[string]string{
		"shared.txt":      "filtered host version\n",
		"remote-only.txt": "preserve me\n",
		"host-only.txt":   "filtered new file\n",
	} {
		if got := gitTestOutput(t, snapshot, "show", "HEAD:"+path); got != want {
			t.Errorf("HEAD:%s = %q, want %q", path, got, want)
		}
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "remote", "get-url", "origin")); got != remote {
		t.Fatalf("origin = %q, want %q", got, remote)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "status", "--porcelain")); got != "" {
		t.Fatalf("worktree status after seeding = %q, want clean", got)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "just-code/test-snapshot-backup")); got != preservedBeforeSuccess {
		t.Fatalf("guest history backup = %s, want %s", got, preservedBeforeSuccess)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "config", "--local", "just-code.github-workflow-origin")); got != remote {
		t.Fatalf("published origin marker = %q, want %q", got, remote)
	}
	if got := strings.TrimSpace(gitTestOutput(t, snapshot, "config", "--local", "remote.origin.fetch")); got != "+refs/heads/*:refs/remotes/origin/*" {
		t.Fatalf("published origin fetch refspec = %q", got)
	}
	runGitTest(t, snapshot, "push", "-u", "origin", "HEAD")
	if got, want := strings.TrimSpace(gitTestOutput(t, remote, "rev-parse", "refs/heads/just-code/test")), strings.TrimSpace(gitTestOutput(t, snapshot, "rev-parse", "HEAD")); got != want {
		t.Fatalf("remote branch tip = %s, want pushed guest commit %s", got, want)
	}
}

func TestConfigureGitHubWorkspaceRequiresResolvedApproval(t *testing.T) {
	client := &fakeMSBClient{}
	m := NewMicrosandboxRuntimeForInstance(Config{
		GitHubRemote: GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"},
	}, "opencode-jc-test-12345")
	m.Client = client
	if err := m.configureGitHubWorkspace(context.Background(), nil); err != nil {
		t.Fatalf("disabled GitHub workflow: %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("disabled GitHub workflow touched the guest: %v", client.calls)
	}
}

func TestGitHubOriginMismatchRefusesBeforeRuntimeChanges(t *testing.T) {
	for _, action := range []string{"start", "restart", "reconcile"} {
		t.Run(action, func(t *testing.T) {
			isolateHostState(t)
			client := &fakeMSBClient{exists: true, status: "running"}
			m := newTestMicrosandbox(t, client)
			m.StateDir = DefaultStateDir()
			m.credentialRead = stubReader("stored")
			m.cfg.GitHubRemote = GitHubRemote{URL: "https://github.com/owner/new.git", Repo: "owner/new"}
			if err := ApproveBinding(DefaultFS, BindingApprovalsPath(m.StateDir, m.InstanceName()), CredentialGithub); err != nil {
				t.Fatal(err)
			}
			if err := WriteInstanceState(DefaultFS, instanceStatePath(m.StateDir, m.InstanceName()), InstanceState{
				SchemaVersion: 1, Instance: m.InstanceName(), GitHubOrigin: "https://github.com/owner/old.git",
			}); err != nil {
				t.Fatal(err)
			}
			var err error
			switch action {
			case "start":
				err = m.Start(context.Background())
			case "restart":
				err = m.Restart(context.Background())
			case "reconcile":
				err = m.Reconcile(context.Background())
			}
			if err == nil || !strings.Contains(err.Error(), "origin differs") {
				t.Fatalf("%s origin mismatch = %v", action, err)
			}
			if len(client.calls) != 0 {
				t.Fatalf("%s touched the runtime before refusing: %v", action, client.calls)
			}
		})
	}
}

func TestConfigureGitHubWorkspaceChecksAccessBeforeSeedingBranch(t *testing.T) {
	remote := GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}
	client := &fakeMSBClient{execCaptureResults: []fakeMSBExecCaptureResult{
		{code: 1}, // no just-code origin marker
		{code: 1}, // no existing origin remote
		{stdout: ""},
		{stdout: "Logged in as test-user"},
		{stdout: "Git credential helper configured"},
		{stdout: "main\n"},
		{stdout: ""},
		{stdout: "true\n"},
	}}
	m := NewMicrosandboxRuntimeForInstance(Config{GitHubRemote: remote}, "opencode-jc-test-12345")
	m.Client = client
	bindings := []resolvedBinding{{msbSecretBinding: msbSecretBinding{Kind: CredentialGithub, GuestEnv: "GITHUB_TOKEN"}, value: "synthetic-test-token"}}
	if err := m.configureGitHubWorkspace(context.Background(), bindings); err != nil {
		t.Fatalf("configureGitHubWorkspace: %v", err)
	}
	if len(client.execCaptureResults) != 0 {
		t.Fatalf("not all expected guest probes ran: %d results remain", len(client.execCaptureResults))
	}
	calls := strings.Join(client.calls, "\n")
	for _, want := range []string{
		"gh auth status --hostname github.com",
		"gh auth setup-git --hostname github.com",
		"gh repo view 'owner/repo'",
		"repos/owner/repo",
		"git fetch --depth=1 'https://github.com/owner/repo.git' 'refs/heads/main'",
		"git config --file \"$temp_config\" just-code.github-workflow-origin",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("guest commands do not include %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "synthetic-test-token") {
		t.Fatal("credential value leaked into guest command arguments")
	}
}

func TestConfigureGitHubWorkspaceRejectsMissingWritePermission(t *testing.T) {
	client := &fakeMSBClient{execCaptureResults: []fakeMSBExecCaptureResult{
		{code: 1}, // no marker
		{code: 1}, // no origin remote
		{},        // no other remotes
		{},        // authenticated
		{},        // credential helper configured
		{stdout: "main\n"},
		{}, // valid branch name
		{stdout: "false\n"},
	}}
	m := NewMicrosandboxRuntimeForInstance(Config{
		GitHubRemote: GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"},
	}, "opencode-jc-test-12345")
	m.Client = client
	bindings := []resolvedBinding{{msbSecretBinding: msbSecretBinding{Kind: CredentialGithub, GuestEnv: "GITHUB_TOKEN"}}}
	err := m.configureGitHubWorkspace(context.Background(), bindings)
	if err == nil || !strings.Contains(err.Error(), "has no push role") {
		t.Fatalf("missing write permission: %v", err)
	}
	for _, call := range client.calls {
		if strings.Contains(call, "git fetch") || strings.Contains(call, "git reset --hard") {
			t.Fatalf("branch seeding ran despite missing write permission: %s", call)
		}
	}
}

func TestConfigureGitHubWorkspaceRejectsAuthenticationBeforeSeeding(t *testing.T) {
	client := &fakeMSBClient{execCaptureResults: []fakeMSBExecCaptureResult{
		{code: 1}, {code: 1}, {}, {code: 1, stderr: "synthetic-sensitive-cli-diagnostic"},
	}}
	m := NewMicrosandboxRuntimeForInstance(Config{
		GitHubRemote: GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"},
	}, "opencode-jc-test-12345")
	m.Client = client
	err := m.configureGitHubWorkspace(context.Background(), []resolvedBinding{{msbSecretBinding: msbSecretBinding{Kind: CredentialGithub}}})
	if err == nil || !strings.Contains(err.Error(), "credential was not accepted") {
		t.Fatalf("authentication failure = %v", err)
	}
	if strings.Contains(err.Error(), "synthetic-sensitive-cli-diagnostic") {
		t.Fatal("external CLI diagnostics reached the host error")
	}
	for _, call := range client.calls {
		if strings.Contains(call, "git fetch") || strings.Contains(call, "git reset --hard") {
			t.Fatalf("authentication failure seeded the branch: %s", call)
		}
	}
}

func TestConfigureGitHubWorkspaceDoesNotReseedAnInitializedGuest(t *testing.T) {
	remote := GitHubRemote{URL: "https://github.com/owner/repo.git", Repo: "owner/repo"}
	client := &fakeMSBClient{execCaptureResults: []fakeMSBExecCaptureResult{
		{stdout: remote.URL}, {stdout: remote.URL},
	}}
	m := NewMicrosandboxRuntimeForInstance(Config{GitHubRemote: remote}, "opencode-jc-test-12345")
	m.Client = client
	if err := m.configureGitHubWorkspace(context.Background(), []resolvedBinding{{msbSecretBinding: msbSecretBinding{Kind: CredentialGithub}}}); err != nil {
		t.Fatal(err)
	}
	for _, call := range client.calls {
		if strings.Contains(call, "git fetch") || strings.Contains(call, "git reset --hard") {
			t.Fatalf("initialized guest work was reseeded: %s", call)
		}
	}
}

func gitTestOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "empty-gitconfig"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func runShellTest(script, dir string) ([]byte, error) {
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(dir, "empty-gitconfig"))
	return cmd.CombinedOutput()
}

func runGitTest(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(root, "empty-gitconfig"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
