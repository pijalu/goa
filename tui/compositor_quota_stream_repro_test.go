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

// quotaScenario drives the /quota-during-stream scenario through a real
// engine and asserts scrollback integrity via the faithful terminal emulator.
type quotaScenario struct {
	t         *testing.T
	engine    *TUI
	term      *fakeTerminal
	chat      *ChatViewport
	steering  *SteeringChrome
	streaming bool
	history   []string
	stream    []string
}

func runQuotaStreamScenario(t *testing.T, streaming, steering bool) {
	s := newQuotaScenario(t, streaming)
	defer s.engine.Stop()

	s.fillHistory(25)
	s.streamChunks(6)
	if steering {
		s.steering.Add("hold on, check quota first")
		s.engine.RenderNow()
	}
	s.appendQuotaTable()
	s.streamChunks(8)
	if steering {
		s.steering.Clear()
	}
	s.engine.RenderNow()

	s.assertScrollbackIntegrity()
}

func newQuotaScenario(t *testing.T, streaming bool) *quotaScenario {
	t.Helper()
	const w, h = 80, 20
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	chat := NewChatViewport()
	status := NewStatusMsg()
	steering := NewSteeringChrome()
	inp := NewEditor()
	for _, c := range []Component{NewHeader("goa", "test"), chat, status, steering, inp, NewFooter()} {
		engine.AddChild(c)
	}
	engine.SetFocus(inp)
	status.SetTUI(engine)
	inp.SetTUI(engine)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return &quotaScenario{t: t, engine: engine, term: term, chat: chat, steering: steering, streaming: streaming}
}

// markerN is the reflow-stable token used to locate stream chunk N in the
// emulator output; the assistant markdown view may re-wrap a long line, but a
// short "chunkNN" token never splits across rows.
func markerN(n int) string { return fmt.Sprintf("chunk%02d", n) }

// fillHistory pre-fills the transcript so the viewport is bottom-anchored and
// rows are flowing into scrollback (the state a real session is in).
func (s *quotaScenario) fillHistory(n int) {
	for i := 0; i < n; i++ {
		row := fmt.Sprintf("history row %02d", i)
		s.history = append(s.history, row)
		s.chat.AddSystemMessage(row)
	}
	s.engine.RenderNow()
}

// streamChunks grows the streaming assistant block by n chunks (each its own
// hard canvas row). When streaming=false the same lines are appended as
// separate static entries instead.
func (s *quotaScenario) streamChunks(n int) {
	if s.streaming && len(s.stream) == 0 {
		// Open the streaming block on the first chunk so UpdateLastMessage has
		// an assistant entry to grow.
		s.chat.AddAssistantMessage("")
	}
	for i := 0; i < n; i++ {
		s.stream = append(s.stream, fmt.Sprintf("%d. %s", len(s.stream)+1, markerN(len(s.stream))))
		if s.streaming {
			s.chat.UpdateLastMessage(strings.Join(s.stream, "\n"), ConsoleAssistantMessage)
		} else {
			s.chat.AddSystemMessage(s.stream[len(s.stream)-1])
		}
		s.engine.RenderNow()
	}
}

// appendQuotaTable appends a tall bordered /quota table as system messages
// while the stream block is still active.
func (s *quotaScenario) appendQuotaTable() {
	s.chat.AddSystemMessage("> /quota")
	quota := []string{"┌─ Quota ────────────────────────────┐"}
	for i := 0; i < 12; i++ {
		quota = append(quota, fmt.Sprintf("│ provider-%02d  72%% left          │", i))
	}
	quota = append(quota, "└────────────────────────────────────┘")
	s.chat.AddSystemMessage(strings.Join(quota, "\n"))
	s.engine.RenderNow()
}

// assertScrollbackIntegrity replays every emitted byte through the faithful
// emulator and asserts no transcript row is lost and none is duplicated within
// scrollback.
func (s *quotaScenario) assertScrollbackIntegrity() {
	emu := NewTermEmulator(s.term.h, s.term.w)
	for _, wr := range s.term.Writes() {
		emu.Process(wr)
	}
	visible := make([]string, s.term.h)
	for r := 0; r < s.term.h; r++ {
		visible[r] = emu.Visible(r)
	}
	scrollback := emu.Scrollback()
	joined := "\n" + strings.Join(append(append([]string{}, scrollback...), visible...), "\n") + "\n"
	sbJoined := "\n" + strings.Join(scrollback, "\n") + "\n"
	dump := "\n--- screen ---\n" + strings.Join(visible, "\n") + "\n--- scrollback tail ---\n" + tailLines(scrollback, 12)

	// A row must never be LOST (recoverable from scrollback or screen) and
	// never DUPLICATED WITHIN SCROLLBACK (the corruption: an already-scrolled
	// row re-emitted). A row MAY legitimately appear in BOTH scrollback and
	// screen across a chrome SHRINK — rows scrolled into scrollback while the
	// bubble was up are revealed on screen when the window grows on clear; that
	// cross-boundary overlap is correct, so only within-scrollback duplication
	// and outright loss are asserted.
	assertRow := func(marker string) {
		if !strings.Contains(joined, marker) {
			s.t.Errorf("row %q LOST (absent from scrollback+screen)%s", marker, dump)
		}
		if n := strings.Count(sbJoined, marker); n > 1 {
			s.t.Errorf("row %q duplicated WITHIN scrollback (%d times) — watermark re-emitted it%s", marker, n, dump)
		}
	}
	for _, row := range s.history {
		assertRow(row)
	}
	for i := range s.stream {
		assertRow(markerN(i))
	}
	assertRow("┌─ Quota")
}

// tailLines returns the last n lines of s (or all of s when shorter).
func tailLines(s []string, n int) string {
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return strings.Join(s, "\n")
}