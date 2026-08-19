// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// goalListRow builds one /goal:list-style markdown row. The real command
// writes each goal's COMPLETE objective (no truncation), so a realistic list
// with several goals can easily fill the screen.
func goalListRow(n int) string {
	return fmt.Sprintf("**%d. [active] goal-%02d**\n\n%s\n\n", n, n,
		strings.Repeat("Build feature set "+fmt.Sprintf("%d", n)+" with a very long objective description that wraps ", 12))
}

// benchmarkGoalListDuringStream measures the per-chunk render cost of the
// reported scenario: a /goal:list that fills the screen is appended while the
// agent is streaming, and the stream keeps updating the assistant block that
// is now ABOVE the goal list (off-screen).
//
// Hypothesis: every stream chunk after the goal list lands triggers a full
// scrollback reset + re-emit (drawWindowResetScrollback) because the growing
// streaming block shifts the goal-list rows inside the scroll-off region, so
// the compositor treats each frame as unstable. That is O(entire transcript)
// terminal bytes PER CHUNK instead of O(1) — the CPU >100% until a new block
// starts after the goal list (when the fast path resumes).
func benchmarkGoalListDuringStream(b *testing.B, goalListSize, chunks int) {
	b.Helper()
	term := &fakeTerminal{w: 80, h: 24}
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
		b.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	// Pre-fill history so the viewport is bottom-anchored (rows flow to
	// scrollback) — the state a real session is in.
	for i := 0; i < 10; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history row %02d", i))
	}
	engine.RenderNow()

	// Open the streaming block and grow it a bit.
	chat.AddAssistantMessage("")
	acc := []string{"Intro sentence."}
	chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
	engine.RenderNow()

	// /goal:list lands: a huge system message appended AFTER the stream block.
	var list strings.Builder
	for i := 0; i < goalListSize; i++ {
		list.WriteString(goalListRow(i))
	}
	chat.AddSystemMessage("> /goal:list")
	chat.AddSystemMessage(list.String())
	engine.RenderNow()

	// Continue streaming: each chunk updates the assistant block that is now
	// NOT the last entry and (with a screen-filling goal list) off-screen.
	b.ResetTimer()
	for i := 0; i < chunks; i++ {
		acc = append(acc, fmt.Sprintf("streamed sentence number %d with some padding words here.", i))
		chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
		engine.RenderNow()
	}
	b.StopTimer()
}

func BenchmarkGoalListDuringStream_Small(b *testing.B)       { benchmarkGoalListDuringStream(b, 2, 10) }
func BenchmarkGoalListDuringStream_FillsScreen(b *testing.B) { benchmarkGoalListDuringStream(b, 8, 10) }
func BenchmarkGoalListDuringStream_Huge(b *testing.B)        { benchmarkGoalListDuringStream(b, 30, 10) }

// TestGoalListDuringStream_NoResetStorm asserts the FIX: after /goal:list
// lands mid-stream, each stream chunk must NOT trigger a full scrollback reset
// + re-emit (\x1b[3J wipe + top-down re-emit of the whole transcript). A
// reset-per-chunk is the CPU >100% mechanism: the terminal receives
// O(transcript) bytes per chunk instead of O(viewport).
func TestGoalListDuringStream_NoResetStorm(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
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

	for i := 0; i < 10; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history row %02d", i))
	}
	engine.RenderNow()

	chat.AddAssistantMessage("")
	acc := []string{"Intro sentence."}
	chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
	engine.RenderNow()

	// A goal list large enough to fill the screen (many rows beyond the
	// 24-row terminal).
	var list strings.Builder
	for i := 0; i < 12; i++ {
		list.WriteString(goalListRow(i))
	}
	chat.AddSystemMessage("> /goal:list")
	chat.AddSystemMessage(list.String())
	engine.RenderNow()
	term.writes = nil // forget the reset that legitimately built the first post-list frame

	// Continue streaming after the goal list. Every chunk updates the
	// now-off-screen assistant block. No chunk may trigger a scrollback wipe.
	for i := 0; i < 20; i++ {
		acc = append(acc, fmt.Sprintf("streamed sentence number %d with some padding words here.", i))
		chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
		engine.RenderNow()
		writes := term.Writes()
		if len(writes) == 0 {
			continue
		}
		joined := strings.Join(writes, "")
		if strings.Contains(joined, "\x1b[3J") {
			t.Fatalf("chunk %d triggered a full scrollback reset (\\x1b[3J wipe); "+
				"per-chunk resets are the CPU >100%% storm. Frame bytes: %d\n%s",
				i, len(joined), joined)
		}
		term.writes = nil
	}
}

// TestGoalListDuringStream_ScrollbackIntegrity guards the correctness side:
// after the mid-stream goal list and continued streaming, every distinct row
// must be present exactly once in scrollback+screen — the fix must not trade
// the CPU storm for lost/duplicated history.
func TestGoalListDuringStream_ScrollbackIntegrity(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
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

	var history []string
	for i := 0; i < 10; i++ {
		row := fmt.Sprintf("history row %02d", i)
		history = append(history, row)
		chat.AddSystemMessage(row)
	}
	engine.RenderNow()

	chat.AddAssistantMessage("")
	acc := []string{"Intro sentence."}
	chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
	engine.RenderNow()

	var list strings.Builder
	for i := 0; i < 12; i++ {
		list.WriteString(goalListRow(i))
	}
	chat.AddSystemMessage("> /goal:list")
	chat.AddSystemMessage(list.String())
	engine.RenderNow()

	// Unique short tokens per chunk (never split by wrap, unambiguous —
	// "sent01" is not a prefix of "sent10").
	for i := 0; i < 20; i++ {
		acc = append(acc, fmt.Sprintf("sent%02d some padding words here.", i))
		chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
		engine.RenderNow()
	}

	// The stream settles: the app renders one final frame after the stream
	// ends (turn-end UI update). That frame sees no chat mutation since the
	// last chunk and triggers the deferred scrollback sync (one full reset)
	// so the scrollback matches the canvas again.
	engine.RenderNow()

	// Replay through the faithful emulator and assert presence-once.
	emu := NewTermEmulator(term.h, term.w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	visible := make([]string, term.h)
	for r := 0; r < term.h; r++ {
		visible[r] = emu.Visible(r)
	}
	scrollback := emu.Scrollback()
	joined := "\n" + strings.Join(append(append([]string{}, scrollback...), visible...), "\n") + "\n"
	sbJoined := "\n" + strings.Join(scrollback, "\n") + "\n"
	dump := "\n--- screen ---\n" + strings.Join(visible, "\n") + "\n--- scrollback tail ---\n" + tailLines(scrollback, 15)

	// Exact-match row assertion: each marker row must be present at least once
	// and never duplicated within scrollback.
	assertRow := func(marker string) {
		if !strings.Contains(joined, marker) {
			t.Errorf("row %q LOST (absent from scrollback+screen)%s", marker, dump)
		}
		if n := strings.Count(sbJoined, marker); n > 1 {
			t.Errorf("row %q duplicated WITHIN scrollback (%d times) — watermark re-emitted it%s", marker, n, dump)
		}
	}
	for _, row := range history {
		assertRow(row)
	}
	for i := 0; i < 20; i++ {
		assertRow(fmt.Sprintf("sent%02d", i))
	}
	for i := 0; i < 12; i++ {
		assertRow(fmt.Sprintf("goal-%02d", i))
	}
}
