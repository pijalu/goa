// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// B53 validation: a real (filmstrip) resize during a long streamed table.
// A 30-row table streams at terminal width 72; mid-table the terminal is
// resized to 140 and streaming continues. Because the geometry that already
// scrolled off into the terminal's native scrollback can never be repainted,
// every frame — before and after the resize — must paint the table at the
// same pinned geometry: no mixed widths anywhere, at any point of the
// session.
func TestB53_Filmstrip_ResizeDuringStreamedTable_NoMixedGeometry(t *testing.T) {
	sc := newUIScenario(t, 72, 24)
	resetSessionTablePinForTest(t)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	row := func(i int) string {
		// Cells long enough that the table's natural width (~90 cols) forces
		// column shrinking at W1=72 but fits naturally at W2=140 — any
		// width-dependent geometry is then caught.
		return fmt.Sprintf(
			"| alpha cell %03d with considerably longer padding text | beta cell %03d longer padding here |\n", i, i)
	}
	tableSep := "|---|---|\n"
	tableHead := "| Alpha | Beta |\n"

	// Stream the table head + first half at W1=72 (content width 70).
	text := "Tall streamed table:\n\n" + tableHead + tableSep
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventContent, Role: agentic.Assistant,
		State: agentic.StateContent, Text: text, IsDelta: true,
	})
	for i := 0; i < 15; i++ {
		text += row(i)
		sc.apply(&agentic.OutputEvent{
			Type: agentic.EventContent, Role: agentic.Assistant,
			State: agentic.StateContent, Text: row(i), IsDelta: true,
		})
	}

	preSeps := frameTableSeparators(sc.engine.AgentFrame())
	if len(preSeps) == 0 {
		t.Fatal("no table separators visible at width 72")
	}
	if sc.engine.AgentFrame().Width != 72 {
		t.Fatalf("expected terminal width 72 before resize, got %d", sc.engine.AgentFrame().Width)
	}

	// ── Real resize mid-table: 72 → 140 ──
	sc.term.w = 140

	// Keep streaming the second half at the new width.
	for i := 15; i < 30; i++ {
		sc.apply(&agentic.OutputEvent{
			Type: agentic.EventContent, Role: agentic.Assistant,
			State: agentic.StateContent, Text: row(i), IsDelta: true,
		})
	}
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	final := sc.engine.AgentFrame()
	if final.Width != 140 {
		t.Fatalf("expected terminal width 140 after resize, got %d", final.Width)
	}

	// Every table row/separator line painted after the resize must carry the
	// exact geometry the table had before it (what scrollback froze).
	postSeps := frameTableSeparators(final)
	if len(postSeps) == 0 {
		t.Fatal("no table separators visible after resize")
	}
	for i, s := range postSeps {
		if s != preSeps[0] {
			t.Fatalf("mixed geometry after mid-stream resize: post-resize row-separator %d\n%q\n!= pinned pre-resize geometry\n%q",
				i, s, preSeps[0])
		}
	}

	// The whole visible table must be homogeneous: data lines share the
	// separator width (no rows at the old/new width mix).
	for _, l := range final.Visible {
		s := strings.TrimRight(l, " ")
		if strings.HasPrefix(s, "│") && ansi.Width(s) != ansi.Width(preSeps[0]) {
			t.Fatalf("table data line width %d differs from separator geometry %d: %q",
				ansi.Width(s), ansi.Width(preSeps[0]), s)
		}
	}

	// No post-resize frame may ever contain a separator at a width other
	// than the pinned one — check every captured filmstrip frame after the
	// resize step.
	frames := sc.filmstrip().Frames()
	resizeSeen := false
	for _, f := range frames {
		if f.Frame.Width == 140 {
			resizeSeen = true
		}
		if !resizeSeen {
			continue
		}
		for _, s := range frameTableSeparatorsFrom(f.Frame.Visible) {
			if s != preSeps[0] {
				t.Fatalf("filmstrip frame %q (post-resize) shows foreign table geometry:\n%q\n!= %q",
					f.Label, s, preSeps[0])
			}
		}
	}
	if !resizeSeen {
		t.Fatal("no filmstrip frame recorded at width 140; resize never rendered")
	}
}

// frameTableSeparators extracts the ├ row-separator lines from the current
// visible viewport (ANSI already stripped by AgentFrame).
func frameTableSeparators(f tui.AgentFrame) []string {
	return frameTableSeparatorsFrom(f.Visible)
}

func frameTableSeparatorsFrom(visible []string) []string {
	var out []string
	for _, l := range visible {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, "├") {
			out = append(out, s)
		}
	}
	return out
}

// resetSessionTablePinForTest isolates the session pin between app tests.
func resetSessionTablePinForTest(t *testing.T) {
	t.Helper()
	tui.ResetSessionTablePin()
	t.Cleanup(tui.ResetSessionTablePin)
}
