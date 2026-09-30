// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// ---------------------------------------------------------------------------
// Request building
// ---------------------------------------------------------------------------

// applyToolChoice attaches the tools array to the body and forces tool use
// when ToolChoice is set (e.g., "required" for workflow agents). A NoTools
// context (P7 final-step collapse) omits the tools array and forces
// tool_choice "none" so the model must answer text-only.
func applyToolChoice(body map[string]any, tools []map[string]any, toolChoice string, noTools bool) {
	if noTools {
		body["tool_choice"] = "none"
		return
	}
	body["tools"] = tools
	if toolChoice != "" {
		body["tool_choice"] = toolChoice
	}
}

// applyTools attaches the tool-call surface to the body: the tools array with
// its tool_choice, plus the z.ai-only "tool_stream": true that opens GLM's
// dedicated tool-call SSE channel. tool_stream is only meaningful next to a
// tools array, so it rides along with it and never appears on a NoTools
// (final-step collapse) request.
func applyTools(body map[string]any, tools []map[string]any, opts schema.StreamOptions, ctx schema.Context, compat openAICompletionsCompat) {
	if len(tools) == 0 {
		if ctx.NoTools {
			// Final-step collapse (P7): the model must answer text-only.
			body["tool_choice"] = "none"
		}
		return
	}
	applyToolChoice(body, tools, opts.ToolChoice, ctx.NoTools)
	if compat.ZaiToolStream && !ctx.NoTools {
		body["tool_stream"] = true
	}
}

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
	// P21 (DS2): the wire request must always be explicit and reconstructable.
	// An explicit request value always wins; when the request omits max_tokens,
	// the provider catalog's default_max_tokens is materialized (DeepSeek
	// 256000 — dsh adapter DEFAULT_MAX_TOKENS). A reasoning-capable model with
	// no configured cap falls back to its own output ceiling (bugs.md #4):
	// reasoning and the answer share one budget, so an uncapped request can
	// spend the whole response on reasoning and emit no answer.
	maxTokens, defaultSource := resolveRequestMaxTokens(opts, model, model.Reasoning)
	if maxTokens > 0 {
		body[compat.MaxTokensField] = maxTokens
	}
	if defaultSource != "" {
		protocolLog.Printf("field max_tokens came from %s default: %d (model=%s provider=%s)",
			defaultSource, maxTokens, model.ID, model.Provider)
	}
	// Omit temperature when the provider does not support it (e.g. kimi-code
	// rejects any value but its fixed default with HTTP 400 "invalid
	// temperature"): send nothing and let the endpoint apply its default.
	if opts.Temperature != nil && compat.SupportsTemperature {
		body["temperature"] = *opts.Temperature
	}
	if opts.TopP != nil {
		body["top_p"] = *opts.TopP
	}
	applyTools(body, tools, opts, ctx, compat)
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

// FallbackReasoningMaxTokens bounds the response of a reasoning-capable model
// whose output ceiling is unknown to the catalog (models.dev Limit.Output
// missing). Reasoning and the answer share one budget, so an uncapped request
// can emit reasoning only and never an answer (pi hazard, packages/ai/src/
// types.ts:834-840; bugs.md #4 — z.ai export A spent 4268 reasoning tokens
// before its first tool call). Last-resort cap only: an explicit request value
// wins, then the provider catalog default_max_tokens, then the model's own
// output ceiling.
const FallbackReasoningMaxTokens = 16384

// resolveRequestMaxTokens returns the output-token cap to send on the wire
// and the source it was resolved from. An explicit request value always wins
// (dsh: "an explicit cap wins"); when absent, the per-provider catalog
// default_max_tokens is materialized so the wire request is explicit and
// reconstructable (P21, DS2); when that is unset too, a reasoning-capable model
// caps itself at its known output ceiling so reasoning cannot starve the
// answer (bugs.md #4). The returned source is "" for an explicit value; the
// caller omits the field when the returned value is 0.
func resolveRequestMaxTokens(opts schema.StreamOptions, model schema.Model, reasoningCapable bool) (int, string) {
	if opts.MaxTokens > 0 {
		return opts.MaxTokens, ""
	}
	if def := schema.LookupProviderDef(model.Provider); def != nil && def.DefaultMaxTokens > 0 {
		return def.DefaultMaxTokens, "provider"
	}
	if !reasoningCapable {
		return 0, ""
	}
	if model.MaxTokens > 0 {
		return model.MaxTokens, "reasoning-ceiling"
	}
	return FallbackReasoningMaxTokens, "reasoning-fallback"
}

func applyThinking(body map[string]any, model schema.Model, opts schema.StreamOptions, profile schema.VariantProfile, compat openAICompletionsCompat) {
	format := compat.ThinkingFormat
	if format == "" {
		format = string(profileForModel(model).Compat.ThinkingFormat)
	}
	if !model.Reasoning && format == "" {
		return
	}
	// P13 (CA2/CA3): purpose=session-title forces thinking off — mirrors the
	// DS-thinking lock (dsh llm-deepseek README: session-title "forces
	// thinking disabled and omits the already-resolved effort, reserving its
	// bounded output for visible title text"). The explicit disabled body is
	// sent on formats that support one so a server-side sticky thinking
	// default cannot leak through.
	if opts.Purpose == schema.PurposeSessionTitle {
		opts.Reasoning = schema.ThinkingOff
	}
	level := resolveThinkingLevel(model, opts, profile)
	// An explicit "off" disables thinking on the server (pi does the same for
	// z.ai: thinking:{type:"disabled"}) instead of omitting the body, so a
	// server-side sticky thinking default cannot leak through.
	if opts.Reasoning == schema.ThinkingOff {
		for k, v := range thinkingDisabledBodyForFormat(format) {
			body[k] = v
		}
		return
	}
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
	var thinking string
	var toolCalls []map[string]any
	for _, block := range msg.Content {
		switch block.Type {
		case schema.ContentBlockText:
			textContent += block.Text
		case schema.ContentBlockThinking:
			// DeepSeek-style reasoning; emitted below on tool-call turns only.
			thinking += block.Thinking
		case schema.ContentBlockToolCall:
			toolCalls = append(toolCalls, map[string]any{
				"id":   block.ToolCallID,
				"type": "function",
				"function": map[string]any{
					"name":      block.ToolName,
					"arguments": schema.SafeToolArguments(block.ToolArguments),
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
		// DeepSeek passback rule (dsh serialize.ts): reasoning_content must
		// return on tool-call turns; it is ignored on plain turns, so drop it
		// there to save tokens. The flag forces the key (empty when the turn
		// carries no thinking) — the API 400s without it in thinking mode.
		if thinking != "" {
			result["reasoning_content"] = thinking
		} else if compat.RequiresReasoningContentOnAssistantMessages {
			result["reasoning_content"] = ""
		}
	} else {
		result["content"] = textContent
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
