package justcode

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// just-code ships its own secret scanner (issue #177).
//
// The workspace transfer filter excludes files a secret scan flags, so the
// scanner is part of the product, not a prerequisite the user has to know
// about. Like the Microsandbox runtime, the binary is fetched from an
// immutable upstream release and verified against a digest baked into this
// build before it is executed: the download source is untrusted by design,
// and the digest is the trust anchor.
//
// Pinning also makes findings reproducible: every user runs the same rule set,
// so a file excluded on one machine is excluded on another.
const (
	gitleaksVersion     = "8.30.1"
	gitleaksReleaseURL  = "https://github.com/gitleaks/gitleaks/releases/download/v8.30.1"
	gitleaksHTTPTimeout = 3 * time.Minute
	// gitleaksMaxArchive bounds the download; the release archives are ~10-15
	// MB, so the cap is slack, not a target.
	gitleaksMaxArchive = 64 << 20
	// gitleaksMaxBinary bounds the extracted executable, whose size the
	// archive header claims.
	gitleaksMaxBinary = 128 << 20
)

// gitleaksReleaseBaseURL is the release the download comes from. It is a
// variable so tests can serve a fake archive from a local server; production
// always uses the pinned release URL above.
var gitleaksReleaseBaseURL = gitleaksReleaseURL

// gitleaksArtifact is one platform's release archive and the executable
// inside it.
type gitleaksArtifact struct {
	name   string
	sha256 string
	binary string
	zip    bool
}

// gitleaksArtifactFor maps a Go platform onto the upstream asset. Digests were
// taken from the release's published checksums and re-computed from the
// downloaded archives (v8.30.1) before being baked in here.
func gitleaksArtifactFor(goos, goarch string) (gitleaksArtifact, error) {
	var platform string
	switch goarch {
	case "amd64":
		platform = "x64"
	case "arm64":
		platform = "arm64"
	default:
		return gitleaksArtifact{}, fmt.Errorf("unsupported architecture for the secret scanner: %s", goarch)
	}

	artifact := gitleaksArtifact{
		name:   fmt.Sprintf("gitleaks_%s_%s_%s", gitleaksVersion, goos, platform),
		binary: "gitleaks",
	}
	switch goos + "/" + goarch {
	case "darwin/arm64":
		artifact.sha256 = "b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5"
	case "darwin/amd64":
		artifact.sha256 = "dfe101a4db2255fc85120ac7f3d25e4342c3c20cf749f2c20a18081af1952709"
	case "linux/arm64":
		artifact.sha256 = "e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080"
	case "linux/amd64":
		artifact.sha256 = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"
	case "windows/arm64":
		artifact.sha256 = "b95f5e4f5c425cedca7ee203d9afd29597e692c4924a12ed42f970537c72cc0f"
		artifact.binary = "gitleaks.exe"
		artifact.zip = true
	case "windows/amd64":
		artifact.sha256 = "d29144deff3a68aa93ced33dddf84b7fdc26070add4aa0f4513094c8332afc4e"
		artifact.binary = "gitleaks.exe"
		artifact.zip = true
	default:
		return gitleaksArtifact{}, fmt.Errorf("unsupported platform for the secret scanner: %s/%s", goos, goarch)
	}
	if artifact.zip {
		artifact.name += ".zip"
	} else {
		artifact.name += ".tar.gz"
	}
	return artifact, nil
}

// gitleaksInstallDir is the per-version directory under the host state
// directory. The version in the name keeps an upgrade side-by-side with the
// previous binary instead of overwriting one a running process may use.
func gitleaksInstallDir(stateDir string) string {
	return filepath.Join(stateDir, "tools", "gitleaks-"+gitleaksVersion)
}

func gitleaksMarkerPath(stateDir string) string {
	return filepath.Join(gitleaksInstallDir(stateDir), ".verified")
}

func gitleaksBinaryPath(stateDir string, artifact gitleaksArtifact) string {
	return filepath.Join(gitleaksInstallDir(stateDir), artifact.binary)
}

// gitleaksExecutable resolves the pinned scanner, installing it on first use.
// It is a variable so tests can inject a stub: the real resolver downloads
// from the network, which a unit test must never do.
var gitleaksExecutable = func(ctx context.Context) (string, error) {
	return ensureGitleaks(ctx, nil, DefaultStateDir())
}

// ensureGitleaks returns the path of the verified executable, downloading and
// installing it when this host does not have a trusted copy yet.
func ensureGitleaks(ctx context.Context, client *http.Client, stateDir string) (string, error) {
	artifact, err := gitleaksArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	return ensureGitleaksFor(ctx, client, stateDir, artifact)
}

// ensureGitleaksFor installs one specific artifact. Kept separate from
// ensureGitleaks so tests can drive a fake archive whose digest they own.
func ensureGitleaksFor(ctx context.Context, client *http.Client, stateDir string, artifact gitleaksArtifact) (string, error) {
	if managedGitleaksTrusted(stateDir, artifact) {
		return gitleaksBinaryPath(stateDir, artifact), nil
	}

	dir := gitleaksInstallDir(stateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create secret scanner directory %s: %w", dir, err)
	}
	if client == nil {
		client = &http.Client{Timeout: gitleaksHTTPTimeout}
	}
	archivePath, err := downloadGitleaksArchive(ctx, client, dir, artifact)
	if err != nil {
		return "", err
	}
	defer os.Remove(archivePath)

	if err := verifyArchiveSHA256(archivePath, artifact); err != nil {
		return "", err
	}
	if err := extractGitleaks(archivePath, dir, artifact); err != nil {
		return "", err
	}
	if err := writeGitleaksMarker(stateDir, artifact); err != nil {
		return "", err
	}
	return gitleaksBinaryPath(stateDir, artifact), nil
}

func downloadGitleaksArchive(ctx context.Context, client *http.Client, dir string, artifact gitleaksArtifact) (string, error) {
	file, err := os.CreateTemp(dir, ".scanner-download-*")
	if err != nil {
		return "", fmt.Errorf("create scanner download: %w", err)
	}
	archivePath := file.Name()
	cleanup := func() {
		file.Close()
		os.Remove(archivePath)
	}

	url := gitleaksReleaseBaseURL + "/" + artifact.name
	reqCtx, cancel := context.WithTimeout(ctx, gitleaksHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		cleanup()
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		cleanup()
		return "", fmt.Errorf("download secret scanner: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cleanup()
		return "", fmt.Errorf("download secret scanner: HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(file, io.LimitReader(resp.Body, gitleaksMaxArchive+1))
	if err != nil {
		cleanup()
		return "", fmt.Errorf("download secret scanner: %w", err)
	}
	if n > gitleaksMaxArchive {
		cleanup()
		return "", fmt.Errorf("secret scanner archive exceeds %d bytes", gitleaksMaxArchive)
	}
	if err := file.Close(); err != nil {
		os.Remove(archivePath)
		return "", err
	}
	return archivePath, nil
}

func verifyArchiveSHA256(archivePath string, artifact gitleaksArtifact) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := verifySHA256(file, artifact.sha256); err != nil {
		return fmt.Errorf("verify %s: %w", artifact.name, err)
	}
	return nil
}

// archiveBaseName reduces an archive member name to its base, accepting both
// separators: a crafted archive (or a zip written on Windows) can carry
// backslashes, and path.Base alone would keep the whole name and miss the
// executable — or, worse, treat an escaping path as a plain name.
func archiveBaseName(name string) string {
	return path.Base(strings.ReplaceAll(name, `\`, "/"))
}

// extractGitleaks writes only the executable out of the archive. Paths inside
// the archive are reduced to their base name, so a crafted member name cannot
// escape the install directory.
func extractGitleaks(archivePath, dir string, artifact gitleaksArtifact) error {
	if artifact.zip {
		return extractGitleaksZip(archivePath, dir, artifact)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tarReader := tar.NewReader(gz)
	found := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if archiveBaseName(header.Name) != artifact.binary {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a regular file", header.Name)
		}
		if found {
			return fmt.Errorf("duplicate %s in archive", artifact.binary)
		}
		if header.Size < 0 || header.Size > gitleaksMaxBinary {
			return fmt.Errorf("%s exceeds %d bytes", artifact.binary, gitleaksMaxBinary)
		}
		if err := writeExecutable(filepath.Join(dir, artifact.binary), tarReader); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return fmt.Errorf("archive does not contain %s", artifact.binary)
	}
	return nil
}

func extractGitleaksZip(archivePath, dir string, artifact gitleaksArtifact) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("zip reader: %w", err)
	}
	defer reader.Close()

	found := false
	for _, entry := range reader.File {
		if archiveBaseName(entry.Name) != artifact.binary {
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", entry.Name)
		}
		if found {
			return fmt.Errorf("duplicate %s in archive", artifact.binary)
		}
		if entry.UncompressedSize64 > gitleaksMaxBinary {
			return fmt.Errorf("%s exceeds %d bytes", artifact.binary, gitleaksMaxBinary)
		}
		body, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeExecutable(filepath.Join(dir, artifact.binary), body)
		body.Close()
		if err != nil {
			return err
		}
		found = true
	}
	if !found {
		return fmt.Errorf("archive does not contain %s", artifact.binary)
	}
	return nil
}

// writeExecutable stages the binary under a temporary name and renames it, so
// a concurrent reader never sees a half-written executable.
func writeExecutable(destination string, body io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".scanner-bin-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, copyErr := io.Copy(tmp, io.LimitReader(body, gitleaksMaxBinary+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

func writeGitleaksMarker(stateDir string, artifact gitleaksArtifact) error {
	content := artifact.sha256 + "  " + artifact.name + "\n"
	return os.WriteFile(gitleaksMarkerPath(stateDir), []byte(content), 0o644)
}

// managedGitleaksTrusted reports whether this host already has the exact
// pinned build. The marker is written last, so its presence means extraction
// completed; the digest in it names the archive that produced the binary.
func managedGitleaksTrusted(stateDir string, artifact gitleaksArtifact) bool {
	marker, err := os.ReadFile(gitleaksMarkerPath(stateDir))
	if err != nil || string(marker) != artifact.sha256+"  "+artifact.name+"\n" {
		return false
	}
	info, err := os.Stat(gitleaksBinaryPath(stateDir, artifact))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	// Windows has no executable bit: Go reports 0666 for every file, so
	// requiring o+x would reject a perfectly usable copy there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	return true
}
