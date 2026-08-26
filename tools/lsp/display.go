// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/lsp"
)

// formatLocations renders a titled list of locations.
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

// formatWorkspaceSymbols renders workspace-wide symbol matches.
func formatWorkspaceSymbols(v []lsp.WorkspaceSymbol) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Workspace symbols (%d):\n", len(v))
	for _, s := range v {
		fmt.Fprintf(&b, "  %s:%d:%d\n", uriToPath(s.Location.URI), s.Location.Range.Start.Line+1, s.Location.Range.Start.Character+1)
	}
	return b.String()
}

// formatCallItems renders call-hierarchy items.
func formatCallItems(v []lsp.CallHierarchyItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Call hierarchy (%d):\n", len(v))
	for _, x := range v {
		fmt.Fprintf(&b, "  %s (%s)\n", x.Name, uriToPath(x.URI))
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
