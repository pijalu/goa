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

// LSPQueryManager is the subset of the LSP manager used by the lsp tool.
type LSPQueryManager interface {
	Definition(context.Context, string, int, int) ([]lsp.Location, error)
	References(context.Context, string, int, int) ([]lsp.Location, error)
	Hover(context.Context, string, int, int) (*lsp.Hover, error)
	DocumentSymbols(context.Context, string) ([]lsp.DocumentSymbol, error)
	Started() bool
}

type lspRefactoringManager interface {
	PrepareRename(context.Context, string, int, int) (*lsp.PrepareRenameResult, error)
	Rename(context.Context, string, int, int, string) (*lsp.WorkspaceEdit, error)
	Completion(context.Context, string, int, int) ([]lsp.CompletionItem, error)
	CodeAction(context.Context, string, lsp.Range) ([]lsp.CodeAction, error)
	Formatting(context.Context, string, lsp.FormattingOptions) ([]lsp.TextEdit, error)
}

type lspNavigationManager interface {
	Implementation(context.Context, string, int, int) ([]lsp.Location, error)
	WorkspaceSymbols(context.Context, string, string) ([]lsp.WorkspaceSymbol, error)
	PrepareCallHierarchy(context.Context, string, int, int) ([]lsp.CallHierarchyItem, error)
	IncomingCalls(context.Context, string, int, int) ([]lsp.CallHierarchyIncomingCall, error)
	OutgoingCalls(context.Context, string, int, int) ([]lsp.CallHierarchyOutgoingCall, error)
	SupportsPath(string) bool
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
	Query     string `json:"query,omitempty"`
	NewName   string `json:"newName,omitempty"`
	Apply     bool   `json:"apply,omitempty"`
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
	if support, ok := t.Manager.(interface{ SupportsPath(string) bool }); ok {
		if !support.SupportsPath(resolvedPath) {
			return "", lspErr("invalid_path", "no configured language server supports %q", p.Path)
		}
	} else if !knownLSPPath(resolvedPath) {
		return "", lspErr("invalid_path", "lsp only supports configured language files (not Go files), got %q", p.Path)
	}

	switch p.Op {
	case "definition", "references", "hover":
		return t.runPositionOp(ctx, p, resolvedPath)
	case "symbols":
		return t.runSymbols(ctx, resolvedPath)
	case "implementation", "workspaceSymbol", "prepareCallHierarchy", "incomingCalls", "outgoingCalls":
		return t.runAdvanced(ctx, p, resolvedPath)
	case "prepareRename", "rename", "completion", "codeAction", "formatting":
		return t.runRefactoring(ctx, p, resolvedPath)
	default:
		return "", lspErr("invalid_input",
			"unknown op %q (use definition|references|hover|symbols)", p.Op)
	}
}

func knownLSPPath(path string) bool {
	for _, ext := range []string{".go", ".js", ".jsx", ".ts", ".tsx", ".java"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
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
func (t *LSPTool) runRefactoring(ctx context.Context, p lspParams, path string) (string, error) {
	mgr, ok := t.Manager.(lspRefactoringManager)
	if !ok {
		return "", lspErr("unavailable", "refactoring unavailable")
	}
	if p.Line < 0 || p.Character < 0 {
		return "", lspErr("invalid_input", "line and character must be >= 0")
	}
	switch p.Op {
	case "prepareRename":
		v, err := mgr.PrepareRename(ctx, path, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "prepare rename failed: %v", err)
		}
		if v == nil {
			return "no rename target\n", nil
		}
		return fmt.Sprintf("Rename range %d:%d-%d:%d (%s)\n", v.Range.Start.Line, v.Range.Start.Character, v.Range.End.Line, v.Range.End.Character, v.Placeholder), nil
	case "rename":
		if p.NewName == "" {
			return "", lspErr("invalid_input", "newName is required")
		}
		v, err := mgr.Rename(ctx, path, p.Line, p.Character, p.NewName)
		if err != nil {
			return "", lspErr("query_failed", "rename failed: %v", err)
		}
		if v == nil {
			return "Workspace edit: none\n", nil
		}
		policy := lsp.WorkspaceEditPolicy{Root: t.ProjectDir}
		preview, err := lsp.PreviewWorkspaceEdit(v, policy)
		if err != nil {
			return "", lspErr("invalid_edit", "rename workspace edit rejected: %v", err)
		}
		if !p.Apply {
			return fmt.Sprintf("Workspace edit preview (%d files, %d edits)\n", len(preview.Files), preview.EditCount), nil
		}
		if _, err := lsp.ApplyWorkspaceEdit(v, policy); err != nil {
			return "", lspErr("apply_failed", "rename workspace edit failed: %v", err)
		}
		return fmt.Sprintf("Workspace edit applied (%d files, %d edits)\n", len(preview.Files), preview.EditCount), nil
	case "completion":
		v, err := mgr.Completion(ctx, path, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "completion failed: %v", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Completions (%d):\n", len(v))
		for _, x := range v {
			fmt.Fprintf(&b, "  %s\n", x.Label)
		}
		return b.String(), nil
	case "codeAction":
		v, err := mgr.CodeAction(ctx, path, lsp.Range{Start: lsp.Position{Line: p.Line, Character: p.Character}, End: lsp.Position{Line: p.Line, Character: p.Character}})
		if err != nil {
			return "", lspErr("query_failed", "code action failed: %v", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Code actions (%d):\n", len(v))
		for _, x := range v {
			fmt.Fprintf(&b, "  %s\n", x.Title)
		}
		return b.String(), nil
	default:
		v, err := mgr.Formatting(ctx, path, lsp.FormattingOptions{TabSize: 4, InsertSpaces: true})
		if err != nil {
			return "", lspErr("query_failed", "formatting failed: %v", err)
		}
		return fmt.Sprintf("Formatting edits (%d)\n", len(v)), nil
	}
}

func (t *LSPTool) runAdvanced(ctx context.Context, p lspParams, path string) (string, error) {
	mgr, ok := t.Manager.(lspNavigationManager)
	if !ok {
		return "", lspErr("unavailable", "advanced LSP operation unavailable")
	}
	if p.Op != "workspaceSymbol" && (p.Line < 0 || p.Character < 0) {
		return "", lspErr("invalid_input", "line and character must be >= 0")
	}
	switch p.Op {
	case "implementation":
		v, err := mgr.Implementation(ctx, path, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "implementation failed: %v", err)
		}
		return formatLocations("Implementation", v), nil
	case "workspaceSymbol":
		v, err := mgr.WorkspaceSymbols(ctx, path, p.Query)
		if err != nil {
			return "", lspErr("query_failed", "workspace symbols failed: %v", err)
		}
		return formatWorkspaceSymbols(v), nil
	case "prepareCallHierarchy":
		v, err := mgr.PrepareCallHierarchy(ctx, path, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "call hierarchy failed: %v", err)
		}
		return formatCallItems(v), nil
	case "incomingCalls":
		v, err := mgr.IncomingCalls(ctx, path, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "incoming calls failed: %v", err)
		}
		return fmt.Sprintf("Incoming calls (%d)\n", len(v)), nil
	default:
		v, err := mgr.OutgoingCalls(ctx, path, p.Line, p.Character)
		if err != nil {
			return "", lspErr("query_failed", "outgoing calls failed: %v", err)
		}
		return fmt.Sprintf("Outgoing calls (%d)\n", len(v)), nil
	}
}

func formatWorkspaceSymbols(v []lsp.WorkspaceSymbol) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace symbols (%d):\n", len(v))
	for _, s := range v {
		fmt.Fprintf(&b, "  %s:%d:%d\n", uriToPath(s.Location.URI), s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
	}
	return b.String()
}
func formatCallItems(v []lsp.CallHierarchyItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Call hierarchy (%d):\n", len(v))
	for _, x := range v {
		fmt.Fprintf(&b, "  %s (%s)\n", x.Name, uriToPath(x.URI))
	}
	return b.String()
}
func (t *LSPTool) runSymbols(ctx context.Context, resolvedPath string) (string, error) {
	syms, err := t.Manager.DocumentSymbols(ctx, resolvedPath)
	if err != nil {
		return "", lspErr("query_failed", "document symbols failed: %v", err)
	}
	return formatSymbols(syms), nil
}

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
