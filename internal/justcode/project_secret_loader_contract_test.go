package justcode

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

// sdkModulePath is the Microsandbox SDK module, shared by this build and the
// separate native qualification kit module so both pins stay visible.
const sdkModulePath = "github.com/superradcompany/microsandbox/sdk/go"

// Test the guest loader itself, not ParseProjectDotenv: the host importer
// deliberately disables interpolation, but guest applications may not.
func TestOpaqueProjectSecretMarkersSurviveGuestGodotenv(t *testing.T) {
	markers := []string{
		"__JUST_CODE_SECRET_COMPONENT_A_API_KEY__",
		"__JUST_CODE_SECRET_COMPONENT_B_API_KEY__",
	}
	for _, marker := range markers {
		for _, quote := range []string{"", "'", "\""} {
			values, err := godotenv.Unmarshal("API_KEY=" + quote + marker + quote + "\n")
			if err != nil || values["API_KEY"] != marker {
				t.Fatalf("guest loader changed an opaque marker: quote=%q err=%v", quote, err)
			}
		}
	}
}

func TestDollarProjectSecretMarkerIsNotGuestGodotenvSafe(t *testing.T) {
	const marker = "$MSB_JUST_CODE_SECRET_A"
	values, err := godotenv.Unmarshal("API_KEY=" + marker + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if values["API_KEY"] != "" {
		t.Fatal("expected an undefined dollar reference to be expanded away")
	}
}

// Validate evidence integrity and agreement with the current runtime pins.
// This does not boot a VM or turn CI into native qualification evidence.
func TestProjectSecretNativeEvidenceMatchesRuntimePins(t *testing.T) {
	if MSBSDKVersion() != "0.7.11" || msbRuntimeVersion != "0.7.11" {
		t.Fatal("SDK/runtime changed; requalify and update the project-secret native evidence")
	}
	artifact, err := msbRuntimeArtifactFor("darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"darwin-arm64-run-1.txt", "darwin-arm64-run-2.txt"} {
		path := filepath.Join("..", "..", "tests", "integration", "project-secrets", "native", "evidence", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if fmt.Sprintf("%x", hash) != "bd135fecc29b40da987cc3521c72a2a7986e4b8019a56fb084dd98ae82b1153a" {
			t.Fatalf("native evidence %s changed", name)
		}
		text := string(data)
		if !strings.Contains(text, "sdk=0.7.11 runtime=0.7.11 archive_sha256="+artifact.sha256) ||
			strings.Contains(text, "FAIL ") || strings.Count(text, "PASS ") != 29 {
			t.Fatalf("native evidence %s does not match the recorded qualification", name)
		}
	}
}

// The native kit is a separate Go module, so the root build never compiles it
// and nothing else noticed when its SDK pin lagged the runtime it drives. A
// kit on an older SDK cannot launch a newer runtime (the probe then fails
// create-inert-sentinels with "no tested sandbox launch contract"), and that
// failure only appears on the machine that runs the qualification.
func TestProjectSecretNativeKitMatchesRuntimeSDK(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "integration", "project-secrets", "native", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		for i, field := range fields {
			if field != sdkModulePath {
				continue
			}
			// Both the single-line `require …` form and a bare block entry put
			// the version directly after the module path.
			if i+1 >= len(fields) {
				t.Fatalf("cannot read the SDK pin from %q", line)
			}
			pinned = fields[i+1]
		}
	}
	if pinned != "v"+msbRuntimeVersion {
		t.Fatalf("native kit pins %s %q, but the embedded runtime is v%s; update both together", sdkModulePath, pinned, msbRuntimeVersion)
	}
	sums, err := os.ReadFile(filepath.Join("..", "..", "tests", "integration", "project-secrets", "native", "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sums), sdkModulePath+" "+pinned+" ") {
		t.Fatalf("native kit go.sum does not cover the %s %s pin", sdkModulePath, pinned)
	}
}
