// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestChatViewport_IsScrolledOff verifies the scrolled-off geometry the app
// uses for completion echoes (Issue 6): an entry is scrolled off only when
// its rendered rows lie entirely below the compositor's scrollback watermark
// (canvas rows [0, watermark) are committed to terminal scrollback and can
// never be repainted).
func TestChatViewport_IsScrolledOff(t *testing.T) {
	cv := NewChatViewport()

	var tools []*ToolExecutionComponent
	for i := 0; i < 8; i++ {
		// Each tool widget renders ~4 rows (pad, header, duration, pad).
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)

	total := len(cv.renderCache.lines)
	const visibleBand = 20 // rows still on screen (terminal band)
	if total <= visibleBand {
		t.Fatalf("content height %d not taller than visible band %d — geometry precondition unmet", total, visibleBand)
	}
	// Commit everything above the band to scrollback.
	cv.SetScrollWatermark(total - visibleBand)

	scrolledCount := 0
	for i, tc := range tools {
		got := cv.IsScrolledOff(tc)
		// Expected from the entry's own geometry: fully below the watermark.
		e := &cv.entries[i]
		want := e.lineOffset+len(e.renderedLines) <= total-visibleBand
		if got != want {
			t.Errorf("widget %d IsScrolledOff = %v, want %v (offset=%d lines=%d watermark=%d)",
				i, got, want, e.lineOffset, len(e.renderedLines), total-visibleBand)
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

// TestChatViewport_IsScrolledOff_TranscriptOrigin is the regression test for
// the spurious "← ✓ …" completion echoes: the header/mascot stacked ABOVE
// the transcript occupies canvas rows, so entry lineOffsets (transcript
// space) must be shifted by the transcript origin before comparing against
// the (canvas-space) watermark. Without the shift, widgets whose completion
// is plainly visible on screen get flagged scrolled-off by the header's
// height.
func TestChatViewport_IsScrolledOff_TranscriptOrigin(t *testing.T) {
	cv := NewChatViewport()
	const headerH = 12 // mascot/header rows above the transcript

	var tools []*ToolExecutionComponent
	for i := 0; i < 10; i++ {
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)
	cv.setTranscriptOrigin(headerH)

	total := len(cv.renderCache.lines)
	const visibleBand = 25 // canvas rows still on screen (terminal minus chrome)
	if total <= visibleBand {
		t.Fatalf("content height %d not taller than visible band %d — geometry precondition unmet", total, visibleBand)
	}
	// The compositor commits canvas rows; the transcript starts at headerH.
	watermark := headerH + total - visibleBand
	cv.SetScrollWatermark(watermark)

	falsePositives := 0
	for i, tc := range tools {
		e := &cv.entries[i]
		end := e.lineOffset + len(e.renderedLines)
		want := headerH+end <= watermark
		if got := cv.IsScrolledOff(tc); got != want {
			t.Errorf("widget %d IsScrolledOff = %v, want %v (offset=%d lines=%d end=%d watermark=%d)",
				i, got, want, e.lineOffset, len(e.renderedLines), end, watermark)
		}
		// Without the origin shift, widgets whose transcript-space end is
		// ≤ watermark would be false positives: their canvas rows (shifted
		// down by headerH) are still on screen.
		if !want && end <= watermark {
			falsePositives++
		}
	}
	if falsePositives == 0 {
		t.Fatal("scenario geometry degenerate: no widget in the origin-shift false-positive band — test proves nothing")
	}
}

// TestChatViewport_IsScrolledOff_ZeroWatermark: when nothing has been
// committed to scrollback (content fits the screen, or no frame rendered
// yet), nothing is scrolled off (no spurious echo).
func TestChatViewport_IsScrolledOff_ZeroWatermark(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	cv.Render(80)
	if cv.IsScrolledOff(tc) {
		t.Error("widget reported scrolled off with zero watermark")
	}
}

// TestChatViewport_IsScrolledOff_UnknownGeometry: for never-rendered entries
// IsScrolledOff reports false — the echo must never fire spuriously.
func TestChatViewport_IsScrolledOff_UnknownGeometry(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	cv.SetScrollWatermark(100)
	if cv.IsScrolledOff(tc) {
		t.Error("no layout geometry set: IsScrolledOff must be false")
	}

	// Geometry present and content overflowing, but the queried entry was
	// appended after the last render (no rendered geometry yet).
	cv2 := NewChatViewport()
	for i := 0; i < 6; i++ {
		cv2.AddToolExecution("bash", `{"command":"ls"}`)
	}
	cv2.Render(80)
	total := len(cv2.renderCache.lines)
	if total <= 10 {
		t.Fatalf("precondition unmet: content %d rows must overflow the 10-row band", total)
	}
	cv2.SetScrollWatermark(total - 10)
	late := cv2.AddToolExecution("bash", `{"command":"late"}`)
	if cv2.IsScrolledOff(late) {
		t.Error("entry never rendered: IsScrolledOff must be false")
	}
}

// TestChatViewport_IsScrolledOff_Suppressed: while the viewport is hidden
// (orchestration mode), its rows are not on the canvas — the check is
// meaningless and must report false.
func TestChatViewport_IsScrolledOff_Suppressed(t *testing.T) {
	cv := NewChatViewport()
	var tools []*ToolExecutionComponent
	for i := 0; i < 8; i++ {
		tools = append(tools, cv.AddToolExecution("bash", `{"command":"ls"}`))
	}
	cv.Render(80)
	cv.SetScrollWatermark(10) // pretend rows committed before suppression
	cv.SetSuppressed(true)
	for i, tc := range tools {
		if cv.IsScrolledOff(tc) {
			t.Errorf("suppressed viewport: widget %d reported scrolled off", i)
		}
	}
}
