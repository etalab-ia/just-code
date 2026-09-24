package justcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file reads the OpenCode project configuration, read-only. Per D-001:
// JSONC is discovered at four locations, comments are legal, and the file is
// never rewritten. just-code parses it only to detect managed-field
// conflicts and execution-relevant inputs (plugins, MCP servers).

// opencodeConfigFiles lists the project config locations in OpenCode's
// discovery order, relative to the project root.
var opencodeConfigFiles = []string{
	"opencode.json",
	"opencode.jsonc",
	".opencode/opencode.json",
	".opencode/opencode.jsonc",
}

// FindProjectOpenCodeConfig returns the first existing OpenCode config path
// under root, or "" when none exists. Discovery order matches OpenCode's
// (files at the root before files in .opencode/).
func FindProjectOpenCodeConfig(root string) string {
	for _, rel := range opencodeConfigFiles {
		path := filepath.Join(root, rel)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path
		}
	}
	return ""
}

// stripJSONCComments removes // line comments and /* */ block comments that
// are outside string literals, so JSONC can be parsed as JSON. Trailing
// commas are also removed. The transformation is intentionally conservative:
// comment markers inside string values are preserved (the scanner tracks
// string state), and anything it cannot classify is left in place for the
// JSON parser to reject loudly rather than silently misparse.
func stripJSONCComments(src string) string {
	var b strings.Builder
	inString := false
	escaped := false
	i := 0
	for i < len(src) {
		c := src[i]
		if inString {
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}
		switch {
		case c == '"':
			inString = true
			b.WriteByte(c)
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			// Line comment: skip to the newline.
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			// Block comment: skip to the closing marker.
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// stripTrailingCommas removes commas that directly precede a closing brace
// or bracket (outside strings), which JSONC permits and JSON rejects.
func stripTrailingCommas(src string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == ',' {
			// Look ahead past whitespace for a closer.
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
				j++
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue // drop the comma
			}
		}
		if c == '"' {
			inString = true
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ParseProjectOpenCodeConfig reads and parses the config at path (JSON or
// JSONC) into a generic map. It is strictly read-only. An empty path returns
// an empty map and no error (no project config is a normal state). A
// malformed file is an error: the conflict detector must not silently treat
// an unparseable project config as "no conflicts".
func ParseProjectOpenCodeConfig(path string) (map[string]any, error) {
	if path == "" {
		return map[string]any{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read project OpenCode config %s: %w", path, err)
	}
	cleaned := stripTrailingCommas(stripJSONCComments(string(raw)))
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return nil, fmt.Errorf("parse project OpenCode config %s: %w", path, err)
	}
	return parsed, nil
}

// ProjectExecutionInputs is the set of project-declared, execution-relevant
// OpenCode inputs just-code must gate behind host-local trust approval
// before OpenCode loads them. Per D-001: declared AND auto-discovered
// plugins execute arbitrary code at config load; MCP server commands launch
// host processes from the project config.
type ProjectExecutionInputs struct {
	// ConfigPath is the project OpenCode config path, when one exists.
	ConfigPath string
	// Plugins lists project plugin declarations (the "plugin" field values
	// from the config).
	Plugins []string
	// AutoDiscoveredPlugins lists .js/.ts files under .opencode/plugin/
	// and .opencode/plugins/ — they execute without any declaration.
	AutoDiscoveredPlugins []string
	// MCPCommands lists the "command" of every mcp server entry declared in
	// the project config (local servers only; remote URLs are endpoints,
	// gated by the endpoint check instead).
	MCPCommands []string
}

// DiscoverProjectExecutionInputs inspects the project root for the inputs
// that execute code or launch processes when OpenCode loads. The config is
// parsed read-only; plugin files are listed, never executed.
func DiscoverProjectExecutionInputs(root string) (ProjectExecutionInputs, error) {
	out := ProjectExecutionInputs{}
	cfgPath := FindProjectOpenCodeConfig(root)
	out.ConfigPath = cfgPath
	if cfgPath != "" {
		cfg, err := ParseProjectOpenCodeConfig(cfgPath)
		if err != nil {
			return out, err
		}
		if plugins, ok := cfg["plugin"].([]any); ok {
			for _, p := range plugins {
				if s, ok := p.(string); ok {
					out.Plugins = append(out.Plugins, s)
				}
			}
		}
		if mcp, ok := cfg["mcp"].(map[string]any); ok {
			for _, server := range mcp {
				m, ok := server.(map[string]any)
				if !ok {
					continue
				}
				// Local servers carry a command; remote servers carry a URL.
				if cmd, ok := m["command"].(string); ok && cmd != "" {
					out.MCPCommands = append(out.MCPCommands, cmd)
				}
			}
		}
	}
	for _, dir := range []string{".opencode/plugin", ".opencode/plugins"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext == ".js" || ext == ".ts" {
				out.AutoDiscoveredPlugins = append(out.AutoDiscoveredPlugins, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(out.Plugins)
	sort.Strings(out.AutoDiscoveredPlugins)
	sort.Strings(out.MCPCommands)
	return out, nil
}
