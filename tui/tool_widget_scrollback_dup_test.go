// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestChatViewport_ToolWidgetDoneNotDuplicatedInScrollback reproduces bugs.md
// item I with the REAL widget path: fill the viewport, run a tool widget
// (spinner header), complete it (✓ header) so the widget scrolls, and assert
// the completed widget's header row is not duplicated in scrollback. The
// user's transcript showed the ✓ header twice at the top of the scroll
// region.
func TestChatViewport_ToolWidgetDoneNotDuplicatedInScrollback(t *testing.T) {
	term := &fakeTerminal{w: 60, h: 12}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Baseline fills most of the viewport.
	for i := 0; i < 10; i++ {
		chat.AddSystemMessage(fmt.Sprintf("base-%d", i))
	}
	engine.RenderNow()

	// Tool widget: running, then success. Distinctive header text.
	tc := chat.AddToolExecution("search", `{"pattern":"SelectOption"}`)
	tc.SetArgsPartial(`{"pattern":"SelectOption"}`)
	tc.SetStatus(ToolRunning)
	engine.RenderNow()
	// Grow the transcript past the viewport so the widget's row scrolls off.
	for i := 0; i < 8; i++ {
		chat.AddSystemMessage(fmt.Sprintf("filler-%d", i))
	}
	engine.RenderNow()
	tc.SetStatus(ToolSuccess)
	engine.RenderNow()

	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emu.Process(w)
	}
	sb := emu.Scrollback()

	// The completed widget's ✓ header must appear at most once in scrollback.
	done := 0
	for _, line := range sb {
		if strings.Contains(line, "SelectOption") && strings.Contains(line, "✓") {
			done++
		}
	}
	if done > 1 {
		t.Errorf("completed tool widget header duplicated %d times in scrollback:\n%s", done, strings.Join(sb, "\n"))
	}
}
