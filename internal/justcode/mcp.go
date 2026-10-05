package justcode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MCPConnector is a curated remote integration. Credentials are intentionally
// not part of its configuration: Context7 permits anonymous access, and its
// optional key remains an independently approved host binding.
type MCPConnector struct {
	ID           string
	Name         string
	Endpoint     string
	Destination  string
	Transport    string
	HealthAction string
	Credential   string
}

// BrowserMCP is a version-pinned local MCP process installed in the selected
// guest. It is kept separate from remote endpoints because it has no URL or
// host-side health probe.
type BrowserMCP struct {
	ID      string
	Name    string
	Package string
	Version string
	Command string
	Args    []string
}

type MCPSelection struct {
	ID          string
	Description string
	Local       bool
}

var mcpCatalogue = map[string]MCPConnector{
	"data-gouv": {ID: "data-gouv", Name: "data-gouv", Endpoint: "https://mcp.data.gouv.fr/mcp", Destination: "data.gouv.fr", Transport: "Streamable HTTP", HealthAction: "MCP initialize", Credential: "none"},
	"context7":  {ID: "context7", Name: "context7", Endpoint: "https://mcp.context7.com/mcp", Destination: "context7.com", Transport: "Streamable HTTP", HealthAction: "MCP initialize", Credential: "optional; anonymous access supported"},
}

// MCPConnectors lists the curated catalogue in stable display order.
func MCPConnectors() []MCPConnector {
	return []MCPConnector{mcpCatalogue["data-gouv"], mcpCatalogue["context7"]}
}

var browserMPCCatalogue = map[string]BrowserMCP{
	"playwright": {
		ID: "playwright", Name: "playwright", Package: "@playwright/mcp", Version: "0.0.82", Command: "playwright-mcp",
		Args: []string{"--headless", "--browser", "chromium", "--executable-path", "/usr/bin/chromium"},
	},
	"chrome-devtools": {
		ID: "chrome-devtools", Name: "chrome-devtools", Package: "chrome-devtools-mcp", Version: "1.10.1", Command: "chrome-devtools-mcp",
		Args: []string{"--browserUrl", "http://127.0.0.1:9222"},
	},
}

// BrowserMCPs lists the individually selectable guest-local browser tools.
func BrowserMCPs() []BrowserMCP {
	return []BrowserMCP{browserMPCCatalogue["playwright"], browserMPCCatalogue["chrome-devtools"]}
}

func BrowserMCPByID(id string) (BrowserMCP, bool) {
	connector, ok := browserMPCCatalogue[id]
	return connector, ok
}

func browserMCPIDs(ids []string) []string {
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	var out []string
	for _, connector := range BrowserMCPs() {
		if selected[connector.ID] {
			out = append(out, connector.ID)
		}
	}
	return out
}

// HasBrowserMCPSelection reports whether a selection requires the browser guest profile.
func HasBrowserMCPSelection(ids []string) bool {
	return len(browserMCPIDs(ids)) > 0
}

func browserMCPInstallPackages(ids []string) []string {
	var packages []string
	for _, id := range browserMCPIDs(ids) {
		connector := browserMPCCatalogue[id]
		packages = append(packages, connector.Package+"@"+connector.Version)
	}
	return packages
}

func browserMCPBinaries(ids []string) []string {
	var binaries []string
	for _, id := range browserMCPIDs(ids) {
		binaries = append(binaries, browserMPCCatalogue[id].Command)
	}
	return binaries
}

// MCPSelections returns the full user-facing choice list in stable order.
func MCPSelections() []MCPSelection {
	choices := make([]MCPSelection, 0, len(mcpCatalogue)+len(browserMPCCatalogue))
	for _, connector := range MCPConnectors() {
		choices = append(choices, MCPSelection{ID: connector.ID, Description: connector.Endpoint + " (remote)"})
	}
	for _, connector := range BrowserMCPs() {
		choices = append(choices, MCPSelection{ID: connector.ID, Description: connector.Package + "@" + connector.Version + " (guest-local)", Local: true})
	}
	return choices
}

// ValidateMCPConnectorIDs validates and canonicalizes a user selection.
func ValidateMCPConnectorIDs(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if _, remote := mcpCatalogue[id]; !remote {
			if _, local := browserMPCCatalogue[id]; !local {
				return nil, fmt.Errorf("unknown managed MCP %q (expected data-gouv, context7, playwright or chrome-devtools)", id)
			}
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func containsMCPConnector(ids []string, wanted string) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func mcpOverlay(ids []string) (map[string]any, error) {
	valid, err := ValidateMCPConnectorIDs(ids)
	if err != nil {
		return nil, err
	}
	// The managed entries are anonymous by design: no headers, no env
	// reference. The proxied CONTEXT7_API_KEY binding serves user-owned
	// `mcp` entries in the guest's own config, not this curated overlay.
	entries := make(map[string]any, len(valid))
	for _, id := range valid {
		connector, ok := mcpCatalogue[id]
		if !ok {
			continue
		}
		entries[connector.Name] = map[string]any{
			"type":    "remote",
			"url":     connector.Endpoint,
			"enabled": true,
		}
	}
	return entries, nil
}

func browserMCPOverlay(ids []string) (map[string]any, error) {
	valid, err := ValidateMCPConnectorIDs(ids)
	if err != nil {
		return nil, err
	}
	entries := make(map[string]any)
	for _, id := range valid {
		connector, ok := browserMPCCatalogue[id]
		if !ok {
			continue
		}
		command := append([]string{connector.Command}, connector.Args...)
		entries[connector.Name] = map[string]any{
			"type":    "local",
			"command": command,
			"enabled": true,
		}
	}
	return entries, nil
}

func managedMCPOverlay(ids []string) (map[string]any, error) {
	remote, err := mcpOverlay(ids)
	if err != nil {
		return nil, err
	}
	local, err := browserMCPOverlay(ids)
	if err != nil {
		return nil, err
	}
	return deepMergeMaps(remote, local), nil
}

func canonicalMCPEntry(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
