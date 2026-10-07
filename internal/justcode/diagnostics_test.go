package justcode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiagnosticsCollectDoesNotPrintCredentialValue(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	home := setupDiagnosticsEnv(t)
	secret := "test-credential-value-must-not-appear"
	t.Setenv("ALBERT_API_KEY", secret)
	settingsPath, err := UserSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteUserSettings(DefaultFS, settingsPath, UserSettings{SchemaVersion: userSettingsSchemaVersion, CredentialRef: secret}); err != nil {
		t.Fatal(err)
	}

	preflight := SetupPreflight{
		Platform:             "test/arch",
		VirtualizationOK:     true,
		DiskFreeBytes:        2 << 30,
		DiskOK:               true,
		RuntimeInstalled:     false,
		Fatal:                false,
		VirtualizationDetail: "test virtualization detail",
	}
	report := Diagnostics{
		Preflight:  &preflight,
		StateDir:   state,
		ProjectDir: root,
		LookPath: func(tool string) (string, error) {
			if tool == "go" {
				return "", exec.ErrNotFound
			}
			return filepath.Join(home, secret), nil
		},
		Run: func(context.Context, string, ...string) (ExecResult, error) {
			return ExecResult{Stdout: secret + " v1.2.3"}, nil
		},
		ProbeAlbert: func(context.Context, string) CredentialProbe {
			return CredentialProbe{Unreachable: true, Detail: "offline test: " + secret}
		},
		VerifyStore: func(context.Context) (string, error) { return "test-store", nil },
	}.Collect(context.Background())
	if strings.Contains(diagnosticText(report), secret) {
		t.Fatalf("diagnostics exposed the credential value: %q", diagnosticText(report))
	}
	if !strings.Contains(diagnosticText(report), "Albert endpoint") {
		t.Fatalf("provider section missing: %q", diagnosticText(report))
	}
	for _, section := range report.Sections {
		if section.Title != "Toolchain" {
			continue
		}
		for _, item := range section.Items {
			if item.Name == "go" && item.Status != DiagnosticInfo {
				t.Fatalf("missing optional Go compiler should be informational, got %+v", item)
			}
		}
	}
}

func TestDiagnosticsProjectSectionsReportSelectionsWithoutFetching(t *testing.T) {
	setupDiagnosticsEnv(t)
	root := t.TempDir()
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".just-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := ProjectManifestPath(root)
	manifest := ProjectManifest{
		SchemaVersion: projectManifestSchemaVersion,
		Runtime:       string(RuntimeMicrosandbox),
		Isolation:     string(IsolationFull),
		Skills:        []string{"official/rgaa"},
		MCPConnectors: []string{"data-gouv"},
	}
	if err := WriteProjectManifest(DefaultFS, manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	lock := Lockfile{
		SchemaVersion:   lockfileSchemaVersion,
		DependencySetID: manifest.DependencySetID,
		Skills: map[string]SkillLock{
			"official/rgaa": {Repository: projectSkillsRepository, Revision: strings.Repeat("1", 40), SHA256: strings.Repeat("a", 64)},
		},
	}
	if err := WriteLockfile(DefaultFS, ProjectLockPath(root), lock); err != nil {
		t.Fatal(err)
	}

	preflight := SetupPreflight{Platform: "test/arch", VirtualizationOK: true, DiskFreeBytes: 2 << 30, DiskOK: true, RuntimeInstalled: false}
	report := Diagnostics{
		Preflight:   &preflight,
		StateDir:    state,
		ProjectDir:  root,
		LookPath:    func(string) (string, error) { return "", execNotFound() },
		VerifyStore: func(context.Context) (string, error) { return "test-store", nil },
		ReadAlbert:  func(context.Context) (string, error) { return "", ErrCredentialNotFound },
	}.Collect(context.Background())
	text := diagnosticText(report)
	for _, want := range []string{"Project skills and MCPs", "official/rgaa", "data-gouv", "Managed Microsandbox runtime"} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostics missing %q: %q", want, text)
		}
	}
}

func TestDiagnosticsStoredCredentialAndJournalUseResolvedStateDir(t *testing.T) {
	setupDiagnosticsEnv(t)
	state := t.TempDir()
	if err := WriteSetupJournal(DefaultFS, state, SetupJournal{
		Stage: SetupStage("credential"), StoreKind: "native", CredentialKinds: []string{"albert"},
	}); err != nil {
		t.Fatal(err)
	}
	preflight := SetupPreflight{Platform: "test/arch", VirtualizationOK: true, DiskFreeBytes: 2 << 30, DiskOK: true}
	secret := "stored-test-credential-never-print"
	var probed string
	report := Diagnostics{
		Preflight: &preflight, StateDir: state, ProjectDir: t.TempDir(),
		LookPath:    func(string) (string, error) { return "", exec.ErrNotFound },
		VerifyStore: func(context.Context) (string, error) { return "native", nil },
		ReadAlbert:  func(context.Context) (string, error) { return secret, nil },
		ProbeAlbert: func(_ context.Context, value string) CredentialProbe {
			probed = value
			return CredentialProbe{Rejected: true, Detail: "test rejection"}
		},
	}.Collect(context.Background())
	text := diagnosticText(report)
	if probed != secret {
		t.Fatalf("stored credential passed to probe = %q", probed)
	}
	if strings.Contains(text, secret) || !strings.Contains(text, "native") || !strings.Contains(text, "albert") {
		t.Fatalf("credential/storage diagnostics missing kinds or exposing secret: %q", text)
	}
}

func TestDiagnosticsDoesNotMutateInterruptedProjectUpdate(t *testing.T) {
	setupDiagnosticsEnv(t)
	root := t.TempDir()
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".just-code"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := ProjectManifest{SchemaVersion: projectManifestSchemaVersion, Runtime: string(RuntimeMicrosandbox)}
	if err := WriteProjectManifest(DefaultFS, ProjectManifestPath(root), manifest); err != nil {
		t.Fatal(err)
	}
	if err := WriteLockfile(DefaultFS, ProjectLockPath(root), Lockfile{SchemaVersion: lockfileSchemaVersion}); err != nil {
		t.Fatal(err)
	}
	journalPath := ProjectUpdateJournalPath(root)
	journal := []byte("interrupted transaction marker")
	if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
		t.Fatal(err)
	}
	previousStateDirFn := projectUpdateStateDirFn
	projectUpdateStateDirFn = func() string { return state }
	t.Cleanup(func() { projectUpdateStateDirFn = previousStateDirFn })
	readonly := &diagnosticsReadOnlyFS{FS: DefaultFS}
	preflight := SetupPreflight{Platform: "test/arch", VirtualizationOK: true, DiskFreeBytes: 2 << 30, DiskOK: true}
	report := Diagnostics{
		Preflight: &preflight, FS: readonly, StateDir: state, ProjectDir: root,
		LookPath:    func(string) (string, error) { return "", exec.ErrNotFound },
		VerifyStore: func(context.Context) (string, error) { return "test-store", nil },
		ReadAlbert:  func(context.Context) (string, error) { return "", ErrCredentialNotFound },
	}.Collect(context.Background())
	if readonly.writes != 0 {
		t.Fatalf("doctor attempted %d filesystem mutations", readonly.writes)
	}
	if got, err := os.ReadFile(journalPath); err != nil || string(got) != string(journal) {
		t.Fatalf("doctor modified the pending project journal: bytes=%q err=%v", got, err)
	}
	lockPath := projectUpdateLockPath(root)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("doctor created the project update lock %s: %v", lockPath, err)
	}
	if !strings.Contains(diagnosticText(report), "Pending project update") {
		t.Fatalf("doctor did not report the pending update without recovering it: %q", diagnosticText(report))
	}
}

func TestDiagnosticsValidatesExplicitRuntimeWithoutManagedInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell runtime fixture is Unix-only")
	}
	setupDiagnosticsEnv(t)
	dir := t.TempDir()
	msbPath := filepath.Join(dir, "msb")
	libPath := filepath.Join(dir, "libkrunfw")
	if err := os.WriteFile(msbPath, []byte("#!/bin/sh\necho 'msb "+msbRuntimeVersion+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(libPath, []byte("library"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MSB_PATH", msbPath)
	t.Setenv("MSB_LIBKRUNFW_PATH", libPath)
	preflight := SetupPreflight{Platform: "test/arch", VirtualizationOK: true, DiskFreeBytes: 2 << 30, DiskOK: true}
	report := Diagnostics{
		Preflight: &preflight, StateDir: t.TempDir(), ProjectDir: t.TempDir(),
		LookPath:    func(string) (string, error) { return "", exec.ErrNotFound },
		VerifyStore: func(context.Context) (string, error) { return "test-store", nil },
		ReadAlbert:  func(context.Context) (string, error) { return "", ErrCredentialNotFound },
	}.Collect(context.Background())
	for _, section := range report.Sections {
		if section.Title != "Runtime and VM state" {
			continue
		}
		for _, item := range section.Items {
			if item.Name == "Explicit Microsandbox runtime" && item.Status == DiagnosticOK {
				return
			}
		}
	}
	t.Fatalf("valid explicit runtime was not accepted without a managed install: %+v", report.Sections)
}

func setupDiagnosticsEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("ALBERT_API_KEY", "")
	t.Setenv("MSB_PATH", "")
	t.Setenv("MSB_LIBKRUNFW_PATH", "")
	return home
}

type diagnosticsReadOnlyFS struct {
	FS
	writes int
}

func (fs *diagnosticsReadOnlyFS) WriteFile(string, []byte, os.FileMode) error {
	fs.writes++
	return errors.New("read-only test filesystem")
}
func (fs *diagnosticsReadOnlyFS) MkdirAll(string, os.FileMode) error {
	fs.writes++
	return errors.New("read-only test filesystem")
}
func (fs *diagnosticsReadOnlyFS) Remove(string) error {
	fs.writes++
	return errors.New("read-only test filesystem")
}
func (fs *diagnosticsReadOnlyFS) RenameTmp(string, string) error {
	fs.writes++
	return errors.New("read-only test filesystem")
}
func (fs *diagnosticsReadOnlyFS) WriteTemp(string, string, []byte, os.FileMode) (string, error) {
	fs.writes++
	return "", errors.New("read-only test filesystem")
}
func (fs *diagnosticsReadOnlyFS) CreateExclusive(string, []byte, os.FileMode) error {
	fs.writes++
	return errors.New("read-only test filesystem")
}

func diagnosticText(report DiagnosticsReport) string {
	var b strings.Builder
	b.WriteString(report.Platform + "\n")
	for _, section := range report.Sections {
		b.WriteString(section.Title + "\n")
		for _, item := range section.Items {
			b.WriteString(item.Name + " " + item.Value + " " + item.Detail + "\n")
		}
	}
	return b.String()
}

func execNotFound() error { return exec.ErrNotFound }
