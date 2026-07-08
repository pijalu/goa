// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestChatViewport_AllocatedHeightKeepsInputPinned replaces the former
// "PositionStability_InputLineDoesNotJumpUp" test. The old test asserted the
// viewport height was monotonically non-decreasing — which encoded the very
// downward-drift / scroll-off bug we fixed.
//
// Correct invariant: with a layout budget, the chat viewport renders at a
// STABLE height (the budget) whether the conversation is full or empty, so the
// input editor stacked below it stays pinned at the same screen row across
// grow/shrink. Overflow scrolls into scrollback instead of pushing the editor.
func TestChatViewport_AllocatedHeightKeepsInputPinned(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)

	chat := NewChatViewport()
	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetMaxLines(1)
	ed.SetFocused(true)

	engine.AddChild(chat)
	engine.AddChild(ed)
	engine.SetFocus(ed)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Grow the conversation past the budget (overflow).
	for i := 0; i < 20; i++ {
		chat.AddUserMessage(strings.Repeat("x", 80))
	}
	engine.RequestRender()
	screen := visibleBottomScreen(engine.AgentFrame(), 24)
	yAfterGrow := strings.LastIndex(screen, strings.TrimSpace(screen)) // sanity: screen non-empty
	_ = yAfterGrow
	editorBottomRowAfterGrow := lastNonBlankRow(screen)

	// Shrink the conversation to nothing. The chat viewport must fill its
	// budget (blank), so the editor stays at the same screen row.
	chat.Clear()
	engine.RequestRender()
	screen = visibleBottomScreen(engine.AgentFrame(), 24)
	editorBottomRowAfterShrink := lastNonBlankRow(screen)

	if editorBottomRowAfterShrink != editorBottomRowAfterGrow {
		t.Fatalf("input/editor screen row moved from %d to %d after the chat shrank; the footer must stay pinned",
			editorBottomRowAfterGrow, editorBottomRowAfterShrink)
	}
}

// visibleBottomScreen returns the visible viewport as a single string. With
// the bottom-anchored layout the last visible row is the bottom-most chrome line.
func visibleBottomScreen(frame AgentFrame, h int) string {
	vis := frame.Visible
	if len(vis) > h {
		vis = vis[len(vis)-h:]
	}
	return strings.Join(vis, "\n")
}

func lastNonBlankRow(s string) int {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}
