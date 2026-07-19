// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// mdStubTool is a Documentable tool whose LongDoc is markdown.
type mdStubTool struct{ long string }

func (m *mdStubTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: "mdstub", Description: "stub", Schema: map[string]interface{}{}}
}
func (m *mdStubTool) Execute(string) (string, error) { return "", nil }
func (m *mdStubTool) IsRetryable(error) bool         { return false }
func (m *mdStubTool) ShortDoc() string               { return "stub" }
func (m *mdStubTool) LongDoc() string                { return m.long }
func (m *mdStubTool) Examples() []string             { return nil }

// TestPrintToolDocs_RendersMarkdown covers the report that /tools:<name> shows
// raw markdown (literal #, ##, _…_) instead of styled output. The long doc
// must go through the markdown renderer so emphasis becomes SGR styling.
func TestPrintToolDocs_RendersMarkdown(t *testing.T) {
	tool := &mdStubTool{long: "# Title\n\nSome _italic_ and **bold** text.\n\n## Section\n"}
	w := newWriter()
	printToolDocs(w, tool)
	out := w.Text()

	// Italic and bold must be styled with SGR, not shown as literal markers.
	if !strings.Contains(out, ansi.Italic) {
		t.Errorf("tool doc should render _italic_ with italic SGR, got:\n%q", out)
	}
	if !strings.Contains(out, ansi.Bold) {
		t.Errorf("tool doc should render **bold** with bold SGR, got:\n%q", out)
	}
	// Literal markdown emphasis markers must be gone.
	if strings.Contains(out, "_italic_") {
		t.Errorf("literal _italic_ marker leaked into output:\n%q", out)
	}
	if strings.Contains(out, "**bold**") {
		t.Errorf("literal **bold** marker leaked into output:\n%q", out)
	}
}
