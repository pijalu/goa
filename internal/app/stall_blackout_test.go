// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// TestClearChatResetsTranscript covers the application-level trigger of the
// stall blackout: clearChat (session restore / fork) empties the viewport, so
// the canvas collapses below the compositor's scrollback watermark — measured
// at 5 canvas rows against scrollTop 115 — and a window clamped to a watermark
// its canvas no longer reaches paints an empty transcript region. clearChat must
// reset the transcript, exactly like handleNewSession does for /new.
//
// This is the app-side half of the defect; the compositor half (a canvas that
// settles below its watermark must not stay black) is covered by
// TestCompositor_ShrinkBelowWatermark_Blackout in package tui.
func TestClearChatResetsTranscript(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	comp := sc.engine.Compositor()

	// A long conversation so the watermark is deep.
	for i := 0; i < 40; i++ {
		sc.chat.AddSystemMessage("history entry that must stay visible")
		sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: ""})
	}
	if got := comp.ScrollWatermark(); got < 50 {
		t.Fatalf("precondition: expected a deep scrollback watermark, got %d", got)
	}

	// The session-restore path: clear the chat, then announce the restore.
	sc.app.clearChat()

	// The transcript reset must drop the watermark, so the fresh canvas is not
	// clamped to a watermark it can no longer reach.
	if got := comp.ScrollWatermark(); got != 0 {
		t.Errorf("clearChat left scrollback watermark at %d; the collapsed canvas would render as an empty window", got)
	}

	sc.chat.AddSystemMessage("Restored session 'x' — 12 events")
	sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: ""})

	screen := visibleScreenText(sc.term)
	if !strings.Contains(screen, "Restored session") {
		t.Errorf("after clearChat the fresh canvas is not on screen (blackout).\n--- screen ---\n%s", screen)
	}
	// The stale watermark must not resurrect the old transcript either.
	if strings.Contains(screen, "history entry that must stay visible") {
		t.Errorf("after clearChat the OLD transcript is still painted.\n--- screen ---\n%s", screen)
	}
}

// visibleScreenText replays every byte written to the fake terminal through the
// faithful emulator and returns the resulting visible screen.
func visibleScreenText(term *testTerminal) string {
	w, h := term.Size()
	emu := tui.NewTermEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	var b strings.Builder
	for r := 0; r < h; r++ {
		b.WriteString(emu.Visible(r))
		b.WriteByte('\n')
	}
	return b.String()
}
