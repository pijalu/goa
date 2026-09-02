// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Agent-level validation for the muse-spark encrypted-reasoning handling
// (bugs.md 2026-09-02: "no thinking"). Following pi/opencode, reasoning that
// exists only as opaque encrypted_content is NOT shown — the parser
// (provider/protocol/opencode_muse_replay_test.go, replayed against the real
// captured stream) emits no thinking block. Instead it surfaces the reasoning
// via Usage.ReasoningTokens. This test pins the downstream contract: that
// usage signal must mark the round as reasoning so trackToolCallingRound does
// NOT count it as a silent tool round marching toward the forced-answer
// limit, all without pushing any StateThinking event to observers.

// newEncryptedReasoningResult builds the stream result the parser produces
// for an encrypted-only reasoning tool round: no thinking block, one tool
// call, and reasoning_tokens > 0 carrying the invisible-reasoning signal.
func newEncryptedReasoningResult() *schema.AssistantMessage {
	return &schema.AssistantMessage{
		Content: []schema.ContentBlock{{
			Type:          schema.ContentBlockToolCall,
			ToolCallID:    "call_1",
			ToolName:      "bash",
			ToolArguments: `{"command":"pwd && ls -la"}`,
		}},
		StopReason: schema.StopReasonEndTurn,
		Usage:      &schema.Usage{OutputTokens: 98, ReasoningTokens: 27},
	}
}

func TestMuseEncryptedReasoning_NotShownButResetsSilentStreak(t *testing.T) {
	a := NewAgent(Config{})

	var thinkingUI []string
	a.AddObserver(OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.State == StateThinking && ev.Text != "" {
			thinkingUI = append(thinkingUI, ev.Text)
		}
	}))

	// Simulate the parser's finished-stream result for an encrypted-only
	// reasoning tool round. captureStreamResult is what handleStreamDone runs.
	stream := provider.NewAssistantMessageEventStream(8)
	stream.End(newEncryptedReasoningResult())
	a.captureStreamResult(stream)

	// pi/opencode: encrypted thinking is NOT shown — nothing reaches the TUI.
	if len(thinkingUI) != 0 {
		t.Fatalf("encrypted reasoning must not surface as thinking, got %v", thinkingUI)
	}
	a.mu.Lock()
	thinkingLen := a.thinkingBuf.Len()
	sawThinking := a.turnSawThinking
	a.mu.Unlock()
	if thinkingLen != 0 {
		t.Fatalf("encrypted reasoning must not buffer visible thinking, thinkingBuf.Len()=%d", thinkingLen)
	}

	// But the reasoning must still be COUNTED: reasoning_tokens > 0 marks the
	// turn as reasoning so the silent tool-round streak resets instead of
	// incrementing toward the consecutive-silent-tool-round limit.
	if !sawThinking {
		t.Fatal("reasoning_tokens>0 must mark the turn as reasoning (turnSawThinking=true)")
	}
	if silent := a.trackToolCallingRound(); silent {
		t.Fatal("trackToolCallingRound must not report a silent round after invisible reasoning")
	}
	a.mu.Lock()
	streak := a.consecutiveToolRounds
	a.mu.Unlock()
	if streak != 0 {
		t.Fatalf("silent tool-round streak must reset, got %d", streak)
	}
}

// Contrast: a genuinely silent tool round (no content, no thinking, no
// reasoning tokens) still counts toward the limit — the reasoning signal must
// not blanket-disable the guard.
func TestMuseSilentToolRound_StillCounts(t *testing.T) {
	a := NewAgent(Config{MaxConsecutiveToolRounds: 2})

	// A round with no reasoning tokens at all: captureStreamResult must NOT
	// mark the turn as reasoning.
	stream := provider.NewAssistantMessageEventStream(8)
	stream.End(&schema.AssistantMessage{
		Content:    []schema.ContentBlock{{Type: schema.ContentBlockToolCall, ToolCallID: "c", ToolName: "bash", ToolArguments: "{}"}},
		StopReason: schema.StopReasonEndTurn,
		Usage:      &schema.Usage{OutputTokens: 5}, // no reasoning tokens
	})
	a.captureStreamResult(stream)
	a.mu.Lock()
	sawThinking := a.turnSawThinking
	a.mu.Unlock()
	if sawThinking {
		t.Fatal("a round with no reasoning tokens must not be marked as reasoning")
	}

	if silent := a.trackToolCallingRound(); silent {
		t.Fatal("first silent round must not reach the limit of 2")
	}
	if silent := a.trackToolCallingRound(); !silent {
		t.Fatal("second consecutive silent round must reach the limit of 2")
	}
}
