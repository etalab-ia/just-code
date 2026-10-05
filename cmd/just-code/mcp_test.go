package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/etalab-ia/just-code/internal/justcode"
)

func stubMCPProject(t *testing.T, ids []string) {
	t.Helper()
	root := t.TempDir()
	if err := justcode.WriteProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(root), justcode.ProjectManifest{MCPConnectors: ids}); err != nil {
		t.Fatal(err)
	}
	old := discoverMCPProjectRootFn
	discoverMCPProjectRootFn = func() (string, error) { return root, nil }
	t.Cleanup(func() { discoverMCPProjectRootFn = old })
}

func TestMCPStatusWithoutManagedConnectorsDoesNotProbe(t *testing.T) {
	stubMCPProject(t, nil)
	oldProbe := probeRemoteMCPFn
	probeCalls := 0
	probeRemoteMCPFn = func(context.Context, *http.Client, justcode.MCPConnector) justcode.MCPHealth {
		probeCalls++
		return justcode.MCPHealth{}
	}
	t.Cleanup(func() { probeRemoteMCPFn = oldProbe })
	if code, err := mcpCommand([]string{"status"}); code != 0 || err != nil || probeCalls != 0 {
		t.Fatalf("mcp status: code=%d err=%v probes=%d", code, err, probeCalls)
	}
}

func TestMCPStatusReturnsFailureForUnverifiedEndpoint(t *testing.T) {
	stubMCPProject(t, []string{"data-gouv"})
	oldProbe := probeRemoteMCPFn
	probeRemoteMCPFn = func(context.Context, *http.Client, justcode.MCPConnector) justcode.MCPHealth {
		return justcode.MCPHealth{State: justcode.MCPOffline, Detail: "offline"}
	}
	t.Cleanup(func() { probeRemoteMCPFn = oldProbe })
	if code, err := mcpCommand([]string{"status"}); code != 1 || err == nil {
		t.Fatalf("mcp status: code=%d err=%v, want verification failure", code, err)
	}
}
