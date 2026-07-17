// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestPinnedChromeNeverEntersScrollback is the regression test for the
// "history rendering leak": when the scrollable transcript (chat + a running
// tool widget) grows the base canvas past the screen height and then SHRINKS
// (tool completes/collapses, status bar clears), the fixed bottom chrome
// (status bar, input editor, footer) must be redrawn in place and must NEVER
// be emitted into terminal scrollback.
//
// The leak is specific to the grow-then-SHRINK path: the shrink logic
// (renderDeletedLines / the shrink-to-fit repaint) misplaces the chrome rows,
// leaving copies frozen in scrollback — visible as the user's input line and
// the "Tool calling" banner appearing as permanent scrollback rows above the
// tool result. Chrome and tool-call text are kept distinct here so the
// assertion cannot false-match the tool widget's own (legitimate) header.
func TestPinnedChromeNeverEntersScrollback(t *testing.T) {
	const w, h = 70, 14
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	header := NewHeader("goa", "test")
	engine.AddChild(header)
	chat := NewChatViewport()
	engine.AddChild(chat)
	statusBar := NewStatusMsg()
	engine.AddChild(statusBar)
	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	footer := NewFooter()
	engine.AddChild(footer)
	engine.SetFocus(ed)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	statusBar.Show("Tool calling")
	ed.HandleInput("git status --porcelain")
	engine.RenderNow()

	// Grow the transcript past the screen height.
	tc := chat.AddAgentToolExecution("", "bash", `{"command":"go test ./..."}`)
	tc.SetStatus(ToolRunning)
	var sb strings.Builder
	for i := 1; i <= h*3; i++ {
		fmt.Fprintf(&sb, "ok pkg %02d\n", i)
	}
	tc.SetOutput(sb.String())
	engine.RenderNow()

	// Shrink: the tool completes with tiny output and the status bar clears.
	tc.SetOutput("done\n")
	tc.SetStatus(ToolSuccess)
	statusBar.Show("")
	engine.RenderNow()

	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	for i, l := range emu.Scrollback() {
		cl := ansiClean(l)
		if strings.Contains(cl, "git status --porcelain") {
			t.Errorf("input editor leaked into scrollback row %d: %q", i, strings.TrimSpace(cl))
		}
		if strings.Contains(cl, "Tool calling") {
			t.Errorf("status bar leaked into scrollback row %d: %q", i, strings.TrimSpace(cl))
		}
	}
}
