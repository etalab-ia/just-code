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
	if MSBSDKVersion() != "0.7.8" || msbRuntimeVersion != "0.7.8" {
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
		if fmt.Sprintf("%x", hash) != "5807c788d549d623b355240e2225488914d83644f765da3c272baa25c34fc231" {
			t.Fatalf("native evidence %s changed", name)
		}
		text := string(data)
		if !strings.Contains(text, "sdk=0.7.6 runtime=0.7.6 archive_sha256="+artifact.sha256) ||
			strings.Contains(text, "FAIL ") || strings.Count(text, "PASS ") != 29 {
			t.Fatalf("native evidence %s does not match the recorded qualification", name)
		}
	}
}
