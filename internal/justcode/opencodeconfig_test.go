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

func TestComposeConfigContentRestrictsAlbertModelsToSelection(t *testing.T) {
	choice := CuratedAlbertModels()[1]
	content, err := ComposeConfigContent(ManagedOverlay{
		Model:  choice.Ref(),
		Models: []AlbertModelConfig{choice.AlbertModelConfig()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatal(err)
	}
	provider := parsed["provider"].(map[string]any)["albert"].(map[string]any)
	models := provider["models"].(map[string]any)
	if len(models) != 1 {
		t.Fatalf("models = %v; unselected base models must not remain visible", models)
	}
	deepseek := models[choice.OpenCodeID].(map[string]any)
	if deepseek["name"] != choice.Name || deepseek["limit"].(map[string]any)["context"] != float64(choice.Context) {
		t.Fatalf("selected model definition = %v", deepseek)
	}
	if deepseek["limit"].(map[string]any)["output"] != float64(65536) {
		t.Fatalf("DeepSeek output token limit was lost: %v", deepseek["limit"])
	}
	if deepseek["modelID"] != choice.ID {
		t.Fatalf("Albert API model ID = %v, want %s", deepseek["modelID"], choice.ID)
	}
	if deepseek["body"].(map[string]any)["tool_choice"] != "auto" {
		t.Fatalf("DeepSeek tool_choice must be forced to auto: %v", deepseek)
	}
	if deepseek["capabilities"].(map[string]any)["input"].([]any)[0] != "text" {
		t.Fatalf("text model input capabilities = %v", deepseek["capabilities"])
	}
}

func TestComposeConfigContentMarksGemmaAsVisionCapable(t *testing.T) {
	choice := CuratedAlbertModels()[3]
	content, err := ComposeConfigContent(ManagedOverlay{
		Model:  choice.Ref(),
		Models: []AlbertModelConfig{choice.AlbertModelConfig()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatal(err)
	}
	provider := parsed["provider"].(map[string]any)["albert"].(map[string]any)
	gemma := provider["models"].(map[string]any)[choice.OpenCodeID].(map[string]any)
	inputs := gemma["capabilities"].(map[string]any)["input"].([]any)
	if len(inputs) != 2 || inputs[0] != "text" || inputs[1] != "image" {
		t.Fatalf("Gemma input capabilities = %v", inputs)
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

// TestManagedConfigDoesNotRelyOnAutoupdate guards the one mistake that would
// silently restore the notice: moving the opt-out into the managed overlay.
// At the pinned version the updater reads `autoupdate` from the guest global
// config only, so an overlay field compiles, looks right in the composed
// document, and does nothing. The load-bearing mechanism is the environment
// variable, and its presence at each launch site is asserted by the runtime
// tests (guest, agent-vm, Microsandbox).
func TestManagedConfigDoesNotRelyOnAutoupdate(t *testing.T) {
	content, err := ComposeConfigContent(ManagedOverlay{})
	if err != nil {
		t.Fatalf("ComposeConfigContent: %v", err)
	}
	// The positive half: the composed config is real and reachable, so the
	// negative assertion below is not passing on an empty document.
	if !strings.Contains(content, `"albert"`) {
		t.Fatalf("composed config lost the provider block: %s", content)
	}
	if strings.Contains(content, "autoupdate") {
		t.Fatalf("managed config must not carry an inert autoupdate field: %s", content)
	}
}
