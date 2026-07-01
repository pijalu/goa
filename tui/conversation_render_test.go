// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// collectFrames splits the captured terminal writes by CSI 2026 synchronized
// output boundaries. Each captured write sequence between \e[?2026h and
// \e[?2026l is one frame.
func collectFrames(term *fakeTerminal) []string {
	var buf strings.Builder
	for _, w := range term.writes {
		buf.WriteString(w)
	}
	all := buf.String()
	parts := strings.Split(all, "\x1b[?2026h")
	var frames []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		frames = append(frames, p)
	}
	return frames
}

// TestChatLargeAppendScrollsWithoutErasingScrollback is a regression test for
// a TUI rendering bug where appending a large block (e.g. a tool result) to a
// chat that already exceeds the viewport triggered a full screen clear
// (\e[2J) plus scrollback erase (\e[3J). This made scrolling up through the
// conversation show missing/corrupted history. Large appends should scroll the
// terminal instead.
func TestChatLargeAppendScrollsWithoutErasingScrollback(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	header := NewHeader("goa", "v0.1")
	chat := NewChatViewport()
	inp := NewEditor()
	footer := NewFooter()
	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Fill the buffer so the viewport is already near the bottom.
	for i := 0; i < 30; i++ {
		chat.AddSystemMessage(fmt.Sprintf("baseline %d", i))
	}
	engine.RenderNow()
	before := engine.compositor.FullRedrawCount()

	// Append a large preformatted block that exceeds the terminal height.
	var big strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&big, "tool output line %d %s\n", i, strings.Repeat("x", 70))
	}
	chat.AddSystemMessagePreformatted(big.String())
	engine.RenderNow()

	if engine.compositor.FullRedrawCount() > before {
		t.Errorf("large chat append triggered a full redraw (scrollback would be erased)")
	}

	frames := collectFrames(term)
	if len(frames) == 0 {
		t.Fatal("no frames rendered")
	}
	last := frames[len(frames)-1]
	if strings.Contains(last, "\x1b[3J") || strings.Contains(last, "\x1b[2J") {
		t.Errorf("last frame contains a full screen/scrollback erase")
	}
	if !strings.Contains(last, "tool output line 199") {
		t.Errorf("latest tool output line not visible in last frame (len=%d)", len(last))
	}
}

// TestOverlayBufferGrowthRedrawsFullScreen verifies that when an overlay is
// open and the base chat buffer grows past the previous viewport, closing the
// overlay performs a full redraw. Differential rendering at stale row positions
// would leave overlay artifacts mixed with the new chat content.
func TestOverlayBufferGrowthRedrawsFullScreen(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	for i := 0; i < 30; i++ {
		chat.AddSystemMessage(fmt.Sprintf("baseline %d", i))
	}
	engine.RenderNow()

	overlay := NewInput()
	handle := engine.ShowOverlay(overlay, OverlayOptions{CaptureInput: true, Height: 10})
	engine.RenderNow()

	// Grow the base buffer while the overlay is visible. The render that
	// incorporates the growth while the overlay is still open must perform a
	// full redraw: the old overlay region in prevLines now lies above the new
	// viewport, so differential rendering would write at stale rows.
	before := engine.compositor.FullRedrawCount()
	for i := 0; i < 20; i++ {
		chat.AddSystemMessage(fmt.Sprintf("behind overlay %d", i))
	}
	engine.RenderNow()
	if engine.compositor.FullRedrawCount() <= before {
		t.Errorf("base-buffer growth while overlay is open did not perform a full redraw")
	}

	// After the overlay is hidden the chat content should be visible without
	// leftover overlay artifacts.
	handle.Hide()
	engine.RenderNow()

	// The virtual buffer after closing the overlay must show the latest
	// message that was added while the overlay was open.
	final := engine.RenderNow()
	joined := strings.Join(final, "\n")
	if !strings.Contains(joined, "behind overlay 19") {
		t.Errorf("latest message added behind the overlay is not visible after closing")
	}
}
