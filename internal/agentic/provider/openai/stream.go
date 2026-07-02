// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

func streamOpenAICompletions(
	model provider.Model,
	ctx provider.Context,
	opts provider.StreamOptions,
	compat provider.OpenAICompletionsCompat,
) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	bodyBytes, err := prepareRequestBody(model, ctx, opts, compat)
	if err != nil {
		return nil, err
	}

	goCtx := ctx.GoContext()
	resp, err := sendStreamRequest(goCtx, model, opts, bodyBytes)
	if err != nil {
		return nil, err
	}

	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = provider.DefaultStreamIdleTimeout
	}
	resp.Body = provider.NewIdleTimeoutReader(resp.Body, idleTimeout)

	go provider.CloseStreamOnCancel(goCtx, stream)
	go parseOpenAIStream(resp.Body, stream)
	return stream, nil
}

func prepareRequestBody(model provider.Model, ctx provider.Context, opts provider.StreamOptions, compat provider.OpenAICompletionsCompat) ([]byte, error) {
	body := buildParams(model, ctx, opts, compat)
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

func sendStreamRequest(goCtx context.Context, model provider.Model, opts provider.StreamOptions, bodyBytes []byte) (*http.Response, error) {
	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/chat/completions"
	}

	req, err := http.NewRequestWithContext(goCtx, "POST", baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := buildHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if opts.OnResponse != nil {
		headers := make(map[string]string)
		for k := range resp.Header {
			headers[k] = resp.Header.Get(k)
		}
		opts.OnResponse(resp.StatusCode, headers)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &errResponse{Status: resp.StatusCode, Body: string(bodyErr)}
	}
	return resp, nil
}

func buildParams(model provider.Model, ctx provider.Context, opts provider.StreamOptions, compat provider.OpenAICompletionsCompat) map[string]interface{} {
	messages := convertMessages(model, ctx.Messages, ctx.SystemPrompt, compat)
	tools := convertTools(ctx.Tools)

	if provider.ToString(compat.CacheControlFormat, "") == "anthropic" {
		cc := newCacheControl(opts.CacheRetention, provider.ToBool(compat.SupportsLongCacheRetention, false))
		applyCacheControl(messages, tools, cc)
	}

	body := map[string]interface{}{
		"model":    model.ID,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]interface{}{
			"include_usage": true,
		},
	}
	if opts.MaxTokens > 0 {
		field := provider.ToString(compat.MaxTokensField, "max_completion_tokens")
		body[field] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if len(tools) > 0 {
		body["tools"] = tools
		// Force tool use when ToolChoice is set (e.g., "required" for workflow agents)
		if opts.ToolChoice != "" {
			body["tool_choice"] = opts.ToolChoice
		}
	}
	if provider.ToBool(compat.SupportsStore, false) {
		body["store"] = false
	}
	if key := promptCacheKey(model, opts, compat); key != "" {
		body["prompt_cache_key"] = key
	}
	if retention := promptCacheRetention(opts, compat); retention != "" {
		body["prompt_cache_retention"] = retention
	}
	return body
}

// ---------------------------------------------------------------------------
// Stream accumulator
// ---------------------------------------------------------------------------

// toolCallAccum tracks a single streaming tool call across chunks.
type toolCallAccum struct {
	Index int
	ID    string
	Name  string
	Args  string
}

// streamAccum tracks all state during an OpenAI SSE stream parse.
type streamAccum struct {
	Stream      *provider.AssistantMessageEventStream
	ToolAccums  []*toolCallAccum
	ContentBuf  string
	ThinkingBuf string
	HasContent  bool
	Started     bool
	Ended       bool // true once Stream.End has been called
	// Timings from usage-only chunks (stream_options.include_usage).
	// These are accumulated and attached to the final AssistantMessage.Usage.
	ProviderTimings *agentic.TokenTimings
	// Pending stop reason — set from the End message but we delay calling
	// finish() until after the SSE parse loop completes to allow the usage
	// chunk (which arrives after finish_reason) to be captured.
	pendingStopReason *provider.StopReason
}

func newStreamAccum(stream *provider.AssistantMessageEventStream) *streamAccum {
	return &streamAccum{Stream: stream}
}

func (a *streamAccum) ensureStarted() {
	if !a.Started {
		a.Started = true
		a.Stream.Push(provider.AssistantMessageEvent{
			Type:    provider.EventStart,
			Partial: &provider.AssistantMessage{},
		})
	}
}

func (a *streamAccum) handleToolCall(msg agentic.Message) {
	idx := msg.ToolCallIndex
	for _, ta := range a.ToolAccums {
		if ta.Index == idx {
			if msg.ToolName != "" {
				ta.Name = msg.ToolName
			}
			if msg.ToolInput != "" {
				ta.Args += msg.ToolInput
			}
			if msg.ToolCallID != "" {
				ta.ID = msg.ToolCallID
			}
			return
		}
	}
	a.ToolAccums = append(a.ToolAccums, &toolCallAccum{
		Index: idx, ID: msg.ToolCallID, Name: msg.ToolName, Args: msg.ToolInput,
	})
	a.HasContent = true
}

func (a *streamAccum) flushToolCalls() {
	for _, ta := range a.ToolAccums {
		a.Stream.Push(provider.AssistantMessageEvent{
			Type: provider.EventToolCallEnd,
			ToolCall: &provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    ta.ID,
				ToolName:      ta.Name,
				ToolArguments: ta.Args,
			},
		})
	}
	a.ToolAccums = nil
}

func (a *streamAccum) handleThinking(delta string) {
	a.ensureStarted()
	a.ThinkingBuf += delta
	a.Stream.Push(provider.AssistantMessageEvent{
		Type:  provider.EventThinkingDelta,
		Delta: delta,
	})
	a.HasContent = true
}

func (a *streamAccum) handleContent(delta string) {
	a.ensureStarted()
	a.ContentBuf += delta
	a.Stream.Push(provider.AssistantMessageEvent{
		Type:  provider.EventTextDelta,
		Delta: delta,
	})
	a.HasContent = true
}

func (a *streamAccum) finish(stopReason provider.StopReason) {
	if a.Ended {
		return
	}
	a.flushToolCalls()
	a.ensureStarted()

	var blocks []provider.ContentBlock
	if a.ThinkingBuf != "" {
		blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockThinking, Thinking: a.ThinkingBuf})
	}
	if a.ContentBuf != "" {
		blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: a.ContentBuf})
	}

	// Include provider timings as Usage if available (from stream_options.include_usage)
	// Note: the usage chunk typically arrives AFTER finish_reason, so this will
	// usually be nil here. updateResultWithUsage handles the post-End case.
	msg := &provider.AssistantMessage{
		Content:    blocks,
		StopReason: stopReason,
	}
	if a.ProviderTimings != nil {
		msg.Usage = &provider.Usage{
			InputTokens:         a.ProviderTimings.PromptN,
			OutputTokens:        a.ProviderTimings.PredictedN,
			CacheReadTokens:     a.ProviderTimings.CacheReadTokens,
			CacheCreationTokens: a.ProviderTimings.CacheWriteTokens,
		}
	}

	a.Ended = true
	a.Stream.End(msg)
}

// updateResultWithUsage updates the stream result with provider timings after
// the stream has already ended. This handles the case where the usage chunk
// (from stream_options.include_usage) arrives after the finish_reason chunk.
func (a *streamAccum) updateResultWithUsage() {
	if a.ProviderTimings == nil {
		return
	}
	usage := &provider.Usage{
		InputTokens:         a.ProviderTimings.PromptN,
		OutputTokens:        a.ProviderTimings.PredictedN,
		CacheReadTokens:     a.ProviderTimings.CacheReadTokens,
		CacheCreationTokens: a.ProviderTimings.CacheWriteTokens,
	}
	a.Stream.UpdateResult(usage)
}

// parseOpenAIStream reads the SSE response and emits events into the stream.
func parseOpenAIStream(body io.ReadCloser, stream *provider.AssistantMessageEventStream) {
	defer body.Close()

	acc := newStreamAccum(stream)
	var decodeErr error
	sawDone := false

	err := provider.ParseSSE(body, func(chunk string) {
		msgs, pErr := parseChunk(chunk)
		if pErr != nil {
			decodeErr = pErr
			return
		}
		for _, m := range msgs {
			acc.dispatchMessage(m)
		}
	}, func() { sawDone = true })

	if err != nil {
		stream.CloseWithError(fmt.Errorf("SSE parse error: %w", err))
		return
	}
	if decodeErr != nil {
		stream.CloseWithError(fmt.Errorf("chunk decode failed: %w", decodeErr))
		return
	}
	// SSE stream fully consumed ([DONE] or clean EOF). If the provider closed
	// the connection without sending a finish_reason (or [DONE]) and we are
	// not in the middle of a tool-call batch, treat it as a premature EOF so
	// the agent retries and the UI surfaces an error instead of freezing on
	// a partial response.
	if acc.pendingStopReason == nil && len(acc.ToolAccums) == 0 && !sawDone {
		stream.CloseWithError(fmt.Errorf("SSE stream ended prematurely: no finish_reason or [DONE] marker"))
		return
	}
	// SSE stream fully consumed ([DONE] or clean EOF).
	// Call finish() now with the pending stop reason (from the finish_reason
	// chunk) or EndTurn as fallback. By this point, any usage-only chunk
	// (stream_options.include_usage) has already been processed by
	// dispatchMessage and stored in ProviderTimings.
	stopReason := provider.StopReasonEndTurn
	if acc.pendingStopReason != nil {
		stopReason = *acc.pendingStopReason
	}
	if !acc.Ended {
		acc.finish(stopReason)
	} else if acc.ProviderTimings != nil && acc.pendingStopReason != nil {
		// Already ended (e.g. from error path) but we have timings — update result
		acc.updateResultWithUsage()
	}
}

// dispatchMessage routes a single parsed SSE message to the appropriate handler.
func (a *streamAccum) dispatchMessage(m agentic.Message) {
	if a.handleTimingMessage(m) {
		return
	}

	switch {
	case m.Type == agentic.ToolCall:
		a.handleToolCall(m)
	case m.Type == agentic.End:
		a.handleEndMessage()
	case m.Role == agentic.Assistant:
		a.handleAssistantMessage(m)
	}
}

func (a *streamAccum) handleTimingMessage(m agentic.Message) bool {
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

func (a *streamAccum) handleEndMessage() {
	sr := provider.StopReasonEndTurn
	a.pendingStopReason = &sr
}

func (a *streamAccum) handleAssistantMessage(m agentic.Message) {
	if m.Thinking != "" {
		a.handleThinking(m.Thinking)
	}
	if m.Content != "" {
		a.handleContent(m.Content)
	}
}
