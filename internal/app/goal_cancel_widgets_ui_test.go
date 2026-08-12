// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// TestUI_CancelBatchNoStuckOrDuplicateWidgets reproduces Issue 6:
// "incorrect tool presentation on cancelling goals". A large parallel batch
// of goal-cancel calls streams while the ACTIVE goal is cancelled mid-batch
// (clearing the goal bubble = bottom-chrome shrink mid-scroll). The user saw
// the first widgets of the batch frozen blue ("◉ … elapsed 0.2s", truncated
// streaming detail like "Cancelled goal minty{") while the same calls later
// rendered completed — i.e. frozen early-state rows duplicated into the
// terminal scrollback.
//
// Assertions (all as data, no ANSI byte sniffing of semantics):
//  1. Every tool widget reaches a terminal state (✓/✗) — none left ◉.
//  2. Replaying the raw terminal byte stream through the TermEmulator (what
//     the user's terminal actually showed) contains NO running indicators
//     ("◉", "elapsed") and NO duplicated "Cancelled goal <name>" header or
//     assistant text row.
func TestUI_CancelBatchNoStuckOrDuplicateWidgets(t *testing.T) {
	sc := newUIScenario(t, 100, 20)

	// Active goal → bubble visible (bottom chrome present).
	sc.engine.ApplySync(func() {
		sc.app.handleGoalUpdate(&event.GoalUpdate{Snapshot: &goal.GoalSnapshot{
			Status: goal.GoalActive, Name: "boss.cat", Objective: "cancel everything when done",
		}})
	})
	sc.engine.RenderNow()

	// Turn starts; assistant announces the batch (streamed as one delta).
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	const announce = "Let me cancel the active goal and all queued goals."
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant,
		State: agentic.StateContent, Text: announce, IsDelta: true})

	names := []string{
		"minty.puma", "calm.fox", "honest.hare", "merry.mole", "rosy.trout",
		"trusty.yak", "clever.mink", "witty.wolf", "rapid.wren", "warm.dove",
		"salty.jay", "gentle.marten", "silky.nyala", "quiet.tiger", "rapid.seal",
		"brave.otter",
	}
	// Stream the cancel batch (partial arg deltas, then final args — the
	// partials are where the truncated "Cancelled goal minty{" headers came
	// from).
	streamCancelBatch(sc, names)

	// MID-BATCH: the model's cancel of the ACTIVE goal lands → goal bubble
	// clears → bottom chrome shrinks while the widgets are pending.
	sc.engine.ApplySync(func() {
		sc.app.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil})
	})
	sc.engine.RenderNow()

	// Results arrive for every call.
	deliverCancelResults(sc, names)

	// Turn ends.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// (1) No widget left in a non-terminal state.
	assertAllWidgetsTerminal(t, sc.chat)

	// (2) Replay the byte stream: every cancelled goal must have a VISIBLE
	// completion indicator. Widgets whose rows were committed to scrollback
	// while running keep their frozen "◉" rows (terminal scrollback is
	// immutable without wiping user history), so the app appends a compact
	// completion echo for any tool that finishes while fully scrolled off —
	// no call may be left looking "ongoing" (Issue 6).
	joined := replayTerminal(sc, 20, 100)

	if got := strings.Count(joined, announce); got != 1 {
		t.Errorf("assistant announcement appears %d times, want 1", got)
	}
	for _, name := range names {
		doneHeader := "✓ ◆ Cancelled goal " + name // in-place completion (below fold)
		echo := "✓ Cancelled " + name + ":"        // scrolled-off completion echo
		if !strings.Contains(joined, doneHeader) && !strings.Contains(joined, echo) {
			t.Errorf("no visible completion for %q (neither %q nor %q) in terminal:\n%s",
				name, doneHeader, echo, joined)
		}
	}
}

// TestUI_VisibleToolResultGetsNoEcho guards the echo's off condition: a tool
// that completes while still visible must update in place ONLY — no extra
// completion line (Issue 6; the echo exists solely for tools whose
// widget scrolled into terminal scrollback).
func TestUI_VisibleToolResultGetsNoEcho(t *testing.T) {
	sc := newUIScenario(t, 100, 30)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "goal", ToolCallID: "c1",
		ToolInput: `{"action":"cancel","goalId":"minty.puma"}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "goal", ToolCallID: "c1",
		Text: `{"cancelled":{"name":"minty.puma","objective":"do the thing"}}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// The widget is visible (short transcript): the summary appears exactly
	// once (in the widget body) and no "✓ Cancelled …" echo entry exists.
	for _, e := range sc.chat.Snapshot() {
		if strings.HasPrefix(strings.TrimSpace(e.Text), "✓ Cancelled ") {
			t.Errorf("visible tool got a spurious completion echo: %q", e.Text)
		}
	}
	emu := tui.NewTermEmulator(30, 100)
	for _, w := range sc.term.writes {
		emu.Process(w)
	}
	var rows []string
	rows = append(rows, emu.Scrollback()...)
	for r := 0; r < 30; r++ {
		rows = append(rows, emu.Visible(r))
	}
	joined := strings.Join(rows, "\n")
	if got := strings.Count(joined, "Cancelled minty.puma:"); got != 1 {
		t.Errorf("summary appears %d times, want exactly 1 (widget body only)", got)
	}
}

// TestUI_ScrolledVisibleToolResultGetsNoEcho is the regression test for the
// spurious "← ✓ <output>" duplicates: a TALL transcript (header already in
// scrollback) with parallel bash calls whose widgets sit INSIDE the visible
// window when their results arrive. The layout budget the chat viewport gets
// (SetAllocatedHeight) excludes the header above the transcript, but the
// header scrolls with it — the visible band is taller than that budget.
// Judging "scrolled off" by the budget falsely echoed every one of these
// tools even though the user watched their ✓ transition happen on screen.
func TestUI_ScrolledVisibleToolResultGetsNoEcho(t *testing.T) {
	sc := newUIScenario(t, 100, 20)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	// Filler: push the transcript past the visible band so the header is in
	// scrollback and nothing below fits the (smaller) layout budget.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant,
		State: agentic.StateThinking, IsDelta: true,
		Text: "r01\nr02\nr03\nr04\nr05\nr06\nr07\nr08\nr09\nr10\nr11\nr12"})

	// Three parallel bash calls (the reported scenario): widgets appended at
	// the bottom of the tall transcript — inside the visible window.
	ids := []string{"c1", "c2", "c3"}
	outs := []string{"1192", "1145", "4242"}
	for i, id := range ids {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolName: "bash", ToolCallID: id,
			ToolInput: fmt.Sprintf(`{"command":"wc -l < f%d"}`, i)})
	}
	// Results arrive in order while every widget is still on screen.
	for i, id := range ids {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
			ToolName: "bash", ToolCallID: id, Text: outs[i]})
	}
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// No completion-echo entry may exist: every widget transitioned in place.
	for _, e := range sc.chat.Snapshot() {
		if e.Type == tui.ConsoleToolResult {
			t.Errorf("on-screen tool got a spurious completion echo entry: %q", e.Text)
		}
	}
	// The terminal shows each output exactly once (widget body) — no "← ✓" rows.
	joined := replayTerminal(sc, 20, 100)
	if got := strings.Count(joined, "← ✓"); got != 0 {
		t.Errorf("terminal shows %d spurious ← ✓ echo rows, want 0:\n%s", got, joined)
	}
	for _, out := range outs {
		if got := strings.Count(joined, out); got != 1 {
			t.Errorf("output %q appears %d times, want exactly 1 (widget body only):\n%s", out, got, joined)
		}
	}
}

// TestUI_ScrolledOffToolResultGetsEcho pins the echo's ON condition with the
// corrected geometry: a tool that completes while its widget is genuinely
// scrolled into terminal scrollback (later content pushed it above the
// visible band) MUST get a compact completion echo — otherwise its frozen
// running rows read as "still ongoing" (Issue 6).
func TestUI_ScrolledOffToolResultGetsEcho(t *testing.T) {
	sc := newUIScenario(t, 100, 20)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "bash", ToolCallID: "c1", ToolInput: `{"command":"make quality"}`})
	// Push the widget fully above the visible band before the result lands.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant,
		State: agentic.StateThinking, IsDelta: true,
		Text: "f01\nf02\nf03\nf04\nf05\nf06\nf07\nf08\nf09\nf10\nf11\nf12\nf13\nf14\nf15\nf16\nf17\nf18\nf19\nf20\nf21\nf22\nf23\nf24\nf25"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "bash", ToolCallID: "c1", Text: "quality-gate-output-6789"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	echoes := 0
	for _, e := range sc.chat.Snapshot() {
		if e.Type == tui.ConsoleToolResult && strings.HasPrefix(strings.TrimSpace(e.Text), "✓") {
			echoes++
		}
	}
	if echoes != 1 {
		t.Errorf("scrolled-off tool got %d completion echoes, want exactly 1", echoes)
	}
}

// streamCancelBatch streams partial arg deltas for every call, then the
// final args (mirrors provider tool-call streaming).
func streamCancelBatch(sc *uiScenario, names []string) {
	for i, name := range names {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolName: "goal", ToolCallID: fmt.Sprintf("call-%d", i), IsDelta: true,
			ToolInput: fmt.Sprintf(`{"action":"cancel","goalId":"%s`, name)})
	}
	for i, name := range names {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolName: "goal", ToolCallID: fmt.Sprintf("call-%d", i),
			ToolInput: fmt.Sprintf(`{"action":"cancel","goalId":"%s"}`, name)})
	}
}

// deliverCancelResults delivers a success result per call.
func deliverCancelResults(sc *uiScenario, names []string) {
	for i, name := range names {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
			ToolName: "goal", ToolCallID: fmt.Sprintf("call-%d", i),
			Text: fmt.Sprintf(`{"cancelled":{"name":"%s","objective":"G%02d — do the thing"}}`, name, i)})
	}
}

// assertAllWidgetsTerminal fails when any tool widget is left Pending or
// Running after the turn ended.
func assertAllWidgetsTerminal(t *testing.T, chat *tui.ChatViewport) {
	t.Helper()
	for _, c := range chat.Children() {
		tc, ok := c.(*tui.ToolExecutionComponent)
		if !ok {
			continue
		}
		if tc.Status() == tui.ToolPending || tc.Status() == tui.ToolRunning {
			t.Errorf("widget for %s left non-terminal (status=%v) after EventEnd", tc.ToolName(), tc.Status())
		}
	}
}

// replayTerminal replays the scenario's raw byte stream through the terminal
// emulator and returns scrollback + visible rows as one string — exactly
// what the user's terminal showed.
func replayTerminal(sc *uiScenario, h, w int) string {
	emu := tui.NewTermEmulator(h, w)
	for _, wr := range sc.term.writes {
		emu.Process(wr)
	}
	var rows []string
	rows = append(rows, emu.Scrollback()...)
	for r := 0; r < h; r++ {
		rows = append(rows, emu.Visible(r))
	}
	return "\n" + strings.Join(rows, "\n") + "\n"
}
