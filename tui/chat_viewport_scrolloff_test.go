// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestChatViewport_IsScrolledOff verifies the scrolled-off geometry the app
// uses for completion echoes (bugs.md Issue 6): an entry is scrolled off
// only when its rendered rows lie entirely above the visible window
// (content taller than the layout-allocated height).
func TestChatViewport_IsScrolledOff(t *testing.T) {
	cv := NewChatViewport()
	cv.SetAllocatedHeight(10)

	var tools []*ToolExecutionComponent
	for i := 0; i < 6; i++ {
		// Each tool widget renders ~4 rows (pad, header, duration, pad).
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)

	total := len(cv.renderCache.lines)
	if total <= 10 {
		t.Fatalf("content height %d not taller than allocated 10 — geometry precondition unmet", total)
	}
	visibleStart := total - 10
	scrolledCount := 0
	for i, tc := range tools {
		got := cv.IsScrolledOff(tc)
		// Expected from the entry's own geometry: fully above the window.
		e := &cv.entries[i]
		want := e.lineOffset+len(e.renderedLines) <= visibleStart
		if got != want {
			t.Errorf("widget %d IsScrolledOff = %v, want %v (offset=%d lines=%d visibleStart=%d)",
				i, got, want, e.lineOffset, len(e.renderedLines), visibleStart)
		}
		if got {
			scrolledCount++
		}
	}
	// Precondition of the scenario: at least one widget is fully scrolled
	// off and at least one is (partially) visible — otherwise the test
	// proves nothing.
	if scrolledCount == 0 || scrolledCount == len(tools) {
		t.Fatalf("scenario geometry degenerate: %d/%d widgets scrolled off", scrolledCount, len(tools))
	}
}

// TestChatViewport_IsScrolledOff_FitsOnScreen: when the content fits the
// allocated height nothing is scrolled off (no spurious echo).
func TestChatViewport_IsScrolledOff_FitsOnScreen(t *testing.T) {
	cv := NewChatViewport()
	cv.SetAllocatedHeight(50)
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	cv.Render(80)
	if cv.IsScrolledOff(tc) {
		t.Error("widget reported scrolled off while content fits the screen")
	}
}

// TestChatViewport_IsScrolledOff_UnknownGeometry: never-rendered entries or
// a missing allocation report false — the echo must never fire spuriously.
func TestChatViewport_IsScrolledOff_UnknownGeometry(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	if cv.IsScrolledOff(tc) {
		t.Error("no allocated height set: IsScrolledOff must be false")
	}
	cv.SetAllocatedHeight(1)
	if cv.IsScrolledOff(tc) {
		t.Error("entry never rendered: IsScrolledOff must be false")
	}
}
