package justcode

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeTarGz builds a gzipped tar archive from name -> content.
func fakeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fakeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

// serveArchive serves one archive at the path the installer builds and returns
// the base URL plus a counter of requests.
func serveArchive(t *testing.T, name string, payload []byte) (string, *int32) {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&hits, 1)
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	return server.URL, &hits
}

func TestGitleaksArtifactFor(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch string
		binary       string
		zip          bool
		sha256       string
	}{
		{"darwin", "arm64", "gitleaks", false, "b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5"},
		{"darwin", "amd64", "gitleaks", false, "dfe101a4db2255fc85120ac7f3d25e4342c3c20cf749f2c20a18081af1952709"},
		{"linux", "arm64", "gitleaks", false, "e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080"},
		{"linux", "amd64", "gitleaks", false, "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"},
		{"windows", "arm64", "gitleaks.exe", true, "b95f5e4f5c425cedca7ee203d9afd29597e692c4924a12ed42f970537c72cc0f"},
		{"windows", "amd64", "gitleaks.exe", true, "d29144deff3a68aa93ced33dddf84b7fdc26070add4aa0f4513094c8332afc4e"},
	} {
		artifact, err := gitleaksArtifactFor(tc.goos, tc.goarch)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.goos, tc.goarch, err)
		}
		if artifact.binary != tc.binary {
			t.Errorf("%s/%s binary = %q, want %q", tc.goos, tc.goarch, artifact.binary, tc.binary)
		}
		if artifact.zip != tc.zip {
			t.Errorf("%s/%s zip = %v, want %v", tc.goos, tc.goarch, artifact.zip, tc.zip)
		}
		if artifact.sha256 != tc.sha256 {
			t.Errorf("%s/%s digest = %q, want %q", tc.goos, tc.goarch, artifact.sha256, tc.sha256)
		}
		wantSuffix := ".tar.gz"
		if tc.zip {
			wantSuffix = ".zip"
		}
		if !strings.HasSuffix(artifact.name, wantSuffix) {
			t.Errorf("%s/%s asset = %q, want suffix %q", tc.goos, tc.goarch, artifact.name, wantSuffix)
		}
	}
	if _, err := gitleaksArtifactFor("linux", "riscv64"); err == nil {
		t.Error("unsupported architecture must be refused")
	}
	if _, err := gitleaksArtifactFor("plan9", "amd64"); err == nil {
		t.Error("unsupported OS must be refused")
	}
}

// TestEnsureGitleaksInstallsVerifiedArchive covers the whole install: download,
// digest check, extraction of the single executable, marker, and reuse.
func TestEnsureGitleaksInstallsVerifiedArchive(t *testing.T) {
	payload := fakeTarGz(t, map[string]string{
		"gitleaks": "#!/bin/sh\necho scanner-ok\n",
		"LICENSE":  "MIT\n",
		"README":   "readme\n",
	})
	artifact := gitleaksArtifact{
		name:   "gitleaks_8.30.1_linux_x64.tar.gz",
		sha256: digestOf(payload),
		binary: "gitleaks",
	}
	baseURL, hits := serveArchive(t, artifact.name, payload)
	orig := gitleaksReleaseBaseURL
	gitleaksReleaseBaseURL = baseURL
	t.Cleanup(func() { gitleaksReleaseBaseURL = orig })

	stateDir := t.TempDir()
	path, err := ensureGitleaksFor(context.Background(), nil, stateDir, artifact)
	if err != nil {
		t.Fatalf("ensureGitleaksFor: %v", err)
	}
	wantPath := filepath.Join(gitleaksInstallDir(stateDir), "gitleaks")
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no executable bit; Go reports 0666 for every file there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: %v", info.Mode())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "#!/bin/sh\necho scanner-ok\n" {
		t.Errorf("installed binary content = %q", body)
	}
	marker, err := os.ReadFile(gitleaksMarkerPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	wantMarker := artifact.sha256 + "  " + artifact.name + "\n"
	if string(marker) != wantMarker {
		t.Errorf("marker = %q, want %q", marker, wantMarker)
	}
	// The archive must not linger next to the binary.
	entries, err := os.ReadDir(gitleaksInstallDir(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".scanner-") {
			t.Errorf("temporary file left behind: %s", entry.Name())
		}
	}

	// A second call reuses the trusted copy without touching the network.
	if _, err := ensureGitleaksFor(context.Background(), nil, stateDir, artifact); err != nil {
		t.Fatalf("second ensureGitleaksFor: %v", err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("downloads = %d, want 1 (trusted copy must be reused)", got)
	}
}

// TestEnsureGitleaksRejectsTamperedArchive pins the trust anchor: the digest is
// checked before anything is extracted or marked trusted.
func TestEnsureGitleaksRejectsTamperedArchive(t *testing.T) {
	payload := fakeTarGz(t, map[string]string{"gitleaks": "#!/bin/sh\necho expected\n"})
	tampered := fakeTarGz(t, map[string]string{"gitleaks": "#!/bin/sh\necho tampered\n"})
	artifact := gitleaksArtifact{
		name:   "gitleaks_8.30.1_linux_x64.tar.gz",
		sha256: digestOf(payload),
		binary: "gitleaks",
	}
	baseURL, _ := serveArchive(t, artifact.name, tampered)
	orig := gitleaksReleaseBaseURL
	gitleaksReleaseBaseURL = baseURL
	t.Cleanup(func() { gitleaksReleaseBaseURL = orig })

	stateDir := t.TempDir()
	_, err := ensureGitleaksFor(context.Background(), nil, stateDir, artifact)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected a digest failure, got %v", err)
	}
	if _, statErr := os.Stat(gitleaksBinaryPath(stateDir, artifact)); statErr == nil {
		t.Error("a rejected archive must not leave an executable behind")
	}
	if _, statErr := os.Stat(gitleaksMarkerPath(stateDir)); statErr == nil {
		t.Error("a rejected archive must not be marked trusted")
	}
}

// TestExtractGitleaksZip covers the Windows archive shape, where the member is
// gitleaks.exe and the container is a zip rather than a tar.gz.
func TestExtractGitleaksZip(t *testing.T) {
	payload := fakeZip(t, map[string]string{
		"gitleaks.exe": "MZ fake\n",
		"LICENSE":      "MIT\n",
	})
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(archivePath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := gitleaksArtifact{name: "x.zip", binary: "gitleaks.exe", zip: true}
	if err := extractGitleaks(archivePath, dir, artifact); err != nil {
		t.Fatalf("extractGitleaks: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "gitleaks.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "MZ fake\n" {
		t.Errorf("extracted content = %q", body)
	}
}

// TestProbeGitleaksUsesBundledScanner proves the probe no longer depends on
// anything installed on the host: PATH holds no scanner, and the only one
// available is the path the resolver hands over.
func TestProbeGitleaksUsesBundledScanner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a POSIX shell script")
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "scanner")
	report := `[{"RuleID":"generic-api-key","File":"config.txt","StartLine":3,"Match":"REDACTED"}]`
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s' '"+report+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	orig := gitleaksExecutable
	gitleaksExecutable = func(context.Context) (string, error) { return stub, nil }
	t.Cleanup(func() { gitleaksExecutable = orig })

	probe, err := probeGitleaks(context.Background(), dir)
	if err != nil {
		t.Fatalf("probeGitleaks: %v", err)
	}
	if probe.missing {
		t.Fatal("a resolvable bundled scanner must not be reported missing")
	}
	if len(probe.findings) != 1 {
		t.Fatalf("findings = %v, want one", probe.findings)
	}
	if probe.findings[0].RuleID != "generic-api-key" || probe.findings[0].File != "config.txt" {
		t.Errorf("finding = %+v", probe.findings[0])
	}
}

// TestProbeGitleaksUnavailableIsNotFatal keeps the documented degradation: the
// dotenv half of the filter still applies, and the user is not told to install
// anything (issue #177).
func TestProbeGitleaksUnavailableIsNotFatal(t *testing.T) {
	orig := gitleaksExecutable
	gitleaksExecutable = func(context.Context) (string, error) {
		return "", fmt.Errorf("download secret scanner: HTTP 503")
	}
	t.Cleanup(func() { gitleaksExecutable = orig })

	probe, err := probeGitleaks(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("an unavailable scanner must not fail the transfer: %v", err)
	}
	if !probe.missing {
		t.Fatal("unavailable scanner must be reported missing")
	}
	if !strings.Contains(probe.detail, "503") {
		t.Errorf("detail = %q, want the underlying cause", probe.detail)
	}
	// The notice must not resurrect the dependency the user used to be asked
	// about. The technical detail may name the failing request, but not a tool
	// to install.
	message := strings.ToLower(workspaceScanUnavailableWarning(probe.detail))
	for _, forbidden := range []string{"gitleaks", "install", "github"} {
		if strings.Contains(message, forbidden) {
			t.Errorf("user-facing warning must not contain %q: %s", forbidden, message)
		}
	}
	base := strings.ToLower(workspaceScanUnavailableWarning(""))
	for _, forbidden := range []string{"gitleaks", "install", "http", "github"} {
		if strings.Contains(base, forbidden) {
			t.Errorf("the bare warning must not contain %q: %s", forbidden, base)
		}
	}
}

// TestTransferSummaryStaysToolAgnostic pins the review-screen wording when the
// scan could not run: it says what did not happen, not what to install.
func TestTransferSummaryStaysToolAgnostic(t *testing.T) {
	man := TransferManifest{GitleaksMissing: true}
	summary := strings.ToLower(man.Summary())
	if !strings.Contains(summary, "secret detection did not run") {
		t.Fatalf("summary must say the detection did not run: %s", summary)
	}
	for _, forbidden := range []string{"gitleaks", "install", "http"} {
		if strings.Contains(summary, forbidden) {
			t.Errorf("summary must not contain %q: %s", forbidden, summary)
		}
	}
}

// TestEnsureGitleaksDownloadsPinnedRelease is opt-in: it fetches the real
// release over the network and runs it. It is the only test that exercises the
// published digests, so it is also the check to run when bumping
// gitleaksVersion.
func TestEnsureGitleaksDownloadsPinnedRelease(t *testing.T) {
	if os.Getenv("JUST_CODE_TEST_GITLEAKS_DOWNLOAD") == "" {
		t.Skip("set JUST_CODE_TEST_GITLEAKS_DOWNLOAD=1 to download the pinned release")
	}
	stateDir := t.TempDir()
	path, err := ensureGitleaks(context.Background(), nil, stateDir)
	if err != nil {
		t.Fatalf("ensureGitleaks: %v", err)
	}
	version, err := exec.Command(path, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run %s version: %v (%s)", path, err, version)
	}
	if !strings.Contains(string(version), gitleaksVersion) {
		t.Fatalf("installed scanner reports %q, want %s", strings.TrimSpace(string(version)), gitleaksVersion)
	}

	// A real scan, through the injected resolver, with nothing on PATH.
	workspace := t.TempDir()
	// A high-entropy value the generic-api-key rule flags. The AWS documented
	// example key is deliberately not used: gitleaks whitelists it.
	if err := os.WriteFile(filepath.Join(workspace, "config.txt"),
		[]byte("password: \"hunter2correcthorsebatterystaple12345\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	orig := gitleaksExecutable
	gitleaksExecutable = func(context.Context) (string, error) { return path, nil }
	t.Cleanup(func() { gitleaksExecutable = orig })

	probe, err := probeGitleaks(context.Background(), workspace)
	if err != nil {
		t.Fatalf("probeGitleaks: %v", err)
	}
	if probe.missing || len(probe.findings) == 0 {
		t.Fatalf("the pinned scanner reported nothing for a secret-shaped file: %+v", probe)
	}
}

// TestArchiveBaseNameAcceptsBothSeparators pins the member-name reduction for
// archives written on either platform; using path.Base alone would miss a
// backslash-separated member entirely.
func TestArchiveBaseNameAcceptsBothSeparators(t *testing.T) {
	for _, name := range []string{"gitleaks", "bin/gitleaks", "bin\\gitleaks", "./gitleaks"} {
		if got := archiveBaseName(name); got != "gitleaks" {
			t.Errorf("archiveBaseName(%q) = %q, want gitleaks", name, got)
		}
	}
	if got := archiveBaseName("..\\..\\etc\\passwd"); got != "passwd" {
		t.Errorf("archiveBaseName of an escaping path = %q, want the base name", got)
	}
}
