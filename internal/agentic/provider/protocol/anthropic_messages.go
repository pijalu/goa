// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

func init() {
	Register(&anthropicMessages{})
}

type anthropicMessages struct{}

func (p *anthropicMessages) API() schema.Api {
	return schema.ApiAnthropicMessages
}

func (p *anthropicMessages) RequestHeaders(model schema.Model, profile schema.VariantProfile) map[string]string {
	headers := make(map[string]string)
	if profile.Auth.Method == schema.AuthMethodOAuth {
		for _, rule := range profile.Auth.OAuthIdentity {
			value := rule.Value
			if rule.EnvVar != "" {
				value = ""
			}
			if value != "" {
				headers[rule.Name] = value
			}
		}
	}
	betas := anthropicBetaHeaders(profile)
	if len(betas) > 0 {
		headers["anthropic-beta"] = strings.Join(betas, ",")
	}
	if profile.CachePolicy.AffinityHeader != "" && model.VariantID != "" {
		headers[profile.CachePolicy.AffinityHeader] = model.VariantID
	}
	return headers
}

func anthropicBetaHeaders(profile schema.VariantProfile) []string {
	var betas []string
	for name, enabled := range map[string]bool{
		"eager-tool-streaming-2025-05-14":        profile.ToolCompat.SupportsParallelToolCalls,
		"fine-grained-tool-streaming-2025-05-14": profile.ToolCompat.SupportsParallelToolCalls,
		"interleaved-thinking-2025-05-14":        profile.Compat.ThinkingFormat != "",
		"claude-code-20250219":                   profile.Auth.Method == schema.AuthMethodOAuth,
		"oauth-2025-04-20":                       profile.Auth.Method == schema.AuthMethodOAuth,
	} {
		if enabled {
			betas = append(betas, name)
		}
	}
	return betas
}

func (p *anthropicMessages) BuildRequest(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile) ([]byte, error) {
	compat := resolveAnthropicCompat(profile)
	messages := applyAnthropicCacheControl(convertAnthropicMessages(ctx.Messages, compat), profile.CachePolicy)
	body := map[string]any{
		"model":      model.ID,
		"stream":     true,
		"messages":   messages,
		"max_tokens": firstNonZero(opts.MaxTokens, 4096),
	}
	if ctx.SystemPrompt != "" {
		body["system"] = []map[string]any{{"type": "text", "text": ctx.SystemPrompt}}
	}
	if len(ctx.Tools) > 0 {
		body["tools"] = convertAnthropicTools(ctx.Tools)
	}
	if opts.Temperature != nil && compat.SupportsTemperature {
		body["temperature"] = *opts.Temperature
	}
	if model.Reasoning {
		applyAnthropicThinking(body, profile, compat)
	}
	return json.Marshal(body)
}

func (p *anthropicMessages) ParseResponse(reader io.Reader, stream *schema.AssistantMessageEventStream) error {
	parseAnthropicSSE(reader, stream)
	return nil
}

type anthropicCompat struct {
	SupportsEagerToolInputStreaming bool
	SupportsLongCacheRetention      bool
	SendSessionAffinityHeaders      bool
	SupportsCacheControlOnTools     bool
	SupportsTemperature             bool
	RequiresAdaptiveThinking        bool
	SupportsThinkingOnTools         bool
	ThinkingBudgetMultiplier        float64
}

func resolveAnthropicCompat(profile schema.VariantProfile) anthropicCompat {
	c := anthropicCompat{
		SupportsEagerToolInputStreaming: true,
		SupportsTemperature:             true,
	}
	if profile.Compat.ThinkingFormat != "" {
		c.SupportsThinkingOnTools = true
	}
	return c
}

func convertAnthropicMessages(messages []schema.Message, compat anthropicCompat) []map[string]any {
	var out []map[string]any
	for _, msg := range messages {
		switch msg.Role {
		case schema.RoleUser:
			out = append(out, map[string]any{"role": "user", "content": convertAnthropicContentBlocks(msg.Content)})
		case schema.RoleAssistant:
			out = append(out, map[string]any{"role": "assistant", "content": convertAnthropicAssistantBlocks(msg.Content)})
		case schema.RoleToolResult:
			for _, block := range msg.Content {
				if block.Type == schema.ContentBlockToolResult {
					out = append(out, map[string]any{
						"role": "user",
						"content": []map[string]any{{
							"type":        "tool_result",
							"tool_use_id": block.ToolCallID,
							"content":     block.Text,
							"is_error":    block.IsError,
						}},
					})
				}
			}
		}
	}
	return out
}

func convertAnthropicContentBlocks(blocks []schema.ContentBlock) []map[string]any {
	result := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case schema.ContentBlockText:
			result = append(result, map[string]any{"type": "text", "text": b.Text})
		case schema.ContentBlockImage:
			result = append(result, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": b.ImageMimeType,
					"data":       b.ImageData,
				},
			})
		}
	}
	if len(result) == 0 {
		result = append(result, map[string]any{"type": "text", "text": ""})
	}
	return result
}

func convertAnthropicAssistantBlocks(blocks []schema.ContentBlock) []map[string]any {
	result := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case schema.ContentBlockText:
			result = append(result, map[string]any{"type": "text", "text": b.Text})
		case schema.ContentBlockThinking:
			r := map[string]any{"type": "thinking"}
			if b.Thinking != "" {
				r["thinking"] = b.Thinking
			}
			if b.ThinkingSignature != "" {
				r["signature"] = b.ThinkingSignature
			}
			result = append(result, r)
		case schema.ContentBlockToolCall:
			result = append(result, map[string]any{
				"type":  "tool_use",
				"id":    b.ToolCallID,
				"name":  b.ToolName,
				"input": json.RawMessage(b.ToolArguments),
			})
		}
	}
	return result
}

func convertAnthropicTools(tools []schema.ToolSchema) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		}
	}
	return out
}

func applyAnthropicThinking(body map[string]any, profile schema.VariantProfile, compat anthropicCompat) {
	budget := resolveThinkingBudget(profile)
	if budget > 0 {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	} else {
		body["thinking"] = map[string]any{"type": "adaptive"}
	}
}

func applyAnthropicCacheControl(messages []map[string]any, policy schema.CachePolicy) []map[string]any {
	cap := policy.BreakpointCap
	if cap <= 0 {
		return messages
	}
	placed := 0
	ttl := policy.TTL
	if ttl == "" {
		ttl = "1h"
	}
	for i := len(messages) - 1; i >= 0 && placed < cap; i-- {
		role, _ := messages[i]["role"].(string)
		if role == "user" {
			messages[i]["content"] = withAnthropicCacheControl(messages[i]["content"], ttl)
			placed++
		}
	}
	return messages
}

func withAnthropicCacheControl(content any, ttl string) any {
	blocks, ok := content.([]map[string]any)
	if !ok || len(blocks) == 0 {
		return content
	}
	last := blocks[len(blocks)-1]
	last["cache_control"] = map[string]any{"type": "ephemeral", "ttl": ttl}
	return blocks
}

// ---------------------------------------------------------------------------
// Response parsing
// ---------------------------------------------------------------------------

type anthropicEventContext struct {
	currentBlock  anthropicBuilder
	contentBlocks []schema.ContentBlock
	stream        *schema.AssistantMessageEventStream
}

type anthropicBuilder struct {
	index     int
	blockType string
	text      string
	thinking  string
	signature string
	input     string
	name      string
	id        string
}

func (b *anthropicBuilder) toContentBlock() *schema.ContentBlock {
	switch b.blockType {
	case "text":
		return &schema.ContentBlock{Type: schema.ContentBlockText, Text: b.text}
	case "thinking":
		return &schema.ContentBlock{Type: schema.ContentBlockThinking, Thinking: b.thinking, ThinkingSignature: b.signature}
	case "tool_use":
		if b.id == "" {
			b.id = fmt.Sprintf("toolu_%d", b.index)
		}
		return &schema.ContentBlock{Type: schema.ContentBlockToolCall, ToolCallID: b.id, ToolName: b.name, ToolArguments: b.input}
	}
	return nil
}

func (ctx *anthropicEventContext) emitBlock() {
	if ctx.currentBlock.blockType != "" {
		if block := ctx.currentBlock.toContentBlock(); block != nil {
			ctx.contentBlocks = append(ctx.contentBlocks, *block)
		}
		ctx.currentBlock = anthropicBuilder{}
	}
}

var anthropicEventHandlers = map[string]func(ctx *anthropicEventContext, data string) error{
	"message_start":       anthropicHandleMessageStart,
	"content_block_start": anthropicHandleContentBlockStart,
	"content_block_delta": anthropicHandleContentBlockDelta,
	"content_block_stop":  anthropicHandleContentBlockStop,
	"message_delta":       anthropicHandleMessageDelta,
	"error":               anthropicHandleError,
}

func anthropicHandleMessageStart(ctx *anthropicEventContext, data string) error {
	ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventStart, Partial: &schema.AssistantMessage{}})
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
		ctx.stream.Push(schema.AssistantMessageEvent{
			Type:         schema.EventToolCallStart,
			ContentIndex: parsed.Index,
			Partial: &schema.AssistantMessage{
				Content: []schema.ContentBlock{{Type: schema.ContentBlockToolCall, ToolCallID: blockType.ID, ToolName: blockType.Name}},
			},
		})
	}
	return nil
}

func anthropicHandleContentBlockDelta(ctx *anthropicEventContext, data string) error {
	var parsed struct {
		Index int             `json:"index"`
		Delta json.RawMessage `json:"delta"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return fmt.Errorf("decode content_block_delta chunk: %w", err)
	}
	ctx.currentBlock.index = parsed.Index
	var deltaType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(parsed.Delta, &deltaType); err != nil {
		return fmt.Errorf("decode content_block_delta type: %w", err)
	}
	switch deltaType.Type {
	case "text_delta":
		var d struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(parsed.Delta, &d); err != nil {
			return err
		}
		ctx.currentBlock.text += d.Text
		ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventTextDelta, ContentIndex: parsed.Index, Delta: d.Text})
	case "thinking_delta":
		var d struct {
			Thinking string `json:"thinking"`
		}
		if err := json.Unmarshal(parsed.Delta, &d); err != nil {
			return err
		}
		ctx.currentBlock.thinking += d.Thinking
		ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventThinkingDelta, ContentIndex: parsed.Index, Delta: d.Thinking})
	case "signature_delta":
		var d struct {
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(parsed.Delta, &d); err != nil {
			return err
		}
		ctx.currentBlock.signature = d.Signature
	case "input_json_delta":
		var d struct {
			PartialJSON string `json:"partial_json"`
		}
		if err := json.Unmarshal(parsed.Delta, &d); err != nil {
			return err
		}
		ctx.currentBlock.input += d.PartialJSON
		ctx.stream.Push(schema.AssistantMessageEvent{Type: schema.EventToolCallDelta, ContentIndex: parsed.Index, Delta: d.PartialJSON})
	}
	return nil
}

func anthropicHandleContentBlockStop(ctx *anthropicEventContext, data string) error {
	ctx.emitBlock()
	return nil
}

func anthropicHandleMessageDelta(ctx *anthropicEventContext, data string) error {
	var parsed struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return fmt.Errorf("decode message_delta chunk: %w", err)
	}
	ctx.emitBlock()
	ctx.stream.End(&schema.AssistantMessage{
		Content:    ctx.contentBlocks,
		StopReason: mapAnthropicStopReason(parsed.Delta.StopReason),
		Usage:      &schema.Usage{InputTokens: parsed.Usage.InputTokens, OutputTokens: parsed.Usage.OutputTokens},
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

func parseAnthropicSSE(body io.Reader, stream *schema.AssistantMessageEventStream) {
	defer closeIfCloser(body)
	ctx := &anthropicEventContext{stream: stream}
	err := parseAnthropicEventStream(body, func(eventType, data string) error {
		if handler, ok := anthropicEventHandlers[eventType]; ok {
			return handler(ctx, data)
		}
		return nil
	})
	if err != nil {
		stream.CloseWithError(fmt.Errorf("anthropic SSE error: %w", err))
	}
}

func parseAnthropicEventStream(r io.Reader, handler func(eventType, data string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var event string
	var data strings.Builder
	var flushErr error
	flush := func() {
		if flushErr != nil || event == "" || data.Len() == 0 {
			return
		}
		flushErr = handler(event, data.String())
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			event = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			flush()
			event = strings.TrimPrefix(line, "event: ")
			data.Reset()
		} else if strings.HasPrefix(line, "data: ") {
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	flush()
	if flushErr != nil {
		return flushErr
	}
	return scanner.Err()
}

func mapAnthropicStopReason(reason string) schema.StopReason {
	switch reason {
	case "max_tokens":
		return schema.StopReasonMaxTokens
	case "stop_sequence":
		return schema.StopReasonStopSequence
	case "tool_use":
		return schema.StopReasonToolCall
	default:
		return schema.StopReasonEndTurn
	}
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func closeIfCloser(r io.Reader) {
	if c, ok := r.(io.Closer); ok {
		_ = c.Close()
	}
}
