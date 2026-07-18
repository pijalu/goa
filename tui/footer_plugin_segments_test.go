// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// renderFooterLine2 renders the footer at width and returns the second
// (model) line, ANSI-stripped, for assertion.
func renderFooterLine2(t *testing.T, f *Footer, width int) string {
	t.Helper()
	lines := f.Render(width)
	if len(lines) < 2 {
		t.Fatalf("Render returned %d lines, want >= 2", len(lines))
	}
	return ansi.Strip(lines[1])
}

func baseFooterData() FooterData {
	return FooterData{
		Workdir: "/tmp/proj",
		Mode:    "coder",
		Profile: "default",
		Model:   "(anthropic) claude-sonnet-4",
	}
}

func TestFooter_PluginSegmentRendered(t *testing.T) {
	f := NewFooter()
	data := baseFooterData()
	data.PluginSegments = []PluginSegment{{ID: "quota", Priority: 10, Text: "5h:42% / 5d:30%"}}
	f.SetData(data)

	line := renderFooterLine2(t, f, 120)
	if !strings.Contains(line, "5h:42% / 5d:30%") {
		t.Fatalf("segment missing from footer: %q", line)
	}
	// Rendered as a bullet suffix of the model display.
	if !strings.Contains(line, "claude-sonnet-4") {
		t.Fatalf("model missing: %q", line)
	}
}

func TestFooter_PluginSegmentsPriorityOrder(t *testing.T) {
	f := NewFooter()
	data := baseFooterData()
	data.PluginSegments = []PluginSegment{
		{ID: "z", Priority: 30, Text: "ZZZ"},
		{ID: "a", Priority: 5, Text: "AAA"},
		{ID: "m", Priority: 15, Text: "MMM"},
	}
	f.SetData(data)
	line := renderFooterLine2(t, f, 140)
	ia, im, iz := strings.Index(line, "AAA"), strings.Index(line, "MMM"), strings.Index(line, "ZZZ")
	if ia < 0 || im < 0 || iz < 0 {
		t.Fatalf("segments missing: %q", line)
	}
	if !(ia < im && im < iz) {
		t.Fatalf("segments not priority-ordered: %q", line)
	}
}

func TestFooter_PluginSegmentEmptyElided(t *testing.T) {
	f := NewFooter()
	data := baseFooterData()
	data.PluginSegments = []PluginSegment{
		{ID: "quota", Priority: 10, Text: ""},
		{ID: "blank", Priority: 20, Text: "   "},
	}
	f.SetData(data)
	line := renderFooterLine2(t, f, 120)
	// No dangling bullet separators from empty segments.
	if strings.Count(line, "•") > 0 {
		t.Fatalf("empty segments rendered bullets: %q", line)
	}
}

func TestFooter_PluginSegmentsPreservedAcrossSetData(t *testing.T) {
	f := NewFooter()
	data := baseFooterData()
	data.PluginSegments = []PluginSegment{{ID: "quota", Priority: 10, Text: "5h:42%"}}
	f.SetData(data)

	// A routine stats update that rebuilds FooterData (PluginSegments nil)
	// must keep the previously-pushed segments.
	f.SetData(FooterData{Stats: "↑10k ↓2k"})
	line := renderFooterLine2(t, f, 120)
	if !strings.Contains(line, "5h:42%") {
		t.Fatalf("segments lost after SetData: %q", line)
	}
}

func TestFooter_PluginSegmentsStrippedUnderWidthPressure(t *testing.T) {
	f := NewFooter()
	data := baseFooterData()
	data.Model = "(anthropic) claude-sonnet-4-with-a-very-long-name"
	data.ThinkingLevel = "high"
	data.PluginSegments = []PluginSegment{{ID: "quota", Priority: 10, Text: "5h:42% / 5d:30%"}}
	f.SetData(data)

	// Narrow terminal: the right side overflows, so compaction runs and must
	// drop the plugin segment first (its own step) rather than mangling it.
	line := renderFooterLine2(t, f, 40)
	if strings.Contains(line, "5h:42%") {
		t.Fatalf("segment should be stripped at width 40: %q", line)
	}
	if visibleWidth(line) > 40 {
		t.Fatalf("line overflow: %q (w=%d)", line, visibleWidth(line))
	}
}

func TestFooter_NoPluginSegmentsUnchanged(t *testing.T) {
	f := NewFooter()
	f.SetData(baseFooterData())
	line := renderFooterLine2(t, f, 120)
	if strings.Contains(line, "•") {
		t.Fatalf("unexpected bullet without segments: %q", line)
	}
	if !strings.Contains(line, "claude-sonnet-4") {
		t.Fatalf("model missing: %q", line)
	}
}

func TestStripPluginSegments_NoSegmentsNoOp(t *testing.T) {
	f := NewFooter()
	f.SetData(baseFooterData())
	in := "claude-sonnet-4 • medium"
	if got := f.stripPluginSegments(in); got != in {
		t.Fatalf("stripPluginSegments modified string without segments: %q", got)
	}
}
