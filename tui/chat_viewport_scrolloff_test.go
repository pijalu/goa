// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestChatViewport_IsScrolledOff verifies the scrolled-off geometry the app
// uses for completion echoes (bugs.md Issue 6): an entry is scrolled off only
// when its rendered rows lie entirely above the visible window. The visible
// window is the compositor's transcript band: terminal height minus the fixed
// bottom chrome — children above the transcript scroll with it and do NOT
// shrink the band.
func TestChatViewport_IsScrolledOff(t *testing.T) {
	cv := NewChatViewport()
	// Terminal 24 rows, 4 rows of bottom chrome → 20-row visible band.
	cv.SetViewportHeight(24)
	cv.SetBottomChromeHeight(4)
	cv.SetAllocatedHeight(20)

	var tools []*ToolExecutionComponent
	for i := 0; i < 8; i++ {
		// Each tool widget renders ~4 rows (pad, header, duration, pad).
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)

	total := len(cv.renderCache.lines)
	if total <= 20 {
		t.Fatalf("content height %d not taller than visible band 20 — geometry precondition unmet", total)
	}
	visibleStart := total - 20
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

// TestChatViewport_IsScrolledOff_HeaderAboveTranscript is the regression test
// for the spurious "← ✓ …" completion echoes: the layout budget pushed via
// SetAllocatedHeight excludes the header/mascot stacked ABOVE the transcript
// (budget = terminalH − header − bottomChrome), but the header scrolls with
// the transcript — the compositor's visible transcript band is taller
// (terminalH − bottomChrome). Widgets ending inside the visible band must NOT
// be reported scrolled off, or the app appends a duplicate echo for a tool
// whose ✓ transition is plainly visible on screen.
func TestChatViewport_IsScrolledOff_HeaderAboveTranscript(t *testing.T) {
	cv := NewChatViewport()
	const (
		termH        = 30 // terminal rows
		headerH      = 12 // mascot/header child above the transcript
		bottomChrome = 5  // status + input + footer
	)
	visibleH := termH - bottomChrome         // 25 — the true compositor band
	budget := termH - headerH - bottomChrome // 13 — what the layout allocates
	cv.SetViewportHeight(termH)
	cv.SetBottomChromeHeight(bottomChrome)
	cv.SetAllocatedHeight(budget)
	if budget >= visibleH {
		t.Fatalf("test geometry broken: budget %d must be < visible band %d", budget, visibleH)
	}

	var tools []*ToolExecutionComponent
	for i := 0; i < 10; i++ {
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)

	total := len(cv.renderCache.lines)
	if total <= visibleH {
		t.Fatalf("content height %d not taller than visible band %d — geometry precondition unmet", total, visibleH)
	}
	trueStart := total - visibleH // rows < trueStart are in immutable scrollback
	buggyBand := 0
	for i, tc := range tools {
		e := &cv.entries[i]
		end := e.lineOffset + len(e.renderedLines)
		want := end <= trueStart
		if got := cv.IsScrolledOff(tc); got != want {
			t.Errorf("widget %d IsScrolledOff = %v, want %v (offset=%d lines=%d end=%d trueStart=%d)",
				i, got, want, e.lineOffset, len(e.renderedLines), end, trueStart)
		}
		// Rows ending in (trueStart, total-budget] are the false-positive band
		// of the old budget-based computation: visible on screen, yet flagged.
		if !want && end <= total-budget {
			buggyBand++
		}
	}
	if buggyBand == 0 {
		t.Fatal("scenario geometry degenerate: no widget in the budget false-positive band — test proves nothing")
	}
}

// TestChatViewport_IsScrolledOff_FitsOnScreen: when the content fits the
// visible band nothing is scrolled off (no spurious echo).
func TestChatViewport_IsScrolledOff_FitsOnScreen(t *testing.T) {
	cv := NewChatViewport()
	cv.SetViewportHeight(50)
	cv.SetBottomChromeHeight(0)
	cv.SetAllocatedHeight(50)
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	cv.Render(80)
	if cv.IsScrolledOff(tc) {
		t.Error("widget reported scrolled off while content fits the screen")
	}
}

// TestChatViewport_IsScrolledOff_UnknownGeometry: without a layout pass, or
// for never-rendered entries, IsScrolledOff reports false — the echo must
// never fire spuriously.
func TestChatViewport_IsScrolledOff_UnknownGeometry(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	if cv.IsScrolledOff(tc) {
		t.Error("no layout geometry set: IsScrolledOff must be false")
	}

	// Layout geometry present and content overflowing, but the queried entry
	// was appended after the last render (no rendered geometry yet).
	cv2 := NewChatViewport()
	cv2.SetViewportHeight(10)
	cv2.SetBottomChromeHeight(0)
	for i := 0; i < 6; i++ {
		cv2.AddToolExecution("bash", `{"command":"ls"}`)
	}
	cv2.Render(80)
	if len(cv2.renderCache.lines) <= 10 {
		t.Fatalf("precondition unmet: content %d rows must overflow the 10-row band", len(cv2.renderCache.lines))
	}
	late := cv2.AddToolExecution("bash", `{"command":"late"}`)
	if cv2.IsScrolledOff(late) {
		t.Error("entry never rendered: IsScrolledOff must be false")
	}
}
