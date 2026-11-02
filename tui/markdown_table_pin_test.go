// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// B53 — markdown tables must never show mixed geometry after a width change
// (Option A: session-pinned table width). Rows already scrolled into the
// native terminal's scrollback can never be repainted, so the only way to
// keep one table visually consistent is to render its geometry at a width
// pinned for the whole session: the minimum transcript width observed so
// far. A repaint at any later (wider) width must then produce byte-identical
// table rows, and a mid-stream width change must never mix widths inside one
// table.

// buildTableText returns a markdown table with rows data rows and cells long
// enough that the table's natural width exceeds a 70-col transcript: at 70
// cols the renderer must shrink columns; at 140 cols it fits naturally, so
// the two geometries differ and any width-dependent render is caught.
func buildTableText(rows int) string {
	var b strings.Builder
	b.WriteString("Intro paragraph before the table.\n\n")
	b.WriteString("| Alpha | Beta |\n")
	b.WriteString("|---|---|\n")
	for i := 0; i < rows; i++ {
		b.WriteString(fmt.Sprintf(
			"| alpha cell %03d padded padded padded | beta cell %03d padded padded |\n", i, i))
	}
	return b.String()
}

// isTableRenderLine reports whether a rendered line belongs to a table frame
// (borders or data rows), after stripping ANSI styling.
func isTableRenderLine(line string) bool {
	s := strings.TrimSpace(ansi.Strip(line))
	if s == "" {
		return false
	}
	for _, p := range []string{"┌", "├", "└", "│"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// tableGeometryKey normalizes a rendered table line to its geometry signature:
// visible content with trailing padding removed (the frame is padded to the
// terminal width, which legitimately differs between W1 and W2).
func tableGeometryKey(line string) string {
	return strings.TrimRight(ansi.Strip(line), " ")
}

// resetSessionTablePin isolates a test from pin state left by other tests.
func resetSessionTablePin(t *testing.T) {
	t.Helper()
	theSessionTablePin.reset()
	t.Cleanup(func() { theSessionTablePin.reset() })
}

// A table rendered at W1 and then re-rendered at a wider W2 must produce
// identical table rows (session-pinned geometry). RED before the fix: the
// width change recreates the incremental renderer and repainting at W2
// changes column widths vs the W1 rows already in scrollback.
func TestB53_TableRepaintAtWiderWidth_Identity(t *testing.T) {
	resetSessionTablePin(t)

	m := newAssistantMessage(buildTableText(30))
	w1 := m.Render(70)
	w2 := m.Render(140)

	t1 := tableRenderLines(w1)
	t2 := tableRenderLines(w2)
	if len(t1) == 0 {
		t.Fatal("no table lines rendered at width 70")
	}
	if len(t1) != len(t2) {
		t.Fatalf("table row count changed after width change: %d at W1 vs %d at W2",
			len(t1), len(t2))
	}
	for i := range t1 {
		if tableGeometryKey(t1[i]) != tableGeometryKey(t2[i]) {
			t.Fatalf("table row %d differs after repaint at wider width:\nW1: %q\nW2: %q",
				i, tableGeometryKey(t1[i]), tableGeometryKey(t2[i]))
		}
	}
}

// While a tall table streams, a terminal width change mid-table must never
// mix widths inside one table: every border separator of the final frame
// must match the geometry the table had when it started (pinned at W1).
func TestB53_MidStreamWidthChange_NoMixedGeometry(t *testing.T) {
	resetSessionTablePin(t)

	text := buildTableText(0)
	rows := strings.Split(strings.TrimRight(buildTableText(30), "\n"), "\n")
	tableStart := 4 // intro, blank, header, separator → first data row index

	// Stream the first half at W1=70, one frame per chunk.
	const w1, w2 = 70, 140
	m := newAssistantMessage("")
	for i := tableStart; i < tableStart+15; i++ {
		text += rows[i] + "\n"
		m.SetText(text)
		m.Render(w1)
	}
	// Capture the geometry the table has at W1 (what scrollback froze).
	w1Lines := m.Render(w1)
	w1Seps := separatorKeys(w1Lines, "├")
	if len(w1Seps) == 0 {
		t.Fatal("no separators rendered at width 70")
	}

	// Resize mid-table, keep streaming at W2.
	for i := tableStart + 15; i < len(rows); i++ {
		text += rows[i] + "\n"
		m.SetText(text)
		m.Render(w2)
	}
	final := m.Render(w2)
	finalSeps := separatorKeys(final, "├")
	finalBottoms := separatorKeys(final, "└")
	if len(finalSeps) == 0 {
		t.Fatal("no separators in final render")
	}

	// Homogeneous frame: every row separator identical.
	for i, s := range finalSeps {
		if s != finalSeps[0] {
			t.Fatalf("mixed widths inside one table after mid-stream resize: separator %d %q != %q",
				i, s, finalSeps[0])
		}
	}
	// Bottom border shares the geometry (width), junction chars differ by design.
	if len(finalBottoms) == 0 || ansi.Width(finalBottoms[0]) != ansi.Width(finalSeps[0]) {
		t.Fatalf("bottom border width %q does not match separator geometry %q",
			finalBottoms, finalSeps[0])
	}
	// Pinned geometry: same separators the table had at W1.
	if finalSeps[0] != w1Seps[0] {
		t.Fatalf("mid-stream resize changed table geometry:\nW1: %q\nW2: %q",
			w1Seps[0], finalSeps[0])
	}
}

// The session pin policy itself: observe monotonic min, reset for new sessions.
func TestB53_SessionTablePinPolicy(t *testing.T) {
	var p sessionTablePin
	if p.pinnedWidth() != 0 {
		t.Fatalf("fresh pin should be 0, got %d", p.pinnedWidth())
	}
	p.observe(80)
	p.observe(120)
	if p.pinnedWidth() != 80 {
		t.Fatalf("wider observation must not raise the pin: got %d", p.pinnedWidth())
	}
	p.observe(40)
	if p.pinnedWidth() != 40 {
		t.Fatalf("narrower observation must lower the pin: got %d", p.pinnedWidth())
	}
	p.observe(0) // ignored
	if p.pinnedWidth() != 40 {
		t.Fatalf("zero observation must be ignored: got %d", p.pinnedWidth())
	}
	p.reset()
	if p.pinnedWidth() != 0 {
		t.Fatalf("reset pin should be 0, got %d", p.pinnedWidth())
	}
}

func tableRenderLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if isTableRenderLine(l) {
			out = append(out, l)
		}
	}
	return out
}

// separatorKeys returns normalized separator lines with the given junction
// prefix (geometry-encoded).
func separatorKeys(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		s := strings.TrimSpace(ansi.Strip(l))
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}
