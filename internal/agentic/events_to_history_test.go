// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"testing"
)

func TestEventsToHistory_Empty(t *testing.T) {
	got := EventsToHistory(nil)
	if len(got) != 0 {
		t.Errorf("EventsToHistory(nil) = %d messages, want 0", len(got))
	}
}

func TestEventsToHistory_EmptyEvents(t *testing.T) {
	got := EventsToHistory([]OutputEvent{})
	if len(got) != 0 {
		t.Errorf("EventsToHistory([]) = %d messages, want 0", len(got))
	}
}

func TestEventsToHistory_UserMessage(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "hello"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Role != User || got[0].Content != "hello" {
		t.Errorf("expected User message 'hello', got Role=%s Content=%q", got[0].Role, got[0].Content)
	}
}

func TestEventsToHistory_AssistantContent(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "tell me"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "Sure, ", IsDelta: true},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "here it is!", IsDelta: true},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != User || got[0].Content != "tell me" {
		t.Errorf("expected user message, got Role=%s Content=%q", got[0].Role, got[0].Content)
	}
	if got[1].Role != Assistant || got[1].Content != "Sure, here it is!" {
		t.Errorf("expected assistant message 'Sure, here it is!', got Role=%s Content=%q", got[1].Role, got[1].Content)
	}
	if len(got[1].ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(got[1].ToolCalls))
	}
}

func TestEventsToHistory_ThinkingContent(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "think"},
		{Type: EventContent, Role: Assistant, State: StateThinking, Text: "let me", IsDelta: true},
		{Type: EventContent, Role: Assistant, State: StateThinking, Text: " think...", IsDelta: true},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "Answer:", IsDelta: true},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[1].Role != Assistant {
		t.Errorf("expected assistant role, got %s", got[1].Role)
	}
	if got[1].Thinking != "let me think..." {
		t.Errorf("expected thinking 'let me think...', got %q", got[1].Thinking)
	}
	if got[1].Content != "Answer:" {
		t.Errorf("expected content 'Answer:', got %q", got[1].Content)
	}
}

func TestEventsToHistory_ToolCallAndResult(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "run command"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "Running:", IsDelta: true},
		{Type: EventToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`, ToolCallID: "call1"},
		{Type: EventToolResult, ToolName: "bash", ToolResult: "file1\nfile2", ToolCallID: "call1"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	// Assistant message with content + tool call
	if got[1].Role != Assistant || got[1].Content != "Running:" {
		t.Errorf("expected assistant content 'Running:', got Role=%s Content=%q", got[1].Role, got[1].Content)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(got[1].ToolCalls))
	}
	if got[1].ToolCalls[0].Name != "bash" || got[1].ToolCalls[0].Arguments != `{"cmd":"ls"}` {
		t.Errorf("tool call mismatch: %+v", got[1].ToolCalls[0])
	}
	// Tool result
	if got[2].Role != ToolRole || got[2].Content != "file1\nfile2" {
		t.Errorf("expected tool result, got Role=%s Content=%q", got[2].Role, got[2].Content)
	}
}

func TestEventsToHistory_SkipsNonMessageEvents(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "hello"},
		{Type: EventTokenStats, Timings: &TokenTimings{PromptN: 10}},
		{Type: EventContextStats, ContextStats: &ContextStats{EstimatedTokens: 100}},
		{Type: EventCompact, Text: "micro"},
		{Type: EventProgress, Text: "processing..."},
		{Type: EventStateChange, State: StateContent},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "world", IsDelta: true},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (user + assistant), got %d", len(got))
	}
	if got[0].Content != "hello" || got[1].Content != "world" {
		t.Errorf("unexpected content: %q, %q", got[0].Content, got[1].Content)
	}
}

func TestEventsToHistory_SkipsSystemMessages(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: System, Text: "system prompt"},
		{Type: EventContent, Role: User, Text: "hi"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "hello!"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (no system), got %d", len(got))
	}
	if got[0].Role != User || got[1].Role != Assistant {
		t.Errorf("expected User then Assistant, got %s then %s", got[0].Role, got[1].Role)
	}
}

func TestEventsToHistory_MultipleTurns(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "first"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "answer 1"},
		{Type: EventEnd},
		{Type: EventContent, Role: User, Text: "second"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "answer 2"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if got[0].Content != "first" || got[1].Content != "answer 1" {
		t.Errorf("turn 1: %q, %q", got[0].Content, got[1].Content)
	}
	if got[2].Content != "second" || got[3].Content != "answer 2" {
		t.Errorf("turn 2: %q, %q", got[2].Content, got[3].Content)
	}
}

func TestEventsToHistory_NoEventEnd(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "hi"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "hey there"},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages even without EventEnd, got %d", len(got))
	}
	if got[1].Content != "hey there" {
		t.Errorf("expected assistant content, got %q", got[1].Content)
	}
}

func TestEventsToHistory_EmptyToolResult(t *testing.T) {
	events := []OutputEvent{
		{Type: EventToolCall, ToolName: "bash", ToolInput: "ls", ToolCallID: "c1"},
		{Type: EventToolResult, ToolCallID: "c1", ToolResult: ""},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	// Tool call without preceding content should still produce assistant + tool messages
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (assistant+tool), got %d", len(got))
	}
	if got[0].Role != Assistant || len(got[0].ToolCalls) != 1 {
		t.Errorf("expected assistant with 1 tool call, got Role=%s toolCalls=%d", got[0].Role, len(got[0].ToolCalls))
	}
	if got[1].Role != ToolRole || got[1].ToolCallID != "c1" {
		t.Errorf("expected tool role message, got Role=%s", got[1].Role)
	}
}

// summarizeTxEvents builds the session-JSONL event sequence for one turn:
// user → assistant → end, followed by a completed summarize compaction
// transaction (start → summary{strategy, summary} → end).
func summarizeTxEvents(id, userText, assistantText, summary string, shadowedEnd int) []OutputEvent {
	return []OutputEvent{
		{Type: EventContent, Role: User, Text: userText},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: assistantText},
		{Type: EventEnd},
		{Type: EventCompactionStart, CompactionTx: &CompactionTx{ID: id}},
		{Type: EventCompactionSummary, CompactionTx: &CompactionTx{
			ID: id, Strategy: string(CompressionSummarize), Summary: summary,
			ShadowedStart: 0, ShadowedEnd: shadowedEnd, ShadowedCount: shadowedEnd,
		}},
		{Type: EventCompactionEnd, CompactionTx: &CompactionTx{ID: id}},
	}
}

// B44 RED: a completed summarize compaction transaction must be honored on
// replay — the shadowed pre-compaction transcript is replaced by the
// checkpoint pair, and post-compaction turns append after it. Restoring such
// a session used to rebuild the FULL transcript, silently growing the context
// back to the pre-compaction size.
func TestEventsToHistory_SummarizeCheckpointReplay(t *testing.T) {
	const summary = "condensed earlier conversation"
	events := summarizeTxEvents("cx-1", "u1", "a1", summary, 2)
	events = append(events,
		OutputEvent{Type: EventContent, Role: User, Text: "u2"},
		OutputEvent{Type: EventContent, Role: Assistant, State: StateContent, Text: "a2"},
		OutputEvent{Type: EventEnd},
	)

	got := EventsToHistory(events)
	want := []Message{
		{Type: Content, Role: User, Content: frameCompactedSummary(summary)},
		{Type: Content, Role: Assistant, Content: summary},
		{Type: Content, Role: User, Content: "u2"},
		{Type: Content, Role: Assistant, Content: "a2"},
	}
	if len(got) != len(want) {
		t.Fatalf("replayed history = %d messages, want %d: %#v", len(got), len(want), got)
	}
	for i, m := range want {
		if got[i].Role != m.Role || got[i].Content != m.Content {
			t.Errorf("replayed[%d] = %s/%q, want %s/%q", i, got[i].Role, got[i].Content, m.Role, m.Content)
		}
	}
}

// B44 RED: a completed fresh_window transaction replays to the preserved
// tail (the same chain-safe boundary the live reset kept), not the full
// transcript.
func TestEventsToHistory_FreshWindowReplay(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "u1"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a1"},
		{Type: EventEnd},
		{Type: EventContent, Role: User, Text: "u2"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a2"},
		{Type: EventEnd},
		{Type: EventContent, Role: User, Text: "u3"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a3"},
		{Type: EventEnd},
		{Type: EventCompactionStart, CompactionTx: &CompactionTx{ID: "cx-fw"}},
		{Type: EventCompactionSummary, CompactionTx: &CompactionTx{
			ID: "cx-fw", Strategy: string(CompressionFreshWindow),
			PreserveTurns: 1, ShadowedStart: 0, ShadowedEnd: 6, ShadowedCount: 6,
		}},
		{Type: EventCompactionEnd, CompactionTx: &CompactionTx{ID: "cx-fw"}},
	}

	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("replayed history = %d messages, want the preserved last turn [u3 a3]: %#v", len(got), got)
	}
	if got[0].Role != User || got[0].Content != "u3" || got[1].Role != Assistant || got[1].Content != "a3" {
		t.Errorf("preserved tail = %s/%q, %s/%q, want u3/a3",
			got[0].Role, got[0].Content, got[1].Role, got[1].Content)
	}
}

// B44 RED: a completed remote-compaction transaction replays to the persisted
// replacement transcript the server produced.
func TestEventsToHistory_RemoteCompactReplay(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "u1"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a1"},
		{Type: EventEnd},
		{Type: EventCompactionStart, CompactionTx: &CompactionTx{ID: "cx-remote"}},
		{Type: EventCompactionSummary, CompactionTx: &CompactionTx{
			ID: "cx-remote", Strategy: string(CompressionRemoteCompact),
			ShadowedStart: 0, ShadowedEnd: 2, ShadowedCount: 2,
			Replacement: []Message{
				{Type: Content, Role: User, Content: "r1"},
				{Type: Content, Role: Assistant, Content: "r2"},
			},
		}},
		{Type: EventCompactionEnd, CompactionTx: &CompactionTx{ID: "cx-remote"}},
		{Type: EventContent, Role: User, Text: "u2"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a2"},
		{Type: EventEnd},
	}

	got := EventsToHistory(events)
	want := []string{"r1", "r2", "u2", "a2"}
	if len(got) != len(want) {
		t.Fatalf("replayed history = %d messages, want %d: %#v", len(got), len(want), got)
	}
	for i, c := range want {
		if got[i].Content != c {
			t.Errorf("replayed[%d].Content = %q, want %q", i, got[i].Content, c)
		}
	}
}

// B44 guard: a FAILED compaction attempt (compaction_end with an error, no
// summary) must not shadow the transcript — the live history was untouched.
func TestEventsToHistory_FailedCompactionKeepsTranscript(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "u1"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a1"},
		{Type: EventEnd},
		{Type: EventCompactionStart, CompactionTx: &CompactionTx{ID: "cx-fail"}},
		{Type: EventCompactionEnd, CompactionTx: &CompactionTx{ID: "cx-fail", Error: "boom"}},
	}

	got := EventsToHistory(events)
	if len(got) != 2 || got[0].Content != "u1" || got[1].Content != "a1" {
		t.Errorf("failed compaction altered the transcript: %#v", got)
	}
}

// B44 guard: a transaction summary without the replay fields (sessions
// recorded before the fields existed) is ignored — the full transcript is
// kept, matching the old restore behavior.
func TestEventsToHistory_LegacyTripleWithoutFieldsIgnored(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "u1"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "a1"},
		{Type: EventEnd},
		{Type: EventCompactionStart, CompactionTx: &CompactionTx{ID: "cx-old"}},
		{Type: EventCompactionSummary, CompactionTx: &CompactionTx{ID: "cx-old"}},
		{Type: EventCompactionEnd, CompactionTx: &CompactionTx{ID: "cx-old"}},
	}

	got := EventsToHistory(events)
	if len(got) != 2 || got[0].Content != "u1" || got[1].Content != "a1" {
		t.Errorf("legacy triple altered the transcript: %#v", got)
	}
}

// The new replay fields must survive the session-JSONL round trip.
func TestCompactionTx_ReplayFieldsJSONRoundTrip(t *testing.T) {
	ev := OutputEvent{
		Type: EventCompactionSummary,
		CompactionTx: &CompactionTx{
			ID: "cx-rt", Strategy: string(CompressionSummarize), Summary: "S",
			ShadowedEnd: 4, ShadowedCount: 4, PreserveTurns: 2,
			Replacement: []Message{{Type: Content, Role: User, Content: "r1"}},
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back OutputEvent
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tx := back.CompactionTx
	if tx == nil || tx.Strategy != string(CompressionSummarize) || tx.Summary != "S" || tx.PreserveTurns != 2 {
		t.Fatalf("round trip lost replay fields: %+v", tx)
	}
	if len(tx.Replacement) != 1 || tx.Replacement[0].Role != User || tx.Replacement[0].Content != "r1" {
		t.Errorf("round trip lost replacement: %+v", tx.Replacement)
	}
}
