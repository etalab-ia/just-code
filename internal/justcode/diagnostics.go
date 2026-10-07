package justcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// This file implements the P21 assembled diagnostics model. It is a
// read-only support report: probes are classified, values are non-secret,
// and missing configuration is reported as missing rather than invented.

// DiagnosticStatus is the display severity for one check.
type DiagnosticStatus string

const (
	DiagnosticOK     DiagnosticStatus = "ok"
	DiagnosticWarn   DiagnosticStatus = "warn"
	DiagnosticFailed DiagnosticStatus = "failed"
	DiagnosticInfo   DiagnosticStatus = "info"
)

// DiagnosticItem is one named check or fact in a section.
type DiagnosticItem struct {
	Name   string           `json:"name"`
	Value  string           `json:"value"`
	Detail string           `json:"detail,omitempty"`
	Status DiagnosticStatus `json:"status"`
}

// DiagnosticSection groups related checks without requiring callers to build
// the rendered text.
type DiagnosticSection struct {
	Title string           `json:"title"`
	Items []DiagnosticItem `json:"items"`
}

// DiagnosticsReport is the full read-only support report.
type DiagnosticsReport struct {
	Platform string              `json:"platform"`
	Fatal    bool                `json:"fatal"`
	Sections []DiagnosticSection `json:"sections"`
}

// Diagnostics collects the report. Network-facing and host-command probes
// are injectable; the production defaults never send a credential value.
type Diagnostics struct {
	Preflight   *SetupPreflight
	FS          FS
	StateDir    string
	ProjectDir  string
	LookPath    func(string) (string, error)
	Run         func(context.Context, string, ...string) (ExecResult, error)
	ProbeAlbert func(context.Context, string) CredentialProbe
	VerifyStore func(context.Context) (string, error)
	ReadAlbert  func(context.Context) (string, error)
}

var diagnosticVersionPattern = regexp.MustCompile(`^(?:go)?v?\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?$`)

// Collect builds the assembled support report. It does not install, write,
// start, stop, or authenticate as a side effect.
func (d Diagnostics) Collect(ctx context.Context) DiagnosticsReport {
	preflight := d.Preflight
	if preflight == nil {
		res := (SetupPreflighter{}).RunPreflight(ctx)
		preflight = &res
	}
	fs := d.FS
	if fs == nil {
		fs = DefaultFS
	}
	stateDir := d.StateDir
	if stateDir == "" {
		stateDir = DefaultStateDir()
	}
	projectDir := d.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}

	report := DiagnosticsReport{Platform: preflight.Platform, Fatal: preflight.Fatal}
	report.Sections = append(report.Sections,
		hostSection(*preflight),
		d.runtimeSection(ctx, fs, stateDir, projectDir, *preflight),
		d.toolchainSection(ctx),
		d.providerSection(ctx, fs),
		d.projectSection(fs, stateDir, projectDir),
		d.credentialSection(ctx, fs, stateDir),
	)
	redactDiagnosticPaths(&report, d.ProjectDir, stateDir)
	return report
}

func redactDiagnosticPaths(report *DiagnosticsReport, projectDir, stateDir string) {
	replacements := map[string]string{
		os.Getenv("MSB_PATH"):           "$MSB_PATH",
		os.Getenv("MSB_LIBKRUNFW_PATH"): "$MSB_LIBKRUNFW_PATH",
		os.Getenv("MSB_HOME"):           "$MSB_HOME",
		os.Getenv("XDG_CONFIG_HOME"):    "$XDG_CONFIG_HOME",
		os.Getenv("XDG_STATE_HOME"):     "$XDG_STATE_HOME",
		os.Getenv("APPDATA"):            "$APPDATA",
		os.Getenv("USERPROFILE"):        "$USERPROFILE",
	}
	if home, err := os.UserHomeDir(); err == nil {
		replacements[home] = "~"
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		replacements[configDir] = "$USER_CONFIG_DIR"
	}
	if stateDir != "" {
		replacements[stateDir] = "$JUST_CODE_STATE_DIR"
	}
	if projectDir != "" {
		if absolute, err := filepath.Abs(projectDir); err == nil {
			replacements[absolute] = "<project>"
		}
		if project, err := DiscoverProject(projectDir); err == nil {
			replacements[project.Root] = "<project>"
		}
	}
	keys := make([]string, 0, len(replacements))
	for path := range replacements {
		if path != "" && filepath.IsAbs(path) {
			keys = append(keys, path)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for sectionIndex := range report.Sections {
		for itemIndex := range report.Sections[sectionIndex].Items {
			item := &report.Sections[sectionIndex].Items[itemIndex]
			for _, path := range keys {
				item.Value = strings.ReplaceAll(item.Value, path, replacements[path])
				item.Detail = strings.ReplaceAll(item.Detail, path, replacements[path])
			}
		}
	}
}

func hostSection(res SetupPreflight) DiagnosticSection {
	disk := humanFreeBytes(res.DiskFreeBytes)
	if res.DiskDetail != "" {
		disk = "probe failed"
	}
	return DiagnosticSection{Title: "Host capability", Items: []DiagnosticItem{
		{Name: "Platform", Value: res.Platform, Status: DiagnosticInfo},
		{Name: "Virtualization", Value: yesNo(res.VirtualizationOK), Detail: res.VirtualizationDetail, Status: statusFor(res.VirtualizationOK)},
		{Name: "Disk free", Value: disk, Detail: res.DiskDetail, Status: statusFor(res.DiskOK)},
	}}
}

func (d Diagnostics) runtimeSection(ctx context.Context, fs FS, stateDir, projectDir string, res SetupPreflight) DiagnosticSection {
	section := DiagnosticSection{Title: "Runtime and VM state"}
	section.Items = append(section.Items, DiagnosticItem{
		Name:   "Managed Microsandbox runtime",
		Value:  yesNo(res.RuntimeInstalled),
		Detail: runtimeInstallDetail(res.RuntimeInstalled),
		Status: runtimeInstalledStatus(res.RuntimeInstalled),
	})
	externalConfigured := os.Getenv("MSB_PATH") != "" || os.Getenv("MSB_LIBKRUNFW_PATH") != ""
	if externalConfigured {
		if err := validateInstalledMSBRuntime(ctx); err != nil {
			section.Items = append(section.Items, DiagnosticItem{
				Name: "Explicit Microsandbox runtime", Value: "failed", Detail: err.Error(), Status: DiagnosticFailed,
			})
		} else {
			section.Items = append(section.Items, DiagnosticItem{
				Name: "Explicit Microsandbox runtime", Value: "validated", Detail: "path pair and version verified; no files changed", Status: DiagnosticOK,
			})
		}
	} else if res.RuntimeInstalled {
		if err := validateInstalledMSBRuntime(ctx); err != nil {
			section.Items = append(section.Items, DiagnosticItem{
				Name: "Managed runtime integrity", Value: "failed", Detail: err.Error(), Status: DiagnosticFailed,
			})
		} else {
			section.Items = append(section.Items, DiagnosticItem{
				Name: "Managed runtime integrity", Value: "validated", Detail: "provenance marker and required files verified", Status: DiagnosticOK,
			})
		}
	} else {
		section.Items = append(section.Items, DiagnosticItem{
			Name: "Selected runtime", Value: "not installed", Detail: "doctor did not install or repair it", Status: DiagnosticInfo,
		})
	}
	project, err := DiscoverProject(projectDir)
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{
			Name: "Project instance", Value: "unavailable", Detail: err.Error(), Status: DiagnosticWarn,
		})
		return section
	}
	state, err := ReadInstanceState(fs, instanceStatePath(stateDir, project.InstanceName()))
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Project instance", Value: project.InstanceName(), Detail: err.Error(), Status: DiagnosticWarn})
		return section
	}
	section.Items = append(section.Items, DiagnosticItem{Name: "Project instance", Value: project.InstanceName(), Status: DiagnosticInfo})
	if state == nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Recorded VM state", Value: "none", Detail: "no managed instance has been created for this project", Status: DiagnosticInfo})
	} else {
		section.Items = append(section.Items, DiagnosticItem{Name: "Recorded VM state", Value: "configured", Detail: "creation-fixed settings are recorded for this instance", Status: DiagnosticOK})
	}
	return section
}

func runtimeInstalledStatus(installed bool) DiagnosticStatus {
	if installed {
		return DiagnosticOK
	}
	return DiagnosticInfo
}

func runtimeInstallDetail(installed bool) string {
	if installed {
		return "provenance marker and required files present"
	}
	return "not installed; run 'just-code start --microsandbox' or 'just-code setup'"
}

func (d Diagnostics) toolchainSection(ctx context.Context) DiagnosticSection {
	section := DiagnosticSection{Title: "Toolchain"}
	for _, tool := range []string{"git", "go", "node", "opencode"} {
		section.Items = append(section.Items, d.toolItem(ctx, tool))
	}
	section.Items = append(section.Items, DiagnosticItem{
		Name:   "Microsandbox SDK",
		Value:  MSBSDKVersion(),
		Detail: "embedded SDK version; the managed runtime must match it",
		Status: DiagnosticInfo,
	})
	return section
}

func (d Diagnostics) toolItem(ctx context.Context, tool string) DiagnosticItem {
	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(tool)
	if err != nil {
		status := DiagnosticFailed
		detail := "install it or add it to PATH"
		if tool == "go" {
			status, detail = DiagnosticInfo, "optional on a machine using a prebuilt just-code binary"
		}
		return DiagnosticItem{Name: tool, Value: "not found", Detail: detail, Status: status}
	}
	run := d.Run
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) (ExecResult, error) {
			return OSRunner{}.Run(ctx, name, args...)
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, runErr := run(probeCtx, path, "--version")
	value := "found"
	status := DiagnosticOK
	detail := ""
	if runErr != nil || res.ExitCode != 0 {
		value = "unusable"
		status = DiagnosticFailed
		if tool == "go" {
			status = DiagnosticInfo
		}
		detail = fmt.Sprintf("version probe failed (exit %d)", res.ExitCode)
		if runErr != nil {
			detail = "could not run version probe"
		}
	} else if version := firstVersionToken(res.Stdout + res.Stderr); version != "" {
		value = version
	} else {
		detail = "version output was not recognized"
	}
	return DiagnosticItem{Name: tool, Value: value, Detail: detail, Status: status}
}

func firstVersionToken(output string) string {
	for _, field := range strings.Fields(output) {
		token := strings.Trim(field, "()[],:;")
		if diagnosticVersionPattern.MatchString(token) {
			return token
		}
	}
	return ""
}

func (d Diagnostics) providerSection(ctx context.Context, fs FS) DiagnosticSection {
	section := DiagnosticSection{Title: "Provider verification"}
	key := strings.TrimSpace(os.Getenv("ALBERT_API_KEY"))
	source := "environment"
	if key == "" {
		read := d.ReadAlbert
		if read == nil {
			read = func(ctx context.Context) (string, error) { return ReadStoredCredential(ctx, CredentialAlbert) }
		}
		readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		stored, err := read(readCtx)
		cancel()
		if err == nil {
			key = strings.TrimSpace(stored)
			source = "credential store"
		} else if !errors.Is(err, ErrCredentialNotFound) && !errors.Is(err, ErrFileStoreAbsent) && !errors.Is(err, ErrFileStoreNotConsented) {
			var storeErr *StoreError
			if errors.As(err, &storeErr) {
				section.Items = append(section.Items, DiagnosticItem{Name: "Albert credential store", Value: storeErr.State, Detail: storeErr.Kind + " store could not be read", Status: DiagnosticWarn})
			} else {
				section.Items = append(section.Items, DiagnosticItem{Name: "Albert credential store", Value: "unverified", Detail: "credential could not be read", Status: DiagnosticWarn})
			}
		}
	}
	if key == "" {
		section.Items = append(section.Items, DiagnosticItem{
			Name: "Albert credential", Value: "not configured", Detail: "set ALBERT_API_KEY, store a credential with 'just-code auth add albert', or run 'just-code setup'", Status: DiagnosticWarn,
		})
		return section
	}
	section.Items = append(section.Items, DiagnosticItem{
		Name: "Albert credential", Value: "present", Detail: source + " value is set; the value is never printed", Status: DiagnosticOK,
	})
	probe := d.ProbeAlbert
	if probe == nil {
		probe = func(ctx context.Context, value string) CredentialProbe {
			return ValidateAlbertKey(ctx, nil, value)
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := probe(probeCtx, key)
	probeDetail := strings.ReplaceAll(result.Detail, key, "[redacted]")
	switch {
	case result.Rejected:
		section.Items = append(section.Items, DiagnosticItem{Name: "Albert endpoint", Value: "rejected", Detail: probeDetail, Status: DiagnosticFailed})
	case result.Unreachable:
		section.Items = append(section.Items, DiagnosticItem{Name: "Albert endpoint", Value: "unreachable", Detail: probeDetail, Status: DiagnosticWarn})
	default:
		section.Items = append(section.Items, DiagnosticItem{Name: "Albert endpoint", Value: "verified", Detail: "models catalogue accepted the credential", Status: DiagnosticOK})
	}
	return section
}

func (d Diagnostics) projectSection(fs FS, stateDir, projectDir string) DiagnosticSection {
	section := DiagnosticSection{Title: "Project skills and MCPs"}
	project, err := DiscoverProject(projectDir)
	if err != nil {
		return DiagnosticSection{Title: section.Title, Items: []DiagnosticItem{{Name: "Project", Value: "unavailable", Detail: err.Error(), Status: DiagnosticFailed}}}
	}
	manifestPath := ProjectManifestPath(project.Root)
	manifest, err := ReadProjectManifest(fs, manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DiagnosticSection{Title: section.Title, Items: []DiagnosticItem{{Name: "Project", Value: project.Name, Detail: "no .just-code manifest; run 'just-code init'", Status: DiagnosticInfo}}}
		}
		return DiagnosticSection{Title: section.Title, Items: []DiagnosticItem{{Name: "Project", Value: project.Name, Detail: err.Error(), Status: DiagnosticFailed}}}
	}
	runtimeName := manifest.Runtime
	if runtimeName == "" {
		runtimeName = "default (microsandbox)"
	}
	isolation := manifest.Isolation
	if isolation == "" {
		isolation = "default (full)"
	}
	section.Items = append(section.Items,
		DiagnosticItem{Name: "Project", Value: project.Name, Detail: "current project", Status: DiagnosticInfo},
		DiagnosticItem{Name: "Runtime intent", Value: runtimeName, Status: DiagnosticInfo},
		DiagnosticItem{Name: "Isolation intent", Value: isolation, Status: DiagnosticInfo},
	)
	if _, err := fs.ReadFile(ProjectUpdateJournalPath(project.Root)); err == nil {
		section.Items = append(section.Items, DiagnosticItem{
			Name: "Pending project update", Value: "recovery required", Detail: "doctor did not recover or modify the transaction; run 'just-code update --recover'", Status: DiagnosticWarn,
		})
	} else if !os.IsNotExist(err) {
		section.Items = append(section.Items, DiagnosticItem{Name: "Pending project update", Value: "unreadable", Detail: err.Error(), Status: DiagnosticWarn})
	}

	packages, ids, localOnly, err := ReadProjectSkillDiagnosticState(fs, project.Root, stateDir, project.InstanceName())
	if err != nil {
		value := "error"
		detail := err.Error()
		if len(ids) > 0 {
			value = fmt.Sprintf("%d selected; not ready", len(ids))
			detail = strings.Join(ids, ", ") + ": " + err.Error()
		}
		section.Items = append(section.Items, DiagnosticItem{Name: "Skills", Value: value, Detail: detail, Status: DiagnosticFailed})
		if localOnly {
			section.Items = append(section.Items, DiagnosticItem{Name: "Skill storage", Value: "host-local", Detail: "selection is not shared with repository clones", Status: DiagnosticInfo})
		} else if len(ids) > 0 {
			section.Items = append(section.Items, DiagnosticItem{Name: "Skill storage", Value: "versioned", Detail: "manifest and lockfile are in the project", Status: DiagnosticInfo})
		}
	} else if len(ids) == 0 {
		section.Items = append(section.Items, DiagnosticItem{Name: "Skills", Value: "none selected", Status: DiagnosticInfo})
	} else {
		section.Items = append(section.Items, DiagnosticItem{Name: "Skills", Value: fmt.Sprintf("%d selected", len(ids)), Detail: strings.Join(ids, ", "), Status: DiagnosticOK})
		for _, pkg := range packages {
			section.Items = append(section.Items, DiagnosticItem{Name: "Skill artifact", Value: pkg.ID, Detail: shortDigest(pkg.Lock.SHA256), Status: DiagnosticOK})
		}
		if localOnly {
			section.Items = append(section.Items, DiagnosticItem{Name: "Skill storage", Value: "host-local", Detail: "selection is not shared with repository clones", Status: DiagnosticInfo})
		} else {
			section.Items = append(section.Items, DiagnosticItem{Name: "Skill storage", Value: "versioned", Detail: "manifest and lockfile are in the project", Status: DiagnosticInfo})
		}
	}

	selected, err := ValidateMCPConnectorIDs(manifest.MCPConnectors)
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "MCPs", Value: "error", Detail: err.Error(), Status: DiagnosticFailed})
	} else if len(selected) == 0 {
		section.Items = append(section.Items, DiagnosticItem{Name: "MCPs", Value: "none selected", Status: DiagnosticInfo})
	} else {
		section.Items = append(section.Items, DiagnosticItem{Name: "MCPs", Value: fmt.Sprintf("%d selected", len(selected)), Detail: strings.Join(selected, ", "), Status: DiagnosticInfo})
		for _, choice := range MCPSelections() {
			if !containsMCPConnector(selected, choice.ID) {
				continue
			}
			if choice.Local {
				section.Items = append(section.Items, DiagnosticItem{Name: "Guest MCP " + choice.ID, Value: "selected", Detail: "readiness requires an active guest integration test", Status: DiagnosticInfo})
			} else {
				section.Items = append(section.Items, DiagnosticItem{Name: "Remote MCP " + choice.ID, Value: "selected", Detail: "handshake is not probed by setup doctor", Status: DiagnosticInfo})
			}
		}
	}
	return section
}

func (d Diagnostics) credentialSection(ctx context.Context, fs FS, stateDir string) DiagnosticSection {
	section := DiagnosticSection{Title: "Credentials and storage"}
	journal, err := ReadSetupJournal(fs, stateDir)
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Setup journal", Value: "error", Detail: err.Error(), Status: DiagnosticFailed})
		return section
	}
	verifyStore := d.VerifyStore
	if verifyStore == nil {
		verifyStore = func(ctx context.Context) (string, error) {
			return verifyCredentialStore(ctx, journal.StoreKind)
		}
	}
	storeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	storeKind, storeErr := verifyStore(storeCtx)
	cancel()
	if storeErr == nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Credential store availability", Value: storeKind, Detail: "read-only probe succeeded", Status: DiagnosticOK})
	} else {
		var typed *StoreError
		if errors.As(storeErr, &typed) {
			section.Items = append(section.Items, DiagnosticItem{Name: "Credential store availability", Value: typed.State, Detail: typed.Kind + " store could not be verified", Status: DiagnosticWarn})
		} else {
			section.Items = append(section.Items, DiagnosticItem{Name: "Credential store availability", Value: "unverified", Detail: "read-only probe failed", Status: DiagnosticWarn})
		}
	}
	if journal.StoreKind != "" {
		section.Items = append(section.Items, DiagnosticItem{Name: "Credential store", Value: journal.StoreKind, Detail: "kind only; no secret values are displayed", Status: DiagnosticInfo})
	}
	if len(journal.CredentialKinds) == 0 {
		section.Items = append(section.Items, DiagnosticItem{Name: "Stored credentials", Value: "none recorded in setup journal", Status: DiagnosticInfo})
	} else {
		section.Items = append(section.Items, DiagnosticItem{Name: "Stored credentials", Value: strings.Join(journal.CredentialKinds, ", "), Detail: "kind only; values are never displayed", Status: DiagnosticInfo})
	}
	if us, err := d.userSettings(fs); err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "User settings", Value: "error", Detail: err.Error(), Status: DiagnosticFailed})
	} else if us.CredentialRef != "" {
		section.Items = append(section.Items, DiagnosticItem{Name: "Default credential reference", Value: "configured", Detail: "reference name omitted from support output", Status: DiagnosticInfo})
	}
	return section
}

func verifyCredentialStore(ctx context.Context, preferred string) (string, error) {
	if preferred == "file" {
		store, err := NewFileCredentialStore()
		if err != nil {
			return "file", err
		}
		if err := store.Verify(ctx); err != nil {
			return store.Kind(), err
		}
		return store.Kind(), nil
	}
	store := DefaultCredentialStore()
	if store == nil {
		return "none", errors.New("native credential store is not supported on this platform")
	}
	if err := store.Verify(ctx); err != nil {
		return store.Kind(), err
	}
	return store.Kind(), nil
}

// ReadProjectSkillDiagnosticState validates only persisted project intent and
// reads the already-cached archives. It deliberately skips the launch loader:
// diagnostics must not acquire project locks or recover interrupted updates.
func ReadProjectSkillDiagnosticState(fs FS, projectRoot, stateDir, instance string) ([]SkillPackage, []string, bool, error) {
	manifest, err := ReadProjectManifest(fs, ProjectManifestPath(projectRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	lock, err := ReadLockfile(fs, ProjectLockPath(projectRoot))
	if err != nil {
		return nil, nil, false, err
	}
	if err := ValidateDependencySet(manifest, lock); err != nil {
		return nil, nil, false, err
	}
	ids, pins, localOnly := manifest.Skills, lock.Skills, manifest.SkillsLocalOnly
	if localOnly {
		path, err := LocalSkillSelectionsPath(stateDir, instance)
		if err != nil {
			return nil, nil, false, err
		}
		local, err := ReadLocalSkillSelections(fs, path)
		if err != nil {
			return nil, nil, false, err
		}
		ids, pins = local.Skills, local.Pins
	}
	packages, err := LoadLockedSkillPackages(ids, pins)
	if err != nil {
		return nil, append([]string(nil), ids...), localOnly, err
	}
	return packages, append([]string(nil), ids...), localOnly, nil
}

func (d Diagnostics) userSettings(fs FS) (UserSettings, error) {
	path, err := UserSettingsPath()
	if err != nil {
		return UserSettings{}, err
	}
	return ReadUserSettings(fs, path)
}

func shortDigest(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

func statusFor(ok bool) DiagnosticStatus {
	if ok {
		return DiagnosticOK
	}
	return DiagnosticFailed
}

func yesNo(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func humanFreeBytes(n int64) string {
	switch {
	case n < 0:
		return "unknown"
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// DiagnosticSummaryPath returns a stable human-readable value for support
// logs; it contains no secret material.
func (r DiagnosticsReport) SummaryPath() string {
	var failed []string
	for _, section := range r.Sections {
		for _, item := range section.Items {
			if item.Status == DiagnosticFailed {
				failed = append(failed, section.Title+"/"+item.Name)
			}
		}
	}
	if len(failed) == 0 {
		return "ok"
	}
	return "failed: " + strings.Join(failed, ", ")
}
