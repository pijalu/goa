// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestChatViewport_ShrinkDoesNotEraseScrollback is a regression test for the
// scroll-correctness bug: when the chat viewport shrinks (e.g. a tool widget
// collapses or the conversation is cleared), the differential renderer must not
// emit a full screen/scrollback erase and the terminal scrollback must remain
// intact.
func TestChatViewport_ShrinkDoesNotEraseScrollback(t *testing.T) {
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

	// Fill chat enough that some lines scroll off into scrollback.
	for i := 0; i < 20; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history line %d", i))
	}
	engine.RenderNow()

	// Replay to a fresh emulator so we can snapshot the scrollback after growth.
	emuBefore := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emuBefore.Process(w)
	}
	if len(emuBefore.Scrollback()) == 0 {
		t.Fatal("expected scrollback after initial growth")
	}
	beforeScrollback := strings.Join(emuBefore.Scrollback(), "\n")

	// Shrink the chat viewport: this is the exact condition that used to
	// trigger a full clear/redraw and wipe scrollback.
	chat.Clear()
	engine.RenderNow()

	// No scrollback erase must be emitted after the initial frame. A screen
	// clear (\x1b[2J) is acceptable for a scrollback-affecting shrink as long as
	// the scrollback history itself is not erased.
	frames := collectFrames(term)
	if len(frames) >= 2 {
		afterInitial := strings.Join(frames[1:], "")
		if strings.Contains(afterInitial, "\x1b[3J") {
			t.Errorf("chat shrink erased scrollback (\x1b[3J)")
		}
	}

	// Replaying the entire session (including the shrink frame) must preserve
	// the scrollback that existed before the shrink.
	emuAfter := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emuAfter.Process(w)
	}
	afterScrollback := strings.Join(emuAfter.Scrollback(), "\n")
	if !strings.Contains(afterScrollback, "history line 0") {
		t.Errorf("scrollback lost early history after shrink; scrollback lines=%d", len(emuAfter.Scrollback()))
	}
	if !strings.Contains(afterScrollback, beforeScrollback) {
		t.Errorf("scrollback changed after shrink:\nbefore=%s\nafter=%s", beforeScrollback, afterScrollback)
	}
}
