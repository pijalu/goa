// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestChatViewport_BottomAnchorsToAllocatedHeight replaces the former
// "RenderHeightMonotonic" test, which encoded the buggy behavior (the viewport
// height could only grow, never shrink, leaking across tabs and scrolling
// filtered content out of view).
//
// The correct invariant: when the layout allocates a height budget, the
// viewport BOTTOM-anchors its content within that budget — content sits just
// above the status line, blank padding above it — so the input/footer stay
// pinned. The rendered height equals the budget when content is smaller than
// it, and equals the natural content height when content overflows it (so the
// compositor can scroll the oldest lines into scrollback).
func TestChatViewport_BottomAnchorsToAllocatedHeight(t *testing.T) {
	cv := NewChatViewport()
	cv.SetAllocatedHeight(10)
	cv.AddUserMessage("first line")
	cv.AddUserMessage("second line")

	lines := cv.Render(80)
	if len(lines) != 10 {
		t.Fatalf("content shorter than budget: expected 10 bottom-anchored lines, got %d", len(lines))
	}
	// Top of the region is blank padding...
	if strings.TrimSpace(lines[0]) != "" {
		t.Errorf("expected blank padding at the top of the region, got %q", lines[0])
	}
	// ...content sits at the bottom of the region.
	tail := strings.Join(lines[len(lines)-4:], "\n")
	if !strings.Contains(tail, "second line") {
		t.Errorf("expected content bottom-anchored; tail was %q", tail)
	}

	// Clearing the conversation keeps the height at the budget: the footer
	// below must NOT jump up when content shrinks.
	cv.Clear()
	if h := len(cv.Render(80)); h != 10 {
		t.Fatalf("after Clear: height should remain the budget (10), got %d — components below would shift", h)
	}

	// Content taller than the budget renders at its natural height (no
	// truncation, no extra padding) so the compositor can scroll it.
	cv2 := NewChatViewport()
	cv2.SetAllocatedHeight(3)
	for i := 0; i < 8; i++ {
		cv2.AddUserMessage("line")
	}
	if h := len(cv2.Render(80)); h <= 3 {
		t.Fatalf("content overflow should render at natural height > budget, got %d (content must not be clipped)", h)
	}
}
