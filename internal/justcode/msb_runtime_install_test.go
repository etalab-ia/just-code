package justcode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

func TestMSBRuntimeVersionMatchesSDK(t *testing.T) {
	if got := msb.SDKVersion(); got != msbRuntimeVersion {
		t.Fatalf("Microsandbox SDK version = %s, hosted runtime = %s; update both together", got, msbRuntimeVersion)
	}
}

func TestMSBRuntimeArtifacts(t *testing.T) {
	tests := []struct {
		goos, goarch string
		name         string
		sha256       string
	}{
		{"darwin", "arm64", "microsandbox-darwin-aarch64.tar.gz", "00d61b1ce488c2575e3450ebf2e03f28eb21653e43f4ecbefdaa4cf0508b53b7"},
		{"linux", "arm64", "microsandbox-linux-aarch64.tar.gz", "aa845611a4cdeb3422b2e2fcc6a290f93f629381de9ba43d4945693ccb120ff9"},
		{"linux", "amd64", "microsandbox-linux-x86_64.tar.gz", "dd4bf2690d02ba319f326191d5ef97554af61fdef56b8d1e178e34eab2da2c98"},
		{"windows", "arm64", "microsandbox-windows-aarch64.tar.gz", "26deda0adfc40a2477516b655749d305440ac1498948d82849741ab5af4e4f3f"},
		{"windows", "amd64", "microsandbox-windows-x86_64.tar.gz", "d7e57b786191dae62403d0f4e6bdf3163e7298956bc2414b7ce9e54aacb340a5"},
	}
	for _, test := range tests {
		t.Run(test.goos+"-"+test.goarch, func(t *testing.T) {
			artifact, err := msbRuntimeArtifactFor(test.goos, test.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if artifact.name != test.name || artifact.sha256 != test.sha256 {
				t.Fatalf("artifact = %+v, want name %q and SHA-256 %q", artifact, test.name, test.sha256)
			}
		})
	}
	for _, unsupported := range [][2]string{{"darwin", "amd64"}, {"freebsd", "amd64"}, {"linux", "riscv64"}} {
		if _, err := msbRuntimeArtifactFor(unsupported[0], unsupported[1]); err == nil {
			t.Errorf("msbRuntimeArtifactFor(%q, %q): expected error", unsupported[0], unsupported[1])
		}
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("verified runtime")
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := verifySHA256(bytes.NewReader(data), digest); err != nil {
		t.Fatalf("verifySHA256(valid): %v", err)
	}
	if err := verifySHA256(strings.NewReader("corrupted runtime"), digest); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("verifySHA256(corrupt) = %v, want mismatch", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestCorruptedMSBRuntimeIsRejectedBeforeExtraction(t *testing.T) {
	home := t.TempDir()
	artifact := msbRuntimeArtifact{
		name:    "microsandbox-test.tar.gz",
		sha256:  strings.Repeat("0", 64),
		msbName: "msb",
		libName: "libkrunfw.so.5.6.1",
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/"+artifact.name) {
			t.Fatalf("download path = %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("not the expected archive")),
			Header:     make(http.Header),
		}, nil
	})}

	err := downloadAndInstallMSBRuntime(context.Background(), client, home, artifact)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("downloadAndInstallMSBRuntime = %v, want checksum mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bin", artifact.msbName)); !os.IsNotExist(err) {
		t.Fatalf("corrupt archive was extracted: %v", err)
	}
	if _, err := os.Stat(msbRuntimeMarker(home)); !os.IsNotExist(err) {
		t.Fatalf("corrupt archive was marked trusted: %v", err)
	}
}

func TestExtractMSBRuntime(t *testing.T) {
	artifact, err := msbRuntimeArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	archive := runtimeArchive(t, map[string]string{
		artifact.msbName: "runtime-binary",
		artifact.libName: "firmware-library",
		"ignored.txt":    "not installed",
	})
	home := t.TempDir()
	if err := extractMSBRuntime(bytes.NewReader(archive), home, artifact); err != nil {
		t.Fatalf("extractMSBRuntime: %v", err)
	}
	assertFileContents(t, filepath.Join(home, "bin", artifact.msbName), "runtime-binary")
	assertFileContents(t, filepath.Join(home, "lib", artifact.libName), "firmware-library")
	if _, err := os.Stat(filepath.Join(home, "bin", "ignored.txt")); !os.IsNotExist(err) {
		t.Fatalf("unexpected archive entry was extracted: %v", err)
	}
	for _, link := range artifact.libSymlink {
		target, err := os.Readlink(filepath.Join(home, "lib", link[0]))
		if err != nil {
			t.Fatalf("read symlink %s: %v", link[0], err)
		}
		if target != link[1] {
			t.Errorf("symlink %s -> %s, want %s", link[0], target, link[1])
		}
	}
}

func TestExtractMSBRuntimeRequiresBothFiles(t *testing.T) {
	artifact, err := msbRuntimeArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	archive := runtimeArchive(t, map[string]string{artifact.msbName: "runtime-binary"})
	err = extractMSBRuntime(bytes.NewReader(archive), t.TempDir(), artifact)
	if err == nil || !strings.Contains(err.Error(), artifact.libName) {
		t.Fatalf("extractMSBRuntime = %v, want missing library error", err)
	}
}

func TestManagedMSBRuntimeTrustMarker(t *testing.T) {
	artifact, err := msbRuntimeArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	home := t.TempDir()
	if managedMSBRuntimeTrusted(home, artifact) {
		t.Fatal("runtime without files or marker must not be trusted")
	}
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bin", artifact.msbName), []byte("msb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "lib", artifact.libName), []byte("lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, link := range artifact.libSymlink {
		if err := os.Symlink(link[1], filepath.Join(home, "lib", link[0])); err != nil {
			t.Fatal(err)
		}
	}
	if managedMSBRuntimeTrusted(home, artifact) {
		t.Fatal("runtime files without provenance marker must not be trusted")
	}
	if err := writeMSBRuntimeMarker(home, artifact); err != nil {
		t.Fatal(err)
	}
	if !managedMSBRuntimeTrusted(home, artifact) {
		t.Fatal("runtime with matching files and provenance marker should be trusted")
	}
	if len(artifact.libSymlink) > 0 {
		if err := os.Remove(filepath.Join(home, "lib", artifact.libSymlink[0][0])); err != nil {
			t.Fatal(err)
		}
		if managedMSBRuntimeTrusted(home, artifact) {
			t.Fatal("runtime with a missing library symlink must not be trusted")
		}
		if err := os.Symlink(artifact.libSymlink[0][1], filepath.Join(home, "lib", artifact.libSymlink[0][0])); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(msbRuntimeMarker(home), []byte(strings.Repeat("f", 64)), 0o644); err != nil {
		t.Fatal(err)
	}
	if managedMSBRuntimeTrusted(home, artifact) {
		t.Fatal("runtime with mismatched provenance marker must not be trusted")
	}
}

func TestExternalMSBRuntimeRequiresBothPaths(t *testing.T) {
	t.Setenv("MSB_PATH", filepath.Join(t.TempDir(), "msb"))
	t.Setenv("MSB_LIBKRUNFW_PATH", "")
	external, err := validateExternalMSBRuntime(context.Background())
	if !external || err == nil || !strings.Contains(err.Error(), "set both") {
		t.Fatalf("validateExternalMSBRuntime = %v, %v; want configured error", external, err)
	}
}

func TestExternalMSBRuntimeValidatesVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
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
	external, err := validateExternalMSBRuntime(context.Background())
	if !external || err != nil {
		t.Fatalf("validateExternalMSBRuntime = %v, %v; want valid external runtime", external, err)
	}
}

func TestMSBRuntimeBinaryHonorsOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom-msb")
	t.Setenv("MSB_PATH", want)
	got, err := msbRuntimeBinary()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("msbRuntimeBinary = %q, want %q", got, want)
	}
}

func runtimeArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != want {
		t.Fatalf("%s = %q, want %q", path, contents, want)
	}
}
