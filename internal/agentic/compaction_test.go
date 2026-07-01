// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
)

func TestMicroCompact_TruncatesOldToolResults(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens:       1000,
				MicroCompaction: DefaultMicroCompactionConfig,
			},
		},
		history: historyWithNToolResults(30, 500), // 30 results with 500-char bodies
	}

	a.microCompactForced(false)

	// The most recent results should still have long content.
	// Old results should be truncated.
	keep := DefaultMicroCompactionConfig.KeepRecentMessages
	for i := 0; i < len(a.history); i++ {
		if a.history[i].Role != ToolRole {
			continue
		}
		isRecent := i >= len(a.history)-keep
		if isRecent {
			if a.history[i].Content == DefaultMicroCompactionConfig.TruncatedMarker {
				t.Errorf("recent message %d was truncated", i)
			}
		} else {
			if a.history[i].Content != DefaultMicroCompactionConfig.TruncatedMarker {
				t.Errorf("old message %d was not truncated, content len=%d", i, len(a.history[i].Content))
			}
		}
	}
}

func TestMicroCompact_DoesNotTruncateSmallContent(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens: 1000,
				MicroCompaction: MicroCompactionConfig{
					KeepRecentMessages: 5,
					MinContentTokens:   999, // only truncate results > 999 tokens
					MinContextRatio:    0.0, // always trigger
					TruncatedMarker:    "[cleared]",
				},
			},
		},
		history: historyWithNToolResults(10, 100), // 10 results with 100-char bodies
	}

	a.microCompactForced(false)

	for _, msg := range a.history {
		if msg.Role == ToolRole && msg.Content == "[cleared]" {
			t.Errorf("tool result was truncated despite small content")
		}
	}
}

func TestMicroCompact_SkipsNonToolMessages(t *testing.T) {
	// History: system, user, assistant (no tool calls), tool result.
	// With KeepRecentMessages=0, all old messages are eligible.
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens: 1000,
				MicroCompaction: MicroCompactionConfig{
					KeepRecentMessages: 0, // 0 = truncate everything eligible
					MinContentTokens:   1,
					MinContextRatio:    0.0,
					TruncatedMarker:    "[cleared]",
				},
			},
		},
		history: []Message{
			{Role: System, Content: "you are helpful"},
			{Role: User, Content: "hello"},
			{Role: Assistant, Content: "hi there" + repeatStr("x", 500)},
			{Role: ToolRole, Content: repeatStr("x", 500)},
		},
	}

	a.microCompactForced(false)

	// System, User, and Assistant should be untouched (only ToolRole is truncated).
	if a.history[0].Content != "you are helpful" {
		t.Errorf("system message was modified")
	}
	if a.history[1].Content != "hello" {
		t.Errorf("user message was modified")
	}
	if a.history[2].Content == "[cleared]" {
		t.Errorf("assistant message was truncated")
	}
	// Only ToolRole at index 3 should be truncated
	if a.history[3].Content != "[cleared]" {
		t.Errorf("tool result should have been truncated")
	}
}

func TestMicroCompact_LowContextRatio_NoOp(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens: 100000, // huge context — ratio will be tiny
				MicroCompaction: MicroCompactionConfig{
					KeepRecentMessages: 1,
					MinContentTokens:   1,
					MinContextRatio:    0.9, // 90%% needed
					TruncatedMarker:    "[cleared]",
				},
			},
		},
		history: []Message{
			{Role: ToolRole, Content: repeatStr("x", 500)},
			{Role: User, Content: "hi"},
		},
	}

	a.microCompactForced(false)

	if a.history[0].Content != repeatStr("x", 500) {
		t.Errorf("tool result was truncated despite low context ratio")
	}
}

// ── helpers ────────────────────────────────────────────────────────

// historyWithNToolResults builds a history with N tool result messages.
// Each result body is filled with 'x' repeated size times.
// Every result is preceded by an assistant with a matching tool call.
func historyWithNToolResults(n, size int) []Message {
	msgs := make([]Message, 0, n*2)
	for i := 0; i < n; i++ {
		msgs = append(msgs, Message{
			Role:      Assistant,
			ToolCalls: []ToolCallInfo{{Name: "tool", Arguments: "{}"}},
		})
		msgs = append(msgs, Message{
			Role:    ToolRole,
			Content: repeatStr("x", size),
		})
	}
	return msgs
}

func repeatStr(s string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = s[0]
	}
	return string(b)
}
