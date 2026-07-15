// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"fmt"
	"io"
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
	body := map[string]any{
		"model":  model.ID,
		"input":  convertResponsesInput(ctx.Messages, ctx.SystemPrompt, profile),
		"stream": true,
		"tools":  convertResponsesTools(ctx.Tools),
	}
	if opts.MaxTokens > 0 {
		body["max_output_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if opts.TopP != nil {
		body["top_p"] = *opts.TopP
	}
	if opts.SessionID != "" {
		body["previous_response_id"] = opts.SessionID
		if shouldSendOpenAIResponsesPromptCacheKey(model, opts) {
			body["prompt_cache_key"] = ClampOpenAIPromptCacheKey(opts.SessionID)
			if opts.CacheRetention == schema.CacheRetentionLong {
				body["prompt_cache_retention"] = "24h"
			}
		}
	}
	if model.Reasoning || profile.Compat.ThinkingFormat != "" {
		body["include"] = []string{"reasoning.encrypted_content"}
		body["text"] = map[string]any{"verbosity": "low"}
		body["reasoning"] = map[string]any{"summary": "auto"}
	}
	store := profile.Compat.SupportsStore
	if store != nil {
		body["store"] = *store
	}
	if opts.ServiceTier != "" {
		body["service_tier"] = opts.ServiceTier
	}
	return json.Marshal(body)
}

// shouldSendOpenAIResponsesPromptCacheKey mirrors Pi's behavior: Azure and Codex
// Responses send prompt_cache_key whenever a session ID is present, while plain
// OpenAI Responses only send it when prompt caching is not explicitly disabled.
func shouldSendOpenAIResponsesPromptCacheKey(model schema.Model, opts schema.StreamOptions) bool {
	if opts.SessionID == "" {
		return false
	}
	if model.Api == schema.ApiAzureOpenAIResponses || model.Api == schema.ApiOpenAICodexResponses {
		return true
	}
	return opts.CacheRetention != schema.CacheRetentionNone
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
			input = append(input, map[string]any{
				"role":    "assistant",
				"content": extractResponsesText(msg.Content),
			})
		case schema.RoleToolResult:
			tcID, tcName, text := extractToolCallInfo(msg.Content)
			input = append(input, map[string]any{
				"role":         "tool",
				"tool_call_id": normalizeResponsesToolCallID(tcID),
				"content":      text,
				"name":         tcName,
			})
		}
	}
	return input
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

type responsesEventContext struct {
	contentBuf string
	outputText string
	started    bool
	ended      bool
	decodeErr  error
	stream     *schema.AssistantMessageEventStream
}

func parseResponsesSSE(body io.Reader, stream *schema.AssistantMessageEventStream) {
	defer closeIfCloser(body)
	ctx := &responsesEventContext{stream: stream}
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
		var blocks []schema.ContentBlock
		if ctx.contentBuf != "" {
			blocks = append(blocks, schema.ContentBlock{Type: schema.ContentBlockText, Text: ctx.contentBuf})
		}
		stream.End(&schema.AssistantMessage{Content: blocks, StopReason: schema.StopReasonEndTurn})
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
	var item struct {
		Item struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(chunk), &item); err != nil {
		ctx.decodeErr = fmt.Errorf("decode output_item.added chunk: %w", err)
		return
	}
	if item.Item.Type == "function_call" {
		if !ctx.started {
			ctx.started = true
			ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventStart, Partial: &schema.AssistantMessage{}})
		}
		ctx.stream.Push(schema.AssistantMessageEvent{
			Type: schema.EventToolCallEnd,
			ToolCall: &schema.ContentBlock{
				Type:          schema.ContentBlockToolCall,
				ToolCallID:    item.Item.ID,
				ToolName:      item.Item.Name,
				ToolArguments: string(item.Item.Input),
			},
		})
	}
}

func handleResponsesCompleted(ctx *responsesEventContext, chunk string) {
	var resp struct {
		Response struct {
			Status string `json:"status"`
			Usage  struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
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
	stopReason := schema.StopReasonEndTurn
	if resp.Response.Status == "incomplete" {
		stopReason = schema.StopReasonMaxTokens
	}
	ctx.stream.End(&schema.AssistantMessage{
		Content:    blocks,
		StopReason: stopReason,
		Usage: &schema.Usage{
			InputTokens:  resp.Response.Usage.InputTokens,
			OutputTokens: resp.Response.Usage.OutputTokens,
		},
	})
	ctx.ended = true
}
