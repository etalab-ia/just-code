package justcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	Name   string
	Value  string
	Detail string
	Status DiagnosticStatus
}

// DiagnosticSection groups related checks without requiring callers to build
// the rendered text.
type DiagnosticSection struct {
	Title string
	Items []DiagnosticItem
}

// DiagnosticsReport is the full read-only support report.
type DiagnosticsReport struct {
	Platform string
	Fatal    bool
	Sections []DiagnosticSection
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
}

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
		d.credentialSection(fs),
	)
	return report
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
		Status: statusFor(res.RuntimeInstalled),
	})
	if !res.RuntimeInstalled {
		return section
	}
	if err := validateInstalledMSBRuntime(ctx); err != nil {
		section.Items = append(section.Items, DiagnosticItem{
			Name: "Selected runtime integrity", Value: "failed", Detail: err.Error(), Status: DiagnosticFailed,
		})
	} else {
		section.Items = append(section.Items, DiagnosticItem{
			Name: "Selected runtime integrity", Value: "ok", Detail: "managed or explicit runtime validated without changing it", Status: DiagnosticOK,
		})
	}
	project, err := DiscoverProject(projectDir)
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Project instance", Value: "unavailable", Detail: err.Error(), Status: DiagnosticWarn})
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
		return DiagnosticItem{Name: tool, Value: "not found", Detail: "install it or add it to PATH", Status: DiagnosticFailed}
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
	detail := path
	if runErr != nil || res.ExitCode != 0 {
		value = "unusable"
		status = DiagnosticFailed
		detail = strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
	} else if version := firstVersionLine(res.Stdout + res.Stderr); version != "" {
		value = version
	}
	return DiagnosticItem{Name: tool, Value: value, Detail: detail, Status: status}
}

func firstVersionLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (d Diagnostics) providerSection(ctx context.Context, fs FS) DiagnosticSection {
	section := DiagnosticSection{Title: "Provider verification"}
	key := strings.TrimSpace(os.Getenv("ALBERT_API_KEY"))
	if key == "" {
		if us, err := d.userSettings(fs); err == nil && us.CredentialRef != "" {
			section.Items = append(section.Items, DiagnosticItem{
				Name: "Albert credential", Value: "configured", Detail: "reference: " + us.CredentialRef + " (value not read)", Status: DiagnosticInfo,
			})
		} else {
			section.Items = append(section.Items, DiagnosticItem{
				Name: "Albert credential", Value: "not configured", Detail: "set ALBERT_API_KEY or run 'just-code setup'", Status: DiagnosticWarn,
			})
		}
		return section
	}
	section.Items = append(section.Items, DiagnosticItem{
		Name: "Albert credential", Value: "present", Detail: "environment value is set; the value is never printed", Status: DiagnosticOK,
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
	switch {
	case result.Rejected:
		section.Items = append(section.Items, DiagnosticItem{Name: "Albert endpoint", Value: "rejected", Detail: result.Detail, Status: DiagnosticFailed})
	case result.Unreachable:
		section.Items = append(section.Items, DiagnosticItem{Name: "Albert endpoint", Value: "unreachable", Detail: result.Detail, Status: DiagnosticWarn})
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

	packages, ids, _, localOnly, err := LoadProjectSkillState(fs, project.Root, stateDir, project.InstanceName())
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Skills", Value: "error", Detail: err.Error(), Status: DiagnosticFailed})
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
		section.Items = append(section.Items, DiagnosticItem{Name: "MCPs", Value: fmt.Sprintf("%d selected", len(selected)), Detail: strings.Join(selected, ", "), Status: DiagnosticOK})
	}
	return section
}

func (d Diagnostics) credentialSection(fs FS) DiagnosticSection {
	section := DiagnosticSection{Title: "Credentials and storage"}
	journal, err := ReadSetupJournal(fs, d.StateDir)
	if err != nil {
		section.Items = append(section.Items, DiagnosticItem{Name: "Setup journal", Value: "error", Detail: err.Error(), Status: DiagnosticFailed})
		return section
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
		section.Items = append(section.Items, DiagnosticItem{Name: "Default credential reference", Value: us.CredentialRef, Status: DiagnosticInfo})
	}
	return section
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
