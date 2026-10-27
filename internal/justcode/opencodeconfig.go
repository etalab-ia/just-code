package justcode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file implements the P10 OpenCode configuration composer. Per
// docs/decisions/2026-09-23-opencode-configuration-contract.md (D-001):
//
//   - OPENCODE_CONFIG_CONTENT is OpenCode's official final-merge mechanism:
//     the inline content deep-merges on top of the user global and project
//     configs, so the managed overlay must carry ONLY managed fields.
//   - OpenCode silently ignores unknown top-level keys, so just-code must
//     detect managed-field conflicts itself, before overriding user settings.
//   - User JSONC files are never rewritten; the managed layer lives in the
//     inline overlay exclusively.

// ManagedOverlay is the set of OpenCode configuration fields just-code
// manages. Only these fields may appear in the overlay: every field placed
// here overrides the same field from the project and user configs (final
// merge wins), so an unconsidered addition silently disables user settings.
// Selected curated MCP entries are added separately by the catalogue.
type ManagedOverlay struct {
	// Model is the effective model ("provider/model-id"). Empty keeps the
	// embedded default.
	Model string
	// SmallModel is the model for background tasks. Defaults to Model.
	SmallModel string
	// Models replaces the managed Albert provider model map when non-empty.
	Models []AlbertModelConfig
	// MCPConnectors selects managed remote and guest-local entries. User-owned
	// entries with other names remain untouched.
	MCPConnectors []string
}

// AlbertModelConfig is one curated model exposed by the managed Albert provider.
type AlbertModelConfig struct {
	ID          string         `json:"id"`
	ModelID     string         `json:"modelID,omitempty"`
	Name        string         `json:"name"`
	Context     int            `json:"context,omitempty"`
	Output      int            `json:"output,omitempty"`
	RequestBody map[string]any `json:"requestBody,omitempty"`
	Vision      bool           `json:"vision,omitempty"`
}

// opencodeManagedField describes one top-level field the overlay may set.
type opencodeManagedField struct {
	name  string
	value func(o ManagedOverlay) string
}

// managedOverlayFields lists the scalar managed fields in a stable order.
// Curated MCP entries are checked separately against the connector catalogue.
var managedOverlayFields = []opencodeManagedField{
	{"model", func(o ManagedOverlay) string { return o.Model }},
	{"small_model", func(o ManagedOverlay) string { return o.SmallModel }},
}

// EffectiveOverlay returns the overlay with defaults applied: the embedded
// model when none is chosen, and SmallModel following Model.
func (o ManagedOverlay) EffectiveOverlay() ManagedOverlay {
	if o.Model == "" {
		o.Model = defaultOpencodeModel
	}
	if o.SmallModel == "" {
		o.SmallModel = o.Model
	}
	return o
}

// defaultOpencodeModel mirrors assets/opencode-config.json's "model" field.
// The embedded asset stays the baseline; this constant is what the composer
// writes when the user selected nothing.
const defaultOpencodeModel = "albert/qwen3-coder"

// ComposeConfigContent builds the full OPENCODE_CONFIG_CONTENT from the
// embedded base and the managed overlay by deep-merging them host-side: the
// base's provider block and permissions survive, and the overlay's managed
// fields override their base counterparts. This keeps one document (no
// reliance on OpenCode parsing multiple concatenated documents) while the
// project and user configs still merge underneath at load time.
func ComposeConfigContent(o ManagedOverlay) (string, error) {
	var base map[string]any
	if err := json.Unmarshal([]byte(opencodeConfigContent), &base); err != nil {
		return "", fmt.Errorf("parse embedded OpenCode config: %w", err)
	}
	overlayJSON, err := renderOverlayJSON(o.EffectiveOverlay())
	if err != nil {
		return "", err
	}
	var overlay map[string]any
	if err := json.Unmarshal([]byte(overlayJSON), &overlay); err != nil {
		return "", fmt.Errorf("parse rendered overlay: %w", err)
	}
	if len(o.Models) > 0 {
		provider, _ := base["provider"].(map[string]any)
		albert, _ := provider["albert"].(map[string]any)
		if albert != nil {
			albert["models"] = map[string]any{}
		}
	}
	merged := deepMergeMaps(base, overlay)
	out, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// deepMergeMaps merges src over dst recursively, map values merging and all
// other values replacing. Neither input is mutated.
func deepMergeMaps(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, ok := out[k].(map[string]any); ok {
				out[k] = deepMergeMaps(dstMap, srcMap)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// renderOverlayJSON renders the managed overlay as the JSON injected into
// OPENCODE_CONFIG_CONTENT. It carries managed fields ONLY — the provider
// block, permissions and schema come from the embedded asset, which remains
// the base configuration; this overlay merges on top of it.
func renderOverlayJSON(o ManagedOverlay) (string, error) {
	fields := map[string]any{}
	for _, f := range managedOverlayFields {
		if v := f.value(o); v != "" {
			fields[f.name] = v
		}
	}
	if len(o.Models) > 0 {
		models := make(map[string]any, len(o.Models))
		for _, model := range o.Models {
			definition := map[string]any{"name": model.Name}
			if model.ModelID != "" {
				definition["modelID"] = model.ModelID
			}
			inputs := []string{"text"}
			if model.Vision {
				inputs = append(inputs, "image")
			}
			definition["capabilities"] = map[string]any{
				"tools":  true,
				"input":  inputs,
				"output": []string{"text"},
			}
			limit := map[string]any{}
			if model.Context > 0 {
				limit["context"] = model.Context
			}
			if model.Output > 0 {
				limit["output"] = model.Output
			}
			if len(limit) > 0 {
				definition["limit"] = limit
			}
			if len(model.RequestBody) > 0 {
				definition["body"] = model.RequestBody
			}
			models[model.ID] = definition
		}
		fields["provider"] = map[string]any{"albert": map[string]any{"models": models}}
	}
	if len(o.MCPConnectors) > 0 {
		mcp, err := managedMCPOverlay(o.MCPConnectors)
		if err != nil {
			return "", err
		}
		fields["mcp"] = mcp
	}
	// Stable key order: the content travels through env files sourced by
	// shells and is diffed in tests; a canonical form avoids noise.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return "", err
		}
		vb, err := json.Marshal(fields[k])
		if err != nil {
			return "", err
		}
		b.Write(kb)
		b.WriteString(":")
		b.Write(vb)
	}
	b.WriteString("}")
	return b.String(), nil
}

// ConfigConflict is one managed field set differently by the project config.
// The project value is the one OpenCode would otherwise use; the overlay
// wins by design (final merge), so the conflict is surfaced, not silently
// applied.
type ConfigConflict struct {
	Field        string
	ProjectValue string
	ManagedValue string
}

// DetectOverlayConflicts compares the managed overlay against a parsed
// project config (any JSON object; JSONC is pre-stripped by the caller). A
// conflict exists when the project sets a managed field to a different value
// than the overlay: the final-merge semantics mean the overlay would
// silently override the project. Unknown top-level keys are ignored —
// OpenCode ignores them too, and they are the user's to manage.
func DetectOverlayConflicts(project map[string]any, overlay ManagedOverlay) []ConfigConflict {
	overlay = overlay.EffectiveOverlay()
	var conflicts []ConfigConflict
	for _, f := range managedOverlayFields {
		managed := f.value(overlay)
		if managed == "" {
			continue
		}
		raw, present := project[f.name]
		if !present {
			continue
		}
		projectValue, ok := raw.(string)
		if !ok {
			// A non-string value in a string field is a project misshape the
			// overlay would replace; report it with its raw JSON.
			rawJSON, _ := json.Marshal(raw)
			projectValue = string(rawJSON)
		}
		if projectValue != managed {
			conflicts = append(conflicts, ConfigConflict{
				Field:        f.name,
				ProjectValue: projectValue,
				ManagedValue: managed,
			})
		}
	}
	managedMCP, err := managedMCPOverlay(overlay.MCPConnectors)
	if err == nil {
		projectMCP, _ := project["mcp"].(map[string]any)
		for name, managed := range managedMCP {
			if projectValue, exists := projectMCP[name]; exists && canonicalMCPEntry(projectValue) != canonicalMCPEntry(managed) {
				conflicts = append(conflicts, ConfigConflict{Field: "mcp." + name, ProjectValue: canonicalMCPEntry(projectValue), ManagedValue: canonicalMCPEntry(managed)})
			}
		}
	}
	return conflicts
}

// FormatConflicts renders the conflict list as user-facing lines, one per
// field, with both values named. The overlay still wins (final merge), so
// the message states that rather than implying the project was preserved.
func FormatConflicts(conflicts []ConfigConflict) string {
	if len(conflicts) == 0 {
		return ""
	}
	sorted := append([]ConfigConflict(nil), conflicts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Field < sorted[j].Field })
	var lines []string
	for _, c := range sorted {
		lines = append(lines, fmt.Sprintf("  %s: project=%s, just-code=%s (the just-code value wins; set the project field to match or change the managed selection)", c.Field, c.ProjectValue, c.ManagedValue))
	}
	return strings.Join(lines, "\n")
}

// OpenCodeDisableUpdateEnv turns off OpenCode's update check in a process.
//
// The guest belongs to just-code: its OpenCode is either the pinned version
// (Microsandbox) or the vendor default just-code installed at first launch
// (Tart, agent-vm). An update notice invites the user out of the set just-code
// supports, and in the pinned case it cannot even be honoured. `opencode
// upgrade` stays available for a deliberate upgrade.
//
// Why the environment variable and not the `autoupdate` setting: in the pinned
// 1.18.32 sources (packages/core/src/flag/flag.ts, config/config.ts,
// cli/upgrade.ts), upgrade() reads `autoupdate` through getGlobal(), which
// loads only the guest's global config files (Global.Path.config/config.json,
// opencode.json, opencode.jsonc). just-code's managed settings travel inline in
// OPENCODE_CONFIG_CONTENT, which getGlobal() never reads, so "autoupdate":
// false there is silently inert. Writing the guest's global config would work,
// but managed settings do not live in a file the user also edits. upgrade()
// tests OPENCODE_DISABLE_AUTOUPDATE before anything else, so every launch path
// must carry it. A bare `just-code shell` is the one path it cannot reach: that
// shell sources neither the managed config nor these variables.
const OpenCodeDisableUpdateEnv = "OPENCODE_DISABLE_AUTOUPDATE"

// OpenCodeDisableUpdateAssignment is the KEY=VALUE form for exec environments
// and sourced secrets files.
const OpenCodeDisableUpdateAssignment = OpenCodeDisableUpdateEnv + "=1"
