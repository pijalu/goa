// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestStreamingScroll_FooterStaysAnchored is the regression test for the
// streaming "footer ghosting/drift" artifact reproduced from a captured
// terminal log (see flash_footer_replay_test.go): during streaming the footer
// visibly moved up/down, and in the worst case drifted all the way to the top
// of the screen.
//
// Two distinct root causes, both in the Compositor's viewport-scroll handling:
//
//  1. Flicker: emitViewportScroll emitted the bare-newline viewport scroll as
//     its OWN synced frame (\x1b[?2026h...\x1b[?2026l), separate from the
//     content repaint that followed. The terminal committed the intermediate
//     frame — every row shifted up, blank rows at the bottom, footer floating
//     high — then the next frame fixed it. Visible as the footer jumping.
//
//  2. Drift: the bare-newline scroll shifts every on-screen row up, but the
//     repaint only redrew content-changed lines. With a STABLE footer (content
//     identical across streaming frames) the footer was skipped, so each scroll
//     lifted it one row and it never came back — marching to the top of the
//     screen until a full redraw eventually reset it.
//
// The fix folds the scroll into the repaint's single synced frame AND repaints
// the whole visible viewport when the scroll used bare newlines (so shifted
// bottom-anchored chrome is repainted at its absolute row). This test would
// have caught both: it drives the real Compositor into a real streaming scroll
// with an unchanged footer and asserts, by replaying the emitted bytes through
// the faithful TermEmulator, that the footer stays on the bottom row in EVERY
// committed frame.
//
// Note: like TestStreaming_NoGhosting_FaithfulEmulator, this MUST replay the
// emitted bytes — the filmstrip/AgentFrame (built from the Scene, which always
// bottom-anchors the footer) cannot see compositor-emission bugs by design.
func TestStreamingScroll_FooterStaysAnchored(t *testing.T) {
	const (
		w = 100
		h = 24
	)
	const footerModel = "STABLEFOOTERMODEL"

	term, chat, engine := setupStreamingFooterTest(t, w, h, footerModel)
	defer engine.Stop()

	fillChatHistory(chat, term, 40)
	streamFrames(chat, engine, term, 30)

	frames := captureFooterFrames(term, h, footerModel, w)
	assertFooterAnchored(t, frames, h, term, w)
}

// setupStreamingFooterTest builds a TUI with header, chat, status, editor, and
// footer for the footer-anchoring regression test.
func setupStreamingFooterTest(t *testing.T, w, h int, footerModel string) (*fakeTerminal, *ChatViewport, *TUI) {
	t.Helper()
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	header := NewHeader("goa", "x")
	engine.AddChild(header)

	chat := NewChatViewport()
	engine.AddChild(chat) // the layout fill: absorbs chrome-height changes

	status := NewStatusMsg()
	engine.AddChild(status)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	engine.SetFocus(ed)

	footer := NewFooter()
	footer.SetData(FooterData{
		Workdir: "/project", Mode: "yolo", Profile: "coder", Model: footerModel,
	})
	engine.AddChild(footer)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return term, chat, engine
}

// fillChatHistory adds enough history to overflow the viewport.
func fillChatHistory(chat *ChatViewport, term *fakeTerminal, n int) {
	for i := 0; i < n; i++ {
		chat.AddSystemMessage("history line that wraps the conversation region")
	}
	term.writes = nil // discard initial fill bytes; the regression is about streaming frames
}

// streamFrames renders more streaming content while the footer stays identical.
func streamFrames(chat *ChatViewport, engine *TUI, term *fakeTerminal, n int) {
	for i := 0; i < n; i++ {
		chat.AddSystemMessage("streaming line growing the conversation tail")
		engine.RenderNow()
	}
}

// footerFrame holds the footer position for one committed frame.
type footerFrame struct {
	idx       int
	footerRow int // bottom-most row containing the footer model token, -1 none
	bottomHas bool
}

// captureFooterFrames replays emitted bytes and snapshots after each synced frame.
func captureFooterFrames(term *fakeTerminal, h int, footerModel string, w int) []footerFrame {
	emu := NewTermEmulator(h, w)
	var frames []footerFrame
	frameIdx := 0
	for _, write := range term.Writes() {
		emu.Process(write)
		if !strings.HasSuffix(write, "\x1b[?2026l") {
			continue
		}
		fr := footerFrame{idx: frameIdx, footerRow: -1}
		for r := h - 1; r >= 0; r-- {
			if strings.Contains(emu.Visible(r), footerModel) {
				fr.footerRow = r
				break
			}
		}
		fr.bottomHas = strings.Contains(emu.Visible(h-1), footerModel)
		frames = append(frames, fr)
		frameIdx++
	}
	return frames
}

// assertFooterAnchored verifies the footer is on the bottom row of every
// committed streaming frame.
func assertFooterAnchored(t *testing.T, frames []footerFrame, h int, term *fakeTerminal, w int) {
	t.Helper()
	if len(frames) == 0 {
		t.Fatalf("no synced frames emitted")
	}

	bad := 0
	for _, fr := range frames {
		if !fr.bottomHas || fr.footerRow != h-1 {
			bad++
		}
	}
	if bad == 0 {
		return
	}

	t.Errorf("footer was NOT bottom-anchored in %d/%d streaming frames (the bug). "+
		"Per-frame footer row (h-1=%d wanted):", bad, len(frames), h-1)
	for _, fr := range frames {
		mark := "ok"
		if fr.footerRow != h-1 {
			mark = "<<< MOVED"
		}
		t.Logf("  frame %d: footerRow=%d %s", fr.idx, fr.footerRow, mark)
	}
	emu := NewTermEmulator(h, w)
	for _, w := range term.Writes() {
		emu.Process(w)
	}
	dumpEmulator(t, emu, h)
}

func dumpEmulator(t *testing.T, emu *TermEmulator, h int) {
	t.Helper()
	t.Logf("final screen:")
	for r := 0; r < h; r++ {
		row := strings.TrimRight(emu.Visible(r), " ")
		if len(row) > 95 {
			row = row[:95]
		}
		t.Logf("  %2d|%s", r, row)
	}
}
