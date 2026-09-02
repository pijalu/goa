// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Replay validation against REAL captured opencode Zen streams
// (muse-spark-1.3-contributor-free, 2026-09-02). Where the hand-built frames
// in openai_responses_reasoning_test.go mirror the capture, these tests feed
// the exact bytes the upstream sent through parseResponsesSSE, so a drift in
// wire shape (new fields like sequence_number/item_id, encrypted_content on
// the reasoning item, event-order changes) fails loudly instead of passing
// against an idealized fixture.

func replayFile(t *testing.T, path string) ([]schema.AssistantMessageEvent, *schema.AssistantMessage) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return collectResponsesEvents(t, string(raw))
}

func thinkingText(events []schema.AssistantMessageEvent) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == schema.EventThinkingDelta {
			b.WriteString(ev.Delta)
		}
	}
	return b.String()
}

// The text turn carries two reasoning summary parts then the message text:
// thinking must stream (start/deltas/end) and land on the final message as a
// thinking block preceding the text block. The capture ALSO carries
// encrypted_content on the reasoning item, but since the summary text is
// visible it is what surfaces — no placeholder, no synthesized block.
func TestMuseReplay_TextTurn_ThinkingStreams(t *testing.T) {
	events, final := replayFile(t, "testdata/muse_spark_text_turn.sse")

	var types []schema.EventType
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	assert.Contains(t, types, schema.EventThinkingStart, "thinking block must open on the real capture")
	assert.Contains(t, types, schema.EventThinkingEnd, "thinking block must close on the real capture")

	got := thinkingText(events)
	assert.Contains(t, got, "arithmetic", "summary deltas must surface as thinking, got %q", got)

	require.NotNil(t, final)
	require.GreaterOrEqual(t, len(final.Content), 2, "thinking + text blocks expected, got %v", final.Content)
	assert.Equal(t, schema.ContentBlockThinking, final.Content[0].Type)
	assert.Contains(t, final.Content[0].Thinking, "arithmetic")
	assert.Equal(t, schema.ContentBlockText, final.Content[1].Type)
	assert.NotEmpty(t, final.Content[1].Text)
	assert.Equal(t, schema.StopReasonEndTurn, final.StopReason)
	require.NotNil(t, final.Usage, "usage must be parsed from response.completed")
	assert.Positive(t, final.Usage.ReasoningTokens, "text turn reasons visibly; reasoning_tokens must surface")
}

// The tool-call turn carries reasoning items with EMPTY summaries whose
// content exists only as opaque encrypted_content (the model reasoned — 27
// reasoning tokens — but nothing of it is user-visible). Following
// pi/opencode, encrypted thinking is NOT shown: the parser must synthesize NO
// thinking block. Instead it surfaces the reasoning via
// Usage.ReasoningTokens so the agent's consecutive-tool-round streak counts
// the round as reasoning without rendering a placeholder, and the tool call
// must survive intact (bugs.md 2026-09-02: "no thinking").
func TestMuseReplay_ToolTurn_EncryptedReasoningNotShownButCounted(t *testing.T) {
	events, final := replayFile(t, "testdata/muse_spark_tool_turn.sse")

	// Encrypted reasoning must NOT be shown: no thinking events at all.
	for _, ev := range events {
		assert.NotEqual(t, schema.EventThinkingStart, ev.Type, "no thinking block for encrypted-only reasoning")
		assert.NotEqual(t, schema.EventThinkingDelta, ev.Type, "no thinking delta for encrypted-only reasoning")
		assert.NotEqual(t, schema.EventThinkingEnd, ev.Type, "no thinking end for encrypted-only reasoning")
	}

	require.NotNil(t, final)
	var thinkingBlocks, toolCalls []schema.ContentBlock
	for _, b := range final.Content {
		switch b.Type {
		case schema.ContentBlockThinking:
			thinkingBlocks = append(thinkingBlocks, b)
		case schema.ContentBlockToolCall:
			toolCalls = append(toolCalls, b)
		}
	}
	assert.Empty(t, thinkingBlocks, "no thinking block on the final message, got %v", final.Content)
	require.Len(t, toolCalls, 1, "exactly one tool call expected, got %v", final.Content)
	assert.Equal(t, "bash", toolCalls[0].ToolName)
	assert.NotEmpty(t, toolCalls[0].ToolCallID)
	assert.JSONEq(t, `{"command":"pwd && ls -la"}`, toolCalls[0].ToolArguments)

	// But the reasoning must still be COUNTED: reasoning_tokens > 0 surfaces
	// the invisible reasoning so the consecutive-tool-round limit sees it.
	require.NotNil(t, final.Usage, "usage must be parsed from response.completed")
	assert.Equal(t, 27, final.Usage.ReasoningTokens,
		"invisible reasoning must surface via reasoning_tokens so the silent-streak accounts for it")
}
