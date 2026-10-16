package justcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Phase 3: managed guest dotenv files. Phase 2 registers the approved values
// with the Microsandbox proxy and exposes a marker under a guest variable.
// Many applications, though, read a dotenv file rather than the environment,
// and some frameworks load a different file per sub-project. This file renders
// the approved deliveries into those files and refuses to touch anything
// just-code did not write.
//
// Two kinds of value appear here, and they are not interchangeable. A
// proxy-protected binding contributes the opaque marker the guest environment
// already holds: the real value stays on the host and only the proxy
// substitutes it, only toward the approved destinations. A guest-readable
// binding contributes the real value, because nothing will substitute it — the
// application consumes it locally. Such a file is a secret: it is created
// 0600, it is marked in its header, and everything in the guest, including the
// agent, can read it. See docs/decisions/2026-10-15-guest-readable-secret-delivery.md.

// guestSecretHeaderPrefix marks a file just-code owns. The second line names
// the component so a human reading the guest can tell where it came from.
const guestSecretHeaderPrefix = "# just-code managed: do not edit"

// dotenvLiteral renders a value for a dotenv file, and reports whether the
// value can be represented unambiguously at all.
//
// dotenv has no single escaping that every loader agrees on, and this was
// measured against the loaders just-code pins (Node dotenv 16.4.7 with and
// without dotenv-expand 12.0.2, python-dotenv 1.2.2, godotenv 1.5.1):
//
//   - Single quotes preserve newlines, whitespace, "#", double quotes and an
//     interior backslash in every loader tested, so they are the encoding to
//     prefer; they are used whenever they work.
//   - A single quote inside the value cannot be escaped: godotenv then
//     refuses the whole file, and python-dotenv misparses it. A single quote
//     is therefore no obstacle to the raw fallback unless it stands first: an
//     unquoted value that begins with one (or with a double quote) is read as
//     a quoted value, which godotenv rejects outright and python-dotenv
//     misparses. Anywhere else it is an ordinary byte, so "it's" and
//     "C:\Users\O'Brien\" are deliverable raw.
//   - Two backslashes in a row are collapsed to one by python-dotenv when
//     quoted, silently. A trailing backslash escapes the closing quote:
//     godotenv refuses the whole file, and python-dotenv loses the value and
//     the next line, because every value in a generated file is followed by
//     another quoted value. Quoting cannot carry either shape.
//   - An unquoted (raw) value has no escape processing at all in any of the
//     three loaders, so a backslash pair and a trailing backslash round-trip
//     raw verbatim, which covers Windows and UNC paths and any value ending
//     in a directory separator. Raw is the fallback for every value quoting
//     cannot carry, not only those two shapes: an apostrophe also forces it,
//     as does any other value whose characters are measured to be safe raw.
//     Raw is a fallback, not the default: it is used only when quoting cannot
//     represent the value, so nothing already working changes form.
//   - Raw is safe only over a measured character set (see dotenvRawObstacle).
//     An unquoted value is ended by a newline or a carriage return, truncated
//     at a "#", expanded on "$", and read as a quoted value when it begins
//     with a single or double quote. Interior and trailing single quotes,
//     double quotes and backticks are ordinary bytes: measurement found that
//     plain Node dotenv strips only a matching quote pair around the whole
//     value, and only a wrapping backtick pair, so a lone leading backtick and
//     every interior or trailing quote round-trip verbatim. Everything else,
//     including every non-ASCII letter, mark, punctuation and symbol, is
//     carried verbatim: an unquoted value undergoes no decoding, so a path
//     such as C:\Répertoire\ or 秘密\ survives whole.
//   - A "$" followed by a name or "{" is expanded by dotenv-expand and by
//     godotenv, and the usual escape, "\$", is then left as a literal
//     backslash by plain Node dotenv and rejected by python-dotenv. So a
//     value containing "$" cannot be delivered through dotenv syntax without
//     picking one loader's semantics and breaking another's. Any "$" is
//     refused, including the trailing positions where it would in fact be
//     inert, because the safe positions depend on the characters that follow
//     and a rule that reproduces three loaders' expansion grammars is a
//     liability, not a feature: a "$" is either a literal or an expansion
//     depending on the loader, and a wrong guess alters a secret.
//
// Values that cannot be represented are refused rather than written: a
// mangled secret is worse than a refused one, because it fails silently and
// far from here.
func dotenvLiteral(value string) (string, error) {
	// Neither form survives invalid UTF-8, so it is refused once, for both.
	// python-dotenv raises on the undecodable bytes and loses the whole file;
	// Go and Node substitute U+FFFD and carry on with the wrong value, and a
	// quoted invalid byte can even leave godotenv reading the next line as
	// part of this value. The original bytes cannot be delivered as a string.
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("the value is not valid UTF-8, which the loaders decode as replacement characters or refuse the file over")
	}
	quotedErr := dotenvQuotedObstacle(value)
	if quotedErr == nil {
		return "'" + value + "'", nil
	}
	rawErr := dotenvRawObstacle(value)
	if rawErr == nil {
		return value, nil
	}
	return "", fmt.Errorf("quoted: %v; unquoted: %v", quotedErr, rawErr)
}

// dotenvRawObstacle reports why a value cannot be written unquoted, or nil
// when it can.
//
// The accepted set excludes, anywhere in the value: "$", "#", a newline and a
// carriage return. It excludes the first character when it is a single quote,
// a double quote or a character a loader strips from a value's edge, and the
// last character when it is a stripped character. A value whose first and last
// characters are both backticks is also refused. The value must be non-empty.
//
// The set was measured, not assumed. A sweep over every assigned code point,
// placed at the leading, trailing and interior position of a value in a
// generated file (the raw value between two quoted values), run through all
// three pinned loaders plus dotenv-expand, found the interior failures above
// and an edge-trim set that is the union of what each loader strips:
//
//	unicode.IsSpace(r)                        (Go, Python and Node all strip these)
//	+ U+001C..U+001F                          (Python's str.isspace, not Go's)
//	+ U+FEFF                                  (Node's trim)
//
// An interior occurrence of a whitespace character is left alone by every
// loader, so only the edges are constrained: a tab or a NBSP inside the value
// is exact, while the same character at the end would be dropped silently.
//
// Quotes and backticks are positional, not forbidden. Only a single or double
// quote standing first is unsafe: it opens a quoted value, so godotenv refuses
// the whole file ("unterminated quoted value") and python-dotenv misparses it.
// Everywhere else a quote is an ordinary byte. Plain Node dotenv strips a
// quote pair only when the same quote character wraps the whole value, and it
// treats a backtick like a quote in that one check, so an interior or trailing
// quote or backtick, a lone leading backtick, and even a backtick-wrapped value
// are preserved by the raw loaders — but a value wrapped in a matching backtick
// pair is refused by this predicate because Node, unlike Python and Go, would
// strip the pair. That is a limit of the raw form alone: the quoted form carries
// the value, so such a value is refused only when something else has already
// ruled quoting out.
//
// Non-ASCII is not a reason to refuse. Unlike a quoted value, an unquoted one
// is never decoded, so UTF-8 bytes pass through as written and a path such as
// C:\Répertoire\, naïve\ or 秘密\ round-trips exactly in all three loaders.
// Refusing them would reject the majority of the world's file names for a
// hazard that only exists inside quotes.
//
// Invalid UTF-8 never reaches this predicate: dotenvLiteral checks the whole
// value once, before either form, because a quoted invalid byte is refused by
// python-dotenv and can even swallow the next line in godotenv.
func dotenvRawObstacle(value string) error {
	if value == "" {
		return fmt.Errorf("is empty, and an unquoted empty value is indistinguishable from an absent one")
	}
	for _, r := range value {
		switch r {
		case '$':
			return fmt.Errorf("contains \"$\", which dotenv-expand and godotenv expand in an unquoted value but plain Node dotenv and python-dotenv leave alone")
		case '#':
			return fmt.Errorf("contains \"#\", which ends an unquoted value: plain Node dotenv cuts it off at any \"#\", and dotenv-expand, python-dotenv and godotenv treat one after whitespace as a comment")
		case '\n':
			return fmt.Errorf("contains a newline, which ends an unquoted value")
		case '\r':
			return fmt.Errorf("contains a carriage return, which the loaders normalise to a newline")
		}
	}
	first, firstSize := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if first == '\'' || first == '"' {
		return fmt.Errorf("starts with %q, which opens a quoted value: godotenv refuses the whole file and python-dotenv misparses it", first)
	}
	if first == '`' && last == '`' && len(value) > firstSize {
		return fmt.Errorf("starts and ends with a backtick, which plain Node dotenv strips as a wrapping quote pair")
	}
	if dotenvTrimmedAtEdge(first) {
		return fmt.Errorf("starts with %U, which the loaders strip from the edge of an unquoted value", first)
	}
	if dotenvTrimmedAtEdge(last) {
		return fmt.Errorf("ends with %U, which the loaders strip from the edge of an unquoted value", last)
	}
	return nil
}

// dotenvTrimmedAtEdge reports whether a loader strips this character from the
// leading or trailing edge of an unquoted value. It is the union of the three
// pinned loaders' trim sets, measured per character, so a value carrying one
// at its edge is refused rather than delivered with the character gone.
func dotenvTrimmedAtEdge(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	// Python's str.isspace includes the four information separators; Go's
	// unicode.IsSpace deliberately does not.
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	// JavaScript's String.trim strips a zero-width no-break space.
	return r == 0xfeff
}

// dotenvQuotedObstacle reports why a value cannot be written as a
// single-quoted literal, or nil when it can.
//
// A carriage return is refused: Node dotenv and python-dotenv normalise CR and
// CRLF to LF inside a quoted value, so 'a\rb' is delivered as "a\nb" and a
// PEM-style secret pasted from a Windows source would be altered in place.
// A double-quoted "\r" escape does reproduce a bare CR, but the three loaders
// then disagree about backslashes and escaped quotes inside double quotes, so
// there is no second form safe to fall back to. A value whose newlines were
// written on Windows cannot be delivered as itself; it is refused, and the
// refusal says so. LF alone is preserved exactly by every loader.
func dotenvQuotedObstacle(value string) error {
	if strings.ContainsRune(value, '\r') {
		return fmt.Errorf("contains a carriage return, which Node dotenv and python-dotenv normalise to a newline inside a quoted value")
	}
	if strings.ContainsRune(value, '\'') {
		return fmt.Errorf("contains a single quote, which dotenv cannot escape: godotenv refuses the whole file and python-dotenv misparses it")
	}
	if strings.ContainsRune(value, '$') {
		return fmt.Errorf("contains \"$\", which dotenv-expand and godotenv expand but plain Node dotenv and python-dotenv do not: no single encoding is exact for every loader")
	}
	if strings.Contains(value, `\\`) {
		return fmt.Errorf("contains two consecutive backslashes, which python-dotenv silently collapses to one when quoted")
	}
	if strings.HasSuffix(value, `\`) {
		return fmt.Errorf("ends with a backslash, which escapes the closing quote: python-dotenv then loses this value and the next line, and godotenv refuses the whole file")
	}
	return nil
}

// CheckGuestReadableValue reports whether a value can be delivered into a
// dotenv file. It is exported so the approval can refuse an undeliverable
// value while the user is still present, instead of letting every start fail.
func CheckGuestReadableValue(value string) error {
	_, err := dotenvLiteral(value)
	return err
}

// GuestSecretFile is one managed dotenv file to publish inside the guest.
type GuestSecretFile struct {
	// Component is the project-relative source path, e.g. "apps/api/.env".
	Component string
	// Content is the rendered dotenv text: markers for proxy-protected
	// bindings, and real values for guest-readable ones.
	Content string
	// Secret reports that Content holds at least one real value, so the file
	// is written with owner-only permissions and never briefly world-readable.
	Secret bool
}

// GuestPath is the in-guest path this file is published to.
func (f GuestSecretFile) GuestPath() (string, error) {
	return guestWorkspaceDotenvPath(f.Component)
}

// BuildGuestSecretFiles renders the approved deliveries into one file per
// component. A proxy-protected binding contributes the marker its guest
// environment already exposes, so an application reading either source gets
// the same placeholder. A guest-readable binding contributes the real value,
// because there is no proxy in front of it to substitute anything.
//
// A component with no approved binding produces no file: generating an empty
// dotenv would silently shadow whatever the project ships.
func BuildGuestSecretFiles(deliveries []ProjectSecretDelivery) ([]GuestSecretFile, error) {
	byComponent := map[string][]ProjectSecretDelivery{}
	for _, d := range deliveries {
		if err := validateGuestSecretComponent(d.Component); err != nil {
			return nil, err
		}
		byComponent[d.Component] = append(byComponent[d.Component], d)
	}
	components := make([]string, 0, len(byComponent))
	for component := range byComponent {
		components = append(components, component)
	}
	sort.Strings(components)

	files := make([]GuestSecretFile, 0, len(components))
	for _, component := range components {
		entries := byComponent[component]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		holdsSecret := false
		for _, d := range entries {
			if d.GuestReadable() {
				holdsSecret = true
				break
			}
		}
		var b strings.Builder
		b.WriteString(guestSecretHeaderPrefix + "\n")
		b.WriteString("# source: " + component + "\n")
		if holdsSecret {
			b.WriteString("# WARNING: this file contains real secret values. Everything running in\n")
			b.WriteString("# this sandbox, including the agent, can read them.\n")
			b.WriteString("# Values marked as placeholders are not secrets: the proxy substitutes\n")
			b.WriteString("# those toward the hosts this project approved.\n")
		} else {
			b.WriteString("# Each value below is an opaque placeholder. The real value stays on\n")
			b.WriteString("# the host; the sandbox proxy substitutes it only toward the hosts this\n")
			b.WriteString("# project approved.\n")
		}
		seen := map[string]bool{}
		for _, d := range entries {
			if seen[d.Name] {
				return nil, fmt.Errorf("component %s has two approved bindings for %s", component, d.Name)
			}
			seen[d.Name] = true
			switch {
			case d.GuestReadable():
				if d.Value == nil {
					return nil, fmt.Errorf("component %s variable %s is guest-readable but its value was not resolved", component, d.Name)
				}
				literal, err := dotenvLiteral(*d.Value)
				if err != nil {
					return nil, fmt.Errorf("component %s variable %s cannot be delivered in a dotenv file: the value %w", component, d.Name, err)
				}
				b.WriteString(d.Name + "=" + literal + "\n")
			case d.Marker == "":
				return nil, fmt.Errorf("component %s variable %s has no guest marker", component, d.Name)
			default:
				b.WriteString(d.Name + "=" + d.Marker + "\n")
			}
		}
		files = append(files, GuestSecretFile{Component: component, Content: b.String(), Secret: holdsSecret})
	}
	return files, nil
}

// validateGuestSecretComponent rejects a component that could escape the
// guest workspace or name something other than a dotenv file.
func validateGuestSecretComponent(component string) error {
	if !validSecretComponent(component) {
		return fmt.Errorf("project secret component %q is not a safe project-relative path", component)
	}
	if !IsProjectDotenvName(path.Base(component)) {
		return fmt.Errorf("project secret component %q is not a dotenv file", component)
	}
	return nil
}

// guestWorkspaceDotenvPath resolves a component to its path inside the guest
// workspace. The result is always a descendant of msbGuestWorkspace; a
// component that would resolve outside it is an error, never a clamped path.
func guestWorkspaceDotenvPath(component string) (string, error) {
	if err := validateGuestSecretComponent(component); err != nil {
		return "", err
	}
	joined := path.Join(msbGuestWorkspace, component)
	if !strings.HasPrefix(joined, msbGuestWorkspace+"/") {
		return "", fmt.Errorf("project secret component %q resolves outside the guest workspace", component)
	}
	return joined, nil
}

// GuestSecretPublishPlan is the decision for one publish pass: which files to
// write, which previously managed files to remove, and which targets were
// refused. It is computed without touching the guest so the decision can be
// reviewed and tested on its own.
type GuestSecretPublishPlan struct {
	Writes []GuestSecretFile
	// Removes lists previously managed guest paths that are no longer wanted.
	Removes []string
	// Unchanged lists targets whose managed content already matches.
	Unchanged []string
	// Conflicts lists targets that exist but were not written by just-code.
	// A publish with any conflict is refused entirely: silently replacing a
	// file the project ships would destroy the user's own dotenv.
	Conflicts []string
}

// PlanGuestSecretPublish compares the desired files with what the guest
// currently holds. existing maps a guest path to its current content; a
// missing path is absent from the map, and previouslyManaged lists the paths
// just-code published on an earlier run.
func PlanGuestSecretPublish(desired []GuestSecretFile, existing map[string]string, previouslyManaged []string) (GuestSecretPublishPlan, error) {
	var plan GuestSecretPublishPlan
	wanted := map[string]bool{}
	for _, file := range desired {
		guestPath, err := file.GuestPath()
		if err != nil {
			return plan, err
		}
		wanted[guestPath] = true
		current, present := existing[guestPath]
		switch {
		case !present:
			plan.Writes = append(plan.Writes, file)
		case !isManagedGuestSecretFile(current):
			plan.Conflicts = append(plan.Conflicts, guestPath)
		case current == file.Content:
			plan.Unchanged = append(plan.Unchanged, guestPath)
		default:
			plan.Writes = append(plan.Writes, file)
		}
	}
	for _, guestPath := range previouslyManaged {
		if wanted[guestPath] {
			continue
		}
		// Only remove what we still own: a file the user replaced with their
		// own is theirs, and deleting it would destroy their content.
		if content, present := existing[guestPath]; present && isManagedGuestSecretFile(content) {
			plan.Removes = append(plan.Removes, guestPath)
		}
	}
	sort.Strings(plan.Removes)
	sort.Strings(plan.Unchanged)
	sort.Strings(plan.Conflicts)
	return plan, nil
}

// isManagedGuestSecretFile reports whether content carries just-code's header
// as its first line.
func isManagedGuestSecretFile(content string) bool {
	first := content
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		first = content[:idx]
	}
	return strings.TrimRight(first, "\r") == guestSecretHeaderPrefix
}

// projectSecretAppliedRecord is the host-local inventory of what this
// project's sandbox currently holds: the guest variables registered for
// project secrets, and the managed guest dotenv files.
//
// It lives beside the binding approvals and never inside InstanceState. Two
// reasons, both learned the hard way: the runtime's state file describes the
// VM configuration and drives reconciliation, so writing it from a second
// code path races the plan (a partial record looks like a changed
// creation-fixed field), and the reconcile journal writes the *desired* state
// before the ops run, which would overwrite the applied inventory exactly when
// it is needed — naming the variables a revocation has to unregister.
type projectSecretAppliedRecord struct {
	SchemaVersion int      `json:"schemaVersion"`
	Namespace     string   `json:"namespace"`
	Envs          []string `json:"envs"`
	Files         []string `json:"files"`
}

const projectSecretAppliedSchemaVersion = 1

// ProjectSecretAppliedPath is the host-local inventory of applied project
// secret deliveries for one namespace.
func ProjectSecretAppliedPath(stateDir, namespace string) string {
	return filepath.Join(stateDir, "project-secrets", namespace, "applied.json")
}

// ProjectSecretApplied is the applied inventory: which guest variables and
// which managed guest files the sandbox holds for this project.
type ProjectSecretApplied struct {
	Envs  []string
	Files []string
}

// ReadProjectSecretApplied returns the applied inventory. A missing record is
// empty, not an error: it means nothing was applied yet. A record that belongs
// to another project is refused.
func ReadProjectSecretApplied(fs FS, stateDir, namespace string) (ProjectSecretApplied, error) {
	record := projectSecretAppliedRecord{SchemaVersion: projectSecretAppliedSchemaVersion, Namespace: namespace}
	data, err := fs.ReadFile(ProjectSecretAppliedPath(stateDir, namespace))
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectSecretApplied{}, nil
		}
		return ProjectSecretApplied{}, fmt.Errorf("read the applied project secret inventory: %w", err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return ProjectSecretApplied{}, fmt.Errorf("the applied project secret inventory is not valid JSON: %w", err)
	}
	if record.SchemaVersion != projectSecretAppliedSchemaVersion || record.Namespace != namespace {
		return ProjectSecretApplied{}, fmt.Errorf("the applied project secret inventory belongs to a different project or schema")
	}
	for _, env := range record.Envs {
		if env == "" || reservedGuestEnvs()[env] {
			return ProjectSecretApplied{}, fmt.Errorf("the applied project secret inventory names an invalid guest variable")
		}
	}
	for _, guestPath := range record.Files {
		if err := validateManagedGuestPath(guestPath); err != nil {
			return ProjectSecretApplied{}, err
		}
	}
	return ProjectSecretApplied{Envs: append([]string(nil), record.Envs...), Files: append([]string(nil), record.Files...)}, nil
}

// WriteProjectSecretApplied records the inventory after a successful apply.
func WriteProjectSecretApplied(fs FS, stateDir, namespace string, applied ProjectSecretApplied) error {
	for _, env := range applied.Envs {
		if env == "" || reservedGuestEnvs()[env] {
			return fmt.Errorf("refusing to record an invalid applied guest variable")
		}
	}
	for _, guestPath := range applied.Files {
		if err := validateManagedGuestPath(guestPath); err != nil {
			return err
		}
	}
	envs := append([]string(nil), applied.Envs...)
	files := append([]string(nil), applied.Files...)
	sort.Strings(envs)
	sort.Strings(files)
	record := projectSecretAppliedRecord{
		SchemaVersion: projectSecretAppliedSchemaVersion,
		Namespace:     namespace,
		Envs:          envs,
		Files:         files,
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(fs, ProjectSecretAppliedPath(stateDir, namespace), append(data, '\n'), 0o600)
}

// validateManagedGuestPath refuses a recorded path that is not inside the
// guest workspace. The record is host state and the paths are written by
// just-code, but a corrupted or hand-edited record must not turn the removal
// step into an arbitrary delete inside the guest.
func validateManagedGuestPath(guestPath string) error {
	if !strings.HasPrefix(guestPath, msbGuestWorkspace+"/") || strings.Contains(guestPath, "..") {
		return fmt.Errorf("recorded project secret file %q is not inside the guest workspace", guestPath)
	}
	return nil
}

// publishGuestSecretFiles applies a plan inside the running guest. Writes go
// through a temporary file in the same directory followed by a rename, so a
// reader never observes a half-written dotenv. Removals only ever target a
// path the plan classified as managed.
func (m *MicrosandboxRuntime) publishGuestSecretFiles(ctx context.Context, plan GuestSecretPublishPlan) error {
	if len(plan.Conflicts) > 0 {
		return fmt.Errorf("refusing to publish project secret files: %s already exist%s in the guest and were not written by just-code; "+
			"move them aside (or stop generating that component) and start again",
			strings.Join(plan.Conflicts, ", "), plural(len(plan.Conflicts)))
	}
	for _, file := range plan.Writes {
		guestPath, err := file.GuestPath()
		if err != nil {
			return err
		}
		if err := m.writeGuestFileAtomically(ctx, guestPath, []byte(file.Content), file.Secret); err != nil {
			return fmt.Errorf("publishing %s: %w", guestPath, err)
		}
	}
	for _, guestPath := range plan.Removes {
		if err := m.removeManagedGuestFile(ctx, guestPath); err != nil {
			return fmt.Errorf("removing the obsolete project secret file %s: %w", guestPath, err)
		}
	}
	return nil
}

// removeManagedGuestFile deletes a guest path only while it still carries
// just-code's header, checked in the same shell invocation as the removal: a
// separate read would leave a window in which a guest process replaces the
// file with its own content, and the removal would then destroy user data.
func (m *MicrosandboxRuntime) removeManagedGuestFile(ctx context.Context, guestPath string) error {
	if err := validateManagedGuestPath(guestPath); err != nil {
		return err
	}
	script := fmt.Sprintf("f=%s; if [ -f \"$f\" ]; then head -n1 \"$f\" | grep -qxF %s && rm -f \"$f\"; fi",
		shellQuote(guestPath), shellQuote(guestSecretHeaderPrefix))
	return m.guestShell(ctx, script)
}

// writeGuestFileAtomically writes payload to guestPath via a sibling temporary
// file and a rename, creating the parent directory first.
//
// A file holding a real secret is created empty with mode 0600 BEFORE the
// payload is written, and the mode is verified before the rename. The
// runtime's filesystem write creates files 0644, so writing first and
// restricting afterwards would leave the value readable by every account in
// the guest for that window, and a crash inside the window would leave it that
// way permanently. Writing into an existing file preserves its mode: the
// phase-4 probe measures this (`rewrite-mode`: 0600 after a rewrite of a 0600
// file), which is what makes the ordering safe.
func (m *MicrosandboxRuntime) writeGuestFileAtomically(ctx context.Context, guestPath string, payload []byte, secret bool) error {
	dir := path.Dir(guestPath)
	tmp := guestPath + ".jc-tmp"
	if err := m.guestShell(ctx, "mkdir -p "+shellQuote(dir)); err != nil {
		return err
	}
	if secret {
		// umask 077 rather than `install -m`: both are portable enough, but
		// this needs no external binary to exist in the guest image.
		if err := m.guestShell(ctx, "umask 077; : > "+shellQuote(tmp)); err != nil {
			return fmt.Errorf("creating %s with restrictive permissions: %w", tmp, err)
		}
	}
	if err := m.Client.WriteFile(ctx, m.InstanceName(), tmp, payload); err != nil {
		return err
	}
	move := "mv -f " + shellQuote(tmp) + " " + shellQuote(guestPath)
	if secret {
		// The mode is checked in the same invocation that renames, so a file
		// that somehow lost its restriction is never published under the
		// final name: the check is what turns the assumption above into a
		// guarantee.
		move = "f=" + shellQuote(tmp) + "; if [ \"$(stat -c %a \"$f\")\" != \"600\" ]; then rm -f \"$f\"; exit 5; fi; " + move
	}
	if err := m.guestShell(ctx, move); err != nil {
		_ = m.guestShell(ctx, "rm -f "+shellQuote(tmp))
		return err
	}
	return nil
}

// readGuestSecretFiles reads the current content of the given guest paths.
// A path that is genuinely absent is absent from the result. A path that
// exists but cannot be read is an error, not an absence: treating it as
// absence would turn a conflict with the project's own file into an
// overwrite.
func (m *MicrosandboxRuntime) readGuestSecretFiles(ctx context.Context, guestPaths []string) (map[string]string, error) {
	out := map[string]string{}
	for _, guestPath := range guestPaths {
		if err := validateManagedGuestPath(guestPath); err != nil {
			return nil, err
		}
		// Exit 3 means "no such file"; any other failure is reported. The
		// paths come from just-code's own plan, never from user input, and
		// are quoted regardless: the guest shell is not a place to rely on
		// the absence of metacharacters.
		command := "f=" + shellQuote(guestPath) + "; if [ ! -e \"$f\" ]; then exit 3; fi; cat \"$f\""
		stdout, stderr, code, err := m.Client.ExecCapture(ctx, m.InstanceName(), command)
		if err != nil {
			return nil, err
		}
		switch code {
		case 0:
			out[guestPath] = stdout
		case 3:
		default:
			return nil, fmt.Errorf("cannot read the existing guest file %s (exit %d): %s", guestPath, code, strings.TrimSpace(stderr))
		}
	}
	return out, nil
}

// guestSecretManagedPaths lists the guest paths a desired set would occupy.
func guestSecretManagedPaths(files []GuestSecretFile) ([]string, error) {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		guestPath, err := file.GuestPath()
		if err != nil {
			return nil, err
		}
		paths = append(paths, guestPath)
	}
	sort.Strings(paths)
	return paths, nil
}

// syncGuestSecretFiles is the runtime entry point: render the approved
// deliveries, compare them with the guest, refuse on a conflict, and apply.
// It returns the guest paths now managed, for the caller to persist.
func (m *MicrosandboxRuntime) syncGuestSecretFiles(ctx context.Context, deliveries []ProjectSecretDelivery, previouslyManaged []string) ([]string, error) {
	desired, err := BuildGuestSecretFiles(deliveries)
	if err != nil {
		return nil, err
	}
	desiredPaths, err := guestSecretManagedPaths(desired)
	if err != nil {
		return nil, err
	}
	lookup := append([]string(nil), desiredPaths...)
	lookup = append(lookup, previouslyManaged...)
	existing, err := m.readGuestSecretFiles(ctx, dedupeStrings(lookup))
	if err != nil {
		return nil, err
	}
	plan, err := PlanGuestSecretPublish(desired, existing, previouslyManaged)
	if err != nil {
		return nil, err
	}
	if err := m.publishGuestSecretFiles(ctx, plan); err != nil {
		return nil, err
	}
	return desiredPaths, nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
}
