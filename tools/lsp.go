// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/lsp"
)

// LSPQueryManager is the subset of the LSP manager used by the lsp tool. It is
// satisfied by *lsp.Manager and is narrow enough to fake in tests.
type LSPQueryManager interface {
	Definition(ctx context.Context, path string, line, character int) ([]lsp.Location, error)
	References(ctx context.Context, path string, line, character int) ([]lsp.Location, error)
	Hover(ctx context.Context, path string, line, character int) (*lsp.Hover, error)
	DocumentSymbols(ctx context.Context, path string) ([]lsp.DocumentSymbol, error)
	// Started reports whether the manager is running; a typed-nil manager
	// (LSP disabled — tools.enabled.lsp:false or global lsp:false) reports false.
	Started() bool
}

// LSPTool lets the model query the language server for precise code navigation:
// go-to-definition, find-references, hover, and document symbols. This replaces
// grep-and-guess with exact, compiler-grade locations (Issue 7).
type LSPTool struct {
	agentic.BaseTool
	WorktreeMgr *internal.WorktreeManager
	ProjectDir  string
	// Manager is the shared LSP manager from bootstrap (multi-language). May
	// be a typed-nil *lsp.Manager when LSP is disabled; Started() reports false then.
	Manager LSPQueryManager
}

type lspParams struct {
	Op        string `json:"op"`
	Path      string `json:"path"`
	Line      int    `json:"line"`      // 0-indexed
	Character int    `json:"character"` // 0-indexed
}

// lspErr builds a ToolError for the lsp tool.
func lspErr(errType, format string, args ...any) *internal.ToolError {
	return &internal.ToolError{
		Tool:   "lsp",
		Type:   errType,
		Detail: fmt.Sprintf(format, args...),
	}
}

// Schema returns the tool schema for lsp.
func (t *LSPTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "lsp",
		Description: "Language-server navigation: definition|references|hover|symbols (any configured language).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op": map[string]any{
					"type": "string",
					"enum":        []string{"definition", "references", "hover", "symbols"},
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
			},
			"required": []string{"op", "path"},
		},
	}
}

// Execute runs the lsp tool (no caller context; see ExecuteContext).
func (t *LSPTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext runs the lsp tool, honoring cancellation so a hung language
// server or a cancelled turn stops the query.
func (t *LSPTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	if t.Manager == nil || !t.Manager.Started() {
		return "", lspErr("unavailable",
			"LSP is disabled or no language server is running (enable via /tools:lsp:on)")
	}
	var p lspParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", lspErr("invalid_input", "invalid JSON input: %v", err)
	}
	if p.Op == "" {
		return "", lspErr("invalid_input", "op is required")
	}
	if p.Path == "" {
		return "", lspErr("invalid_input", "path is required")
	}
	resolvedPath, _, err := ResolveFileToolPath(t.WorktreeMgr, p.Path)
	if err != nil {
		return "", lspErr("invalid_path", "cannot resolve path %q: %v", p.Path, err)
	}
	if !strings.HasSuffix(resolvedPath, ".go") {
		return "", lspErr("invalid_path", "lsp only supports Go files, got %q", p.Path)
	}

	switch p.Op {
	case "definition", "references", "hover":
		return t.runPositionOp(ctx, p, resolvedPath)
	case "symbols":
		return t.runSymbols(ctx, resolvedPath)
	default:
		return "", lspErr("invalid_input",
			"unknown op %q (use definition|references|hover|symbols)", p.Op)
	}
}

// runPositionOp handles definition/references/hover, which all take a position.
func (t *LSPTool) runPositionOp(ctx context.Context, p lspParams, resolvedPath string) (string, error) {
	if p.Line < 0 || p.Character < 0 {
		return "", lspErr("invalid_input", "line and character must be >= 0 for op %q", p.Op)
	}
	switch p.Op {
	case "definition":
		locs, err := t.Manager.Definition(ctx, resolvedPath, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "definition failed: %v", err)
		}
		return formatLocations("Definition", locs), nil
	case "references":
		locs, err := t.Manager.References(ctx, resolvedPath, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "references failed: %v", err)
		}
		return formatLocations("References", locs), nil
	default: // hover
		h, err := t.Manager.Hover(ctx, resolvedPath, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "hover failed: %v", err)
		}
		return formatHover(h), nil
	}
}

// runSymbols handles the documentSymbol operation.
func (t *LSPTool) runSymbols(ctx context.Context, resolvedPath string) (string, error) {
	syms, err := t.Manager.DocumentSymbols(ctx, resolvedPath)
	if err != nil {
		return "", lspErr("query_failed", "document symbols failed: %v", err)
	}
	return formatSymbols(syms), nil
}

// formatLocations renders definition/reference locations as a compact list.
func formatLocations(title string, locs []lsp.Location) string {
	if len(locs) == 0 {
		return title + ": none found\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d):\n", title, len(locs))
	for _, l := range locs {
		fmt.Fprintf(&b, "  %s:%d:%d\n", uriToPath(l.URI), l.Range.Start.Line+1, l.Range.Start.Character+1)
	}
	return b.String()
}

// formatHover renders hover markdown contents.
func formatHover(h *lsp.Hover) string {
	if h == nil {
		return "Hover: no information\n"
	}
	return "Hover:\n" + hoverContentsString(h.Contents) + "\n"
}

// hoverContentsString extracts readable text from the LSP hover Contents union
// (markdown markup content, marked string, or plain string).
func hoverContentsString(contents any) string {
	m, ok := contents.(map[string]any)
	if !ok {
		return fmt.Sprintf("%v", contents)
	}
	if v, ok := m["value"].(string); ok {
		return v
	}
	return fmt.Sprintf("%v", contents)
}

// formatSymbols renders a document's symbols as an indented tree.
func formatSymbols(syms []lsp.DocumentSymbol) string {
	if len(syms) == 0 {
		return "Symbols: none found\n"
	}
	var b strings.Builder
	b.WriteString("Symbols:\n")
	for _, s := range syms {
		writeSymbol(&b, s, 1)
	}
	return b.String()
}

func writeSymbol(b *strings.Builder, s lsp.DocumentSymbol, depth int) {
	indent := strings.Repeat("  ", depth)
	line := s.SelectionRange.Start.Line + 1
	if s.Detail != "" {
		fmt.Fprintf(b, "%s%s %s (line %d)\n", indent, s.Name, s.Detail, line)
	} else {
		fmt.Fprintf(b, "%s%s (line %d)\n", indent, s.Name, line)
	}
	for _, c := range s.Children {
		writeSymbol(b, c, depth+1)
	}
}

// uriToPath converts a file:// URI back to a filesystem path for display.
func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}
