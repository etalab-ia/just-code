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

var mcpCatalogue = map[string]MCPConnector{
	"data-gouv": {ID: "data-gouv", Name: "data-gouv", Endpoint: "https://mcp.data.gouv.fr/mcp", Destination: "data.gouv.fr", Transport: "Streamable HTTP", HealthAction: "MCP initialize", Credential: "none"},
	"context7":  {ID: "context7", Name: "context7", Endpoint: "https://mcp.context7.com/mcp", Destination: "context7.com", Transport: "Streamable HTTP", HealthAction: "MCP initialize", Credential: "optional; anonymous access supported"},
}

// MCPConnectors lists the curated catalogue in stable display order.
func MCPConnectors() []MCPConnector {
	return []MCPConnector{mcpCatalogue["data-gouv"], mcpCatalogue["context7"]}
}

// ValidateMCPConnectorIDs validates and canonicalizes a user selection.
func ValidateMCPConnectorIDs(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if _, ok := mcpCatalogue[id]; !ok {
			return nil, fmt.Errorf("unknown remote MCP %q (expected data-gouv or context7)", id)
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
		connector := mcpCatalogue[id]
		entries[connector.Name] = map[string]any{
			"type":    "remote",
			"url":     connector.Endpoint,
			"enabled": true,
		}
	}
	return entries, nil
}

func canonicalMCPEntry(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
