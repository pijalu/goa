// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestParseChunk_TextStop(t *testing.T) {
	chunk := `{"choices":[{"delta":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
	msgs, _ := parseChunk(chunk)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Type != agentic.Content || msgs[0].Content != "hello" {
		t.Errorf("expected content message, got %+v", msgs[0])
	}
	if msgs[1].Type != agentic.End {
		t.Errorf("expected End message for stop, got %+v", msgs[1])
	}
}

func TestParseChunk_ToolCallsFlushesEnd(t *testing.T) {
	chunk := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"calculator","arguments":"{\"a\":10}"}}]},"finish_reason":"tool_calls"}]}`
	msgs, _ := parseChunk(chunk)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Type != agentic.ToolCall {
		t.Errorf("expected ToolCall message, got %+v", msgs[0])
	}
	if msgs[0].ToolName != "calculator" || msgs[0].ToolCallID != "call_1" {
		t.Errorf("unexpected tool call fields: %+v", msgs[0])
	}
	if msgs[1].Type != agentic.End {
		t.Errorf("expected End message for tool_calls, got %+v", msgs[1])
	}
}

func TestParseChunk_ToolCallsWithoutDelta(t *testing.T) {
	chunk := `{"choices":[{"finish_reason":"tool_calls"}]}`
	msgs, _ := parseChunk(chunk)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Type != agentic.End {
		t.Errorf("expected End message, got %+v", msgs[0])
	}
}

func TestHandleFinishReason_KnownReasons(t *testing.T) {
	tests := []struct {
		name    string
		reason  interface{}
		wantNil bool
	}{
		{"stop", "stop", false},
		{"tool_calls", "tool_calls", false},
		{"length", "length", false},                 // DeepSeek / OpenAI max_tokens
		{"content_filter", "content_filter", false}, // Azure content filter
		{"non-string", 123, true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handleFinishReason(tt.reason)
			if tt.wantNil && len(got) != 0 {
				t.Errorf("expected nil, got %+v", got)
			}
			if !tt.wantNil && len(got) == 0 {
				t.Errorf("expected End message, got nil")
			}
		})
	}
}

// TestParseOpenAIStream_EndsWithoutFinishReason verifies that a provider which
// emits content but never sends a finish_reason still terminates the stream.
// This prevents consumers from blocking forever on models/servers that omit
// the final stop chunk (reported with LM Studio + Qwen).
func TestParseOpenAIStream_EndsWithoutFinishReason(t *testing.T) {
	body := io.NopCloser(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\" world\"}}]}\n\n" +
			"data: [DONE]\n\n",
	))
	stream := provider.NewAssistantMessageEventStream(16)
	go parseOpenAIStream(body, stream)

	events := collectEvents(stream)
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}

	var text string
	for _, ev := range events {
		if ev.Type == provider.EventTextDelta {
			text += ev.Delta
		}
	}
	if text != "hello world" {
		t.Fatalf("expected accumulated text %q, got %q", "hello world", text)
	}

	// Stream must terminate gracefully (Result() unblocks after End()).
	result := stream.Result()
	if result == nil {
		t.Fatal("expected stream result after End(), got nil")
	}
	if result.StopReason != provider.StopReasonEndTurn {
		t.Fatalf("expected StopReasonEndTurn, got %v", result.StopReason)
	}
}

// TestParseOpenAIStream_EndsToolCallWithoutFinishReason verifies that a tool
// call turn which does not include a finish_reason still terminates the stream
// and emits the accumulated tool call.
func TestParseOpenAIStream_EndsToolCallWithoutFinishReason(t *testing.T) {
	body := io.NopCloser(strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"read\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]}}]}\n\n" +
			"data: [DONE]\n\n",
	))
	stream := provider.NewAssistantMessageEventStream(16)
	go parseOpenAIStream(body, stream)

	events := collectEvents(stream)
	var toolCalls []provider.AssistantMessageEvent
	for _, ev := range events {
		if ev.Type == provider.EventToolCallEnd {
			toolCalls = append(toolCalls, ev)
		}
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call end event, got %d", len(toolCalls))
	}
	tc := toolCalls[0].ToolCall
	if tc == nil {
		t.Fatal("expected ToolCall data, got nil")
	}
	if tc.ToolName != "read" {
		t.Errorf("tool name = %q, want %q", tc.ToolName, "read")
	}

	result := stream.Result()
	if result == nil {
		t.Fatal("expected stream result after End(), got nil")
	}
}

func collectEvents(stream *provider.AssistantMessageEventStream) []provider.AssistantMessageEvent {
	var out []provider.AssistantMessageEvent
	for ev := range stream.Seq() {
		out = append(out, ev)
	}
	return out
}

// ── Cache token format tests ──

func TestParseRootFields_llamacpp_tokens_cached(t *testing.T) {
	// llama.cpp emits tokens_cached at root level in final streaming chunk
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"tokens_cached":42}`
	msgs, _ := parseChunk(chunk)

	// Should emit content, timings (with cache), and end
	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message from tokens_cached")
	}
	if timings.CacheReadTokens != 42 {
		t.Errorf("CacheReadTokens = %d, want 42", timings.CacheReadTokens)
	}
}

func TestParseRootFields_openai_standard_cache(t *testing.T) {
	// OpenAI standard: usage.prompt_tokens_details.cached_tokens
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":150,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":100}}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message from usage")
	}
	// Semantics for prompt usage calculation: PromptN = prompt_tokens - cacheRead - cacheWrite. This tracks the net token cost used in testing.
	if timings.PromptN != 50 {
		t.Errorf("PromptN = %d, want 50 (150 - 100 cached)", timings.PromptN)
	}
	if timings.PredictedN != 50 {
		t.Errorf("PredictedN = %d, want 50", timings.PredictedN)
	}
	if timings.CacheReadTokens != 100 {
		t.Errorf("CacheReadTokens = %d, want 100", timings.CacheReadTokens)
	}
}

func TestParseRootFields_openrouter_cache_write(t *testing.T) {
	// OpenRouter: usage.prompt_tokens_details.cache_write_tokens
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":200,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":50,"cache_write_tokens":30}}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message")
	}
	if timings.PromptN != 120 {
		t.Errorf("PromptN = %d, want 120 (200 - 50 cached - 30 write)", timings.PromptN)
	}
	if timings.CacheReadTokens != 50 {
		t.Errorf("CacheReadTokens = %d, want 50", timings.CacheReadTokens)
	}
	if timings.CacheWriteTokens != 30 {
		t.Errorf("CacheWriteTokens = %d, want 30", timings.CacheWriteTokens)
	}
}

func TestParseRootFields_github_copilot_cache(t *testing.T) {
	// GitHub Copilot: usage.prompt_cache_hit_tokens (flat field)
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":20,"prompt_cache_hit_tokens":80}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message")
	}
	if timings.PromptN != 40 {
		t.Errorf("PromptN = %d, want 40 (120 - 80 cached)", timings.PromptN)
	}
	if timings.CacheReadTokens != 80 {
		t.Errorf("CacheReadTokens = %d, want 80", timings.CacheReadTokens)
	}
}

func TestParseRootFields_openai_responses_input_tokens_details(t *testing.T) {
	// OpenAI Responses API: usage.input_tokens_details.cached_tokens
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":90,"completion_tokens":10,"input_tokens_details":{"cached_tokens":60}}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message")
	}
	if timings.PromptN != 30 {
		t.Errorf("PromptN = %d, want 30 (90 - 60 cached)", timings.PromptN)
	}
	if timings.CacheReadTokens != 60 {
		t.Errorf("CacheReadTokens = %d, want 60", timings.CacheReadTokens)
	}
}

func TestParseRootFields_llamacpp_usage_tokens_cached(t *testing.T) {
	// llama.cpp sometimes puts tokens_cached INSIDE the usage object
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":15,"tokens_cached":40}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message")
	}
	if timings.PromptN != 40 {
		t.Errorf("PromptN = %d, want 40 (80 - 40 cached)", timings.PromptN)
	}
	if timings.CacheReadTokens != 40 {
		t.Errorf("CacheReadTokens = %d, want 40", timings.CacheReadTokens)
	}
}

func TestParseRootFields_completion_tokens_details_cache(t *testing.T) {
	// Some providers put cache info in completion_tokens_details
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":25,"completion_tokens_details":{"cached_tokens":10}}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message")
	}
	if timings.CacheReadTokens != 10 {
		t.Errorf("CacheReadTokens = %d, want 10", timings.CacheReadTokens)
	}
	if timings.PromptN != 100 {
		t.Errorf("PromptN = %d, want 100 (no prompt cache)", timings.PromptN)
	}
}

func TestParseRootFields_timings_format(t *testing.T) {
	// Custom Goa timings format (prompt_n, predicted_n at root level)
	chunk := `{"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}],"timings":{"prompt_n":100,"predicted_n":20,"prompt_ms":500,"predicted_ms":2000}}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message from timings")
	}
	if timings.PromptN != 100 {
		t.Errorf("PromptN = %d, want 100", timings.PromptN)
	}
	if timings.PredictedN != 20 {
		t.Errorf("PredictedN = %d, want 20", timings.PredictedN)
	}
	if timings.PromptMs != 500 {
		t.Errorf("PromptMs = %.0f, want 500", timings.PromptMs)
	}
	if timings.PredictedMs != 2000 {
		t.Errorf("PredictedMs = %.0f, want 2000", timings.PredictedMs)
	}
}

func TestParseRootFields_root_level_prompt_completion_tokens(t *testing.T) {
	// Some backends (llama.cpp variants) put token counts at root level
	chunk := `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"prompt_tokens":60,"completion_tokens":12,"tokens_cached":25}`
	msgs, _ := parseChunk(chunk)

	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings message")
	}
	if timings.PromptN != 35 {
		t.Errorf("PromptN = %d, want 35 (60 - 25 cached)", timings.PromptN)
	}
	if timings.PredictedN != 12 {
		t.Errorf("PredictedN = %d, want 12", timings.PredictedN)
	}
	if timings.CacheReadTokens != 25 {
		t.Errorf("CacheReadTokens = %d, want 25", timings.CacheReadTokens)
	}
}

func TestParseRootFields_usage_only_chunk(t *testing.T) {
	// stream_options.include_usage emits a final chunk with only usage, no choices
	chunk := `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":25,"total_tokens":125}}`
	msgs, _ := parseChunk(chunk)

	if len(msgs) == 0 {
		t.Fatal("expected usage messages from usage-only chunk")
	}
	var timings *agentic.TokenTimings
	for _, m := range msgs {
		if m.Timings != nil {
			timings = m.Timings
			break
		}
	}
	if timings == nil {
		t.Fatal("expected TokenTimings from usage-only chunk")
	}
	if timings.PromptN != 100 {
		t.Errorf("PromptN = %d, want 100", timings.PromptN)
	}
	if timings.PredictedN != 25 {
		t.Errorf("PredictedN = %d, want 25", timings.PredictedN)
	}
}

func TestParseRootFields_no_cache_no_timings(t *testing.T) {
	// Chunk with no cache or timings should not emit TokenTimings
	chunk := `{"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}`
	msgs, _ := parseChunk(chunk)

	for _, m := range msgs {
		if m.Timings != nil {
			t.Fatal("unexpected TokenTimings message when no cache/timings present")
		}
	}
}

// TestParseChunkMalformedSurfacesError verifies AGENT-B5: malformed JSON
// returns a descriptive decode error rather than being silently swallowed.
func TestParseChunkMalformedSurfacesError(t *testing.T) {
	_, err := parseChunk("{not valid json")
	if err == nil {
		t.Fatal("expected error for malformed chunk, got nil")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected descriptive decode error, got: %v", err)
	}
}

func TestParseChunk_ErrorPayloadSurfacesError(t *testing.T) {
	chunk := `{"error":{"message":"Context length exceeded"},"message":"Context length exceeded"}`
	_, err := parseChunk(chunk)
	if err == nil {
		t.Fatal("expected error for error payload, got nil")
	}
	if !strings.Contains(err.Error(), "Context length exceeded") {
		t.Fatalf("expected error to contain provider message, got: %v", err)
	}
}

func TestParseOpenAIStream_ErrorEventSurfacesError(t *testing.T) {
	body := io.NopCloser(strings.NewReader(
		"event: error\n" +
			"data: {\"error\":{\"message\":\"Context length exceeded\"},\"message\":\"Context length exceeded\"}\n\n",
	))
	stream := provider.NewAssistantMessageEventStream(16)
	go parseOpenAIStream(body, stream)

	for range stream.Seq() {
	}
	err := stream.Err()
	if err == nil {
		t.Fatal("expected stream error after error event, got nil")
	}
	if !strings.Contains(err.Error(), "Context length exceeded") {
		t.Fatalf("expected error to contain provider message, got: %v", err)
	}
}
