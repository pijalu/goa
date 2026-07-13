// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestShrinkFromScrollbackPreservesContent reproduces the regression where
// shrinking a scrolled chat (e.g. after cancelling a turn) leaves the terminal
// scrollback intact.
func TestShrinkFromScrollbackPreservesContent(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 10}
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

	// Grow the chat well beyond the viewport so scrollback is populated.
	for i := 0; i < 30; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history %d", i))
	}
	engine.RenderNow()

	emuBefore := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emuBefore.Process(w)
	}
	before := strings.Join(emuBefore.Scrollback(), "\n")
	if !strings.Contains(before, "history 0") {
		t.Fatalf("expected initial scrollback to contain history 0; got %d lines", len(emuBefore.Scrollback()))
	}

	// Shrink the chat by removing the last several messages (like cancel).
	for i := 0; i < 10; i++ {
		chat.RemoveLastMessage()
	}
	engine.RenderNow()

	// No scrollback erase must be emitted.
	frames := collectFrames(term)
	if len(frames) >= 2 {
		afterInitial := strings.Join(frames[1:], "")
		if strings.Contains(afterInitial, "\x1b[3J") {
			t.Errorf("shrink erased scrollback (\x1b[3J)")
		}
	}

	emuAfter := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emuAfter.Process(w)
	}
	after := strings.Join(emuAfter.Scrollback(), "\n")
	if !strings.Contains(after, "history 0") {
		t.Errorf("scrollback lost early content after shrink; before=%d after=%d lines", len(emuBefore.Scrollback()), len(emuAfter.Scrollback()))
		t.Logf("before:\n%s\n\nafter:\n%s", before, after)
	}
}
