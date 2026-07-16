// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func streamAnthropic(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1/messages"
	}

	compat := provider.ResolveAnthropicCompat(model)
	body := buildParams(model, ctx, opts, compat)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", opts.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	if provider.ToBool(compat.SupportsEagerToolInputStreaming, true) {
		req.Header.Set("anthropic-beta", "eager-tool-streaming-2025-05-14")
	} else {
		req.Header.Set("anthropic-beta", "fine-grained-tool-streaming-2025-05-14")
	}

	if opts.SessionID != "" && provider.ToBool(compat.SendSessionAffinityHeaders, false) {
		req.Header.Set("x-session-affinity", opts.SessionID)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseAnthropicSSE(resp.Body, stream)
	return stream, nil
}

func buildParams(model provider.Model, ctx provider.Context, opts provider.StreamOptions, compat provider.AnthropicMessagesCompat) map[string]interface{} {
	body := map[string]interface{}{
		"model":      model.ID,
		"messages":   convertMessages(model, ctx.Messages, compat),
		"max_tokens": maxTokens(opts),
		"stream":     true,
	}

	if ctx.SystemPrompt != "" {
		body["system"] = []map[string]interface{}{
			{"type": "text", "text": ctx.SystemPrompt},
		}
	}
	if len(ctx.Tools) > 0 {
		body["tools"] = convertTools(ctx.Tools)
	}
	if opts.Temperature != nil && provider.ToBool(compat.SupportsTemperature, true) {
		body["temperature"] = *opts.Temperature
	}

	return body
}

func maxTokens(opts provider.StreamOptions) int {
	if opts.MaxTokens > 0 {
		return opts.MaxTokens
	}
	return 4096
}

func convertMessages(model provider.Model, messages []provider.Message, compat provider.AnthropicMessagesCompat) []map[string]interface{} {
	var out []map[string]interface{}
	for _, msg := range messages {
		switch msg.Role {
		case provider.RoleUser:
			out = append(out, map[string]interface{}{
				"role":    "user",
				"content": convertContentBlocks(msg.Content),
			})
		case provider.RoleAssistant:
			out = append(out, map[string]interface{}{
				"role":    "assistant",
				"content": convertAssistantBlocks(msg.Content),
			})
		case provider.RoleToolResult:
			for _, block := range msg.Content {
				if block.Type == provider.ContentBlockToolResult {
					out = append(out, map[string]interface{}{
						"role": "user",
						"content": []map[string]interface{}{
							{
								"type":        "tool_result",
								"tool_use_id": block.ToolCallID,
								"content":     block.Text,
								"is_error":    block.IsError,
							},
						},
					})
				}
			}
		}
	}
	return out
}

func convertContentBlocks(blocks []provider.ContentBlock) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case provider.ContentBlockText:
			result = append(result, map[string]interface{}{"type": "text", "text": b.Text})
		case provider.ContentBlockImage:
			result = append(result, map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type":       "base64",
					"media_type": b.ImageMimeType,
					"data":       b.ImageData,
				},
			})
		}
	}
	if len(result) == 0 {
		result = append(result, map[string]interface{}{"type": "text", "text": ""})
	}
	return result
}

func convertAssistantBlocks(blocks []provider.ContentBlock) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case provider.ContentBlockText:
			result = append(result, map[string]interface{}{"type": "text", "text": b.Text})
		case provider.ContentBlockThinking:
			r := map[string]interface{}{"type": "thinking"}
			if b.Thinking != "" {
				r["thinking"] = b.Thinking
			}
			if b.ThinkingSignature != "" {
				r["signature"] = b.ThinkingSignature
			}
			result = append(result, r)
		case provider.ContentBlockToolCall:
			result = append(result, map[string]interface{}{
				"type":  "tool_use",
				"id":    b.ToolCallID,
				"name":  b.ToolName,
				"input": json.RawMessage(b.ToolArguments),
			})
		}
	}
	return result
}

func convertTools(tools []provider.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
	}
	return out
}

// anthropicEventContext carries shared state across Anthropic SSE event handlers.
type anthropicEventContext struct {
	currentBlock  builder
	contentBlocks []provider.ContentBlock
	stream        *provider.AssistantMessageEventStream
	// usage accumulates token accounting across the stream: message_start
	// carries the cumulative input + cache creation/read counts, message_delta
	// carries only the output count. Merged into the final Usage.
	usage provider.Usage
}

func (ctx *anthropicEventContext) emitBlock() {
	if ctx.currentBlock.blockType != "" {
		if block := ctx.currentBlock.toContentBlock(); block != nil {
			ctx.contentBlocks = append(ctx.contentBlocks, *block)
		}
		ctx.currentBlock = builder{}
	}
}

// anthropicEventHandlers maps Anthropic event types to handler methods.
// Using a registry avoids a single large switch (cognitive complexity budget).
var anthropicEventHandlers = map[string]func(ctx *anthropicEventContext, data string) error{
	"message_start":       anthropicHandleMessageStart,
	"content_block_start": anthropicHandleContentBlockStart,
	"content_block_delta": anthropicHandleContentBlockDelta,
	"content_block_stop":  anthropicHandleContentBlockStop,
	"message_delta":       anthropicHandleMessageDelta,
	"error":               anthropicHandleError,
}

func anthropicHandleMessageStart(ctx *anthropicEventContext, data string) error {
	var parsed struct {
		Message struct {
			Usage struct {
				InputTokens       int `json:"input_tokens"`
				CacheCreateTokens int `json:"cache_creation_input_tokens"`
				CacheReadTokens   int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err == nil {
		ctx.usage.InputTokens = parsed.Message.Usage.InputTokens
		ctx.usage.CacheCreationTokens = parsed.Message.Usage.CacheCreateTokens
		ctx.usage.CacheReadTokens = parsed.Message.Usage.CacheReadTokens
	}
	ctx.stream.Push(provider.AssistantMessageEvent{
		Type:    provider.EventStart,
		Partial: &provider.AssistantMessage{},
	})
	return nil
}

func anthropicHandleContentBlockStart(ctx *anthropicEventContext, data string) error {
	ctx.emitBlock()
	var parsed struct {
		Index int             `json:"index"`
		Block json.RawMessage `json:"content_block"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return fmt.Errorf("decode content_block_start chunk: %w", err)
	}
	ctx.currentBlock.index = parsed.Index
	var blockType struct {
		Type string `json:"type"`
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
	}
	if err := json.Unmarshal(parsed.Block, &blockType); err != nil {
		return fmt.Errorf("decode content_block_start block: %w", err)
	}
	ctx.currentBlock.blockType = blockType.Type
	ctx.currentBlock.id = blockType.ID
	ctx.currentBlock.name = blockType.Name

	if blockType.Type == "tool_use" {
		ctx.stream.Push(provider.AssistantMessageEvent{
			Type:         provider.EventToolCallStart,
			ContentIndex: parsed.Index,
			Partial: &provider.AssistantMessage{
				Content: []provider.ContentBlock{
					{
						Type:       provider.ContentBlockToolCall,
						ToolCallID: blockType.ID,
						ToolName:   blockType.Name,
					},
				},
			},
		})
	}
	return nil
}

func anthropicHandleContentBlockDelta(ctx *anthropicEventContext, data string) error {
	return handleContentBlockDelta(&ctx.currentBlock, ctx.stream, data)
}

func anthropicHandleContentBlockStop(ctx *anthropicEventContext, data string) error {
	ctx.emitBlock()
	return nil
}

func anthropicHandleMessageDelta(ctx *anthropicEventContext, data string) error {
	var parsed struct {
		Delta struct {
			StopReason   string `json:"stop_reason"`
			StopSequence string `json:"stop_sequence"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return fmt.Errorf("decode message_delta chunk: %w", err)
	}
	ctx.usage.OutputTokens = parsed.Usage.OutputTokens
	finalUsage := ctx.usage
	ctx.stream.End(&provider.AssistantMessage{
		Content:    ctx.contentBlocks,
		StopReason: mapStopReason(parsed.Delta.StopReason),
		Usage:      &finalUsage,
	})
	return nil
}

func anthropicHandleError(ctx *anthropicEventContext, data string) error {
	var errData struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &errData); err != nil {
		return fmt.Errorf("decode error chunk: %w", err)
	}
	ctx.stream.CloseWithError(fmt.Errorf("anthropic error: %s — %s", errData.Error.Type, errData.Error.Message))
	return nil
}

// parseAnthropicSSE reads Anthropic's event-type SSE format.
func parseAnthropicSSE(body io.ReadCloser, stream *provider.AssistantMessageEventStream) {
	defer body.Close()

	ctx := &anthropicEventContext{stream: stream}

	err := parseAnthropicEventStream(body, func(eventType, data string) error {
		if handler, ok := anthropicEventHandlers[eventType]; ok {
			return handler(ctx, data)
		}
		// "ping" has no handler — intentional no-op
		return nil
	})

	if err != nil {
		stream.CloseWithError(fmt.Errorf("anthropic SSE error: %w", err))
	}
}

type builder struct {
	index     int
	blockType string
	text      string
	thinking  string
	signature string
	input     string
	name      string
	id        string
}

func (b *builder) toContentBlock() *provider.ContentBlock {
	switch b.blockType {
	case "text":
		return &provider.ContentBlock{Type: provider.ContentBlockText, Text: b.text}
	case "thinking":
		return &provider.ContentBlock{
			Type:              provider.ContentBlockThinking,
			Thinking:          b.thinking,
			ThinkingSignature: b.signature,
		}
	case "tool_use":
		if b.id == "" {
			b.id = fmt.Sprintf("toolu_%d", b.index)
		}
		return &provider.ContentBlock{
			Type:          provider.ContentBlockToolCall,
			ToolCallID:    b.id,
			ToolName:      b.name,
			ToolArguments: b.input,
		}
	case "tool_result":
		return &provider.ContentBlock{
			Type:       provider.ContentBlockToolResult,
			ToolCallID: b.id,
			Text:       b.text,
		}
	}
	return nil
}

func handleContentBlockDelta(currentBlock *builder, stream *provider.AssistantMessageEventStream, data string) error {
	var parsed struct {
		Index int             `json:"index"`
		Delta json.RawMessage `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return fmt.Errorf("decode content_block_delta chunk: %w", err)
	}
	currentBlock.index = parsed.Index

	var deltaType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(parsed.Delta, &deltaType); err != nil {
		return fmt.Errorf("decode content_block_delta type: %w", err)
	}

	switch deltaType.Type {
	case "text_delta":
		return handleTextDelta(currentBlock, stream, parsed.Index, parsed.Delta)
	case "thinking_delta":
		return handleThinkingDelta(currentBlock, stream, parsed.Index, parsed.Delta)
	case "signature_delta":
		return handleSignatureDelta(currentBlock, parsed.Delta)
	case "input_json_delta":
		return handleInputJSONDelta(currentBlock, stream, parsed.Index, parsed.Delta)
	}
	return nil
}

func handleTextDelta(currentBlock *builder, stream *provider.AssistantMessageEventStream, index int, delta json.RawMessage) error {
	var d struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(delta, &d); err != nil {
		return fmt.Errorf("decode text_delta: %w", err)
	}
	currentBlock.text += d.Text
	stream.Push(provider.AssistantMessageEvent{
		Type:         provider.EventTextDelta,
		ContentIndex: index,
		Delta:        d.Text,
	})
	return nil
}

func handleThinkingDelta(currentBlock *builder, stream *provider.AssistantMessageEventStream, index int, delta json.RawMessage) error {
	var d struct {
		Thinking string `json:"thinking"`
	}
	if err := json.Unmarshal(delta, &d); err != nil {
		return fmt.Errorf("decode thinking_delta: %w", err)
	}
	currentBlock.thinking += d.Thinking
	stream.Push(provider.AssistantMessageEvent{
		Type:         provider.EventThinkingDelta,
		ContentIndex: index,
		Delta:        d.Thinking,
	})
	return nil
}

func handleSignatureDelta(currentBlock *builder, delta json.RawMessage) error {
	var d struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(delta, &d); err != nil {
		return fmt.Errorf("decode signature_delta: %w", err)
	}
	currentBlock.signature = d.Signature
	return nil
}

func handleInputJSONDelta(currentBlock *builder, stream *provider.AssistantMessageEventStream, index int, delta json.RawMessage) error {
	var d struct {
		PartialJSON string `json:"partial_json"`
	}
	if err := json.Unmarshal(delta, &d); err != nil {
		return fmt.Errorf("decode input_json_delta: %w", err)
	}
	currentBlock.input += d.PartialJSON
	stream.Push(provider.AssistantMessageEvent{
		Type:         provider.EventToolCallDelta,
		ContentIndex: index,
		Delta:        d.PartialJSON,
	})
	return nil
}

func mapStopReason(reason string) provider.StopReason {
	switch reason {
	case "end_turn":
		return provider.StopReasonEndTurn
	case "max_tokens":
		return provider.StopReasonMaxTokens
	case "stop_sequence":
		return provider.StopReasonStopSequence
	case "tool_use":
		return provider.StopReasonToolCall
	default:
		return provider.StopReasonEndTurn
	}
}
