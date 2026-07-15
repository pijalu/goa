// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

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
	ToolResultAsUser                            bool
}

func resolveOpenAICompat(model schema.Model, profile schema.VariantProfile) openAICompletionsCompat {
	c := openAICompletionsCompat{
		MaxTokensField:     profile.Compat.MaxTokensField,
		ThinkingFormat:     profile.Compat.ThinkingFormat,
		CacheControlFormat: "",
	}
	if c.MaxTokensField == "" {
		c.MaxTokensField = "max_completion_tokens"
	}
	if profile.Compat.SupportsStore != nil {
		c.SupportsStore = *profile.Compat.SupportsStore
	}
	if profile.CachePolicy.Mode != "" && profile.CachePolicy.Mode != schema.CacheModeNone {
		c.CacheControlFormat = "anthropic"
	}
	return c
}

// ---------------------------------------------------------------------------
// Request building
// ---------------------------------------------------------------------------

func buildOpenAIParams(model schema.Model, ctx schema.Context, opts schema.StreamOptions, profile schema.VariantProfile, compat openAICompletionsCompat) map[string]any {
	messages := convertMessages(model, ctx.Messages, ctx.SystemPrompt, compat)
	tools := convertTools(ctx.Tools)

	if compat.CacheControlFormat == "anthropic" {
		cc := newOpenAICacheControl(opts.CacheRetention, compat.SupportsLongCacheRetention)
		applyOpenAICacheControl(messages, tools, cc)
	}

	body := map[string]any{
		"model":    model.ID,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if opts.MaxTokens > 0 {
		body[compat.MaxTokensField] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if opts.TopP != nil {
		body["top_p"] = *opts.TopP
	}
	if len(tools) > 0 {
		body["tools"] = tools
		if opts.ToolChoice != "" {
			body["tool_choice"] = opts.ToolChoice
		}
	}
	if compat.SupportsStore {
		body["store"] = false
	}
	if key := promptCacheKey(model, opts, compat); key != "" {
		body["prompt_cache_key"] = key
	}
	if retention := promptCacheRetention(opts, compat); retention != "" {
		body["prompt_cache_retention"] = retention
	}

	applyThinking(body, model, opts, profile, compat)
	return body
}

func applyThinking(body map[string]any, model schema.Model, opts schema.StreamOptions, profile schema.VariantProfile, compat openAICompletionsCompat) {
	format := compat.ThinkingFormat
	if format == "" {
		format = string(profileForModel(model).Compat.ThinkingFormat)
	}
	if !model.Reasoning && format == "" {
		return
	}
	level := resolveThinkingLevel(model, opts, profile)
	for k, v := range thinkingBodyForFormat(format, level) {
		body[k] = v
	}
	if compat.SupportsReasoningEffort && model.Reasoning {
		body["reasoning_effort"] = level
	}
}

func profileForModel(model schema.Model) schema.VariantProfile {
	return schema.ResolveProfile(model)
}

func convertMessages(model schema.Model, messages []schema.Message, systemPrompt string, compat openAICompletionsCompat) []map[string]any {
	var out []map[string]any
	if systemPrompt != "" {
		role := "system"
		if compat.SupportsDeveloperRole {
			role = "developer"
		}
		out = append(out, map[string]any{"role": role, "content": systemPrompt})
	}
	for _, msg := range messages {
		switch msg.Role {
		case schema.RoleUser:
			out = append(out, convertUserMessage(msg))
		case schema.RoleAssistant:
			out = append(out, convertAssistantMessage(msg, compat))
		case schema.RoleToolResult:
			out = append(out, convertToolResultMessage(msg, compat))
		case schema.RoleSystem:
			out = append(out, map[string]any{"role": "system", "content": extractOpenAIText(msg.Content)})
		}
	}
	return out
}

func convertUserMessage(msg schema.Message) map[string]any {
	return map[string]any{"role": "user", "content": buildUserContent(msg.Content)}
}

func buildUserContent(blocks []schema.ContentBlock) any {
	if !hasImageBlock(blocks) {
		return extractText(blocks)
	}
	parts := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case schema.ContentBlockText:
			if b.Text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": b.Text})
			}
		case schema.ContentBlockImage:
			dataURL := imagePathToDataURL(b.ImageData)
			if dataURL != "" {
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}})
			}
		}
	}
	return parts
}

func hasImageBlock(blocks []schema.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == schema.ContentBlockImage {
			return true
		}
	}
	return false
}

func imagePathToDataURL(path string) string {
	if strings.HasPrefix(path, "data:") {
		return path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	mime := http.DetectContentType(data)
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
}

func convertAssistantMessage(msg schema.Message, compat openAICompletionsCompat) map[string]any {
	result := map[string]any{"role": "assistant"}
	var textContent string
	var toolCalls []map[string]any
	for _, block := range msg.Content {
		switch block.Type {
		case schema.ContentBlockText:
			textContent += block.Text
		case schema.ContentBlockThinking:
			result["reasoning_content"] = block.Thinking
		case schema.ContentBlockToolCall:
			toolCalls = append(toolCalls, map[string]any{
				"id":   block.ToolCallID,
				"type": "function",
				"function": map[string]any{
					"name":      block.ToolName,
					"arguments": block.ToolArguments,
				},
			})
		}
	}
	if len(toolCalls) > 0 {
		result["tool_calls"] = toolCalls
		if textContent == "" {
			result["content"] = ""
		} else {
			result["content"] = textContent
		}
	} else {
		result["content"] = textContent
	}
	if compat.RequiresReasoningContentOnAssistantMessages {
		if _, ok := result["reasoning_content"]; !ok {
			result["reasoning_content"] = ""
		}
	}
	return result
}

func convertToolResultMessage(msg schema.Message, compat openAICompletionsCompat) map[string]any {
	text := extractOpenAIText(msg.Content)
	toolCallID := ""
	toolName := ""
	for _, block := range msg.Content {
		if block.Type == schema.ContentBlockToolResult {
			toolCallID = block.ToolCallID
			toolName = block.ToolName
			if block.Text != "" {
				text = block.Text
			}
		}
	}
	if compat.ToolResultAsUser {
		formatted := fmt.Sprintf("<tool_result>\n<tool_name>%s</tool_name>\n<tool_call_id>%s</tool_call_id>\n<content>\n%s\n</content>\n</tool_result>",
			toolName, toolCallID, text)
		return map[string]any{"role": "user", "content": formatted}
	}
	return map[string]any{"role": "tool", "content": text, "tool_call_id": toolCallID}
}

func extractOpenAIText(blocks []schema.ContentBlock) string {
	var text string
	for _, block := range blocks {
		if block.Type == schema.ContentBlockText {
			text += block.Text
		}
	}
	return text
}

func convertTools(tools []schema.ToolSchema) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Cache control helpers
// ---------------------------------------------------------------------------

type cacheControl struct {
	Type string  `json:"type"`
	TTL  *string `json:"ttl,omitempty"`
}

func newOpenAICacheControl(retention schema.CacheRetention, supportsLong bool) *cacheControl {
	if !shouldApplyOpenAICacheControl(retention, supportsLong) {
		return nil
	}
	cc := &cacheControl{Type: "ephemeral"}
	if retention == schema.CacheRetentionLong && supportsLong {
		ttl := "1h"
		cc.TTL = &ttl
	}
	return cc
}

func shouldApplyOpenAICacheControl(retention schema.CacheRetention, supportsLong bool) bool {
	return retention == schema.CacheRetentionShort || (retention == schema.CacheRetentionLong && supportsLong)
}

func applyOpenAICacheControl(messages []map[string]any, tools []map[string]any, cc *cacheControl) {
	if cc == nil {
		return
	}
	addOpenAICacheControlToSystemPrompt(messages, cc)
	addOpenAICacheControlToLastTool(tools, cc)
	addOpenAICacheControlToLastConversationMessage(messages, cc)
}

func addOpenAICacheControlToSystemPrompt(messages []map[string]any, cc *cacheControl) {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "system" || role == "developer" {
			addOpenAICacheControlToTextContent(msg, cc)
			return
		}
	}
}

func addOpenAICacheControlToLastTool(tools []map[string]any, cc *cacheControl) {
	if len(tools) == 0 {
		return
	}
	tools[len(tools)-1]["cache_control"] = cc
}

func addOpenAICacheControlToLastConversationMessage(messages []map[string]any, cc *cacheControl) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		role, _ := msg["role"].(string)
		if role == "user" || role == "assistant" {
			if addOpenAICacheControlToTextContent(msg, cc) {
				return
			}
		}
	}
}

func addOpenAICacheControlToTextContent(msg map[string]any, cc *cacheControl) bool {
	content, ok := msg["content"]
	if !ok {
		return false
	}
	if s, ok := content.(string); ok {
		if s == "" {
			return false
		}
		msg["content"] = []map[string]any{
			{"type": "text", "text": s, "cache_control": cc},
		}
		return true
	}
	parts, ok := content.([]map[string]any)
	if !ok {
		return false
	}
	for i := len(parts) - 1; i >= 0; i-- {
		if t, _ := parts[i]["type"].(string); t == "text" {
			parts[i]["cache_control"] = cc
			return true
		}
	}
	return false
}

func promptCacheKey(model schema.Model, opts schema.StreamOptions, compat openAICompletionsCompat) string {
	if opts.CacheRetention == schema.CacheRetentionNone && !isLocalProvider(model.Provider, model.BaseURL) {
		return ""
	}
	if opts.SessionID == "" {
		return ""
	}
	isOpenAI := strings.Contains(model.BaseURL, "api.openai.com")
	if isOpenAI || (opts.CacheRetention == schema.CacheRetentionLong && compat.SupportsLongCacheRetention) || isLocalProvider(model.Provider, model.BaseURL) {
		return ClampOpenAIPromptCacheKey(opts.SessionID)
	}
	return ""
}

func promptCacheRetention(opts schema.StreamOptions, compat openAICompletionsCompat) string {
	if opts.CacheRetention != schema.CacheRetentionLong {
		return ""
	}
	if !compat.SupportsLongCacheRetention {
		return ""
	}
	return "24h"
}

func isLocalProvider(prov schema.Provider, baseURL string) bool {
	p := strings.ToLower(string(prov))
	u := strings.ToLower(baseURL)
	return p == "lm-studio" || p == "ollama" ||
		strings.Contains(u, "localhost:1234") || strings.Contains(u, "127.0.0.1:1234") ||
		strings.Contains(u, "localhost:11434") || strings.Contains(u, "127.0.0.1:11434")
}

const OpenAIPromptCacheKeyMaxLen = 64

func ClampOpenAIPromptCacheKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= OpenAIPromptCacheKeyMaxLen {
		return key
	}
	return string(runes[:OpenAIPromptCacheKeyMaxLen])
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
		a.handleEndMessage()
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

func (a *streamAccum) handleEndMessage() {
	sr := schema.StopReasonEndTurn
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
	return []parserMessage{{Type: parserEnd}}
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

func resolveThinkingLevel(model schema.Model, opts schema.StreamOptions, profile schema.VariantProfile) string {
	if opts.Reasoning != "" && opts.Reasoning != schema.ThinkingOff {
		if native, ok := profile.Defaults.ThinkingLevelMap[opts.Reasoning]; ok {
			return native
		}
		return string(opts.Reasoning)
	}
	if profile.Defaults.Thinking != "" {
		return profile.Defaults.Thinking
	}
	return "medium"
}

func thinkingBodyForFormat(format, level string) map[string]any {
	builders := map[string]func(string) map[string]any{
		"openai":             openaiThinking,
		"ant-ling":           openaiThinking,
		"deepseek":           deepseekThinking,
		"zai":                zaiThinking,
		"together":           togetherThinking,
		"openrouter":         openrouterThinking,
		"string-thinking":    deepseekThinking,
		"qwen":               qwenThinking,
		"qwen-chat-template": qwenThinking,
		"chat-template":      qwenThinking,
		"chat-template-arg":  qwenThinking,
	}
	if b, ok := builders[format]; ok {
		return b(level)
	}
	return nil
}

func openaiThinking(level string) map[string]any { return map[string]any{"reasoning_effort": level} }
func deepseekThinking(level string) map[string]any {
	body := map[string]any{"thinking": map[string]any{"type": "enabled"}}
	if level != "" {
		body["reasoning_effort"] = level
	}
	return body
}
func zaiThinking(level string) map[string]any {
	return map[string]any{"thinking": map[string]any{"type": "enabled", "clear_thinking": false}}
}
func togetherThinking(level string) map[string]any {
	body := map[string]any{"reasoning": map[string]any{"enabled": true}}
	if level != "" {
		body["reasoning_effort"] = level
	}
	return body
}
func openrouterThinking(level string) map[string]any {
	return map[string]any{"reasoning": map[string]any{"effort": level}}
}
func qwenThinking(level string) map[string]any { return map[string]any{"thinking": true} }
