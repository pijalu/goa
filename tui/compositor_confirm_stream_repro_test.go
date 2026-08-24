// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_ConfirmDuringStream_NoCorruption is the M3 compositor
// regression (plugins plan §4 step 6): a plugin confirmation modal shown
// MID-STREAM must not corrupt frames. The card composites as a LayerOverlay;
// opening/closing it must never push transcript rows into scrollback twice,
// lose rows, or break the streaming block underneath. This is also the live
// proof behind the §9 Q3 decision (selector-style capturing overlay works
// during a stream).
func TestCompositor_ConfirmDuringStream_NoCorruption(t *testing.T) {
	s := newConfirmStreamScenario(t)
	defer s.engine.Stop()

	// 1. Bottom-anchored transcript with active scrollback flow.
	s.fillHistory(25)

	// 2. Start the assistant stream.
	s.streamChunks(6)

	// 3. Plugin confirm pops mid-stream as a capturing overlay.
	result, _ := s.engine.ShowConfirm("Use rate-limit reset?",
		"This consumes one credit.", []ConfirmOption{
			{ID: "yes", Label: "Yes, use reset", Style: "danger"},
			{ID: "no", Label: "Not now", Style: "ok"},
		}, "no", true)
	s.engine.RenderNow()

	// The card must be visible ON TOP of the still-growing transcript…
	if vis := s.engine.VisibleText(); !strings.Contains(vis, "Use rate-limit reset?") {
		s.t.Fatalf("confirm card not composited over the stream:\n%s", vis)
	}
	// …while the stream keeps flowing underneath.
	s.streamChunks(4)

	// 4. The user answers through the normal key path (overlay captures).
	s.engine.SendKey(KeyEnter) // highlighted row = defaultID "no"

	select {
	case got := <-result:
		if got != "no" {
			s.t.Fatalf("confirm answer = %q, want \"no\"", got)
		}
	default:
		s.t.Fatal("answer not delivered after enter")
	}
	if len(s.engine.overlayStack) != 0 {
		s.t.Fatalf("card overlay still up after answer (%d entries)", len(s.engine.overlayStack))
	}

	// 5. Stream resumes post-answer; frames must stay coherent.
	s.streamChunks(8)
	s.engine.RenderNow()

	// 6. Full scrollback integrity: nothing lost, nothing re-emitted.
	s.assertScrollbackIntegrity()
}

// confirmStreamScenario mirrors quotaScenario's harness for the confirm flow.
type confirmStreamScenario struct {
	t       *testing.T
	engine  *TUI
	term    *fakeTerminal
	chat    *ChatViewport
	history []string
	stream  []string
}

func newConfirmStreamScenario(t *testing.T) *confirmStreamScenario {
	t.Helper()
	const w, h = 80, 20
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
	return &confirmStreamScenario{t: t, engine: engine, term: term, chat: chat}
}

func (s *confirmStreamScenario) fillHistory(n int) {
	for i := 0; i < n; i++ {
		row := fmt.Sprintf("history row %02d", i)
		s.history = append(s.history, row)
		s.chat.AddSystemMessage(row)
	}
	s.engine.RenderNow()
}

// streamChunks grows the streaming assistant block by n chunks (each its own
// hard canvas row), exactly like the quota repro's streaming path.
func (s *confirmStreamScenario) streamChunks(n int) {
	if len(s.stream) == 0 {
		s.chat.AddAssistantMessage("")
	}
	for i := 0; i < n; i++ {
		s.stream = append(s.stream, fmt.Sprintf("%d. chunk%02d", len(s.stream)+1, len(s.stream)))
		s.chat.UpdateLastMessage(strings.Join(s.stream, "\n"), ConsoleAssistantMessage)
		s.engine.RenderNow()
	}
}

// assertScrollbackIntegrity replays every emitted byte through the faithful
// emulator and asserts no transcript row was lost and none was duplicated
// within scrollback (the overlay must be invisible to scrollback accounting).
func (s *confirmStreamScenario) assertScrollbackIntegrity() {
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

	assertRow := func(marker string) {
		if !strings.Contains(joined, marker) {
			s.t.Errorf("row %q LOST (absent from scrollback+screen)%s", marker, dump)
		}
		if n := strings.Count(sbJoined, marker); n > 1 {
			s.t.Errorf("row %q duplicated WITHIN scrollback (%d times)%s", marker, n, dump)
		}
	}
	for _, row := range s.history {
		assertRow(row)
	}
	for i := range s.stream {
		assertRow(fmt.Sprintf("chunk%02d", i))
	}
}
