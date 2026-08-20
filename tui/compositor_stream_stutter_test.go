// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// stutterSentence returns the i-th streaming chunk: a sentence whose length
// varies by index so the accumulated block re-wraps across frames. The short
// unique marker tokNN never splits across rows regardless of re-wrap.
func stutterSentence(i int) string {
	marker := fmt.Sprintf("tok%02d", i)
	switch i % 3 {
	case 0:
		return marker + " short."
	case 1:
		return marker + " " + strings.Repeat("word ", 14) + " end." // forces re-wrap
	default:
		return marker + " medium length clause here."
	}
}

// TestCompositor_StreamingGrowth_NoStutter is the regression test for the TUI
// streaming stutter bug: during a live stream the chat repainted the
// same streamed text as duplicated/overlapping fragments, and glued re-wrapped
// sentences onto one row. The stutter began only once the growing block crossed
// the viewport bottom / a wrap boundary — i.e. a growth desync in the diff /
// watermark path, not a per-token bug.
//
// This drives a single streaming assistant block that grows in varied
// increments (so it crosses the viewport bottom AND re-wraps long lines) and
// asserts, via the faithful TermEmulator, that (a) every distinct marker is
// recoverable, (b) no marker is duplicated within scrollback, and (c) no two
// distinct sentences are glued onto a single row.
func TestCompositor_StreamingGrowth_NoStutter(t *testing.T) {
	const w, h = 60, 16
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	chat := NewChatViewport()
	status := NewStatusMsg()
	inp := NewEditor()
	for _, c := range []Component{NewHeader("goa", "test"), chat, status, inp, NewFooter()} {
		engine.AddChild(c)
	}
	engine.SetFocus(inp)
	status.SetTUI(engine)
	inp.SetTUI(engine)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Pre-fill so the viewport is bottom-anchored and rows flow to scrollback.
	for i := 0; i < 20; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history row %02d", i))
	}
	engine.RenderNow()

	// Open the streaming block and grow it in varied increments. Each chunk is
	// a sentence whose length varies so the accumulated text re-wraps across
	// frames; a short unique marker (tokNN) never splits across rows so we can
	// locate each chunk in the emulator output regardless of re-wrap.
	chat.AddAssistantMessage("")
	var acc []string
	for i := 0; i < 30; i++ {
		acc = append(acc, stutterSentence(i))
		chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
		engine.RenderNow()
	}

	// Replay every emitted byte through the emulator.
	emu := NewTermEmulator(term.h, term.w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	visible := make([]string, term.h)
	for r := 0; r < term.h; r++ {
		visible[r] = emu.Visible(r)
	}
	scrollback := emu.Scrollback()
	all := append(append([]string{}, scrollback...), visible...)
	joined := "\n" + strings.Join(all, "\n") + "\n"
	sbJoined := "\n" + strings.Join(scrollback, "\n") + "\n"

	for i := 0; i < 30; i++ {
		marker := fmt.Sprintf("tok%02d", i)
		if !strings.Contains(joined, marker) {
			t.Errorf("stream chunk %q LOST (absent from scrollback+screen)", marker)
		}
		if n := strings.Count(sbJoined, marker); n > 1 {
			t.Errorf("stream chunk %q DUPLICATED within scrollback (%d times) — stutter", marker, n)
		}
	}

	// No two distinct sentence markers may be glued onto one row without the
	// separating space the join inserted (the "concatenated re-paint" form:
	// "...suites:Let me..."). Each marker was joined with a single space, so a
	// row containing "tokNN" immediately followed (after the sentence text) by
	// another "tokMM" with no space indicates a glued repaint. We assert the
	// simpler invariant: the literal substring ".tok" (a period from one
	// sentence glued to the next marker with no space) never appears.
	for _, row := range all {
		if strings.Contains(row, ".tok") {
			t.Errorf("glued row (no space between sentences): %q", row)
		}
	}
}
