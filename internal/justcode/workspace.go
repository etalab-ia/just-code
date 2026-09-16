package justcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The workspace is bind-mounted into the sandbox, so anything readable in it is
// readable by the agent. The startup scan refuses to expose a workspace that
// carries dotenv files or (when gitleaks is installed) detectable secrets.

// dotenvSafeSuffixes are .env file suffixes that never carry real values.
var dotenvSafeSuffixes = map[string]bool{
	".example": true,
	".sample":  true,
}

// ScanResult reports what the workspace scan found.
type ScanResult struct {
	// DotenvFiles are paths of files named .env or .env.* (excluding safe
	// suffixes) found under the workspace, relative to the workspace root.
	DotenvFiles []string
	// GitleaksFindings are the redacted findings, one entry per detection,
	// already formatted for display. Empty when gitleaks found nothing.
	GitleaksFindings []string
	// GitleaksMissing records that gitleaks was not found on the host, so the
	// secret scan could not run.
	GitleaksMissing bool
}

// Blocking reports whether the scan found a reason to refuse startup.
func (r ScanResult) Blocking() bool {
	return len(r.DotenvFiles) > 0 || len(r.GitleaksFindings) > 0
}

// ScanWorkspace walks the workspace for dotenv files and, when gitleaks is
// installed on the host, runs a redacted secret scan over it. Findings are
// data, not errors; the returned error is reserved for failures to inspect the
// workspace at all.
func ScanWorkspace(ctx context.Context, dir string) (ScanResult, error) {
	res := ScanResult{}

	root, err := filepath.Abs(dir)
	if err != nil {
		return res, err
	}
	// A symlinked workspace root is common (WORKSPACE_DIR pointing at a link
	// to the real project). WalkDir does not follow it, so resolve the root
	// itself before walking; symlinks inside the workspace stay un-followed.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if _, err := os.Stat(root); err != nil {
		return res, err
	}

	// filepath.WalkDir uses Lstat, so symlinks are reported as themselves and
	// never traversed: a symlink named .env is still flagged (it is a .env
	// entry in the workspace), while a symlink to a directory outside the
	// workspace is not descended into. Gitleaks matches this by default
	// (--follow-symlinks is off).
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The workspace root's own name is the user's choice, not a
			// dotenv file.
			if path == root {
				return nil
			}
			if isDotenvName(d.Name()) {
				fmt.Fprintf(os.Stderr, "Warning: skipping dotenv-named directory %s\n", relTo(root, path))
			}
			return nil
		}
		if isDotenvName(d.Name()) {
			res.DotenvFiles = append(res.DotenvFiles, relTo(root, path))
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("scanning workspace %s: %w", root, err)
	}
	sort.Strings(res.DotenvFiles)

	if _, err := exec.LookPath("gitleaks"); err != nil {
		res.GitleaksMissing = true
		return res, nil
	}
	findings, err := runGitleaks(ctx, root)
	if err != nil {
		return res, err
	}
	res.GitleaksFindings = findings
	return res, nil
}

// isDotenvName reports whether name is a dotenv file that can carry real
// values: .env, or .env.<something> other than the safe example suffixes.
func isDotenvName(name string) bool {
	if name == ".env" {
		return true
	}
	if !strings.HasPrefix(name, ".env.") {
		return false
	}
	return !dotenvSafeSuffixes[filepath.Ext(name)]
}

// gitleaksFinding is the subset of a gitleaks JSON report that the gate shows.
type gitleaksFinding struct {
	RuleID string `json:"RuleID"`
	File   string `json:"File"`
	Line   int    `json:"StartLine"`
	Match  string `json:"Match"` // redacted by --redact
}

// runGitleaks runs `gitleaks detect --no-git` over the working tree (what the
// sandbox will see, not git history) with findings redacted. Gitleaks exits 1
// on leaks and 0 when clean; both are normal outcomes here.
func runGitleaks(ctx context.Context, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "gitleaks", "detect",
		"--no-banner", "--no-git", "--redact",
		"--report-format", "json", "--report-path", "-",
		"--source", dir,
	)
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 1 {
		return nil, fmt.Errorf("gitleaks exited %d on %s: %s", ee.ExitCode(), dir, ee.Stderr)
	}
	if err != nil && !isExitError(err) {
		return nil, fmt.Errorf("running gitleaks on %s: %w", dir, err)
	}
	var findings []gitleaksFinding
	if err := json.Unmarshal(out, &findings); err != nil {
		return nil, fmt.Errorf("parsing gitleaks report for %s: %w", dir, err)
	}
	var lines []string
	for _, f := range findings {
		lines = append(lines, fmt.Sprintf("%s:%d: %s (%s)", f.File, f.Line, f.Match, f.RuleID))
	}
	return lines, nil
}

func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	// Normalize to forward slashes so reported paths are identical on every
	// platform (Windows filepath.Rel yields backslash separators).
	return filepath.ToSlash(rel)
}

// CheckWorkspaceGate is the startup gate shared by the runtimes: it scans the
// workspace and returns an error when the scan found secrets, explaining what
// to do. It prints a warning when gitleaks is missing so users know the
// secret scan did not run.
func CheckWorkspaceGate(ctx context.Context, dir string) error {
	res, err := ScanWorkspace(ctx, dir)
	if err != nil {
		return err
	}
	if res.GitleaksMissing {
		fmt.Fprintln(os.Stderr, "Warning: gitleaks is not installed; the workspace secret scan was skipped. Install it (https://github.com/gitleaks/gitleaks) for full coverage.")
	}
	if !res.Blocking() {
		return nil
	}
	var b strings.Builder
	b.WriteString("refusing to start: the workspace is mounted into the sandbox and contains secrets.\n")
	for _, f := range res.DotenvFiles {
		fmt.Fprintf(&b, "  dotenv file: %s\n", f)
	}
	for _, f := range res.GitleaksFindings {
		fmt.Fprintf(&b, "  gitleaks: %s\n", f)
	}
	b.WriteString("\nMove real secrets out of the workspace (the agent can read everything mounted there),\n" +
		"or replace the files with non-secret .env.example templates, then run 'just-code start' again.")
	return fmt.Errorf("%s", b.String())
}
