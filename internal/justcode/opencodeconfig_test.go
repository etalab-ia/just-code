package justcode

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComposeConfigContentOverridesModelOnly(t *testing.T) {
	content, err := ComposeConfigContent(ManagedOverlay{Model: "albert/gpt-oss-120b"})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("composed content must be valid JSON: %v\n%s", err, content)
	}
	if parsed["model"] != "albert/gpt-oss-120b" {
		t.Fatalf("model = %v", parsed["model"])
	}
	if parsed["small_model"] != "albert/gpt-oss-120b" {
		t.Fatalf("small_model must follow model: %v", parsed["small_model"])
	}
	// The base asset's provider block and permissions survive the compose.
	provider, ok := parsed["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider block lost: %v", parsed["provider"])
	}
	albert, ok := provider["albert"].(map[string]any)
	if !ok || albert["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("albert provider lost: %v", provider)
	}
	if _, ok := parsed["permission"]; !ok {
		t.Fatal("permissions block lost")
	}
	// The API key reference survives (never a literal).
	if !strings.Contains(content, "{env:ALBERT_API_KEY}") {
		t.Fatal("the api key env reference must survive the compose")
	}
}

func TestComposeConfigContentDefaults(t *testing.T) {
	content, err := ComposeConfigContent(ManagedOverlay{})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != defaultOpencodeModel {
		t.Fatalf("default model = %v, want %s", parsed["model"], defaultOpencodeModel)
	}
}

func TestDetectOverlayConflicts(t *testing.T) {
	overlay := ManagedOverlay{Model: "albert/gpt-oss-120b"}
	project := map[string]any{
		"model":               "albert/deepseek-v4-flash",
		"totally_unknown_key": "kept",
	}
	conflicts := DetectOverlayConflicts(project, overlay)
	if len(conflicts) != 1 || conflicts[0].Field != "model" {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	if conflicts[0].ProjectValue != "albert/deepseek-v4-flash" || conflicts[0].ManagedValue != "albert/gpt-oss-120b" {
		t.Fatalf("conflict values = %+v", conflicts[0])
	}
	// Matching values are not conflicts; unknown keys are ignored.
	agreeing := map[string]any{"model": "albert/gpt-oss-120b", "other": 1}
	if conflicts := DetectOverlayConflicts(agreeing, overlay); len(conflicts) != 0 {
		t.Fatalf("agreeing project must not conflict: %+v", conflicts)
	}
	// Absent fields are not conflicts.
	if conflicts := DetectOverlayConflicts(map[string]any{}, overlay); len(conflicts) != 0 {
		t.Fatalf("empty project must not conflict: %+v", conflicts)
	}
	// A non-string project value is reported with its raw JSON.
	weird := map[string]any{"model": 42}
	conflicts = DetectOverlayConflicts(weird, overlay)
	if len(conflicts) != 1 || conflicts[0].ProjectValue != "42" {
		t.Fatalf("non-string value = %+v", conflicts)
	}
}

func TestFormatConflictsNamesBothValues(t *testing.T) {
	msg := FormatConflicts([]ConfigConflict{{Field: "model", ProjectValue: "a/b", ManagedValue: "c/d"}})
	if !strings.Contains(msg, "model") || !strings.Contains(msg, "a/b") || !strings.Contains(msg, "c/d") {
		t.Fatalf("message = %q", msg)
	}
	if FormatConflicts(nil) != "" {
		t.Fatal("no conflicts must format empty")
	}
}

func TestDeepMergeMaps(t *testing.T) {
	dst := map[string]any{
		"provider": map[string]any{"albert": map[string]any{"npm": "x", "name": "Albert"}},
		"model":    "old",
	}
	src := map[string]any{
		"provider": map[string]any{"albert": map[string]any{"npm": "y"}},
		"model":    "new",
	}
	merged := deepMergeMaps(dst, src)
	if merged["model"] != "new" {
		t.Fatalf("scalar override = %v", merged["model"])
	}
	albert := merged["provider"].(map[string]any)["albert"].(map[string]any)
	if albert["npm"] != "y" || albert["name"] != "Albert" {
		t.Fatalf("nested merge = %v", albert)
	}
	// Inputs are not mutated.
	if dst["model"] != "old" {
		t.Fatal("dst was mutated")
	}
}
