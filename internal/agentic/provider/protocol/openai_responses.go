// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

func init() {
	Register(&openAIResponses{})
	Register(&openAICodexResponses{})
	Register(&azureOpenAIResponses{})
}

type openAIResponses struct{}

func (p *openAIResponses) API() schema.Api {
	return schema.ApiOpenAIResponses
}

func (p *openAIResponses) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	return nil
}

func (p *openAIResponses) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	return buildResponsesBody(model, ctx, opts, profile, "")
}

func (p *openAIResponses) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseResponsesSSE(reader, stream)
	return nil
}

type openAICodexResponses struct{}

func (p *openAICodexResponses) API() schema.Api {
	return schema.ApiOpenAICodexResponses
}

func (p *openAICodexResponses) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	return nil
}

func (p *openAICodexResponses) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	return buildResponsesBody(model, ctx, opts, profile, "codex")
}

func (p *openAICodexResponses) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseResponsesSSE(reader, stream)
	return nil
}

type azureOpenAIResponses struct{}

func (p *azureOpenAIResponses) API() schema.Api {
	return schema.ApiAzureOpenAIResponses
}

func (p *azureOpenAIResponses) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	return map[string]string{"api-key": "unused"}
}

func (p *azureOpenAIResponses) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	return buildResponsesBody(model, ctx, opts, profile, "")
}

func (p *azureOpenAIResponses) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseResponsesSSE(reader, stream)
	return nil
}

func buildResponsesBody(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile, flavor string) ([]byte, error) {
	// Codex carries the system prompt in the dedicated instructions field
	// (matching the codex CLI and Pi) rather than as a leading input message.
	// Other responses flavors keep the system prompt as an input message.
	isCodex := flavor == "codex"
	systemPrompt := ctx.SystemPrompt
	if isCodex {
		systemPrompt = ""
	}

	body := map[string]any{
		"model":  model.ID,
		"input":  convertResponsesInput(ctx.Messages, systemPrompt, profile),
		"stream": true,
	}
	if isCodex {
		applyCodexBodyFields(body, ctx)
	}
	applyResponsesToolFields(body, ctx)
	applyResponsesSessionFields(body, model, opts)
	applyResponsesSamplingFields(body, model, opts, profile, isCodex)
	if store := profile.Compat.SupportsStore; store != nil {
		body["store"] = *store
	}
	if opts.ServiceTier != "" {
		body["service_tier"] = opts.ServiceTier
	}
	return json.Marshal(body)
}

// applyCodexBodyFields sets the codex subscription request fields: the system
// prompt in the dedicated instructions field (matching the codex CLI and Pi)
// and parallel tool calls enabled by default.
func applyCodexBodyFields(body map[string]any, ctx schema.Context) {
	instructions := ctx.SystemPrompt
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	body["instructions"] = instructions
	body["tool_choice"] = "auto"
	body["parallel_tool_calls"] = true
}

// applyResponsesToolFields wires the tool list, honoring the final-step
// text-only collapse (P7): NoTools forces tool_choice "none" and drops the
// parallel-tool flag so the model answers text-only.
func applyResponsesToolFields(body map[string]any, ctx schema.Context) {
	if ctx.NoTools {
		body["tool_choice"] = "none"
		delete(body, "parallel_tool_calls")
		return
	}
	body["tools"] = convertResponsesTools(ctx.Tools)
}

// applyResponsesSessionFields wires session continuation and prompt caching.
// Session affinity rides prompt_cache_key on every Responses flavor —
// matching opencode, which never sends previous_response_id over SSE. The
// previous_response_id field must reference a server-issued response object
// ("resp_*"); Goa replays its full history every turn, and sending the
// client-side session ID there is a hard HTTP 400 on strict upstreams
// (opencode Zen: "previous_response_id must start with resp_", 2026-09-02 —
// the same rejection class the Codex flavor already carved out).
func applyResponsesSessionFields(body map[string]any, model schema.Model, opts schema.StreamOptions) {
	if shouldSendOpenAIResponsesPromptCacheKey(model, opts) {
		body["prompt_cache_key"] = ClampOpenAIPromptCacheKey(promptCacheIdentity(opts))
		if opts.CacheRetention == schema.CacheRetentionLong {
			body["prompt_cache_retention"] = "24h"
		}
	}
}

// applyResponsesSamplingFields wires token/temperature/top_p and the reasoning
// (thinking) block request fields.
func applyResponsesSamplingFields(body map[string]any, model schema.Model, opts schema.StreamOptions, profile schema.VariantProfile, isCodex bool) {
	if opts.MaxTokens > 0 {
		body["max_output_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if opts.TopP != nil {
		body["top_p"] = *opts.TopP
	}
	if model.Reasoning || profile.Compat.ThinkingFormat != "" {
		if responsesWantsEncryptedContent(profile) {
			body["include"] = []string{"reasoning.encrypted_content"}
		}
		body["text"] = map[string]any{"verbosity": "low"}
		body["reasoning"] = map[string]any{"summary": "auto"}
	}
}

// responsesWantsEncryptedContent decides whether the request asks for
// reasoning.encrypted_content. Encrypted reasoning content exists so a
// STATELESS client replaying its full history can carry reasoning items
// itself — and every Goa Responses request is stateless now that no flavor
// chains server-side via previous_response_id (strict upstreams demand a
// server-issued resp_* id there; opencode Zen 400s on a session ID,
// 2026-09-02). The include therefore rides all flavors by default, matching
// what the Codex flavor already pinned and what opencode sends for reasoning
// models. CompatFlags.SupportsEncryptedContent overrides the default in both
// directions (escape hatch for backends that reject encrypted content
// outright).
func responsesWantsEncryptedContent(profile schema.VariantProfile) bool {
	if profile.Compat.SupportsEncryptedContent != nil {
		return *profile.Compat.SupportsEncryptedContent
	}
	return true
}

// shouldSendOpenAIResponsesPromptCacheKey mirrors Pi's behavior: Azure and Codex
// Responses send prompt_cache_key whenever a cache identity is present, while
// plain OpenAI Responses only sends it when prompt caching is not explicitly
// disabled.
func shouldSendOpenAIResponsesPromptCacheKey(model schema.Model, opts schema.StreamOptions) bool {
	if promptCacheIdentity(opts) == "" {
		return false
	}
	if model.Api == schema.ApiAzureOpenAIResponses || model.Api == schema.ApiOpenAICodexResponses {
		return true
	}
	return opts.CacheRetention != schema.CacheRetentionNone
}

func promptCacheIdentity(opts schema.StreamOptions) string {
	if opts.PromptCacheKey != "" {
		return opts.PromptCacheKey
	}
	return opts.SessionID
}

func convertResponsesInput(messages []schema.Message, systemPrompt string, profile schema.VariantProfile) []map[string]any {
	var input []map[string]any
	if systemPrompt != "" {
		role := "system"
		if profile.Compat.SystemAsInstructions {
			role = "developer"
		}
		input = append(input, map[string]any{
			"role":    role,
			"content": systemPrompt,
		})
	}
	for _, msg := range messages {
		switch msg.Role {
		case schema.RoleSystem:
			input = append(input, map[string]any{
				"role":    "developer",
				"content": extractResponsesText(msg.Content),
			})
		case schema.RoleUser:
			input = append(input, map[string]any{
				"role":    "user",
				"content": extractResponsesText(msg.Content),
			})
		case schema.RoleAssistant:
			input = append(input, convertResponsesAssistant(msg)...)
		case schema.RoleToolResult:
			tcID, _, text := extractToolCallInfo(msg.Content)
			// The responses API expects a function_call_output item keyed by the
			// originating call_id, not a chat-completions role:"tool" message.
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": normalizeResponsesToolCallID(tcID),
				"output":  text,
			})
		}
	}
	return input
}

// convertResponsesAssistant serializes an assistant turn to responses-API
// output items: text becomes a message item carrying output_text content, and
// each tool call becomes a function_call item keyed by its call_id. This is
// the shape the responses API (and the Codex subscription backend) requires on
// history replay — chat-completions assistant/tool roles are rejected.
func convertResponsesAssistant(msg schema.Message) []map[string]any {
	var out []map[string]any
	if text := extractResponsesText(msg.Content); text != "" {
		out = append(out, map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": text},
			},
		})
	}
	for _, b := range msg.Content {
		if b.Type != schema.ContentBlockToolCall {
			continue
		}
		out = append(out, map[string]any{
			"type":      "function_call",
			"call_id":   normalizeResponsesToolCallID(b.ToolCallID),
			"name":      b.ToolName,
			"arguments": b.ToolArguments,
		})
	}
	return out
}

func normalizeResponsesToolCallID(id string) string {
	if id == "" || strings.HasPrefix(id, "call_") {
		return id
	}
	return "fc_" + id
}

func convertResponsesTools(tools []schema.ToolSchema) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.InputSchema,
			"strict":      false,
		}
	}
	return out
}

func extractResponsesText(blocks []schema.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == schema.ContentBlockText {
			return b.Text
		}
	}
	return ""
}

func extractToolCallInfo(blocks []schema.ContentBlock) (id, name, text string) {
	for _, b := range blocks {
		if b.Type == schema.ContentBlockToolResult {
			return b.ToolCallID, b.ToolName, b.Text
		}
	}
	return "", "", ""
}

// responsesToolCall accumulates one in-flight function_call output item. The
// responses API streams tool-call identity via output_item.added and the
// arguments incrementally via function_call_arguments.delta, finalizing them
// in function_call_arguments.done / output_item.done.
type responsesToolCall struct {
	id      string
	name    string
	args    string // accumulated arguments JSON
	callID  string // the call_id used to match tool results (falls back to id)
	emitted bool   // tool-call event already pushed
}

type responsesEventContext struct {
	contentBuf string
	outputText string
	started    bool
	ended      bool
	decodeErr  error
	stream     *schema.AssistantMessageEventStream
	// toolCalls tracks in-flight function_call items by output_index so
	// streamed argument deltas accumulate onto the right call.
	toolCalls map[int]*responsesToolCall
}

func parseResponsesSSE(body io.Reader, stream *schema.AssistantMessageEventStream) {
	defer closeIfCloser(body)
	ctx := &responsesEventContext{stream: stream, toolCalls: map[int]*responsesToolCall{}}
	if err := transport.ParseSSE(body, func(ev transport.SSEEvent) bool {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &event); err != nil {
			ctx.decodeErr = fmt.Errorf("decode responses event chunk: %w", err)
			return false
		}
		switch event.Type {
		case "response.output_text.delta":
			handleResponsesTextDelta(ctx, ev.Data)
		case "response.output_item.added":
			handleResponsesOutputItemAdded(ctx, ev.Data)
		case "response.function_call_arguments.delta":
			handleResponsesFuncArgsDelta(ctx, ev.Data)
		case "response.function_call_arguments.done":
			handleResponsesFuncArgsDone(ctx, ev.Data)
		case "response.output_item.done":
			handleResponsesOutputItemDone(ctx, ev.Data)
		case "response.completed":
			handleResponsesCompleted(ctx, ev.Data)
		}
		return true
	}); err != nil {
		stream.CloseWithError(fmt.Errorf("sse stream read failed: %w", err))
		return
	}
	if ctx.decodeErr != nil {
		stream.CloseWithError(fmt.Errorf("responses chunk decode failed: %w", ctx.decodeErr))
		return
	}
	if !ctx.ended {
		// Stream terminated without a completed event: flush any pending tool
		// calls that never saw output_item.done, then end with buffered text.
		flushPendingToolCalls(ctx)
		var blocks []schema.ContentBlock
		if ctx.contentBuf != "" {
			blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockText, Text: ctx.contentBuf})
		}
		blocks = append(blocks, toolCallBlocks(ctx)...)
		stream.End(&schema.AssistantMessage{Content: blocks, StopReason: schema.StopReasonEndTurn})
	}
}

// emitToolCall pushes the completed tool-call event exactly once, preferring
// the call_id (the identifier tool results reference) over the item id.
func emitToolCall(ctx *responsesEventContext, tc *responsesToolCall) {
	if tc.emitted {
		return
	}
	tc.emitted = true
	if !ctx.started {
		ctx.started = true
		ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventStart, Partial: &schema.AssistantMessage{}})
	}
	id := tc.callID
	if id == "" {
		id = tc.id
	}
	ctx.stream.Push(schema.AssistantMessageEvent{
		Type: schema.EventToolCallEnd,
		ToolCall: &schema.ContentBlock{
			Type:          schema.ContentBlockToolCall,
			ToolCallID:    id,
			ToolName:      tc.name,
			ToolArguments: tc.args,
		},
	})
}

// flushPendingToolCalls emits any tool calls still buffered when the stream
// ends (defensive: a well-formed stream finalizes each via output_item.done).
func flushPendingToolCalls(ctx *responsesEventContext) {
	// Deterministic order by output_index.
	indices := make([]int, 0, len(ctx.toolCalls))
	for idx := range ctx.toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		emitToolCall(ctx, ctx.toolCalls[idx])
	}
}

func handleResponsesTextDelta(ctx *responsesEventContext, chunk string) {
	var delta struct {
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(chunk), &delta); err != nil {
		ctx.decodeErr = fmt.Errorf("decode output_text.delta chunk: %w", err)
		return
	}
	if !ctx.started {
		ctx.started = true
		ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventStart, Partial: &schema.AssistantMessage{}})
	}
	ctx.outputText += delta.Delta
	ctx.contentBuf += delta.Delta
	ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventTextDelta, Delta: delta.Delta})
}

func handleResponsesOutputItemAdded(ctx *responsesEventContext, chunk string) {
	var ev struct {
		OutputIndex int `json:"output_index"`
		Item        struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments string          `json:"arguments"`
			Input     json.RawMessage `json:"input"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(chunk), &ev); err != nil {
		ctx.decodeErr = fmt.Errorf("decode output_item.added chunk: %w", err)
		return
	}
	if ev.Item.Type != "function_call" {
		return
	}
	// Register the in-flight call; arguments arrive via the delta/done events.
	// Some backends send the full arguments inline (no streaming); seed the
	// buffer from whichever field is populated so output_item.done can emit.
	tc := &responsesToolCall{
		id:     ev.Item.ID,
		callID: ev.Item.CallID,
		name:   ev.Item.Name,
		args:   ev.Item.Arguments,
	}
	if tc.args == "" && len(ev.Item.Input) > 0 {
		tc.args = string(ev.Item.Input)
	}
	ctx.toolCalls[ev.OutputIndex] = tc
}

// handleResponsesFuncArgsDelta accumulates a streamed arguments fragment onto
// the in-flight function_call for this output_index.
func handleResponsesFuncArgsDelta(ctx *responsesEventContext, chunk string) {
	var ev struct {
		OutputIndex int    `json:"output_index"`
		Delta       string `json:"delta"`
	}
	if err := json.Unmarshal([]byte(chunk), &ev); err != nil {
		ctx.decodeErr = fmt.Errorf("decode function_call_arguments.delta chunk: %w", err)
		return
	}
	if tc := ctx.toolCalls[ev.OutputIndex]; tc != nil {
		tc.args += ev.Delta
	}
}

// handleResponsesFuncArgsDone sets the final arguments for the in-flight
// function_call (the done event carries the complete arguments JSON).
func handleResponsesFuncArgsDone(ctx *responsesEventContext, chunk string) {
	var ev struct {
		OutputIndex int    `json:"output_index"`
		Arguments   string `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(chunk), &ev); err != nil {
		ctx.decodeErr = fmt.Errorf("decode function_call_arguments.done chunk: %w", err)
		return
	}
	if tc := ctx.toolCalls[ev.OutputIndex]; tc != nil && ev.Arguments != "" {
		tc.args = ev.Arguments
	}
}

// handleResponsesOutputItemDone finalizes a function_call output item: the
// item carries the complete arguments, so emit the tool call now that its
// arguments are fully known.
func handleResponsesOutputItemDone(ctx *responsesEventContext, chunk string) {
	var ev struct {
		OutputIndex int `json:"output_index"`
		Item        struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(chunk), &ev); err != nil {
		ctx.decodeErr = fmt.Errorf("decode output_item.done chunk: %w", err)
		return
	}
	if ev.Item.Type != "function_call" {
		return
	}
	tc := ctx.toolCalls[ev.OutputIndex]
	if tc == nil {
		// No added/delta seen (backend sent a single done event): build it now.
		tc = &responsesToolCall{}
		ctx.toolCalls[ev.OutputIndex] = tc
	}
	// Item fields are authoritative on done; fill any gaps.
	if tc.id == "" {
		tc.id = ev.Item.ID
	}
	if tc.callID == "" {
		tc.callID = ev.Item.CallID
	}
	if tc.name == "" {
		tc.name = ev.Item.Name
	}
	if ev.Item.Arguments != "" {
		tc.args = ev.Item.Arguments
	}
	emitToolCall(ctx, tc)
}

func handleResponsesCompleted(ctx *responsesEventContext, chunk string) {
	var resp struct {
		Response struct {
			Status string `json:"status"`
			Usage  struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				InputTokensDetails struct {
					CachedTokens     int `json:"cached_tokens"`
					CacheWriteTokens int `json:"cache_write_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(chunk), &resp); err != nil {
		ctx.decodeErr = fmt.Errorf("decode response.completed chunk: %w", err)
		return
	}
	var blocks []schema.ContentBlock
	if ctx.contentBuf != "" {
		blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockText, Text: ctx.contentBuf})
	}
	// Tool calls are part of the assistant turn: include them in the final
	// message content (matching the chat-completions parser contract) so the
	// agent loop sees them from result.Content, not only from stream events.
	blocks = append(blocks, toolCallBlocks(ctx)...)
	stopReason := schema.StopReasonEndTurn
	if resp.Response.Status == "incomplete" {
		stopReason = schema.StopReasonMaxTokens
	}
	usage := resp.Response.Usage
	cachedTokens := usage.InputTokensDetails.CachedTokens
	cacheWriteTokens := usage.InputTokensDetails.CacheWriteTokens
	// Responses input_tokens is gross; expose net input separately from cache
	// reads/writes so context accounting and pricing do not double-count them.
	netInputTokens := usage.InputTokens - cachedTokens - cacheWriteTokens
	if netInputTokens < 0 {
		netInputTokens = 0
	}
	ctx.stream.End(&schema.AssistantMessage{
		Content:    blocks,
		StopReason: stopReason,
		Usage: &schema.Usage{
			InputTokens:         netInputTokens,
			OutputTokens:        usage.OutputTokens,
			CacheReadTokens:     cachedTokens,
			CacheCreationTokens: cacheWriteTokens,
		},
	})
	ctx.ended = true
}

// toolCallBlocks materializes the accumulated in-flight function_call items as
// content blocks, ordered by output_index for determinism.
func toolCallBlocks(ctx *responsesEventContext) []schema.ContentBlock {
	if len(ctx.toolCalls) == 0 {
		return nil
	}
	indices := make([]int, 0, len(ctx.toolCalls))
	for idx := range ctx.toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	blocks := make([]schema.ContentBlock, 0, len(indices))
	for _, idx := range indices {
		tc := ctx.toolCalls[idx]
		id := tc.callID
		if id == "" {
			id = tc.id
		}
		blocks = append(blocks, schema.ContentBlock{
			Type:          schema.ContentBlockToolCall,
			ToolCallID:    id,
			ToolName:      tc.name,
			ToolArguments: tc.args,
		})
	}
	return blocks
}
