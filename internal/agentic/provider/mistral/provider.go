// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mistral

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func init() { provider.RegisterApiProvider(&MistralProvider{}) }

type MistralProvider struct{}

func (p *MistralProvider) API() provider.Api { return provider.ApiMistralConversations }

func (p *MistralProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamMistral(model, ctx, opts)
}

func (p *MistralProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func streamMistral(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = "https://api.mistral.ai/v1/chat/completions"
	}

	body := buildMistralParams(model, ctx, opts)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", baseURL, bytes.NewReader(bodyBytes))
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

	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("mistral returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseMistralStream(resp.Body, stream, model, ctx, opts)
	return stream, nil
}

func buildMistralParams(model provider.Model, ctx provider.Context, opts provider.StreamOptions) map[string]interface{} {
	body := map[string]interface{}{
		"model":    model.ID,
		"messages": convertMistralMessages(ctx.Messages, ctx.SystemPrompt),
		"stream":   true,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if len(ctx.Tools) > 0 {
		body["tools"] = convertMistralTools(ctx.Tools)
	}
	return body
}

func convertMistralMessages(messages []provider.Message, systemPrompt string) []map[string]interface{} {
	var out []map[string]interface{}

	if systemPrompt != "" {
		out = append(out, map[string]interface{}{
			"role":    "system",
			"content": systemPrompt,
		})
	}

	for _, msg := range messages {
		switch msg.Role {
		case provider.RoleUser:
			out = append(out, map[string]interface{}{
				"role":    "user",
				"content": extractText(msg.Content),
			})
		case provider.RoleAssistant:
			out = append(out, convertMistralAssistant(msg))
		case provider.RoleToolResult:
			out = append(out, map[string]interface{}{
				"role":         "tool",
				"content":      extractText(msg.Content),
				"tool_call_id": extractToolCallID(msg.Content),
				"name":         extractToolName(msg.Content),
			})
		case provider.RoleSystem:
			out = append(out, map[string]interface{}{
				"role":    "system",
				"content": extractText(msg.Content),
			})
		}
	}
	return out
}

func convertMistralAssistant(msg provider.Message) map[string]interface{} {
	result := map[string]interface{}{
		"role": "assistant",
	}

	var textContent string
	var toolCalls []map[string]interface{}

	for _, block := range msg.Content {
		switch block.Type {
		case provider.ContentBlockText:
			textContent += block.Text
		case provider.ContentBlockToolCall:
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   block.ToolCallID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      block.ToolName,
					"arguments": block.ToolArguments,
				},
			})
		}
	}

	if len(toolCalls) > 0 {
		result["tool_calls"] = toolCalls
	}
	if textContent != "" {
		result["content"] = textContent
	} else {
		result["content"] = ""
	}

	return result
}

func convertMistralTools(tools []provider.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	return out
}

func extractText(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockText {
			return b.Text
		}
	}
	return ""
}

func extractToolCallID(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockToolResult {
			return b.ToolCallID
		}
	}
	return ""
}

func extractToolName(blocks []provider.ContentBlock) string {
	for _, b := range blocks {
		if b.Type == provider.ContentBlockToolResult {
			return b.ToolName
		}
	}
	return ""
}

func parseMistralStream(body io.ReadCloser, stream *provider.AssistantMessageEventStream, _ provider.Model, _ provider.Context, _ provider.StreamOptions) {
	defer body.Close()

	acc := &mistralStreamAcc{stream: stream}
	err := provider.ParseSSE(body, func(chunk string) {
		for _, m := range parseMistralChunk(chunk) {
			acc.dispatch(m)
		}
	})
	if err != nil {
		stream.CloseWithError(fmt.Errorf("mistral SSE error: %w", err))
	}
	if !acc.started {
		stream.End(&provider.AssistantMessage{})
	}
}

type mistralStreamAcc struct {
	stream     *provider.AssistantMessageEventStream
	toolAccums []mistralToolAccum
	contentBuf strings.Builder
	started    bool
}

func (a *mistralStreamAcc) dispatch(m mistralChunk) {
	if m.isTool {
		a.ensureStarted()
		a.accumulateTool(m)
	} else if m.isEnd {
		a.finish(m)
	} else if m.content != "" {
		a.ensureStarted()
		a.contentBuf.WriteString(m.content)
		a.stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: m.content})
	}
}

func (a *mistralStreamAcc) ensureStarted() {
	if a.started {
		return
	}
	a.started = true
	a.stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
}

func (a *mistralStreamAcc) accumulateTool(m mistralChunk) {
	for i := range a.toolAccums {
		if a.toolAccums[i].index == m.toolIndex {
			if m.toolName != "" {
				a.toolAccums[i].name = m.toolName
			}
			if m.toolArgs != "" {
				a.toolAccums[i].args += m.toolArgs
			}
			if m.toolID != "" {
				a.toolAccums[i].id = m.toolID
			}
			return
		}
	}
	a.toolAccums = append(a.toolAccums, mistralToolAccum{
		index: m.toolIndex, id: m.toolID, name: m.toolName, args: m.toolArgs,
	})
}

func (a *mistralStreamAcc) finish(m mistralChunk) {
	var blocks []provider.ContentBlock
	for _, ta := range a.toolAccums {
		blocks = append(blocks, provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: ta.id,
			ToolName: ta.name, ToolArguments: ta.args,
		})
	}
	if s := a.contentBuf.String(); s != "" {
		blocks = append([]provider.ContentBlock{{Type: provider.ContentBlockText, Text: s}}, blocks...)
	}
	a.stream.End(&provider.AssistantMessage{Content: blocks, StopReason: m.stopReason})
}

type mistralChunk struct {
	content    string
	toolID     string
	toolName   string
	toolArgs   string
	toolIndex  int
	isTool     bool
	isEnd      bool
	stopReason provider.StopReason
}

func parseMistralChunk(chunk string) []mistralChunk {
	choice := getChoice(chunk)
	if choice == nil {
		return nil
	}

	delta := getMap(choice, "delta")
	if delta == nil {
		if fr := getString(choice, "finish_reason"); fr != "" {
			return []mistralChunk{{isEnd: true, stopReason: mapMistralStopReason(fr)}}
		}
		return nil
	}

	var result []mistralChunk

	if c := getString(delta, "content"); c != "" {
		result = append(result, mistralChunk{content: c})
	}

	result = append(result, parseMistralToolCalls(delta)...)

	if fr := getString(choice, "finish_reason"); fr != "" {
		result = append(result, mistralChunk{isEnd: true, stopReason: mapMistralStopReason(fr)})
	}

	return result
}

func getChoice(chunk string) map[string]interface{} {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(chunk), &raw); err != nil {
		return nil
	}
	choices, _ := raw["choices"].([]interface{})
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]interface{})
	return choice
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	v, _ := m[key].(map[string]interface{})
	return v
}

func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func parseMistralToolCalls(delta map[string]interface{}) []mistralChunk {
	tcList, _ := delta["tool_calls"].([]interface{})
	if len(tcList) == 0 {
		return nil
	}
	var result []mistralChunk
	for _, t := range tcList {
		tcMap, _ := t.(map[string]interface{})
		if tcMap == nil {
			continue
		}
		fn := getMap(tcMap, "function")
		c := mistralChunk{isTool: true, toolID: getString(tcMap, "id")}
		if fn != nil {
			c.toolName = getString(fn, "name")
			c.toolArgs = getString(fn, "arguments")
		}
		if idx, ok := tcMap["index"].(float64); ok {
			c.toolIndex = int(idx)
		}
		result = append(result, c)
	}
	return result
}

func mapMistralStopReason(reason string) provider.StopReason {
	switch reason {
	case "stop":
		return provider.StopReasonEndTurn
	case "length":
		return provider.StopReasonMaxTokens
	case "tool_calls":
		return provider.StopReasonToolCall
	default:
		return provider.StopReasonEndTurn
	}
}

type mistralToolAccum struct {
	index int
	id    string
	name  string
	args  string
}
