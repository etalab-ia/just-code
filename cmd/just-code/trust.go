package main

import (
	"fmt"
	"os"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// trustCmd implements `just-code trust <subcommand>` (P10): the host-local
// approval of execution-relevant project OpenCode inputs (project plugins,
// auto-discovered plugins, MCP server commands). The record lives in host
// state keyed by the canonical project root and content hashes — never in
// the repository.
//
//	trust status    # what is declared, what is approved, what changed
//	trust approve   # record approval for the current content of every input
func trustCmd(args []string, projectRoot string) (int, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: just-code trust status | approve")
		return 2, nil
	}
	switch args[0] {
	case "status":
		return trustStatusCmd(projectRoot)
	case "approve":
		if err := justcode.ApproveExecutionInputs(justcode.DefaultFS, justcode.DefaultStateDir(), projectRoot); err != nil {
			return 1, err
		}
		fmt.Println("Approved the current content of every execution input this project declares.")
		fmt.Println("The record is host-local and never travels with the repository; a changed file needs a new approval.")
		return 0, nil
	default:
		return 2, fmt.Errorf("Unknown trust command: %s (expected status or approve)", args[0])
	}
}

// trustStatusCmd reports the declared execution inputs and their approval
// state, without creating or changing anything.
func trustStatusCmd(projectRoot string) (int, error) {
	inputs, err := justcode.DiscoverProjectExecutionInputs(projectRoot)
	if err != nil {
		return 1, err
	}
	unapproved, err := justcode.DiscoverUnapprovedInputs(justcode.DefaultFS, justcode.DefaultStateDir(), projectRoot)
	if err != nil {
		return 1, err
	}
	if inputs.ConfigPath == "" && len(inputs.AutoDiscoveredPlugins) == 0 {
		fmt.Println("No execution-relevant OpenCode inputs found in this project.")
		return 0, nil
	}
	fmt.Println("Execution-relevant OpenCode inputs in this project:")
	if inputs.ConfigPath != "" && (len(inputs.Plugins) > 0 || len(inputs.MCPCommands) > 0) {
		for _, p := range inputs.Plugins {
			fmt.Printf("  plugin declared in the project config: %s\n", p)
		}
		for _, c := range inputs.MCPCommands {
			fmt.Printf("  MCP server command declared in the project config: %s\n", c)
		}
		fmt.Printf("  (approved as the config file %s at its content hash)\n", inputs.ConfigPath)
	}
	for _, p := range inputs.AutoDiscoveredPlugins {
		fmt.Printf("  auto-discovered plugin (executes without declaration): %s\n", p)
	}
	if len(unapproved) == 0 {
		fmt.Println("All inputs are approved at their current content.")
		return 0, nil
	}
	fmt.Println("Needs approval (run 'just-code trust approve' after reviewing):")
	for _, u := range unapproved {
		fmt.Printf("  %s (%s)\n", u.Path, u.Reason)
	}
	return 0, nil
}
