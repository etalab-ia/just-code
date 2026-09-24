package justcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProjectOpenCodeConfigJSONC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `{
  // model chosen by the team
  "model": "albert/deepseek-v4-flash",
  "mcp": {
    "docs": { "command": "npx", "args": ["-y", "docs-server"] } /* local */
  },
  "plugin": ["./tools/format.ts",],
}`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseProjectOpenCodeConfig(path)
	if err != nil {
		t.Fatalf("JSONC must parse: %v", err)
	}
	if parsed["model"] != "albert/deepseek-v4-flash" {
		t.Fatalf("model = %v", parsed["model"])
	}
	// Comment markers inside strings survive stripping.
	mcp := parsed["mcp"].(map[string]any)["docs"].(map[string]any)
	if mcp["command"] != "npx" {
		t.Fatalf("mcp command = %v", mcp)
	}
}

func TestStripJSONCCommentsPreservesStringContents(t *testing.T) {
	src := `{"a": "http://x//y", "b": "/* not a comment */"} // trailing`
	got := stripJSONCComments(src)
	if !strings.Contains(got, `"http://x//y"`) {
		t.Fatalf("string slashes must survive: %q", got)
	}
	if !strings.Contains(got, `"/* not a comment */"`) {
		t.Fatalf("string comment markers must survive: %q", got)
	}
	if strings.Contains(got, "// trailing") {
		t.Fatalf("the trailing comment must be stripped: %q", got)
	}
}

func TestParseProjectOpenCodeConfigRejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(`{"model": broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseProjectOpenCodeConfig(path); err == nil {
		t.Fatal("a malformed project config must be an error, never 'no conflicts'")
	}
	// Empty path is a normal state.
	if m, err := ParseProjectOpenCodeConfig(""); err != nil || m == nil {
		t.Fatalf("empty path: %v, %v", m, err)
	}
}

func TestDiscoverProjectExecutionInputs(t *testing.T) {
	dir := t.TempDir()
	// A config with plugins and an MCP command.
	cfg := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(cfg, []byte(`{
		"plugin": ["./tools/p.ts"],
		"mcp": {"docs": {"command": "npx"}, "remote": {"url": "https://example.com/sse"}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Auto-discovered plugins (.js and .ts execute; .mjs does not, per D-001).
	for _, rel := range []string{".opencode/plugin/a.js", ".opencode/plugins/b.ts", ".opencode/plugin/c.mjs"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := DiscoverProjectExecutionInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.ConfigPath != cfg {
		t.Fatalf("config path = %s", inputs.ConfigPath)
	}
	if len(inputs.Plugins) != 1 || inputs.Plugins[0] != "./tools/p.ts" {
		t.Fatalf("plugins = %v", inputs.Plugins)
	}
	if len(inputs.MCPCommands) != 1 || inputs.MCPCommands[0] != "npx" {
		t.Fatalf("mcp commands = %v (remote URLs are not commands)", inputs.MCPCommands)
	}
	if len(inputs.AutoDiscoveredPlugins) != 2 {
		t.Fatalf("auto-discovered = %v (mjs must be excluded)", inputs.AutoDiscoveredPlugins)
	}
}
