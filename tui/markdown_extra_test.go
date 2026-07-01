// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestIsTableSeparator(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"|---|---|", true},
		{"|:---:|", true},
		{"|:---|---:|", true},
		{"||", false},
		{"|", false},
		{"", false},
		{"not a table", false},
		{"a|b|c", false},
	}
	for _, tt := range tests {
		got := isTableSeparator(tt.input)
		if got != tt.want {
			t.Errorf("isTableSeparator(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseTableRow(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"| a | b | c |", []string{"a", "b", "c"}},
		{"a|b|c", []string{"a", "b", "c"}},
		{"|a|", []string{"a"}},
		{"||", []string{""}},
		{"|", []string{""}},
		{"", []string{""}},
	}
	for _, tt := range tests {
		got := parseTableRow(tt.input)
		if len(got) != len(tt.want) {
			t.Fatalf("parseTableRow(%q) = %v (len=%d), want %v (len=%d)", tt.input, got, len(got), tt.want, len(tt.want))
		}
		for i := range got {
			if strings.TrimSpace(got[i]) != strings.TrimSpace(tt.want[i]) {
				t.Errorf("parseTableRow(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestRenderTableNoCrash(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())

	// Single pipe row should not panic
	lines := r.Render("|a|")
	t.Logf("Single pipe row: %d lines", len(lines))

	// Empty table should not panic
	lines = r.Render("||")
	t.Logf("Double pipe row: %d lines", len(lines))
}

func TestRenderTableAfterParagraph(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	input := "**Parameters:**\n| Field | Type | Description |\n|-------|------|-------------|\n| path | string | required |\n"
	lines := r.Render(input)

	// The table should be rendered as a table, not flattened into the paragraph.
	var foundBorder bool
	for _, line := range lines {
		if strings.Contains(line, "┌") && strings.Contains(line, "┐") {
			foundBorder = true
			break
		}
	}
	if !foundBorder {
		t.Errorf("expected table border in rendered output, got:\n%s", strings.Join(lines, "\n"))
	}

	// The paragraph line should remain separate from the table rows.
	for _, line := range lines {
		if strings.Contains(line, "Parameters:") && strings.Contains(line, "| Field") {
			t.Errorf("table was flattened into paragraph: %q", line)
		}
	}
}

func TestRenderFencedCodeAfterParagraph(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	input := "**Example:**\n```json\n{\n  \"file_path\": \"src/main.go\"\n}\n```\n"
	lines := r.Render(input)

	// The fenced code block should be recognized, not flattened into the paragraph.
	var foundCode bool
	for _, line := range lines {
		if strings.Contains(line, "file_path") {
			foundCode = true
			break
		}
	}
	if !foundCode {
		t.Errorf("expected code content in rendered output, got:\n%s", strings.Join(lines, "\n"))
	}

	// The paragraph line should not contain backtick remnants from an absorbed fence.
	for _, line := range lines {
		if strings.Contains(line, "Example:") && strings.Contains(line, "json") {
			t.Errorf("code fence was flattened into paragraph: %q", line)
		}
	}
}

func TestRenderFencedCodeFullWidthBackground(t *testing.T) {
	r := NewMDStreamRenderer(40, DarkTheme())
	input := "```\nline one\n\nshort\n```"
	lines := r.Render(input)

	// Code content lines, including intentionally blank ones inside the block,
	// should span the full terminal width with the background color applied.
	// The leading/trailing blank lines use the default background.
	for _, line := range lines {
		if ansi.Strip(line) == "" {
			continue
		}
		w := ansi.Width(line)
		if w != 40 {
			t.Errorf("code line width = %d, want 40: %q", w, line)
		}
		if !strings.HasPrefix(line, ansi.Bg("#21262d")) {
			t.Errorf("code line missing background prefix: %q", line)
		}
	}
}

func TestRenderInline_EntityInHeading(t *testing.T) {
	r := NewMDStreamRenderer(80, DarkTheme())
	lines := r.Render("# Steps $rightarrow$ result")
	if len(lines) == 0 {
		t.Fatal("expected rendered heading")
	}
	clean := ansi.Strip(lines[0])
	if strings.Contains(clean, "$rightarrow$") {
		t.Errorf("expected entity to be translated in heading, got %q", clean)
	}
	if !strings.Contains(clean, "→") {
		t.Errorf("expected arrow character → in heading, got %q", clean)
	}
}

func TestRenderInline_AdditionalEntities(t *testing.T) {
	tests := []struct {
		entity string
		want   string
	}{
		{"$Leftrightarrow$", "⇔"},
		{"$times$", "×"},
		{"$div$", "÷"},
		{"$pm$", "±"},
	}
	for _, tt := range tests {
		got := translateEntities(tt.entity)
		if got != tt.want {
			t.Errorf("translateEntities(%q) = %q, want %q", tt.entity, got, tt.want)
		}
	}
}

func TestRenderFencedCodeBackgroundUniformWithHighlighting(t *testing.T) {
	r := NewMDStreamRenderer(40, DarkTheme())
	input := "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n\n}\n```"
	lines := r.Render(input)

	bg := ansi.Bg("#21262d")
	for _, line := range lines {
		if ansi.Strip(line) == "" {
			continue
		}
		if ansi.Width(line) != 40 {
			t.Errorf("code line width = %d, want 40: %q", ansi.Width(line), line)
		}
		if !strings.HasPrefix(line, bg) {
			t.Errorf("code line missing background prefix: %q", line)
		}
		// Syntax highlighting must not use a full reset mid-line, because
		// that would clear the background color for the rest of the line.
		if strings.Count(line, ansi.Reset) != 1 {
			t.Errorf("code line has extra full reset(s); background not uniform: resets=%d line=%q", strings.Count(line, ansi.Reset), line)
		}
	}
}
