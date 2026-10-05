package justcode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GitHubRemote is the credential-free form of a GitHub.com origin.
type GitHubRemote struct {
	URL  string
	Repo string
}

const (
	githubCLIVersion  = "2.100.0"
	githubCLIAMD64SHA = "e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be"
	githubCLIARM64SHA = "ea4e7a581a32ccad6cc7923cb1576ac5859ba4b9a16ab22eb8f8a96e78e2e961"
)

// GitHubOriginForProject reads the host checkout's origin and returns a
// credential-free HTTPS URL. Host credentials embedded in remote URLs are
// discarded; guest authentication is provided separately by the proxy.
func GitHubOriginForProject(ctx context.Context, root string) (GitHubRemote, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "--all", "origin")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return GitHubRemote{}, fmt.Errorf("the project has no readable origin remote; configure a GitHub.com origin to enable the guest GitHub workflow")
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		return GitHubRemote{}, fmt.Errorf("the project must have exactly one origin URL to enable the guest GitHub workflow")
	}
	return ParseGitHubRemote(strings.TrimSpace(lines[0]))
}

// ParseGitHubRemote accepts GitHub.com HTTPS, SSH and SCP-style origin URLs,
// but never carries their userinfo into the returned remote.
func ParseGitHubRemote(raw string) (GitHubRemote, error) {
	var path string
	switch {
	case strings.HasPrefix(raw, "git@github.com:"):
		path = strings.TrimPrefix(raw, "git@github.com:")
	case strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || (u.Port() != "" && u.Port() != "22") || u.RawQuery != "" || u.Fragment != "" {
			return GitHubRemote{}, fmt.Errorf("the origin is not a supported GitHub.com remote")
		}
		path = u.EscapedPath()
	case strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || (u.Port() != "" && u.Port() != "443") || u.RawQuery != "" || u.Fragment != "" {
			return GitHubRemote{}, fmt.Errorf("the origin is not a supported GitHub.com remote")
		}
		path = u.EscapedPath()
	default:
		return GitHubRemote{}, fmt.Errorf("the origin must use HTTPS or SSH on github.com")
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return GitHubRemote{}, fmt.Errorf("the GitHub origin must identify exactly one owner and repository")
	}
	owner, err := url.PathUnescape(parts[0])
	if err != nil || !validGitHubOwner(owner) {
		return GitHubRemote{}, fmt.Errorf("the GitHub origin contains an invalid owner")
	}
	repo, err := url.PathUnescape(parts[1])
	if err != nil || !validGitHubRepo(repo) {
		return GitHubRemote{}, fmt.Errorf("the GitHub origin contains an invalid repository name")
	}
	slug := owner + "/" + repo
	return GitHubRemote{URL: "https://github.com/" + slug + ".git", Repo: slug}, nil
}

func validGitHubOwner(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func validGitHubRepo(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// githubCLIInstallScript installs the pinned, statically linked GitHub CLI in
// the Microsandbox guest only. The release checksums are embedded; no host gh
// executable or host credential helper is used by the guest workflow.
func githubCLIInstallScript() string {
	return fmt.Sprintf(`
just_code_install_gh() (
  set -eu
version=%s
case "$(uname -m)" in
  x86_64|amd64) arch=amd64; digest=%s ;;
  aarch64|arm64) arch=arm64; digest=%s ;;
  *) echo "GitHub CLI %s is unavailable for guest architecture $(uname -m)" >&2; exit 1 ;;
esac
binary=/usr/local/bin/gh
if [ -x "$binary" ] && "$binary" --version 2>/dev/null | grep -q "^gh version $version"; then
  exit 0
fi
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
archive="$tmp/gh.tar.gz"
asset="gh_${version}_linux_${arch}.tar.gz"
curl -fsSL "https://github.com/cli/cli/releases/download/v${version}/${asset}" -o "$archive"
printf '%%s  %%s\n' "$digest" "$archive" | sha256sum -c - >/dev/null
tar -xzf "$archive" -C "$tmp"
mkdir -p /usr/local/bin
cp "$tmp/gh_${version}_linux_${arch}/bin/gh" "$tmp/gh"
chmod 0755 "$tmp/gh"
mv "$tmp/gh" "$binary"
"$binary" --version | grep -q "^gh version $version"
)
just_code_install_gh
`, githubCLIVersion, githubCLIAMD64SHA, githubCLIARM64SHA, githubCLIVersion)
}

// GitHubBranchName creates a unique guest-local work branch so a recreated
// project instance never overwrites an existing remote branch.
func GitHubBranchName(instance string) (string, error) {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("create a unique guest branch name: %w", err)
	}
	name := safeFileName(instance)
	if len(name) > 60 {
		name = name[:60]
	}
	return fmt.Sprintf("just-code/%s-%s-%s", name, time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(nonce[:])), nil
}

// githubSnapshotBranchScript overlays the P22 filtered snapshot index on the
// remote default-branch tree. It refuses a dirty guest workspace; in
// particular, it never hard-resets over guest-local work while setting the
// branch base.
func githubSnapshotBranchScript(workspace string, remote GitHubRemote, baseBranch, branch, gitName, gitEmail string) string {
	baseRef := "refs/heads/" + baseBranch
	return "set -eu; cd " + shellQuote(workspace) +
		"; if [ -n \"$(git status --porcelain --untracked-files=all)\" ]; then echo 'guest worktree is dirty; export or review its changes before enabling the GitHub workflow' >&2; exit 1; fi" +
		"; if [ -n \"$(git ls-files --others --ignored --exclude-standard)\" ]; then echo 'guest workspace contains ignored files; review them before enabling the GitHub workflow' >&2; exit 1; fi" +
		"; tmp=$(mktemp -d /tmp/just-code-github-index.XXXXXX)" +
		"; temp_config=''; trap 'rm -rf \"$tmp\"; if [ -n \"$temp_config\" ]; then rm -f \"$temp_config\"; fi' EXIT HUP INT TERM" +
		"; git ls-files --stage -z > \"$tmp/snapshot\"" +
		"; git fetch --depth=1 " + shellQuote(remote.URL) + " " + shellQuote(baseRef) +
		"; base=$(git rev-parse FETCH_HEAD)" +
		"; GIT_INDEX_FILE=\"$tmp/index\" git read-tree \"$base\"" +
		"; GIT_INDEX_FILE=\"$tmp/index\" git update-index -z --index-info < \"$tmp/snapshot\"" +
		"; tree=$(GIT_INDEX_FILE=\"$tmp/index\" git write-tree)" +
		"; base_tree=$(git rev-parse \"$base^{tree}\")" +
		"; if [ \"$tree\" = \"$base_tree\" ]; then commit=\"$base\"; else commit=$(printf '%s\\n' 'just-code: filtered host snapshot' | git -c user.name=" + shellQuote(gitName) + " -c user.email=" + shellQuote(gitEmail) + " commit-tree \"$tree\" -p \"$base\"); fi" +
		"; config=$(git rev-parse --git-path config); temp_config=$(mktemp \"$config.just-code.XXXXXX\"); cp \"$config\" \"$temp_config\"" +
		"; git config --file \"$temp_config\" remote.origin.url " + shellQuote(remote.URL) +
		"; git config --file \"$temp_config\" remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'" +
		"; git config --file \"$temp_config\" push.autoSetupRemote true" +
		"; git config --file \"$temp_config\" just-code.github-workflow-origin " + shellQuote(remote.URL) +
		"; git config --file \"$temp_config\" just-code.github-workflow-branch " + shellQuote(branch) +
		"; git branch " + shellQuote(branch+"-snapshot-backup") + " HEAD" +
		"; git branch -m " + shellQuote(branch) +
		"; git reset --hard \"$commit\"" +
		"; mv \"$temp_config\" \"$config\""
}
