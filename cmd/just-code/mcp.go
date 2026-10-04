package main

import (
	"context"
	"fmt"
	"os"

	"github.com/etalab-ia/just-code/internal/justcode"
)

var discoverMCPProjectRootFn = func() (string, error) {
	project, err := justcode.DiscoverProject(".")
	if err != nil {
		return "", err
	}
	return project.Root, nil
}

var probeRemoteMCPFn = justcode.ProbeRemoteMCP

func mcpCommand(args []string) (int, error) {
	if len(args) != 1 || args[0] != "status" {
		return 2, fmt.Errorf("Usage: just-code mcp status")
	}
	projectRoot, err := discoverMCPProjectRootFn()
	if err != nil {
		return 1, err
	}
	manifest, err := justcode.ReadProjectManifest(justcode.DefaultFS, justcode.ProjectManifestPath(projectRoot))
	if err != nil {
		return 1, err
	}
	ids, err := justcode.ValidateMCPConnectorIDs(manifest.MCPConnectors)
	if err != nil {
		return 1, err
	}
	if len(ids) == 0 {
		fmt.Println("No managed remote MCPs configured.")
		return 0, nil
	}
	failed := false
	for _, id := range ids {
		connector := connectorByID(id)
		health := probeRemoteMCPFn(context.Background(), nil, connector)
		fmt.Fprintf(os.Stdout, "%-10s %-28s configured -> %s (%s): %s\n", id, connector.Endpoint, health.State, connector.HealthAction, health.Detail)
		if health.State != justcode.MCPVerified {
			failed = true
		}
	}
	if failed {
		return 1, fmt.Errorf("one or more remote MCPs could not be verified")
	}
	return 0, nil
}

func connectorByID(id string) justcode.MCPConnector {
	for _, connector := range justcode.MCPConnectors() {
		if connector.ID == id {
			return connector
		}
	}
	return justcode.MCPConnector{}
}
