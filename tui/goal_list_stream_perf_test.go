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
const (
	streamHistoryRows = 10 // pre-fill so the viewport is bottom-anchored
	goalListRows      = 12 // enough to fill an 80x24 screen and push history off-screen
	streamChunkCount  = 20 // post-list chunks re-rendering the off-screen block
)

// newStreamTUIHarness builds the component stack shared by the mid-stream
// regression tests: header/chat/status/editor/footer on an 80x24 fake
// terminal, focused and started.
func newStreamTUIHarness(t *testing.T) (*TUI, *ChatViewport, *fakeTerminal) {
	t.Helper()
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
	t.Cleanup(engine.Stop)
	return engine, chat, term
}

// runMidStreamGoalListScenario drives the shared regression sequence: seed
// bottom-anchored history, open and prime the streaming assistant block, then
// land a screen-filling /goal:list AFTER the stream block. Returns the seeded
// history markers and the stream accumulator for continued streaming.
func runMidStreamGoalListScenario(t *testing.T, engine *TUI, chat *ChatViewport) (history []string, acc []string) {
	t.Helper()
	for i := 0; i < streamHistoryRows; i++ {
		row := fmt.Sprintf("history row %02d", i)
		history = append(history, row)
		chat.AddSystemMessage(row)
	}
	engine.RenderNow()

	chat.AddAssistantMessage("")
	acc = []string{"Intro sentence."}
	chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
	engine.RenderNow()

	// /goal:list lands: many markdown rows appended after the stream block.
	var list strings.Builder
	for i := 0; i < goalListRows; i++ {
		list.WriteString(goalListRow(i))
	}
	chat.AddSystemMessage("> /goal:list")
	chat.AddSystemMessage(list.String())
	engine.RenderNow()
	return history, acc
}

func TestGoalListDuringStream_NoResetStorm(t *testing.T) {
	engine, chat, term := newStreamTUIHarness(t)

	_, acc := runMidStreamGoalListScenario(t, engine, chat)
	term.writes = nil // forget the reset that legitimately built the first post-list frame

	// Continue streaming after the goal list. Every chunk updates the
	// now-off-screen assistant block. No chunk may trigger a scrollback wipe.
	for i := 0; i < streamChunkCount; i++ {
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
	engine, chat, term := newStreamTUIHarness(t)

	history, acc := runMidStreamGoalListScenario(t, engine, chat)

	// Unique short tokens per chunk (never split by wrap, unambiguous —
	// "sent01" is not a prefix of "sent10").
	streamUniqueChunks(engine, chat, acc, streamChunkCount)

	// The stream settles: the app renders one final frame after the stream
	// ends (turn-end UI update). That frame sees no chat mutation since the
	// last chunk and triggers the deferred scrollback sync (one full reset)
	// so the scrollback matches the canvas again.
	engine.RenderNow()

	// Replay through the faithful emulator and assert presence-once for every
	// distinct row: history, streamed tokens, and goal-list rows.
	joined, sbJoined, dump := replayTranscript(term)
	for _, marker := range integrityMarkers(history) {
		requireMarkerOnce(t, joined, sbJoined, dump, marker)
	}
}

// streamUniqueChunks appends n uniquely token-marked sentences to the
// streaming assistant block, rendering after each chunk. Tokens ("sent01")
// never prefix each other and never split by line wrap, so presence checks
// stay unambiguous.
func streamUniqueChunks(engine *TUI, chat *ChatViewport, acc []string, n int) []string {
	for i := 0; i < n; i++ {
		acc = append(acc, fmt.Sprintf("sent%02d some padding words here.", i))
		chat.UpdateLastMessage(strings.Join(acc, " "), ConsoleAssistantMessage)
		engine.RenderNow()
	}
	return acc
}

// integrityMarkers lists every row that must survive exactly once: seeded
// history, streamed chunk tokens, and /goal:list rows.
func integrityMarkers(history []string) []string {
	markers := append([]string{}, history...)
	for i := 0; i < streamChunkCount; i++ {
		markers = append(markers, fmt.Sprintf("sent%02d", i))
	}
	for i := 0; i < goalListRows; i++ {
		markers = append(markers, fmt.Sprintf("goal-%02d", i))
	}
	return markers
}

// replayTranscript replays every terminal write through the emulator and
// returns the combined scrollback+screen transcript, the scrollback-only
// transcript, and a debug dump of both for failure messages.
func replayTranscript(term *fakeTerminal) (joined, sbJoined, dump string) {
	emu := NewTermEmulator(term.h, term.w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	visible := make([]string, term.h)
	for r := 0; r < term.h; r++ {
		visible[r] = emu.Visible(r)
	}
	scrollback := emu.Scrollback()
	lines := append(append([]string{}, scrollback...), visible...)
	joined = "\n" + strings.Join(lines, "\n") + "\n"
	sbJoined = "\n" + strings.Join(scrollback, "\n") + "\n"
	dump = "\n--- screen ---\n" + strings.Join(visible, "\n") +
		"\n--- scrollback tail ---\n" + tailLines(scrollback, 15)
	return joined, sbJoined, dump
}

// requireMarkerOnce asserts the marker row is present somewhere on
// screen+scrollback and never duplicated within scrollback alone (the
// watermark must not re-emit settled rows).
func requireMarkerOnce(t *testing.T, joined, sbJoined, dump, marker string) {
	t.Helper()
	if !strings.Contains(joined, marker) {
		t.Errorf("row %q LOST (absent from scrollback+screen)%s", marker, dump)
	}
	if n := strings.Count(sbJoined, marker); n > 1 {
		t.Errorf("row %q duplicated WITHIN scrollback (%d times) — watermark re-emitted it%s", marker, n, dump)
	}
}
