// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/mcp"
)

// runMCPCLI handles `goa mcp <subcommand> [args...]` — installing, listing,
// and removing MCP servers without entering the TUI. It returns true when the
// args were an mcp invocation (the caller should exit afterwards).
//
// The subcommand set mirrors the /mcp slash command; both share MCPCommand so
// parsing, persistence (project vs --global), and validation stay identical.
// `add` also verifies the server by connecting once (OpenCode behavior).
func runMCPCLI(args []string) bool {
	if len(args) == 0 || args[0] != "mcp" {
		return false
	}
	rest := args[1:]
	if len(rest) == 0 {
		rest = []string{"list"}
	}
	if rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		fmt.Print(mcpCLIUsage)
		os.Exit(0)
	}

	projectDir := MustGetwd()
	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goa mcp: load config: %v\n", err)
		os.Exit(1)
	}

	mgr := mcp.NewManager(nil)
	mgr.SetProjectDir(projectDir)
	ctx := core.Context{
		Config:      cfg,
		ProjectDir:  projectDir,
		ConfigSaver: loader,
		MCP:         mgr,
		// OutputBuffer nil → Writef prints to stdout.
	}

	cmd := &commands.MCPCommand{}
	if err := cmd.Run(ctx, rest); err != nil {
		fmt.Fprintf(os.Stderr, "goa mcp: %v\n", err)
		os.Exit(1)
	}
	return true
}

const mcpCLIUsage = `goa mcp — manage MCP servers from the shell

Usage:
  goa mcp list                                        List servers with status
  goa mcp add [--global] <name> --url <u> [--header K=V]...
  goa mcp add [--global] <name> -- <cmd...> [--env K=V]...
  goa mcp remove [--global] <name>                    Uninstall a server
  goa mcp enable|disable [--global] <name>            Toggle a server (persisted)
  goa mcp reconnect <name>                            Drop and reconnect
  goa mcp debug <name>                                One-shot connect + tool count

Scope: servers are saved to the project .goa/config.yaml by default;
--global (-g) saves to ~/.goa/config.yaml so the server is available
in every project. A project server with the same name overrides the global one.

Examples:
  goa mcp add chrome-devtools -- npx -y chrome-devtools-mcp@latest
  goa mcp add --global github --url https://api.githubcopilot.com/mcp/ \
      --header Authorization=Bearer $GITHUB_TOKEN
`
