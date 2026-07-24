// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_QuotaDuringStream_NoDuplicatedRows is the regression test for
// bugs.md "/quota request during streaming corrupts the TUI": issuing /quota
// while a streaming block (e.g. "Thinking…") is being written repaints the
// input box and trailing border lines many times over, interleaved with the
// stream, leaving duplicated box fragments.
//
// Reproduction: a stream grows the last transcript entry (UpdateLastMessage,
// the path handleThinkingContent/handleAssistantContent use). Mid-stream a
// tall /quota table is appended as a system message (echoCommandResult's
// AddSystemMessage). The stream then resumes. Every distinct content row must
// land in scrollback-or-on-screen EXACTLY once; a duplicated row means the
// scrollback watermark desynced and re-emitted already-scrolled content.
func TestCompositor_QuotaDuringStream_NoDuplicatedRows(t *testing.T) {
	for _, tc := range []struct {
		name       string
		streaming  bool
		steering   bool
	}{
		{name: "full repro: stream+steering+quota", streaming: true, steering: true},
		{name: "no steering bubble", streaming: true, steering: false},
		{name: "no streaming (static last msg)", streaming: false, steering: true},
		{name: "neither stream nor steering", streaming: false, steering: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runQuotaStreamScenario(t, tc.streaming, tc.steering)
		})
	}
}

func runQuotaStreamScenario(t *testing.T, streaming, steering bool) {
	const w, h = 80, 20
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	header := NewHeader("goa", "test")
	chat := NewChatViewport()
	status := NewStatusMsg()
	steeringChrome := NewSteeringChrome()
	inp := NewEditor()
	footer := NewFooter()

	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(status)
	engine.AddChild(steeringChrome)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)
	status.SetTUI(engine)
	inp.SetTUI(engine)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Pre-fill history so the viewport is already bottom-anchored and rows
	// are flowing into scrollback (the state a real session is in).
	var history []string
	for i := 0; i < 25; i++ {
		s := fmt.Sprintf("history row %02d", i)
		history = append(history, s)
		chat.AddSystemMessage(s)
	}
	engine.RenderNow()

	// Begin a streaming assistant block that grows across several frames,
	// pushing the viewport up as it goes (the "Thinking…" block in flight).
	// Each stream chunk is its own hard canvas row (ordered markdown list) so
	// rows are individually identifiable in the emulator output. When
	// streaming=false the same lines are appended as separate static entries.
	chat.AddAssistantMessage("")
	var streamLines []string
	// markerN is the reflow-stable token used to locate chunk N in the
	// emulator output. The assistant markdown view may re-wrap a long line,
	// but a short "chunkNN" token never splits across rows.
	markerN := func(n int) string { return fmt.Sprintf("chunk%02d", n) }
	streamStep := func() {
		streamLines = append(streamLines, fmt.Sprintf("%d. %s", len(streamLines)+1, markerN(len(streamLines))))
		if streaming {
			chat.UpdateLastMessage(strings.Join(streamLines, "\n"), ConsoleAssistantMessage)
		} else {
			chat.AddSystemMessage(streamLines[len(streamLines)-1])
		}
		engine.RenderNow()
	}
	for i := 0; i < 6; i++ {
		streamStep()
	}

	// The user queues a steering message mid-turn (ESC steering): the pending
	// bubble shows "(alt+e to edit)" as pinned bottom chrome — NOT a transcript
	// entry — so the transcript stays append-only and the scrollback watermark
	// is never perturbed by the stream + quota appends below.
	if steering {
		steeringChrome.Add("hold on, check quota first")
		engine.RenderNow()
	}

	// Mid-stream: /quota returns a tall bordered table. echoCommandResult
	// appends it as system messages while the stream block is still active —
	// and each Append re-orders the pending steering bubble below it.
	chat.AddSystemMessage("> /quota")
	var quota []string
	quota = append(quota, "┌─ Quota ────────────────────────────┐")
	for i := 0; i < 12; i++ {
		quota = append(quota, fmt.Sprintf("│ provider-%02d  72%% left          │", i))
	}
	quota = append(quota, "└────────────────────────────────────┘")
	chat.AddSystemMessage(strings.Join(quota, "\n"))
	engine.RenderNow()

	// The stream resumes and grows past the viewport bottom.
	for i := 0; i < 8; i++ {
		streamStep()
	}
	// Turn ends: the stream block finalizes and the steering bubble is consumed.
	if steering {
		steeringChrome.Clear()
	}
	engine.RenderNow()

	// Replay every byte the compositor emitted through the faithful emulator.
	emu := NewTermEmulator(h, w)
	resetSeen := false
	for _, wr := range term.Writes() {
		if strings.Contains(wr, "\x1b[3J") {
			resetSeen = true
		}
		emu.Process(wr)
	}
	visible := make([]string, h)
	for r := 0; r < h; r++ {
		visible[r] = emu.Visible(r)
	}
	scrollback := emu.Scrollback()
	all := append(append([]string{}, scrollback...), visible...)
	joined := "\n" + strings.Join(all, "\n") + "\n"
	sbJoined := "\n" + strings.Join(scrollback, "\n") + "\n"

	dump := func() string {
		return "\n--- screen ---\n" + strings.Join(visible, "\n") + "\n--- scrollback tail ---\n" + tailLines(scrollback, 12)
	}
	// A row must never be LOST (it has to be recoverable from scrollback or
	// the screen) and never DUPLICATED WITHIN SCROLLBACK (the corruption:
	// already-scrolled rows re-emitted). Note: a row MAY legitimately appear
	// in BOTH scrollback and the visible screen across a chrome SHRINK — rows
	// scrolled into scrollback while the bubble was up are revealed back on
	// screen when the window grows on clear. That cross-boundary overlap is
	// correct, so the assertions are (a) present somewhere, (b) not duplicated
	// inside scrollback.
	assertNoScrollbackDup := func(marker string) {
		if n := strings.Count(sbJoined, marker); n > 1 {
			t.Errorf("row %q duplicated WITHIN scrollback (%d times) — watermark re-emitted it%s", marker, n, dump())
		}
	}
	assertPresent := func(marker string) {
		if n := strings.Count(joined, marker); n < 1 {
			t.Errorf("row %q LOST (absent from scrollback+screen)%s", marker, dump())
		}
	}

	_ = resetSeen // the fix must not require a scrollback reset; presence is
	// asserted the same way whether or not one fired.
	for _, s := range history {
		assertPresent(s)
		assertNoScrollbackDup(s)
	}
	for i := range streamLines {
		assertPresent(markerN(i))
		assertNoScrollbackDup(markerN(i))
	}
	// The quota header survives the system-message box reflow; the corruption
	// re-emitted it into scrollback, so it must not be duplicated there.
	assertPresent("┌─ Quota")
	assertNoScrollbackDup("┌─ Quota")
}

// tailLines returns the last n lines of s (or all of s when shorter).
func tailLines(s []string, n int) string {
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return strings.Join(s, "\n")
}
