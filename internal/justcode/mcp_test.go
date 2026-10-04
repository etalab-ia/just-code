package justcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateMCPConnectorIDs(t *testing.T) {
	got, err := ValidateMCPConnectorIDs([]string{" context7 ", "data-gouv", "context7"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "context7,data-gouv" {
		t.Fatalf("connector IDs = %v", got)
	}
	if _, err := ValidateMCPConnectorIDs([]string{"playwright"}); err == nil {
		t.Fatal("uncurated connector was accepted")
	}
}

func TestComposeConfigContentAddsOnlySelectedRemoteMCPs(t *testing.T) {
	content, err := ComposeConfigContent(ManagedOverlay{MCPConnectors: []string{"data-gouv", "context7"}})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	servers := config["mcp"].(map[string]any)
	for name, endpoint := range map[string]string{
		"data-gouv": "https://mcp.data.gouv.fr/mcp",
		"context7":  "https://mcp.context7.com/mcp",
	} {
		entry := servers[name].(map[string]any)
		if entry["type"] != "remote" || entry["url"] != endpoint || entry["enabled"] != true {
			t.Errorf("unexpected %s entry: %#v", name, entry)
		}
	}
	if strings.Contains(content, "playwright") || strings.Contains(content, "chrome-devtools") {
		t.Fatal("P15 configuration included unrelated browser tooling")
	}
}

func TestDeselectedMCPIsAbsentFromOverlay(t *testing.T) {
	content, err := ComposeConfigContent(ManagedOverlay{MCPConnectors: []string{"context7"}})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	servers := config["mcp"].(map[string]any)
	if _, ok := servers["context7"]; !ok {
		t.Fatal("selected Context7 entry missing")
	}
	if _, ok := servers["data-gouv"]; ok {
		t.Fatal("deselected data.gouv entry remains")
	}
}

func TestDetectOverlayConflictsForManagedMCPName(t *testing.T) {
	project := map[string]any{"mcp": map[string]any{"context7": map[string]any{"type": "remote", "url": "https://other.example/mcp"}}}
	conflicts := DetectOverlayConflicts(project, ManagedOverlay{MCPConnectors: []string{"context7"}})
	if len(conflicts) != 1 || conflicts[0].Field != "mcp.context7" {
		t.Fatalf("conflicts = %#v", conflicts)
	}
}

func TestProbeRemoteMCPDistinguishesVerificationAuthenticationAndDrift(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   MCPHealthState
	}{
		{"verified", http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`, MCPVerified},
		{"authentication required", http.StatusUnauthorized, `{}`, MCPAuthNeeded},
		{"bad credentials", http.StatusForbidden, `{}`, MCPAuthError},
		{"endpoint drift", http.StatusNotFound, `{}`, MCPDrift},
		{"schema drift", http.StatusOK, `{"unexpected":true}`, MCPDrift},
		{"mismatched JSON-RPC response ID", http.StatusOK, `{"jsonrpc":"2.0","id":2,"result":{"protocolVersion":"2025-03-26"}}`, MCPDrift},
		{"unsupported protocol version", http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2030-01-01"}}`, MCPDrift},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("request = %s %s content-type=%q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result := ProbeRemoteMCP(context.Background(), server.Client(), MCPConnector{ID: "test", Endpoint: server.URL})
			if result.State != tc.want {
				t.Fatalf("state = %q, want %q (%s)", result.State, tc.want, result.Detail)
			}
		})
	}
}

func TestProbeRemoteMCPReportsUnavailableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()
	result := ProbeRemoteMCP(context.Background(), nil, MCPConnector{ID: "offline", Endpoint: endpoint})
	if result.State != MCPOffline {
		t.Fatalf("state = %q, want offline", result.State)
	}
}
