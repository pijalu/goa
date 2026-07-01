// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import agentic "github.com/pijalu/goa/internal/agentic"

// Tools returns all tools for the sub-agent.
func Tools(workDir string, logger *agentic.Logger) []agentic.Tool {
	return []agentic.Tool{
		NewReadFileTool(workDir, logger),
		NewEditFileTool(workDir, logger),
		NewRunCommandTool(workDir, logger),
		NewRestClientTool(logger),
	}
}
