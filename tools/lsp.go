// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

// The lsp tool implementation lives in the tools/lsp package, grouped by
// function: tool contract and entry (tool.go), request routing (routing.go),
// navigation queries (navigation.go), refactoring ops (refactor.go), and
// result rendering (display.go). This file keeps the historical LSPTool name
// available to the registry, bootstrap, and tests.

import "github.com/pijalu/goa/tools/lsp"

// LSPTool is the language-server navigation tool; see tools/lsp.Tool.
type LSPTool = lsp.Tool

// LSPQueryManager is the subset of the LSP manager used by the lsp tool.
type LSPQueryManager = lsp.LSPQueryManager
