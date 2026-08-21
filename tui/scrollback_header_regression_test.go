// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestScrollback_HeaderEmittedOnceContiguous is the regression test for the
// startup scrollback corruption: logo rows were duplicated deep into the
// transcript scrollback. Root cause: when the transcript window was "partial"
// (content shorter than the transcript region, so blank padding rows existed),
// the steady-scroll path re-emitted the already-visible top rows at the scroll
// bottom, pushing a second copy of the header into scrollback.
//
// The invariant: every transcript row enters scrollback exactly once, in
// order; the header (logo) rows appear only as the leading contiguous block.
func TestScrollback_HeaderEmittedOnceContiguous(t *testing.T) {
	const w, h = 120, 30
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	header := NewHeader("goa", "v0.1.0-dev")
	engine.AddChild(header)
	chat := NewChatViewport()
	engine.AddChild(chat)
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

	// Partial window first (content shorter than the screen), then overflow.
	chat.AddSystemMessage("Context loaded")
	chat.AddSystemMessage("skills loaded")
	chat.AddSystemMessage("Connected to model")
	engine.RenderNow()
	chat.AddUserMessage("/model")
	chat.AddSystemMessage("model done")
	for i := 0; i < 40; i++ {
		chat.AddSystemMessage(fmt.Sprintf("filler %02d", i))
		engine.RenderNow()
	}

	emu := NewTermEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	sb := emu.Scrollback()

	assertScrollbackHeader(t, sb)
	if t.Failed() {
		t.Logf("scrollback (%d rows):\n%s", len(sb), strings.Join(sb, "\n"))
	}
}
