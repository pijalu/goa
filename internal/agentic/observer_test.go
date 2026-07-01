// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
)

func TestOutputState_String(t *testing.T) {
	tests := []struct {
		state OutputState
		want  string
	}{
		{StateIdle, "idle"},
		{StateThinking, "thinking"},
		{StateContent, "content"},
		{StateToolResult, "tool_result"},
		{StateToolCall, "tool_call"},
		{OutputState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestEventType_Constants(t *testing.T) {
	if EventStateChange != "state_change" {
		t.Errorf("EventStateChange = %q, want state_change", EventStateChange)
	}
	if EventContent != "content" {
		t.Errorf("EventContent = %q, want content", EventContent)
	}
	if EventToolCall != "tool_call" {
		t.Errorf("EventToolCall = %q, want tool_call", EventToolCall)
	}
	if EventToolResult != "tool_result" {
		t.Errorf("EventToolResult = %q, want tool_result", EventToolResult)
	}
	if EventEnd != "end" {
		t.Errorf("EventEnd = %q, want end", EventEnd)
	}
	if EventClear != "clear" {
		t.Errorf("EventClear = %q, want clear", EventClear)
	}
	if EventCompact != "compact" {
		t.Errorf("EventCompact = %q, want compact", EventCompact)
	}
}

func TestEventType_NewConstants(t *testing.T) {
	if EventTokenStats != "token_stats" {
		t.Errorf("EventTokenStats = %q, want token_stats", EventTokenStats)
	}
	if EventProgress != "progress" {
		t.Errorf("EventProgress = %q, want progress", EventProgress)
	}
}

func TestOutputEvent_TokenStatsFields(t *testing.T) {
	event := OutputEvent{
		Type:    EventTokenStats,
		Timings: &TokenTimings{PromptN: 10, PredictedN: 5},
	}

	if event.Type != EventTokenStats {
		t.Errorf("expected type token_stats, got %s", event.Type)
	}
	if event.Timings == nil {
		t.Fatal("expected Timings to be set")
	}
	if event.Timings.PromptN != 10 {
		t.Errorf("expected PromptN=10, got %d", event.Timings.PromptN)
	}
}

func TestOutputEvent_ProgressFields(t *testing.T) {
	event := OutputEvent{
		Type:           EventProgress,
		PromptProgress: &PromptProgress{Total: 24, Processed: 12},
	}

	if event.Type != EventProgress {
		t.Errorf("expected type progress, got %s", event.Type)
	}
	if event.PromptProgress == nil {
		t.Fatal("expected PromptProgress to be set")
	}
	if event.PromptProgress.Processed != 12 {
		t.Errorf("expected Processed=12, got %d", event.PromptProgress.Processed)
	}
}
