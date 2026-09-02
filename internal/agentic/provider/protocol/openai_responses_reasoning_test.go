// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Reasoning-summary streaming on the Responses surface (bugs.md: muse shows no
// thinking). Live probes against opencode Zen (muse-spark-1.3, 2026-09-02)
// show reasoning arrives as a "reasoning" output item followed by
// response.reasoning_summary_part.added / response.reasoning_summary_text.delta
// /.done / response.reasoning_summary_part.done. The parser must surface the
// summary text as thinking events and as a ContentBlockThinking on the final
// message — today every reasoning_* event falls through the dispatch switch
// and is silently dropped.

// collectResponsesEvents runs parseResponsesSSE over the given SSE body and
// returns every pushed event plus the final message.
func collectResponsesEvents(t *testing.T, body string) ([]schema.AssistantMessageEvent, *schema.AssistantMessage) {
	t.Helper()
	stream := schema.NewAssistantMessageEventStream(64)
	parseResponsesSSE(strings.NewReader(body), stream)
	var events []schema.AssistantMessageEvent
	for ev := range stream.Seq() {
		events = append(events, ev)
	}
	return events, stream.Result()
}

// sseFrame builds one SSE frame (event + data lines) as the Responses API
// emits them.
func sseFrame(eventType, data string) string {
	return "event: " + eventType + "\ndata: " + data + "\n\n"
}

// museReasoningTextSSE mirrors the captured muse-spark text turn: a reasoning
// item with two summary-part deltas, then a message item with one text delta,
// then response.completed.
func museReasoningTextSSE() string {
	var b strings.Builder
	b.WriteString(sseFrame("response.created", `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`))
	b.WriteString(sseFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`))
	b.WriteString(sseFrame("response.reasoning_summary_part.added", `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"item_id":"rs_1","part":{"type":"summary_text","text":""}}`))
	b.WriteString(sseFrame("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"item_id":"rs_1","delta":"Evaluating a simple arithmetic question."}`))
	b.WriteString(sseFrame("response.reasoning_summary_text.done", `{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"item_id":"rs_1","text":"Evaluating a simple arithmetic question."}`))
	b.WriteString(sseFrame("response.reasoning_summary_part.done", `{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0,"item_id":"rs_1","part":{"type":"summary_text","text":"Evaluating a simple arithmetic question."}}`))
	b.WriteString(sseFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"Evaluating a simple arithmetic question."}]}}`))
	b.WriteString(sseFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","status":"in_progress"}}`))
	b.WriteString(sseFrame("response.content_part.added", `{"type":"response.content_part.added","output_index":1,"content_index":0,"item_id":"msg_1","part":{"type":"output_text","text":""}}`))
	b.WriteString(sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","output_index":1,"content_index":0,"item_id":"msg_1","delta":"2+2 equals 4."}`))
	b.WriteString(sseFrame("response.content_part.done", `{"type":"response.content_part.done","output_index":1,"content_index":0,"item_id":"msg_1","part":{"type":"output_text","text":"2+2 equals 4."}}`))
	b.WriteString(sseFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","status":"completed"}}`))
	b.WriteString(sseFrame("response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":24,"output_tokens":686,"input_tokens_details":{"cached_tokens":0}}}}`))
	return b.String()
}

func TestOpenAIResponses_ReasoningSummaryStreamsAsThinking(t *testing.T) {
	events, final := collectResponsesEvents(t, museReasoningTextSSE())

	var types []schema.EventType
	var thinkingDeltas []string
	for _, ev := range events {
		types = append(types, ev.Type)
		if ev.Type == schema.EventThinkingDelta {
			thinkingDeltas = append(thinkingDeltas, ev.Delta)
		}
	}

	assert.Contains(t, types, schema.EventThinkingStart, "thinking block must open")
	assert.Contains(t, types, schema.EventThinkingEnd, "thinking block must close")
	require.NotEmpty(t, thinkingDeltas, "reasoning summary deltas must stream as thinking deltas")
	assert.Equal(t, "Evaluating a simple arithmetic question.", strings.Join(thinkingDeltas, ""))

	// Thinking must precede text in the event flow.
	thinkIdx := indexOfEvent(types, schema.EventThinkingStart)
	textIdx := indexOfEvent(types, schema.EventTextDelta)
	require.True(t, thinkIdx >= 0 && textIdx >= 0, "both thinking and text events expected, got %v", types)
	assert.Less(t, thinkIdx, textIdx, "thinking block precedes the message text")

	// Final message carries the thinking block before the text block.
	require.NotNil(t, final)
	require.GreaterOrEqual(t, len(final.Content), 2, "thinking + text blocks expected, got %v", final.Content)
	assert.Equal(t, schema.ContentBlockThinking, final.Content[0].Type)
	assert.Equal(t, "Evaluating a simple arithmetic question.", final.Content[0].Thinking)
	assert.Equal(t, schema.ContentBlockText, final.Content[1].Type)
	assert.Equal(t, "2+2 equals 4.", final.Content[1].Text)
}

func indexOfEvent(types []schema.EventType, target schema.EventType) int {
	for i, ty := range types {
		if ty == target {
			return i
		}
	}
	return -1
}

// museReasoningToolCallSSE mirrors the captured muse tool-call turn: reasoning
// items with EMPTY summaries (nothing to render) followed by a function_call.
// The parser must not emit thinking events for empty summaries, must keep the
// tool call intact, and must not produce a thinking content block.
func museReasoningToolCallSSE() string {
	var b strings.Builder
	b.WriteString(sseFrame("response.created", `{"type":"response.created","response":{"id":"resp_2","status":"in_progress"}}`))
	b.WriteString(sseFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`))
	b.WriteString(sseFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[]}}`))
	b.WriteString(sseFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"id":"rs_2","type":"reasoning","status":"in_progress","summary":[]}}`))
	b.WriteString(sseFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"id":"rs_2","type":"reasoning","status":"completed","summary":[]}}`))
	b.WriteString(sseFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"","status":"in_progress"}}`))
	b.WriteString(sseFrame("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"path\":\"go.mod\"}"}`))
	b.WriteString(sseFrame("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","output_index":2,"arguments":"{\"path\":\"go.mod\"}"}`))
	b.WriteString(sseFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"go.mod\"}","status":"completed"}}`))
	b.WriteString(sseFrame("response.completed", `{"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":547,"output_tokens":105,"input_tokens_details":{"cached_tokens":0}}}}`))
	return b.String()
}

func TestOpenAIResponses_EmptyReasoningSummaryEmitsNoThinking(t *testing.T) {
	events, final := collectResponsesEvents(t, museReasoningToolCallSSE())

	for _, ev := range events {
		assert.NotEqual(t, schema.EventThinkingStart, ev.Type, "empty summaries must not open a thinking block")
		assert.NotEqual(t, schema.EventThinkingDelta, ev.Type, "empty summaries must not stream thinking deltas")
	}

	// Tool call survives intact, keyed by call_id, with full arguments.
	require.NotNil(t, final)
	require.Len(t, final.Content, 1, "only the tool call block expected, got %v", final.Content)
	tc := final.Content[0]
	assert.Equal(t, schema.ContentBlockToolCall, tc.Type)
	assert.Equal(t, "call_1", tc.ToolCallID)
	assert.Equal(t, "read", tc.ToolName)
	assert.JSONEq(t, `{"path":"go.mod"}`, tc.ToolArguments)
}

// TestOpenAIResponses_ReasoningSummaryMultiplePartsAccumulate covers several
// summary parts on one reasoning item: deltas accumulate in order into one
// thinking block.
func TestOpenAIResponses_ReasoningSummaryMultiplePartsAccumulate(t *testing.T) {
	var b strings.Builder
	b.WriteString(sseFrame("response.created", `{"type":"response.created","response":{"id":"resp_3","status":"in_progress"}}`))
	b.WriteString(sseFrame("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`))
	for i, delta := range []string{"First thought.", "Second thought."} {
		part := `{"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":` + itoa(i) + `,"item_id":"rs_1","part":{"type":"summary_text","text":""}}`
		b.WriteString(sseFrame("response.reasoning_summary_part.added", part))
		d := `{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":` + itoa(i) + `,"item_id":"rs_1","delta":"` + delta + `"}`
		b.WriteString(sseFrame("response.reasoning_summary_text.delta", d))
		done := `{"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":` + itoa(i) + `,"item_id":"rs_1","part":{"type":"summary_text","text":"` + delta + `"}}`
		b.WriteString(sseFrame("response.reasoning_summary_part.done", done))
	}
	b.WriteString(sseFrame("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[]}}`))
	b.WriteString(sseFrame("response.completed", `{"type":"response.completed","response":{"id":"resp_3","status":"completed","usage":{"input_tokens":10,"output_tokens":50,"input_tokens_details":{"cached_tokens":0}}}}`))

	events, final := collectResponsesEvents(t, b.String())

	var deltas []string
	for _, ev := range events {
		if ev.Type == schema.EventThinkingDelta {
			deltas = append(deltas, ev.Delta)
		}
	}
	assert.Equal(t, []string{"First thought.", "Second thought."}, deltas)

	require.NotNil(t, final)
	require.Len(t, final.Content, 1, "single thinking block expected, got %v", final.Content)
	assert.Equal(t, schema.ContentBlockThinking, final.Content[0].Type)
	assert.Equal(t, "First thought.\nSecond thought.", final.Content[0].Thinking)
}

func itoa(i int) string {
	return string(rune('0' + i))
}
