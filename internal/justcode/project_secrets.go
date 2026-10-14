package justcode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Project secrets are separate from global auth credentials and VM identity.
// A full digest of the canonical checkout root scopes the host-local index;
// the short instance-name suffix is not an authorization boundary.
type ProjectSecretStore struct {
	root      string
	namespace string
	stateDir  string
	dir       string
	store     CredentialStore
	fs        projectSecretsFS
}

type projectSecretsFS interface {
	FS
	Lstat(string) (os.FileInfo, error)
}

type projectSecretsRealFS struct{ realFS }

func (projectSecretsRealFS) Lstat(p string) (os.FileInfo, error) { return os.Lstat(p) }

// NewProjectSecretStore takes the already-discovered project root, not a cwd
// or manifest label. Symlink aliases share a scope; separate clones do not.
// It does not create state or import anything.
func NewProjectSecretStore(root, stateDir string, store CredentialStore) (*ProjectSecretStore, error) {
	if store == nil || root == "" || stateDir == "" {
		return nil, fmt.Errorf("project secret storage requires a root, state directory and credential store")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve project secret scope: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project secret root must be an existing directory")
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(canonical)))
	namespace := hex.EncodeToString(digest[:])
	return &ProjectSecretStore{
		root: canonical, namespace: namespace,
		stateDir: stateDir,
		dir:      filepath.Join(stateDir, "project-secrets", namespace),
		store:    store, fs: projectSecretsRealFS{},
	}, nil
}

func (s *ProjectSecretStore) Namespace() string { return s.namespace }
func (s *ProjectSecretStore) Root() string      { return s.root }

// ProjectSecretEntry is metadata only. Component is the relative dotenv file
// identity; it is not a component name shared across projects.
type ProjectSecretEntry struct {
	Profile   string `json:"profile"`
	Component string `json:"component"`
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

type projectSecretIndex struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Namespace     string               `json:"namespace"`
	Store         string               `json:"store"`
	Generation    string               `json:"generation,omitempty"`
	Entries       []ProjectSecretEntry `json:"entries"`
}

type projectSecretJournal struct {
	SchemaVersion int      `json:"schemaVersion"`
	Namespace     string   `json:"namespace"`
	Store         string   `json:"store"`
	Generation    string   `json:"generation"`
	Prepared      []string `json:"prepared"`
	Retired       []string `json:"retired"`
}

// ProjectSecretImportResult distinguishes a successful publication with
// deferred cleanup from an import that did not change the live index.
type ProjectSecretImportResult struct {
	Committed bool
	Revision  string
	Added     int
	Replaced  int
	Unchanged int
	Removed   int
}

var ErrProjectSecretCleanupPending = errors.New("project secret cleanup pending; retry with the same credential store")
var ErrProjectSecretSnapshotChanged = errors.New("project secret set changed since preview; review a fresh preview before applying")

// Windows Credential Manager caps the blob at 2560 bytes. WinCredStore encodes
// our ASCII envelope as UTF-16: 951 raw bytes produce 1278 code units (2556
// bytes), while 952 bytes produce 1282 code units (2564 bytes).
const MaxProjectSecretValueBytes = 951

func validateProjectSecretValues(values map[string]string) error {
	for name, value := range values {
		if !validSecretIdentifier(name) {
			return fmt.Errorf("invalid dotenv variable name")
		}
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("dotenv variable %s is not valid UTF-8 text", name)
		}
		if len(encodeProjectSecret(value))*2 > 2560 {
			return fmt.Errorf("dotenv variable %s exceeds the native credential store limit (%d bytes)", name, MaxProjectSecretValueBytes)
		}
	}
	return nil
}

func validSecretIdentifier(v string) bool {
	if v == "" || len(v) > 128 {
		return false
	}
	for i, r := range v {
		if r != '_' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func validSecretProfile(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if r != '_' && r != '-' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func IsValidProjectSecretProfile(v string) bool { return validSecretProfile(v) }

func validSecretComponent(v string) bool {
	if v == "" || v == "." || path.IsAbs(v) || path.Clean(v) != v || strings.ContainsAny(v, "\\:") {
		return false
	}
	for _, part := range strings.Split(v, "/") {
		if part == ".." {
			return false
		}
	}
	for _, r := range v {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func (s *ProjectSecretStore) indexPath() string       { return filepath.Join(s.dir, "index.json") }
func (s *ProjectSecretStore) journalPath() string     { return filepath.Join(s.dir, "pending.json") }
func (s *ProjectSecretStore) referencePrefix() string { return "project-env-" + s.namespace + "-" }

func secretRandomID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func validSecretID(v string) bool {
	if len(v) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(v)
	return err == nil && len(decoded) == 16 && v == strings.ToLower(v)
}

func (s *ProjectSecretStore) validReference(ref string) bool {
	return strings.HasPrefix(ref, s.referencePrefix()) && validSecretID(strings.TrimPrefix(ref, s.referencePrefix()))
}

// The keychain command language and native stores need not preserve an empty
// or multiline value verbatim. Encode inside the secret store only; the index
// never receives this representation, which is sensitive despite the encoding.
const projectSecretValuePrefix = "jc-env-v1:"

func encodeProjectSecret(value string) string {
	return projectSecretValuePrefix + base64.StdEncoding.EncodeToString([]byte(value))
}

func decodeProjectSecret(value string) (string, error) {
	if !strings.HasPrefix(value, projectSecretValuePrefix) {
		return "", fmt.Errorf("invalid stored project secret encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, projectSecretValuePrefix))
	if err != nil {
		return "", fmt.Errorf("invalid stored project secret encoding")
	}
	return string(raw), nil
}

// Store stderr can contain a rejected secret (or its encoded representation).
// Retain its actionable state, not its value-bearing diagnostic text.
func (s *ProjectSecretStore) storeError(op string, err error) error {
	if isNotFound(err) {
		return ErrCredentialNotFound
	}
	state := "unavailable"
	var se *StoreError
	if errors.As(err, &se) {
		state = se.State
	}
	return &StoreError{Op: op, Kind: s.store.Kind(), State: state, Err: errors.New("project secret operation failed; check the credential store")}
}

func (s *ProjectSecretStore) checkPaths() error {
	for _, p := range []string{s.stateDir, filepath.Dir(s.dir), s.dir, s.indexPath(), s.journalPath(), filepath.Join(s.dir, "import.lock")} {
		info, err := s.fs.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("project secret state must not use symlinks")
		}
		if (p == s.stateDir || p == s.dir || p == filepath.Dir(s.dir)) && !info.IsDir() {
			return fmt.Errorf("project secret state directory is not a directory")
		}
	}
	return nil
}

func (s *ProjectSecretStore) lock() (func(), error) {
	if err := s.checkPaths(); err != nil {
		return nil, err
	}
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	return (&ProjectLock{Path: filepath.Join(s.dir, "import.lock"), FS: s.fs}).Acquire()
}

func (s *ProjectSecretStore) readIndex() (projectSecretIndex, error) {
	idx := projectSecretIndex{SchemaVersion: 1, Namespace: s.namespace, Store: s.store.Kind()}
	if err := s.checkPaths(); err != nil {
		return idx, err
	}
	raw, err := s.fs.ReadFile(s.indexPath())
	if os.IsNotExist(err) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	if json.Unmarshal(raw, &idx) != nil || idx.SchemaVersion != 1 || idx.Namespace != s.namespace || idx.Store != s.store.Kind() || !validSecretID(idx.Generation) {
		return idx, fmt.Errorf("project secret index has an invalid schema, scope or credential store")
	}
	seen, refs := map[string]bool{}, map[string]bool{}
	for _, e := range idx.Entries {
		identity := e.Profile + "\x00" + e.Component + "\x00" + e.Name
		if !validSecretProfile(e.Profile) || !validSecretComponent(e.Component) || !validSecretIdentifier(e.Name) || !s.validReference(e.Reference) || seen[identity] || refs[e.Reference] {
			return idx, fmt.Errorf("project secret index contains invalid or duplicate entries")
		}
		seen[identity], refs[e.Reference] = true, true
	}
	return idx, nil
}

func (s *ProjectSecretStore) writeJSON(p string, v any) error {
	if err := s.checkPaths(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.fs, p, append(raw, '\n'), 0o600)
}

// recoverPending chooses cleanup using the published generation, never by
// whether some store writes succeeded. A journal is written before any Put,
// so even an uncertain/partially successful Put is recoverable.
func (s *ProjectSecretStore) recoverPending(ctx context.Context) error {
	raw, err := s.fs.ReadFile(s.journalPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j projectSecretJournal
	if json.Unmarshal(raw, &j) != nil || j.SchemaVersion != 1 || j.Namespace != s.namespace || j.Store != s.store.Kind() || !validSecretID(j.Generation) {
		return fmt.Errorf("invalid project secret recovery journal; no credentials removed")
	}
	seen := map[string]bool{}
	for _, ref := range append(append([]string(nil), j.Prepared...), j.Retired...) {
		if !s.validReference(ref) || seen[ref] {
			return fmt.Errorf("invalid project secret recovery references; no credentials removed")
		}
		seen[ref] = true
	}
	idx, err := s.readIndex()
	if err != nil {
		return err
	}
	cleanup := j.Prepared
	if idx.Generation == j.Generation {
		cleanup = j.Retired
	}
	live := map[string]bool{}
	for _, e := range idx.Entries {
		live[e.Reference] = true
	}
	for _, ref := range cleanup {
		if live[ref] {
			return fmt.Errorf("recovery journal targets a live project secret; no credentials removed")
		}
	}
	for _, ref := range cleanup {
		if err := s.store.Remove(ctx, CredentialKind(ref)); err != nil && !isNotFound(err) {
			return fmt.Errorf("%w: %w", ErrProjectSecretCleanupPending, s.storeError("remove", err))
		}
	}
	return s.fs.Remove(s.journalPath())
}

// Preview compares one component snapshot without writing a lockfile, creating
// directories, resolving to another project/profile, or exposing values.
func (s *ProjectSecretStore) Preview(ctx context.Context, profile, component string, values map[string]string) (ProjectSecretImportResult, error) {
	var result ProjectSecretImportResult
	if !validSecretProfile(profile) || !validSecretComponent(component) {
		return result, fmt.Errorf("an explicit profile and normalized project-relative component file are required")
	}
	if err := validateProjectSecretValues(values); err != nil {
		return result, err
	}
	if err := s.checkPaths(); err != nil {
		return result, err
	}
	if _, err := s.fs.Lstat(s.journalPath()); err == nil {
		return result, fmt.Errorf("a previous project-secret operation needs recovery; retry the import with --apply")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	idx, err := s.readIndex()
	if err != nil {
		return result, err
	}
	result.Revision = projectSecretComponentRevision(s.namespace, profile, component, idx.Entries)
	old := map[string]ProjectSecretEntry{}
	for _, entry := range idx.Entries {
		if entry.Profile == profile && entry.Component == component {
			old[entry.Name] = entry
		}
	}
	for name, incoming := range values {
		previous, exists := old[name]
		if !exists {
			result.Added++
			continue
		}
		stored, err := s.store.Get(ctx, CredentialKind(previous.Reference))
		if err != nil {
			return result, s.storeError("get", err)
		}
		current, err := decodeProjectSecret(stored)
		if err != nil {
			return result, err
		}
		if current == incoming {
			result.Unchanged++
		} else {
			result.Replaced++
		}
		delete(old, name)
	}
	result.Removed = len(old)
	return result, nil
}

func projectSecretComponentRevision(namespace, profile, component string, entries []ProjectSecretEntry) string {
	h := sha256.New()
	for _, value := range []string{"project-secret-component-v1", namespace, profile, component} {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	selected := make([]ProjectSecretEntry, 0)
	for _, entry := range entries {
		if entry.Profile == profile && entry.Component == component {
			selected = append(selected, entry)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	for _, entry := range selected {
		_, _ = h.Write([]byte(entry.Name))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(entry.Reference))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Recover resumes an interrupted import without touching another project's
// namespace. Callers must use the same credential-store backend.
func (s *ProjectSecretStore) Recover(ctx context.Context) error {
	release, err := s.lock()
	if err != nil {
		return err
	}
	defer release()
	return s.recoverPending(ctx)
}

// Import replaces one profile/component snapshot. It includes removals; callers
// should preview and obtain consent, using ImportIfRevision to prevent applying
// a stale preview. Import never approves or binds a runtime secret.
func (s *ProjectSecretStore) Import(ctx context.Context, profile, component string, values map[string]string) (result ProjectSecretImportResult, err error) {
	return s.importComponent(ctx, profile, component, values, "")
}

// ImportIfRevision commits only if this component's metadata still matches
// the snapshot shown to the user during preview. Other project/components
// may change independently without invalidating this consent.
func (s *ProjectSecretStore) ImportIfRevision(ctx context.Context, profile, component string, values map[string]string, expectedRevision string) (result ProjectSecretImportResult, err error) {
	if expectedRevision == "" {
		return result, fmt.Errorf("a preview revision is required before applying project secrets")
	}
	return s.importComponent(ctx, profile, component, values, expectedRevision)
}

func (s *ProjectSecretStore) importComponent(ctx context.Context, profile, component string, values map[string]string, expectedRevision string) (result ProjectSecretImportResult, err error) {
	if !validSecretProfile(profile) || !validSecretComponent(component) {
		return result, fmt.Errorf("an explicit profile and normalized project-relative component file are required")
	}
	if err := validateProjectSecretValues(values); err != nil {
		return result, err
	}
	release, err := s.lock()
	if err != nil {
		return result, err
	}
	defer release()
	if err := s.store.Verify(ctx); err != nil {
		// A specifically consented fallback may start empty. All other store
		// failures remain hard errors, never implicit plaintext creation.
		fallback, consented := s.store.(*FileCredentialStore)
		if !consented || !fallback.CreateOnConsent || !errors.Is(err, ErrFileStoreAbsent) {
			return result, s.storeError("verify", err)
		}
	}
	if err := s.recoverPending(ctx); err != nil {
		return result, err
	}
	idx, err := s.readIndex()
	if err != nil {
		return result, err
	}
	result.Revision = projectSecretComponentRevision(s.namespace, profile, component, idx.Entries)
	if expectedRevision != "" && expectedRevision != result.Revision {
		return result, ErrProjectSecretSnapshotChanged
	}
	generation, err := secretRandomID()
	if err != nil {
		return result, err
	}
	j := projectSecretJournal{SchemaVersion: 1, Namespace: s.namespace, Store: s.store.Kind(), Generation: generation}
	var retained []ProjectSecretEntry
	old := map[string]ProjectSecretEntry{}
	for _, e := range idx.Entries {
		if e.Profile == profile && e.Component == component {
			old[e.Name] = e
		} else {
			retained = append(retained, e)
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var preparedNames []string
	for _, name := range names {
		previous, existed := old[name]
		if existed {
			stored, err := s.store.Get(ctx, CredentialKind(previous.Reference))
			if err != nil {
				return result, s.storeError("get", err)
			}
			value, err := decodeProjectSecret(stored)
			if err != nil {
				return result, err
			}
			if value == values[name] {
				retained = append(retained, previous)
				result.Unchanged++
				delete(old, name)
				continue
			}
			j.Retired = append(j.Retired, previous.Reference)
			result.Replaced++
		} else {
			result.Added++
		}
		id, err := secretRandomID()
		if err != nil {
			return result, err
		}
		ref := s.referencePrefix() + id
		j.Prepared = append(j.Prepared, ref)
		preparedNames = append(preparedNames, name)
		retained = append(retained, ProjectSecretEntry{Profile: profile, Component: component, Name: name, Reference: ref})
		delete(old, name)
	}
	for _, previous := range old {
		j.Retired = append(j.Retired, previous.Reference)
	}
	result.Removed = len(old)
	if err := s.writeJSON(s.journalPath(), j); err != nil {
		return result, err
	}
	// Leave the journal on any rollback failure, including cancellation.
	rollback := func(cause error) (ProjectSecretImportResult, error) {
		return result, errors.Join(cause, s.recoverPending(ctx))
	}
	for i, name := range preparedNames {
		if err := s.store.Put(ctx, CredentialKind(j.Prepared[i]), encodeProjectSecret(values[name])); err != nil {
			return rollback(s.storeError("add", err))
		}
	}
	idx.Generation, idx.Entries = generation, retained
	if err := s.writeJSON(s.indexPath(), idx); err != nil {
		return rollback(err)
	}
	result.Committed = true
	result.Revision = projectSecretComponentRevision(s.namespace, profile, component, idx.Entries)
	return result, s.recoverPending(ctx)
}

// Get resolves only the exact project/profile/component/key. There is no
// global credential lookup or fallback to a different profile or component.
func (s *ProjectSecretStore) Get(ctx context.Context, profile, component, name string) (string, error) {
	if !validSecretProfile(profile) || !validSecretComponent(component) || !validSecretIdentifier(name) {
		return "", fmt.Errorf("invalid project secret identity")
	}
	release, err := s.lock()
	if err != nil {
		return "", err
	}
	defer release()
	idx, err := s.readIndex()
	if err != nil {
		return "", err
	}
	for _, e := range idx.Entries {
		if e.Profile == profile && e.Component == component && e.Name == name {
			value, err := s.store.Get(ctx, CredentialKind(e.Reference))
			if err != nil {
				return "", s.storeError("get", err)
			}
			return decodeProjectSecret(value)
		}
	}
	return "", ErrCredentialNotFound
}
