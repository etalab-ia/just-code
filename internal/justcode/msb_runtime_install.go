package justcode

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

const (
	msbRuntimeVersion     = "0.7.0"
	msbRuntimeReleaseURL  = "https://github.com/etalab-ia/just-code/releases/download/msb-runtime-v0.7.0"
	msbRuntimeHTTPTimeout = 5 * time.Minute
	msbRuntimeMaxArchive  = 128 << 20
	msbRuntimeMaxFile     = 64 << 20
)

type msbRuntimeArtifact struct {
	name       string
	sha256     string
	msbName    string
	libName    string
	libSymlink [][2]string
}

func msbRuntimeArtifactFor(goos, goarch string) (msbRuntimeArtifact, error) {
	platform := ""
	switch goarch {
	case "amd64":
		platform = "x86_64"
	case "arm64":
		platform = "aarch64"
	default:
		return msbRuntimeArtifact{}, fmt.Errorf("unsupported Microsandbox architecture: %s", goarch)
	}

	var artifact msbRuntimeArtifact
	switch goos + "/" + goarch {
	case "darwin/arm64":
		artifact = msbRuntimeArtifact{
			sha256:     "00d61b1ce488c2575e3450ebf2e03f28eb21653e43f4ecbefdaa4cf0508b53b7",
			msbName:    "msb",
			libName:    "libkrunfw.5.dylib",
			libSymlink: [][2]string{{"libkrunfw.dylib", "libkrunfw.5.dylib"}},
		}
	case "linux/arm64":
		artifact = msbRuntimeArtifact{
			sha256:  "aa845611a4cdeb3422b2e2fcc6a290f93f629381de9ba43d4945693ccb120ff9",
			msbName: "msb",
			libName: "libkrunfw.so.5.6.1",
			libSymlink: [][2]string{
				{"libkrunfw.so.5", "libkrunfw.so.5.6.1"},
				{"libkrunfw.so", "libkrunfw.so.5"},
			},
		}
	case "linux/amd64":
		artifact = msbRuntimeArtifact{
			sha256:  "dd4bf2690d02ba319f326191d5ef97554af61fdef56b8d1e178e34eab2da2c98",
			msbName: "msb",
			libName: "libkrunfw.so.5.6.1",
			libSymlink: [][2]string{
				{"libkrunfw.so.5", "libkrunfw.so.5.6.1"},
				{"libkrunfw.so", "libkrunfw.so.5"},
			},
		}
	case "windows/arm64":
		artifact = msbRuntimeArtifact{
			sha256:  "26deda0adfc40a2477516b655749d305440ac1498948d82849741ab5af4e4f3f",
			msbName: "msb.exe",
			libName: "libkrunfw.dll",
		}
	case "windows/amd64":
		artifact = msbRuntimeArtifact{
			sha256:  "d7e57b786191dae62403d0f4e6bdf3163e7298956bc2414b7ce9e54aacb340a5",
			msbName: "msb.exe",
			libName: "libkrunfw.dll",
		}
	default:
		return msbRuntimeArtifact{}, fmt.Errorf("unsupported Microsandbox platform: %s/%s", goos, goarch)
	}
	artifact.name = fmt.Sprintf("microsandbox-%s-%s.tar.gz", goos, platform)
	return artifact, nil
}

// MSBSDKVersion returns the Microsandbox SDK version this build embeds. It
// backs the hidden -print-msb-sdk-version CLI flag used by CI to compare the
// embedded SDK against the latest upstream release.
func MSBSDKVersion() string {
	return msb.SDKVersion()
}

func ensureMSBRuntime(ctx context.Context, client *http.Client) error {
	if msb.SDKVersion() != msbRuntimeVersion {
		return fmt.Errorf("Microsandbox SDK/runtime version mismatch: SDK %s, hosted runtime %s", msb.SDKVersion(), msbRuntimeVersion)
	}
	if external, err := validateExternalMSBRuntime(ctx); external || err != nil {
		return err
	}

	artifact, err := msbRuntimeArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	home, err := msbRuntimeHome()
	if err != nil {
		return err
	}

	if managedMSBRuntimeTrusted(home, artifact) {
		if _, err := msb.ResolveRuntime(msb.RuntimeConfig{}); err == nil {
			return nil
		}
		_ = os.Remove(msbRuntimeMarker(home))
	}

	fmt.Printf("Downloading the verified Microsandbox runtime v%s...\n", msbRuntimeVersion)
	if client == nil {
		client = &http.Client{Timeout: msbRuntimeHTTPTimeout}
	}
	if err := downloadAndInstallMSBRuntime(ctx, client, home, artifact); err != nil {
		return err
	}
	if _, err := msb.ResolveRuntime(msb.RuntimeConfig{}); err != nil {
		_ = os.Remove(msbRuntimeMarker(home))
		return fmt.Errorf("validate installed Microsandbox runtime: %w", err)
	}
	return writeMSBRuntimeMarker(home, artifact)
}

// validateExternalMSBRuntime preserves the SDK's direct path overrides as an
// explicit alternative to the managed, verified download.
func validateExternalMSBRuntime(ctx context.Context) (bool, error) {
	msbPath := os.Getenv("MSB_PATH")
	libPath := os.Getenv("MSB_LIBKRUNFW_PATH")
	if msbPath == "" && libPath == "" {
		return false, nil
	}
	if msbPath == "" || libPath == "" {
		return true, fmt.Errorf("set both MSB_PATH and MSB_LIBKRUNFW_PATH for a manually installed Microsandbox runtime")
	}
	for name, file := range map[string]string{"MSB_PATH": msbPath, "MSB_LIBKRUNFW_PATH": libPath} {
		info, err := os.Stat(file)
		if err != nil {
			return true, fmt.Errorf("%s points at %s: %w", name, file, err)
		}
		if !info.Mode().IsRegular() {
			return true, fmt.Errorf("%s must point at a regular file: %s", name, file)
		}
	}
	version, err := installedMSBVersion(ctx, msbPath)
	if err != nil {
		return true, err
	}
	if version != msbRuntimeVersion {
		return true, fmt.Errorf("MSB_PATH reports msb %s; just-code requires msb %s", version, msbRuntimeVersion)
	}
	return true, nil
}

func installedMSBVersion(ctx context.Context, binary string) (string, error) {
	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %s --version: %w: %s", binary, err, strings.TrimSpace(string(out)))
	}
	version := strings.TrimSpace(string(out))
	if !strings.HasPrefix(version, "msb ") {
		return "", fmt.Errorf("unexpected %s --version output: %q", binary, version)
	}
	return strings.TrimPrefix(version, "msb "), nil
}

func msbRuntimeHome() (string, error) {
	if home := os.Getenv("MSB_HOME"); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".microsandbox"), nil
}

func msbRuntimeMarker(home string) string {
	return filepath.Join(home, fmt.Sprintf(".just-code-runtime-v%s.sha256", msbRuntimeVersion))
}

func markerContent(artifact msbRuntimeArtifact) string {
	return artifact.sha256 + "  " + artifact.name + "\n"
}

func managedMSBRuntimeTrusted(home string, artifact msbRuntimeArtifact) bool {
	marker, err := os.ReadFile(msbRuntimeMarker(home))
	if err != nil || string(marker) != markerContent(artifact) {
		return false
	}
	for _, file := range []string{
		filepath.Join(home, "bin", artifact.msbName),
		filepath.Join(home, "lib", artifact.libName),
	} {
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	for _, link := range artifact.libSymlink {
		target, err := os.Readlink(filepath.Join(home, "lib", link[0]))
		if err != nil || target != link[1] {
			return false
		}
	}
	return true
}

func downloadAndInstallMSBRuntime(ctx context.Context, client *http.Client, home string, artifact msbRuntimeArtifact) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create Microsandbox home %s: %w", home, err)
	}
	archive, err := os.CreateTemp(home, ".just-code-msb-runtime-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create runtime download: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	url := msbRuntimeReleaseURL + "/" + artifact.name
	reqCtx, cancel := context.WithTimeout(ctx, msbRuntimeHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		archive.Close()
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		archive.Close()
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		archive.Close()
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	n, err := io.Copy(archive, io.LimitReader(resp.Body, msbRuntimeMaxArchive+1))
	if err != nil {
		archive.Close()
		return fmt.Errorf("download %s: %w", url, err)
	}
	if n > msbRuntimeMaxArchive {
		archive.Close()
		return fmt.Errorf("download %s: archive exceeds %d bytes", url, msbRuntimeMaxArchive)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		archive.Close()
		return err
	}
	if err := verifySHA256(archive, artifact.sha256); err != nil {
		archive.Close()
		return fmt.Errorf("verify %s: %w", artifact.name, err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		archive.Close()
		return err
	}
	if err := extractMSBRuntime(archive, home, artifact); err != nil {
		archive.Close()
		return fmt.Errorf("extract %s: %w", artifact.name, err)
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return nil
}

func verifySHA256(r io.Reader, expected string) error {
	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", hash.Sum(nil))
	if actual != strings.ToLower(expected) {
		return fmt.Errorf("SHA-256 mismatch: got %s, expected %s", actual, expected)
	}
	return nil
}

func extractMSBRuntime(r io.Reader, home string, artifact msbRuntimeArtifact) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	destinations := map[string]string{
		artifact.msbName: filepath.Join(home, "bin", artifact.msbName),
		artifact.libName: filepath.Join(home, "lib", artifact.libName),
	}
	found := make(map[string]bool, len(destinations))
	tarReader := tar.NewReader(gz)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		name := path.Base(header.Name)
		destination, wanted := destinations[name]
		if !wanted {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s is not a regular file", header.Name)
		}
		if found[name] {
			return fmt.Errorf("duplicate runtime file %s", name)
		}
		if err := atomicCopyFile(destination, tarReader, 0o755); err != nil {
			return err
		}
		found[name] = true
	}
	for name := range destinations {
		if !found[name] {
			return fmt.Errorf("runtime archive is missing %s", name)
		}
	}
	for _, link := range artifact.libSymlink {
		linkPath := filepath.Join(home, "lib", link[0])
		if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old symlink %s: %w", linkPath, err)
		}
		if err := os.Symlink(link[1], linkPath); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", linkPath, link[1], err)
		}
	}
	return nil
}

func atomicCopyFile(destination string, source io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".just-code-runtime-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	n, err := io.Copy(temp, io.LimitReader(source, msbRuntimeMaxFile+1))
	if err != nil {
		temp.Close()
		return err
	}
	if n > msbRuntimeMaxFile {
		temp.Close()
		return fmt.Errorf("runtime file exceeds %d bytes", msbRuntimeMaxFile)
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(destination)
	}
	if err := os.Rename(tempName, destination); err != nil {
		return fmt.Errorf("replace %s: %w", destination, err)
	}
	return nil
}

func writeMSBRuntimeMarker(home string, artifact msbRuntimeArtifact) error {
	return atomicCopyFile(msbRuntimeMarker(home), strings.NewReader(markerContent(artifact)), 0o644)
}
