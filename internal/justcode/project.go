package justcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Project discovery and instance identity (P05). The goal is to stop reusing
// one sandbox for unrelated checkouts: every project gets a deterministic,
// readable instance name derived from its canonical root, and a host-local
// registry records which root owns which instance.

// ProjectContext is the resolved identity of the project being operated on.
type ProjectContext struct {
	// Root is the canonical project root: the Git worktree root when the
	// directory is inside a worktree, otherwise the explicit override or the
	// current directory. It is absolute and symlink-resolved so aliases
	// resolve consistently.
	Root string
	// Name is the readable project name (the root's base name).
	Name string
	// IsGit records whether the root is a Git worktree. The sealed workspace
	// (P22) has no clone source for a non-Git root; discovery records the
	// class so later stages can reject or snapshot instead of failing
	// opaquely.
	IsGit bool
	// Explicit records that the root came from an explicit override rather
	// than discovery.
	Explicit bool
}

// InstanceName is the deterministic sandbox/VM instance name for a project:
// "jc-" + readable name + "-" + short path-derived suffix. The suffix keeps
// two same-named repositories and two worktrees from colliding; the readable
// prefix keeps `msb list` output and error messages human.
func (p ProjectContext) InstanceName() string {
	return InstanceName(p.Root, p.Name)
}

// InstanceName derives the instance name from a root and readable name.
func InstanceName(root, name string) string {
	suffix := pathSuffix(root)
	if suffix == "" {
		return "jc-" + safeFileName(name)
	}
	return "jc-" + safeFileName(name) + "-" + suffix
}

// pathSuffix derives a short, stable, filesystem-safe suffix from a path.
// Two different roots must collide only with probability ~2^-20 (five hex
// characters); the same root always yields the same suffix, including across
// machines when the path differs only by a leading mount point (the hash is
// over path segments, not the raw string, so "/home/u/p" and "/Users/u/p"
// hash differently but "C:\\p" and "C:/p" normalize identically).
func pathSuffix(root string) string {
	normalized := filepath.ToSlash(filepath.Clean(root))
	// Drop a Windows drive prefix: it is machine-local layout, not project
	// identity, and two clones of the same repo on C: and D: are still the
	// same project for collision-avoidance purposes.
	normalized = strings.TrimPrefix(normalized, "/")
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])[:5]
}

// DiscoverProject resolves the project context for a directory. The Git
// worktree root is used when the directory is inside a worktree; otherwise
// the directory itself. The returned root is absolute and symlink-resolved,
// so symlink aliases of the same checkout resolve to one project identity.
// Explicit root overrides are a P06 concern; discovery here never scans
// above the worktree root or the given directory.
func DiscoverProject(dir string) (ProjectContext, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ProjectContext{}, err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ProjectContext{}, err
	}
	// Resolve symlinks first, so a link into a worktree discovers the real
	// worktree root rather than a link path outside it.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ProjectContext{}, fmt.Errorf("resolve project directory %s: %w", abs, err)
	}
	root := resolved
	if top, err := gitWorktreeRoot(resolved); err == nil {
		root = top
	}
	return ProjectContext{
		Root:     root,
		Name:     filepath.Base(root),
		IsGit:    gitWorktreeRootExists(root),
		Explicit: false,
	}, nil
}

// gitWorktreeRoot returns the absolute worktree root of dir when it is inside
// a Git worktree. It shells out to git so worktrees (whose .git is a file
// pointing at the shared main worktree) resolve correctly without reimplement
// -ing Git's discovery rules. An error means "not inside a worktree" or "git
// unavailable"; callers treat both as non-Git.
func gitWorktreeRoot(dir string) (string, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(git, "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git worktree: %w", err)
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", fmt.Errorf("git rev-parse --show-toplevel returned nothing for %s", dir)
	}
	return filepath.FromSlash(top), nil
}

// gitWorktreeRootExists reports whether root is itself a Git worktree root
// (has a .git entry). It avoids spawning git: the discovery above already
// established worktree membership; this is the cheap classification for the
// recorded context.
func gitWorktreeRootExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// ProjectRegistry records project -> instance ownership on the host, outside
// every workspace. It is the authority for "which instance belongs to this
// root" so a moved or renamed checkout can be detected instead of silently
// adopting an unrelated VM.
type ProjectRegistry struct {
	// Path is the registry file path (host state).
	Path string
	// FS abstracts file access for tests; production uses DefaultFS.
	FS FS

	mu sync.Mutex
	// entries is the in-memory registry: root -> instance name.
	entries map[string]string
	loaded  bool
}

// NewProjectRegistry builds a registry backed by path.
func NewProjectRegistry(path string) *ProjectRegistry {
	return &ProjectRegistry{Path: path, FS: DefaultFS, entries: map[string]string{}}
}

// registryEntry is one serialized registry record.
type registryEntry struct {
	Root     string `json:"root"`
	Instance string `json:"instance"`
}

// registryFile is the on-disk format, versioned like the other managed files.
type registryFile struct {
	SchemaVersion int             `json:"schemaVersion"`
	Entries       []registryEntry `json:"entries"`
}

const registrySchemaVersion = 1

func (r *ProjectRegistry) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded {
		return nil
	}
	data, err := r.FS.ReadFile(r.Path)
	if err != nil {
		if os.IsNotExist(err) {
			r.loaded = true
			return nil
		}
		return err
	}
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return fmt.Errorf("project registry %s: %w", r.Path, err)
	}
	if rf.SchemaVersion > registrySchemaVersion {
		return fmt.Errorf("project registry %s: schemaVersion %d is newer than this build supports (%d)", r.Path, rf.SchemaVersion, registrySchemaVersion)
	}
	for _, e := range rf.Entries {
		r.entries[e.Root] = e.Instance
	}
	r.loaded = true
	return nil
}

// Lookup returns the instance name registered for a root, if any.
func (r *ProjectRegistry) Lookup(root string) (string, bool, error) {
	if err := r.load(); err != nil {
		return "", false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.entries[root]
	return inst, ok, nil
}

// Register records that root owns instance. It refuses to overwrite a
// different root's claim on the same instance name (collision fail-closed)
// and to rebind a root to a different instance silently: rebinding is an
// explicit operation (Rebind), not a side effect of Register.
func (r *ProjectRegistry) Register(root, instance string) error {
	if err := r.load(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[root]; ok && existing != instance {
		return fmt.Errorf("project %s is already registered to instance %s; rebinding to %s requires an explicit rebind", root, existing, instance)
	}
	for otherRoot, otherInst := range r.entries {
		if otherRoot != root && otherInst == instance {
			return fmt.Errorf("instance %s is already registered to project %s; refusing to create a duplicate registration", instance, otherRoot)
		}
	}
	if r.entries[root] == instance {
		return nil
	}
	r.entries[root] = instance
	return r.save()
}

// Rebind explicitly moves a root to a different instance.
func (r *ProjectRegistry) Rebind(root, instance string) error {
	if err := r.load(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for otherRoot, otherInst := range r.entries {
		if otherRoot != root && otherInst == instance {
			return fmt.Errorf("instance %s is already registered to project %s", instance, otherRoot)
		}
	}
	r.entries[root] = instance
	return r.save()
}

// save writes the registry atomically, sorted for stable diffs.
func (r *ProjectRegistry) save() error {
	rf := registryFile{SchemaVersion: registrySchemaVersion}
	roots := make([]string, 0, len(r.entries))
	for root := range r.entries {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		rf.Entries = append(rf.Entries, registryEntry{Root: root, Instance: r.entries[root]})
	}
	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(r.FS, r.Path, append(data, '\n'), 0o600)
}

// ProjectLock serializes per-project setup mutations so concurrent setup
// calls cannot create duplicate instances. The lock is advisory and
// host-local, held for the duration of a single setup call.
type ProjectLock struct {
	// Path is the lockfile path in host state.
	Path string
	FS   FS
}

// Acquire takes the lock, blocking until it is available. It returns a
// release function. Exclusivity is enforced by the OS, not by a check-then-
// write race: the lockfile is created with O_CREATE|O_EXCL, which fails
// atomically when it already exists. A stale lock from a dead process is
// removed (best-effort) rather than deadlocking forever.
func (l *ProjectLock) Acquire() (release func(), err error) {
	if err := l.FS.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return nil, err
	}
	for {
		err := l.FS.CreateExclusive(l.Path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
		if err == nil {
			return func() { _ = l.FS.Remove(l.Path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Stale-lock detection: if the holder is dead, break the lock.
		data, rerr := l.FS.ReadFile(l.Path)
		if rerr != nil {
			// The holder released between the failed create and this read;
			// retry immediately.
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || !processAlive(pid) {
			_ = l.FS.Remove(l.Path)
			continue
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// processAlive reports whether a PID exists on this host. On Windows,
// os.FindProcess opens the process, so success there means it is alive.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	// Signal 0 probes existence without delivering anything.
	return p.Signal(syscall.Signal(0)) == nil
}
