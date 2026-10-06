package justcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsCollectDoesNotPrintCredentialValue(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	secret := "test-credential-value-must-not-appear"
	t.Setenv("ALBERT_API_KEY", secret)

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
		LookPath:   func(string) (string, error) { return "", execNotFound() },
		Run: func(context.Context, string, ...string) (ExecResult, error) {
			return ExecResult{}, nil
		},
		ProbeAlbert: func(context.Context, string) CredentialProbe {
			return CredentialProbe{Unreachable: true, Detail: "offline test"}
		},
	}.Collect(context.Background())
	if strings.Contains(diagnosticText(report), secret) {
		t.Fatalf("diagnostics exposed the credential value: %q", diagnosticText(report))
	}
	if !strings.Contains(diagnosticText(report), "Albert endpoint") {
		t.Fatalf("provider section missing: %q", diagnosticText(report))
	}
}

func TestDiagnosticsProjectSectionsReportSelectionsWithoutFetching(t *testing.T) {
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
		Preflight:  &preflight,
		StateDir:   state,
		ProjectDir: root,
		LookPath:   func(string) (string, error) { return "", execNotFound() },
	}.Collect(context.Background())
	text := diagnosticText(report)
	for _, want := range []string{"Project skills and MCPs", "official/rgaa", "data-gouv", "Managed Microsandbox runtime"} {
		if !strings.Contains(text, want) {
			t.Fatalf("diagnostics missing %q: %q", want, text)
		}
	}
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
