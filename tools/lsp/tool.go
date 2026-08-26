// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"fmt"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/lsp"
)

// LSPQueryManager is the subset of the LSP manager used by the lsp tool.
type LSPQueryManager interface {
	Definition(context.Context, string, int, int) ([]lsp.Location, error)
	References(context.Context, string, int, int) ([]lsp.Location, error)
	Hover(context.Context, string, int, int) (*lsp.Hover, error)
	DocumentSymbols(context.Context, string) ([]lsp.DocumentSymbol, error)
	Started() bool
}

// Tool lets the model query the language server for precise code navigation:
// go-to-definition, find-references, hover, and document symbols. This replaces
// grep-and-guess with exact, compiler-grade locations (Issue 7).
type Tool struct {
	agentic.BaseTool
	WorktreeMgr *internal.WorktreeManager
	ProjectDir  string
	// Manager is the shared LSP manager from bootstrap (multi-language). May
	// be a typed-nil *lsp.Manager when LSP is disabled; Started() reports false then.
	Manager LSPQueryManager
}

// Params are the tool's input parameters.
type Params struct {
	Op        string `json:"op"`
	Path      string `json:"path"`
	Line      int    `json:"line"`      // 0-indexed
	Character int    `json:"character"` // 0-indexed
	Query     string `json:"query,omitempty"`
	NewName   string `json:"newName,omitempty"`
	Apply     bool   `json:"apply,omitempty"`
}

// newError builds a ToolError for the lsp tool.
func newError(errType, format string, args ...any) *internal.ToolError {
	return &internal.ToolError{
		Tool:   "lsp",
		Type:   errType,
		Detail: fmt.Sprintf(format, args...),
	}
}

// Schema returns the tool schema for lsp.
func (t *Tool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "lsp",
		Description: "Language-server navigation across configured languages.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op": map[string]any{
					"type": "string",
					"enum": []string{"definition", "references", "hover", "symbols", "implementation", "workspaceSymbol", "prepareCallHierarchy", "incomingCalls", "outgoingCalls", "prepareRename", "rename", "completion", "codeAction", "formatting"},
				},
				"path": map[string]any{
					"type":        "string",
					"description": "source file path",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "0-indexed symbol line (not for symbols)",
				},
				"character": map[string]any{
					"type":        "integer",
					"description": "0-indexed symbol column (not for symbols)",
				},
				"query": map[string]any{
					"type": "string", "description": "workspace symbol query (for workspaceSymbol)",
				},
				"newName": map[string]any{"type": "string", "description": "replacement name for rename"},
				"apply":   map[string]any{"type": "boolean", "description": "apply rename workspace edit; defaults to preview"},
			},
			"required": []string{"op", "path"},
		},
	}
}

// Execute runs the lsp tool (no caller context; see ExecuteContext).
func (t *Tool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// Deferred marks the lsp tool as opt-in: its schema is withheld from the
// per-request eager block and loaded on demand via tool_search.
func (*Tool) Deferred() bool { return true }

// ExecuteContext runs the lsp tool, honoring cancellation so a hung language
// server or a cancelled turn stops the query.
func (t *Tool) ExecuteContext(ctx context.Context, input string) (string, error) {
	if t.Manager == nil || !t.Manager.Started() {
		return "", newError("unavailable",
			"LSP is disabled or no language server is running (enable via /tools:lsp:on)")
	}
	p, err := parseInput(input)
	if err != nil {
		return "", err
	}
	resolvedPath, err := t.resolveTarget(p)
	if err != nil {
		return "", err
	}
	run, ok := opRoutes(t)[p.Op]
	if !ok {
		return "", newError("invalid_input",
			"unknown op %q (use definition|references|hover|symbols)", p.Op)
	}
	return run(ctx, p, resolvedPath)
}
