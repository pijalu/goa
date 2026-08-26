// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// protocolLog emits diagnostics for materialized provider defaults.
var protocolLog = log.New(os.Stderr, "goa/protocol: ", log.LstdFlags)

func init() {
	Register(&openAICompletions{})
}

type openAICompletions struct{}

func (p *openAICompletions) API() schema.Api {
	return schema.ApiOpenAICompletions
}

func (p *openAICompletions) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	return nil
}

func (p *openAICompletions) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	compat := resolveOpenAICompat(model, profile)
	body := buildOpenAIParams(model, ctx, opts, profile, compat)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if opts.OnPayload != nil {
		modified, hookErr := opts.OnPayload(bodyBytes, model)
		if hookErr != nil {
			return nil, fmt.Errorf("onPayload hook: %w", hookErr)
		}
		if m, ok := modified.([]byte); ok {
			bodyBytes = m
		}
	}
	return bodyBytes, nil
}

func (p *openAICompletions) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseOpenAIStream(reader, stream)
	return nil
}

// ---------------------------------------------------------------------------
// Compatibility resolution
// ---------------------------------------------------------------------------

type openAICompletionsCompat struct {
	SupportsStore                               bool
	SupportsDeveloperRole                       bool
	SupportsReasoningEffort                     bool
	SupportsUsageInStreaming                    bool
	MaxTokensField                              string
	RequiresToolResultName                      bool
	RequiresAssistantAfterToolResult            bool
	RequiresThinkingAsText                      bool
	RequiresReasoningContentOnAssistantMessages bool
	ThinkingFormat                              string
	ZaiToolStream                               bool
	SupportsStrictMode                          bool
	CacheControlFormat                          string
	SendSessionAffinityHeaders                  bool
	SupportsLongCacheRetention                  bool
	SupportsPromptCache                         bool
	ToolResultAsUser                            bool
	// SupportsTemperature false omits the temperature field (kimi-code
	// rejects any value but its fixed default with HTTP 400).
	SupportsTemperature bool
}

func resolveOpenAICompat(model schema.Model, profile schema.VariantProfile) openAICompletionsCompat {
	c := openAICompletionsCompat{
		MaxTokensField:     profile.Compat.MaxTokensField,
		ThinkingFormat:     profile.Compat.ThinkingFormat,
		CacheControlFormat: "",
		// DeepSeek-class models (thinking mode) 400 when reasoning_content
		// is not passed back. The variant profile carries the requirement
		// (e.g. opencode.json) — it must reach the serializer
		// thinking-mode 400).
		RequiresReasoningContentOnAssistantMessages: profile.Compat.RequiresReasoningContentOnAssistantMessages,
	}
	if c.MaxTokensField == "" {
		c.MaxTokensField = "max_completion_tokens"
	}
	if profile.Compat.SupportsStore != nil {
		c.SupportsStore = *profile.Compat.SupportsStore
	}
	// Temperature is supported unless the variant profile explicitly disables
	// it (nil = supported, the standard behavior).
	c.SupportsTemperature = true
	if profile.Compat.SupportsTemperature != nil {
		c.SupportsTemperature = *profile.Compat.SupportsTemperature
	}
	if profile.CachePolicy.Mode != "" && profile.CachePolicy.Mode != schema.CacheModeNone {
		c.CacheControlFormat = "anthropic"
	}
	// The long-retention gate must be derived here: the protocol layer owns
	// the prompt_cache_key emission, and nothing else populates this flag on
	// the wire path (the provider-layer compat struct never crosses the
	// boundary). Without it, long retention silently sent no cache identity.
	c.SupportsLongCacheRetention = supportsLongCacheRetention(model)
	c.SupportsPromptCache = profile.Compat.SupportsPromptCache
	return c
}

// ---------------------------------------------------------------------------
// Response parsing
// ---------------------------------------------------------------------------

type toolCallAccum struct {
	Index int
	ID    string
	Name  string
	Args  string
}

type streamAccum struct {
	Stream            *schema.AssistantMessageEventStream
	ToolAccums        []*toolCallAccum
	ContentBuf        string
	ThinkingBuf       string
	HasContent        bool
	Started           bool
	Ended             bool
	ProviderTimings   *parserTimings
	pendingStopReason *schema.StopReason
}

func newStreamAccum(stream *schema.AssistantMessageEventStream) *streamAccum {
	return &streamAccum{Stream: stream}
}

func parseOpenAIStream(body io.Reader, stream *schema.AssistantMessageEventStream) {
	acc := newStreamAccum(stream)
	var decodeErr error

	if err := transport.ParseSSE(body, func(ev transport.SSEEvent) bool {
		// OpenAI-compatible servers terminate SSE streams with a [DONE] marker.
		// Treat it as a graceful end-of-stream instead of a JSON parse error.
		if strings.TrimSpace(ev.Data) == "[DONE]" {
			return true
		}
		msgs, pErr := parseOpenAIChunk(ev.Data)
		if pErr != nil {
			decodeErr = pErr
			return false
		}
		for _, m := range msgs {
			acc.dispatchMessage(m)
		}
		return true
	}); err != nil {
		// Surface I/O failures (idle timeout, connection drop, oversized line)
		// as a stream error so the agent retries instead of finalizing a
		// truncated/empty turn silently.
		stream.CloseWithError(fmt.Errorf("sse stream read failed: %w", err))
		return
	}

	if decodeErr != nil {
		stream.CloseWithError(fmt.Errorf("chunk decode failed: %w", decodeErr))
		return
	}
	stopReason := schema.StopReasonEndTurn
	if acc.pendingStopReason != nil {
		stopReason = *acc.pendingStopReason
	}
	if !acc.Ended {
		acc.finish(stopReason)
	} else if acc.ProviderTimings != nil && acc.pendingStopReason != nil {
		acc.updateResultWithUsage()
	}
}

func (a *streamAccum) dispatchMessage(m parserMessage) {
	if a.handleTimingMessage(m) {
		return
	}
	switch {
	case m.Type == parserToolCall:
		a.handleToolCall(m)
	case m.Type == parserEnd:
		a.handleEndMessage(m)
	case m.Role == parserRoleAssistant:
		a.handleAssistantMessage(m)
	}
}

func (a *streamAccum) handleTimingMessage(m parserMessage) bool {
	if m.Timings == nil || (m.Timings.PromptN == 0 && m.Timings.PredictedN == 0 && m.Timings.CacheReadTokens == 0) {
		return false
	}
	a.ProviderTimings = m.Timings
	if a.Ended {
		a.updateResultWithUsage()
		return true
	}
	return false
}

func (a *streamAccum) handleEndMessage(m parserMessage) {
	sr := mapOpenAIFinishReason(m.FinishReason)
	a.pendingStopReason = &sr
}

func (a *streamAccum) handleAssistantMessage(m parserMessage) {
	if m.Thinking != "" {
		a.handleThinking(m.Thinking)
	}
	if m.Content != "" {
		a.handleContent(m.Content)
	}
}

func (a *streamAccum) handleToolCall(m parserMessage) {
	idx := m.ToolCallIndex
	for _, ta := range a.ToolAccums {
		if ta.Index == idx {
			if m.ToolName != "" {
				ta.Name = m.ToolName
			}
			if m.ToolInput != "" {
				ta.Args += m.ToolInput
				// Emit incremental args delta so the TUI can show progress
				// as the tool call arguments are being streamed in.
				a.Stream.Push(schema.AssistantMessageEvent{
					Type:         schema.EventToolCallDelta,
					ContentIndex: idx,
					Delta:        m.ToolInput,
					Partial: &schema.AssistantMessage{
						Content: []schema.ContentBlock{{
							Type:          schema.ContentBlockToolCall,
							ToolCallID:    ta.ID,
							ToolName:      ta.Name,
							ToolArguments: ta.Args,
						}},
					},
				})
			}
			if m.ToolCallID != "" {
				ta.ID = m.ToolCallID
			}
			return
		}
	}
	// New tool call: emit EventToolCallStart with what we know so far.
	a.ToolAccums = append(a.ToolAccums, &toolCallAccum{
		Index: idx, ID: m.ToolCallID, Name: m.ToolName, Args: m.ToolInput,
	})
	a.HasContent = true
	a.ensureStarted()
	a.Stream.Push(schema.AssistantMessageEvent{
		Type:         schema.EventToolCallStart,
		ContentIndex: idx,
		Partial: &schema.AssistantMessage{
			Content: []schema.ContentBlock{{
				Type:          schema.ContentBlockToolCall,
				ToolCallID:    m.ToolCallID,
				ToolName:      m.ToolName,
				ToolArguments: m.ToolInput,
			}},
		},
	})
}

func (a *streamAccum) handleThinking(delta string) {
	a.ensureStarted()
	a.ThinkingBuf += delta
	a.Stream.Push(schema.AssistantMessageEvent{Type: schema.EventThinkingDelta, Delta: delta})
	a.HasContent = true
}

func (a *streamAccum) handleContent(delta string) {
	a.ensureStarted()
	a.ContentBuf += delta
	a.Stream.Push(schema.AssistantMessageEvent{Type: schema.EventTextDelta, Delta: delta})
	a.HasContent = true
}

func (a *streamAccum) ensureStarted() {
	if !a.Started {
		a.Started = true
		a.Stream.Push(schema.AssistantMessageEvent{Type: schema.EventStart, Partial: &schema.AssistantMessage{}})
	}
}

func (a *streamAccum) flushToolCalls() {
	for _, ta := range a.ToolAccums {
		a.Stream.Push(schema.AssistantMessageEvent{
			Type: schema.EventToolCallEnd,
			ToolCall: &schema.ContentBlock{
				Type:          schema.ContentBlockToolCall,
				ToolCallID:    ta.ID,
				ToolName:      ta.Name,
				ToolArguments: ta.Args,
			},
		})
	}
}

func (a *streamAccum) finish(stopReason schema.StopReason) {
	if a.Ended {
		return
	}
	a.flushToolCalls()
	a.ensureStarted()
	var blocks []schema.ContentBlock
	if a.ThinkingBuf != "" {
		blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockThinking, Thinking: a.ThinkingBuf})
	}
	if a.ContentBuf != "" {
		blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockText, Text: a.ContentBuf})
	}
	for _, ta := range a.ToolAccums {
		blocks = append(blocks, schema.ContentBlock{
			Type:          schema.ContentBlockToolCall,
			ToolCallID:    ta.ID,
			ToolName:      ta.Name,
			ToolArguments: ta.Args,
		})
	}
	msg := &schema.AssistantMessage{Content: blocks, StopReason: stopReason}
	if a.ProviderTimings != nil {
		msg.Usage = &schema.Usage{
			InputTokens:         a.ProviderTimings.PromptN,
			OutputTokens:        a.ProviderTimings.PredictedN,
			CacheReadTokens:     a.ProviderTimings.CacheReadTokens,
			CacheCreationTokens: a.ProviderTimings.CacheWriteTokens,
		}
	}
	a.Ended = true
	a.Stream.End(msg)
}

func (a *streamAccum) updateResultWithUsage() {
	if a.ProviderTimings == nil {
		return
	}
	usage := &schema.Usage{
		InputTokens:         a.ProviderTimings.PromptN,
		OutputTokens:        a.ProviderTimings.PredictedN,
		CacheReadTokens:     a.ProviderTimings.CacheReadTokens,
		CacheCreationTokens: a.ProviderTimings.CacheWriteTokens,
	}
	a.Stream.UpdateResult(usage)
}

func parseOpenAIChunk(chunk string) ([]parserMessage, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(chunk), &raw); err != nil {
		return nil, fmt.Errorf("decode openai chunk: %w", err)
	}
	if err := detectOpenAIChunkError(raw, chunk); err != nil {
		return nil, err
	}
	choices, ok := raw["choices"].([]any)
	if !ok || len(choices) == 0 {
		if rootMsgs := parseRootFields(raw); len(rootMsgs) > 0 {
			return rootMsgs, nil
		}
		return nil, nil
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return nil, nil
	}
	// Masked upstream failure check must run before any delta/finish
	// handling: an error-marked native_finish_reason invalidates the whole
	// chunk regardless of what else it carries.
	if nerr := detectNativeFinishError(choice); nerr != nil {
		return nil, nerr
	}
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		if finishReason, ok := choice["finish_reason"]; ok {
			return handleFinishReason(finishReason), nil
		}
		return nil, nil
	}
	var out []parserMessage
	out = append(out, parseToolCalls(delta)...)
	out = append(out, parseContentDelta(delta)...)
	out = append(out, parseThinkingDeltas(delta)...)
	out = append(out, parseRootFields(raw)...)
	if finishReason := handleFinishReason(choice["finish_reason"]); finishReason != nil {
		out = append(out, finishReason...)
	}
	return out, nil
}

// detectOpenAIChunkError surfaces provider error frames delivered inside
// the SSE stream. LM Studio/llama.cpp report request failures (e.g. HTTP 400
// chat-template rejections such as "System message must be at the
// beginning") as HTTP 200 + "event: error" frames whose data payload carries
// {"error": {...}} instead of a choices chunk. Without this check the frame
// parses to zero messages and the stream ends "cleanly", so the agent
// misclassifies a hard provider error as an empty response and retries a
// payload that can never succeed — the session breaks with no diagnosable
// message (2026-08-04 LM Studio export).
//
// The frame is classified into a *hooks.ProviderError so a mid-stream
// 5xx/408/429 (llama.cpp "[503] The request queue is full.", overloads,
// rate limits) is retried by the agent instead of killing the turn as a
// non-retryable bare error: these frames bypass the transport's error hook
// (HTTP 200), so without classification here the retry loop never engages.
func detectOpenAIChunkError(raw map[string]any, chunk string) error {
	errObj, ok := raw["error"]
	if !ok || errObj == nil {
		return nil
	}
	msg := "provider error"
	if m, ok := raw["message"].(string); ok && m != "" {
		msg = m
	}
	if m, ok := openAIErrorMessage(errObj); ok && m != "" {
		msg = m
	}
	status := hooks.ExtractStreamErrorStatus(errObj, msg)
	return hooks.NewStreamFrameError(fmt.Errorf("LLM error: %s", msg), status, chunk)
}

func openAIErrorMessage(errObj any) (string, bool) {
	m, ok := errObj.(map[string]any)
	if !ok {
		return "", false
	}
	msg, ok := m["message"].(string)
	return msg, ok
}

// detectNativeFinishError surfaces upstream failures that OpenRouter-style
// gateways mask as clean completions (regression 2026-08-24 export): the
// router's upstream provider died mid-generation and the final SSE chunk
// arrived as HTTP 200 with finish_reason="stop" but ZERO content — only
// native_finish_reason="network_error" tells the truth. The parser honored
// the flattened finish_reason, the turn ended cleanly with no message, no
// error and no retry: the session just went silent ("frozen") right after a
// tool call.
//
// Any error-marked native_finish_reason ("network_error", "error", …) is
// classified as HTTP 502 Bad Gateway — semantically exact: the gateway at
// the edge answered 200 while its upstream hop failed — so the existing
// mid-stream retry machinery engages exactly as it does for llama.cpp 5xx
// frames. Normal native reasons (stop/length/tool_calls/content_filter and
// provider-specific pass-throughs) keep the historical flattening behavior.
func detectNativeFinishError(choice map[string]any) error {
	native, ok := choice["native_finish_reason"].(string)
	if !ok || !strings.Contains(strings.ToLower(native), "error") {
		return nil
	}
	err := fmt.Errorf("LLM error: upstream provider failed (native_finish_reason: %s)", native)
	return hooks.NewStreamFrameError(err, http.StatusBadGateway, native)
}

func parseToolCalls(delta map[string]any) []parserMessage {
	tc, ok := delta["tool_calls"].([]any)
	if !ok {
		return nil
	}
	var out []parserMessage
	for _, t := range tc {
		toolCall, ok := t.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := toolCall["function"].(map[string]any)
		if !ok {
			continue
		}
		msg := parserMessage{Type: parserToolCall, Delta: true}
		if id, ok := toolCall["id"].(string); ok {
			msg.ToolCallID = id
		}
		if name, ok := fn["name"].(string); ok {
			msg.ToolName = name
		}
		if args, ok := fn["arguments"].(string); ok {
			msg.ToolInput = args
		}
		if idx, ok := toolCall["index"].(float64); ok {
			msg.ToolCallIndex = int(idx)
		}
		out = append(out, msg)
	}
	return out
}

func parseContentDelta(delta map[string]any) []parserMessage {
	c, ok := delta["content"].(string)
	if !ok || c == "" {
		return nil
	}
	return []parserMessage{{Type: parserContent, Role: parserRoleAssistant, Content: c, Delta: true}}
}

func parseThinkingDeltas(delta map[string]any) []parserMessage {
	var out []parserMessage
	for _, field := range []string{"reasoning_content", "reasoning", "thinking"} {
		if t, ok := delta[field].(string); ok && t != "" {
			out = append(out, parserMessage{Type: parserContent, Role: parserRoleAssistant, Thinking: t, Delta: true})
			break
		}
	}
	return out
}

func handleFinishReason(reason any) []parserMessage {
	finishReason, ok := reason.(string)
	if !ok || finishReason == "" {
		return nil
	}
	return []parserMessage{{Type: parserEnd, FinishReason: finishReason}}
}

// mapOpenAIFinishReason maps an OpenAI-style finish_reason string to the
// provider-agnostic stop reason. Unknown or absent reasons degrade to
// EndTurn, preserving the historical flattening behavior for every reason
// except the ones the agent acts on ("length" above all: window-edge
// truncation is the last warning before a context_length_exceeded rejection).
func mapOpenAIFinishReason(reason string) schema.StopReason {
	switch reason {
	case "length":
		return schema.StopReasonMaxTokens
	case "tool_calls", "function_call":
		return schema.StopReasonToolCall
	case "content_filter":
		return schema.StopReasonContentFiltered
	default: // "stop", "end_turn", "", unknown
		return schema.StopReasonEndTurn
	}
}

func parseRootFields(raw map[string]any) []parserMessage {
	var out []parserMessage
	timings := mergeRootTimings(raw)
	rootPromptTokens := applyRootTokenCounts(raw, &timings)
	applyRootCacheFields(raw, &timings)
	computeRootPromptN(rootPromptTokens, &timings)
	if timings != nil {
		out = append(out, parserMessage{Timings: timings})
	}
	return out
}
