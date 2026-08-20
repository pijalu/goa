// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// scrollbackScenario builds a viewport with count tool widgets, renders it,
// and commits everything above a visibleBand-row tail to scrollback — the
// geometry behind bugs.md "Out-of-screen tool call results corrupt the
// terminal UI". Returns the widgets and the watermark.
func scrollbackScenario(t *testing.T, count int) (*ChatViewport, []*ToolExecutionComponent) {
	t.Helper()
	cv := NewChatViewport()
	tools := make([]*ToolExecutionComponent, 0, count)
	for i := 0; i < count; i++ {
		// Each tool widget renders ~4 rows (pad, header, duration, pad).
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)

	total := len(cv.renderCache.lines)
	const visibleBand = 20
	if total <= visibleBand {
		t.Fatalf("content height %d not taller than visible band %d — geometry precondition unmet", total, visibleBand)
	}
	cv.SetScrollWatermark(total - visibleBand)
	return cv, tools
}

// scrolledOffIndex returns the index of the first fully scrolled-off widget
// and requires that at least one widget is scrolled off and at least one is
// still visible (otherwise the test proves nothing).
func scrolledOffIndex(t *testing.T, cv *ChatViewport, tools []*ToolExecutionComponent) int {
	t.Helper()
	idx, visible := -1, 0
	for i, tc := range tools {
		if cv.IsScrolledOff(tc) {
			if idx < 0 {
				idx = i
			}
		} else {
			visible++
		}
	}
	if idx < 0 || visible == 0 {
		t.Fatalf("scenario geometry degenerate: scrolled-off idx=%d, visible=%d", idx, visible)
	}
	return idx
}

// TestToolExecution_ScrolledOffToggleNoOp is the per-widget regression for
// the out-of-screen corruption: pressing Ctrl+O/Enter on a tool widget whose
// rows are committed to terminal scrollback must NOT rebuild the widget
// (its new height would shift later entries' repaint geometry and corrupt
// the screen). The toggle is a no-op and the user gets a one-line flash.
func TestToolExecution_ScrolledOffToggleNoOp(t *testing.T) {
	cv, tools := scrollbackScenario(t, 8)
	off := scrolledOffIndex(t, cv, tools)
	tc := tools[off]

	linesBefore := append([]string(nil), cv.entries[off].renderedLines...)

	tc.HandleInput("ctrl+o")

	if tc.effectiveExpanded() {
		t.Error("scrolled-off widget expanded — toggle must be a no-op")
	}
	if tc.expandedSet {
		t.Error("scrolled-off toggle recorded an explicit override — must not touch state")
	}
	linesAfter := cv.entries[off].renderedLines
	if len(linesAfter) != len(linesBefore) {
		t.Errorf("scrolled-off widget rebuilt: %d lines before, %d after", len(linesBefore), len(linesAfter))
	}

	// The user got a visible explanation instead of a silent no-op.
	found := false
	cv.ForEach(func(e MessageEntry) {
		if e.Data.Type == ConsoleSystemMessage && strings.Contains(e.Data.Text, "scrolled off-screen") {
			found = true
		}
	})
	if !found {
		t.Error("no flash notice appended for the blocked scrolled-off toggle")
	}

	// Sanity: the same toggle on a VISIBLE widget still works.
	vis := (off + len(tools) - 1) % len(tools) // last widget is visible by construction
	if cv.IsScrolledOff(tools[vis]) {
		vis = len(tools) - 1
	}
	tools[vis].HandleInput("ctrl+o")
	if !tools[vis].expandedSet {
		t.Error("visible widget toggle did not record the explicit override — guard over-fires")
	}
}

// TestChatViewport_ToggleAllToolsViewSkipsScrolledOff is the global-toggle
// regression: Ctrl+O (ToggleAllToolsView) must not rebuild scrolled-off
// widgets. Their geometry is frozen; rebuilding them is what corrupted the
// terminal. Skipped widgets keep byte-identical rows and the user gets one
// summary flash.
func TestChatViewport_ToggleAllToolsViewSkipsScrolledOff(t *testing.T) {
	cv, tools := scrollbackScenario(t, 8)
	off := scrolledOffIndex(t, cv, tools)

	// Snapshot every entry's rendered rows.
	before := make([][]string, len(tools))
	for i := range tools {
		before[i] = append([]string(nil), cv.entries[i].renderedLines...)
	}

	cv.ToggleAllToolsView()

	for i, tc := range tools {
		if cv.IsScrolledOff(tc) {
			if len(cv.entries[i].renderedLines) != len(before[i]) {
				t.Errorf("scrolled-off widget %d rebuilt: %d → %d lines", i, len(before[i]), len(cv.entries[i].renderedLines))
			}
			// The per-widget explicit override must not be touched (a stale
			// override would win over the committed geometry on any later
			// rebuild). effectiveExpanded may follow the global policy — the
			// widget is never repainted while scrolled off, so only the
			// frozen rendered rows matter.
			if tc.expandedSet {
				t.Errorf("scrolled-off widget %d explicit override set by global toggle", i)
			}
		}
	}

	// One summary flash mentions the skipped blocks.
	found := false
	cv.ForEach(func(e MessageEntry) {
		if e.Data.Type == ConsoleSystemMessage && strings.Contains(e.Data.Text, "scrolled off-screen") {
			found = true
		}
	})
	if !found {
		t.Error("no summary flash for skipped scrolled-off widgets")
	}
	if _, ok := scrolledOffEntry(tools, cv, off); !ok {
		t.Error("scrolled-off precondition lost after toggle")
	}
}

// scrolledOffEntry re-checks the guard geometry after the toggle.
func scrolledOffEntry(tools []*ToolExecutionComponent, cv *ChatViewport, idx int) (*ToolExecutionComponent, bool) {
	return tools[idx], cv.IsScrolledOff(tools[idx])
}
