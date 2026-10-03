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

// These tests validate the open item in bugs.md:
//
//	"Live stream splits when a provider interleaves reasoning and answer deltas"
//
// The provider (opencode-go / deepseek-v4-1-flash) streams the reasoning and the
// answer channels concurrently, so deltas arrive alternately inside one turn.
// The live-stream state must route each delta to the block it belongs to
// whatever the arrival order, instead of closing the current block on every
// state change.
//
// The export that reported it (`.goa/exports/goa-export-20261003-203241.zip`,
// session/events.jsonl 9976–9995) is the fixture replayed below.

// interleavedTurn replays the reported event order: reasoning, then the answer
// starts, then one more reasoning delta, then the rest of the answer. The late
// reasoning delta completes the reasoning sentence, exactly as the export shows
// (session/events.jsonl 9976–9995: "… now" then ".").
func interleavedTurn(sc *uiScenario) {
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(stateDelta(agentic.StateThinking, "F"))
	sc.apply(stateDelta(agentic.StateThinking, "8 done. Let me verify the test detects the old behavior"))
	sc.apply(stateDelta(agentic.StateContent, "F"))
	sc.apply(stateDelta(agentic.StateThinking, "."))
	sc.apply(stateDelta(agentic.StateContent, "8 done"))
	sc.apply(stateDelta(agentic.StateContent, ". Full suite + gates"))
	sc.apply(stateDelta(agentic.StateContent, " now.\n\n"))
}

func stateDelta(state agentic.OutputState, text string) *agentic.OutputEvent {
	return &agentic.OutputEvent{
		Type:    agentic.EventContent,
		State:   state,
		Role:    agentic.Assistant,
		Text:    text,
		IsDelta: true,
	}
}

// chatBlocks counts the chat viewport's messages by kind, in insertion order.
func chatBlocks(sc *uiScenario) []tui.ConsoleItemType {
	var kinds []tui.ConsoleItemType
	for _, m := range sc.chat.Snapshot() {
		kinds = append(kinds, m.Type)
	}
	return kinds
}

// blockTexts returns the text of every chat block, keyed by its position, so a
// test can assert both the structure and the content of a turn.
func blockTexts(sc *uiScenario) []string {
	var texts []string
	for _, m := range sc.chat.Snapshot() {
		texts = append(texts, m.Text)
	}
	return texts
}

// countKind counts blocks of one kind in the chat viewport.
func countKind(sc *uiScenario, kind tui.ConsoleItemType) int {
	n := 0
	for _, k := range chatBlocks(sc) {
		if k == kind {
			n++
		}
	}
	return n
}

// One interleaved turn must render exactly one reasoning block and one answer
// block: the answer is never split, and no second "thinking" header appears.
func TestStream_InterleavedReasoningAndAnswerStayInOneBlockEach(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	interleavedTurn(sc)

	if got := countKind(sc, tui.ConsoleThinkingBlock); got != 1 {
		t.Errorf("thinking blocks = %d, want 1 (a late reasoning delta must join the block on screen)", got)
	}
	if got := countKind(sc, tui.ConsoleAssistantMessage); got != 1 {
		t.Errorf("assistant messages = %d, want 1 (the answer must not be split)", got)
	}
	kinds := chatBlocks(sc)
	if len(kinds) != 2 || kinds[0] != tui.ConsoleThinkingBlock || kinds[1] != tui.ConsoleAssistantMessage {
		t.Fatalf("blocks = %v, want [thinking assistant]", kinds)
	}
	texts := blockTexts(sc)
	if want := "F8 done. Let me verify the test detects the old behavior."; texts[0] != want {
		t.Errorf("reasoning = %q, want %q", texts[0], want)
	}
	if want := "F8 done. Full suite + gates now.\n\n"; texts[1] != want {
		t.Errorf("answer = %q, want %q", texts[1], want)
	}
}

// The screen itself must be free of the reported artifact: no stray answer
// fragment above a second thinking header.
func TestStream_InterleavedTurnScreenHasOneThinkingHeader(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	interleavedTurn(sc)

	screen := visibleText(sc)
	// The block header, not the bare word: the footer also prints the thinking
	// level. One header means no second reasoning block was opened.
	if got := strings.Count(screen, "▾ thinking"); got != 1 {
		t.Errorf("screen shows %d thinking headers, want 1:\n%s", got, screen)
	}
	if !strings.Contains(screen, "F8 done. Full suite + gates now.") {
		t.Errorf("the answer is not intact on screen:\n%s", screen)
	}
}

// The ordinary order — all reasoning, then all answer — must keep working, and
// a real boundary (a tool call) must still start fresh blocks for the next
// segment so a previous segment's reasoning is never appended to.
func TestStream_SegmentsStartFreshBlocksAfterAToolCall(t *testing.T) {
	sc := newUIScenario(t, 100, 30)

	// Segment 1: reasoning then answer.
	sc.apply(stateDelta(agentic.StateThinking, "first thought"))
	sc.apply(stateDelta(agentic.StateContent, "first answer"))
	if got := countKind(sc, tui.ConsoleThinkingBlock); got != 1 {
		t.Fatalf("thinking blocks after segment 1 = %d, want 1", got)
	}

	// A tool call ends the segment.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "bash", ToolInput: "{}"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult, ToolName: "bash", Text: "ok"})

	// Segment 2: reasoning must open a NEW block, not extend the first one.
	sc.apply(stateDelta(agentic.StateThinking, "second thought"))
	sc.apply(stateDelta(agentic.StateContent, "second answer"))

	if got := countKind(sc, tui.ConsoleThinkingBlock); got != 2 {
		t.Errorf("thinking blocks = %d, want 2 (one per segment)", got)
	}
	texts := blockTexts(sc)
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "first thought") || !strings.Contains(joined, "second thought") {
		t.Fatalf("blocks lost a segment's reasoning: %q", joined)
	}
	if strings.Contains(texts[len(texts)-1], "first thought") {
		t.Errorf("second answer merged the first segment's reasoning: %q", texts[len(texts)-1])
	}
}

// Cancelling mid-stream must retract every half-streamed bubble of the segment,
// not just the last kind that happened to be streaming.
func TestStream_CancelRetractsBothInFlightBubbles(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	sc.apply(stateDelta(agentic.StateThinking, "half a thought"))
	sc.apply(stateDelta(agentic.StateContent, "half an answer"))

	sc.apply(&agentic.OutputEvent{
		Type:     agentic.EventEnd,
		Metadata: map[string]string{"cancelled": "true"},
	})

	if got := countKind(sc, tui.ConsoleThinkingBlock); got != 0 {
		t.Errorf("thinking blocks after cancel = %d, want 0", got)
	}
	if got := countKind(sc, tui.ConsoleAssistantMessage); got != 0 {
		t.Errorf("assistant messages after cancel = %d, want 0", got)
	}
	if screen := visibleText(sc); !strings.Contains(screen, "Generation stopped by user.") {
		t.Errorf("cancel notice missing from screen:\n%s", screen)
	}
}
