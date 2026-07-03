// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
)

// parseChunk parses a single SSE data chunk from an OpenAI-compatible API.
// Returns a non-nil error when the chunk cannot be decoded as JSON.
func parseChunk(chunk string) ([]agentic.Message, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(chunk), &raw); err != nil {
		return nil, fmt.Errorf("decode openai chunk: %w", err)
	}
	if err := detectChunkError(raw); err != nil {
		return nil, err
	}
	choices, ok := raw["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		// Handle usage-only chunks (stream_options.include_usage final chunk).
		// These have no choices but carry usage/timings at root level.
		if rootMsgs := parseRootFields(raw); len(rootMsgs) > 0 {
			return rootMsgs, nil
		}
		return nil, nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		if finishReason, ok := choice["finish_reason"]; ok {
			return handleFinishReason(finishReason), nil
		}
		return nil, nil
	}

	var out []agentic.Message

	out = append(out, parseToolCalls(delta)...)
	out = append(out, parseContentDelta(delta)...)
	out = append(out, parseThinkingDeltas(delta)...)
	out = append(out, parseRootFields(raw)...)

	if finishReason := handleFinishReason(choice["finish_reason"]); finishReason != nil {
		out = append(out, finishReason...)
	}
	return out, nil
}

func detectChunkError(raw map[string]any) error {
	errObj, ok := raw["error"]
	if !ok || errObj == nil {
		return nil
	}
	msg := "provider error"
	if m, ok := raw["message"].(string); ok && m != "" {
		msg = m
	}
	if m, ok := extractErrorMessage(errObj); ok && m != "" {
		msg = m
	}
	return fmt.Errorf("LLM error: %s", msg)
}

func extractErrorMessage(errObj any) (string, bool) {
	m, ok := errObj.(map[string]any)
	if !ok {
		return "", false
	}
	msg, ok := m["message"].(string)
	return msg, ok
}

func parseToolCalls(delta map[string]interface{}) []agentic.Message {
	tc, ok := delta["tool_calls"].([]interface{})
	if !ok {
		return nil
	}
	var out []agentic.Message
	for _, t := range tc {
		toolCall, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		fn, ok := toolCall["function"].(map[string]interface{})
		if !ok {
			continue
		}
		msg := agentic.Message{Type: agentic.ToolCall, Delta: true}
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

func parseContentDelta(delta map[string]interface{}) []agentic.Message {
	c, ok := delta["content"].(string)
	if !ok || c == "" {
		return nil
	}
	return []agentic.Message{{
		Type:    agentic.Content,
		Role:    agentic.Assistant,
		Content: c,
		Delta:   true,
	}}
}

func parseThinkingDeltas(delta map[string]interface{}) []agentic.Message {
	var out []agentic.Message
	// Check all possible thinking field names: reasoning_content, reasoning, thinking
	for _, field := range []string{"reasoning_content", "reasoning", "thinking"} {
		if t, ok := delta[field].(string); ok && t != "" {
			out = append(out, agentic.Message{
				Type:     agentic.Content,
				Role:     agentic.Assistant,
				Thinking: t,
				Delta:    true,
			})
			break // only one thinking field per chunk
		}
	}
	return out
}

func parseRootFields(raw map[string]interface{}) []agentic.Message {
	var out []agentic.Message

	timings := mergeRootTimings(raw)
	rootPromptTokens := applyRootTokenCounts(raw, &timings)
	applyRootCacheFields(raw, &timings)
	computeRootPromptN(rootPromptTokens, &timings)

	if timings != nil {
		out = append(out, agentic.Message{Timings: timings})
	}

	if progressRaw, ok := raw["prompt_progress"].(map[string]interface{}); ok {
		if p := parsePromptProgress(progressRaw); p != nil {
			out = append(out, agentic.Message{PromptProgress: p})
		}
	}

	return out
}

// mergeRootTimings merges custom timings and standard usage into a single
// TokenTimings value. Returns nil if neither source produced data.
func mergeRootTimings(raw map[string]interface{}) *agentic.TokenTimings {
	var timings *agentic.TokenTimings
	if timingsRaw, ok := raw["timings"].(map[string]interface{}); ok {
		timings = parseTimings(timingsRaw)
	}
	if usageRaw, ok := raw["usage"].(map[string]interface{}); ok {
		if t := parseOpenAIUsage(usageRaw); t != nil {
			if timings == nil {
				timings = t
			} else {
				mergeTimings(timings, t)
			}
		}
	}
	return timings
}

// applyRootTokenCounts reads root-level prompt_tokens/completion_tokens fields
// (used by some backends outside the usage object). It returns the raw prompt
// token count and updates timings with completion_tokens if present.
func applyRootTokenCounts(raw map[string]interface{}, timings **agentic.TokenTimings) int {
	var rootPromptTokens int
	if pv, ok := raw["prompt_tokens"].(float64); ok && pv > 0 {
		rootPromptTokens = int(pv)
	}
	if cv, ok := raw["completion_tokens"].(float64); ok && cv > 0 {
		ensureTimings(timings)
		if (*timings).PredictedN == 0 {
			(*timings).PredictedN = int(cv)
		}
	}
	return rootPromptTokens
}

// applyRootCacheFields reads root-level cache fields from various backends.
func applyRootCacheFields(raw map[string]interface{}, timings **agentic.TokenTimings) {
	if cachedVal, ok := raw["tokens_cached"].(float64); ok && cachedVal > 0 {
		ensureTimings(timings)
		(*timings).CacheReadTokens = int(cachedVal)
	}
	if cachedVal, ok := raw["prompt_cache_hit_tokens"].(float64); ok && cachedVal > 0 {
		ensureTimings(timings)
		if (*timings).CacheReadTokens == 0 {
			(*timings).CacheReadTokens = int(cachedVal)
		}
	}
}

// computeRootPromptN applies cache subtraction to root-level prompt_tokens.
// Semantics for prompt usage calculation: PromptN = prompt_tokens - cacheRead - cacheWrite. This tracks the net token cost.
func computeRootPromptN(rootPromptTokens int, timings **agentic.TokenTimings) {
	if rootPromptTokens == 0 || *timings == nil {
		return
	}
	t := *timings
	if t.PromptN == 0 || t.CacheReadTokens > 0 || t.CacheWriteTokens > 0 {
		if t.PromptN == 0 || t.PromptN == rootPromptTokens {
			rawPrompt := rootPromptTokens
			if t.PromptN > 0 {
				rawPrompt = t.PromptN + t.CacheReadTokens + t.CacheWriteTokens
			}
			t.PromptN = rawPrompt - t.CacheReadTokens - t.CacheWriteTokens
			if t.PromptN < 0 {
				t.PromptN = 0
			}
		}
	}
}

// ensureTimings allocates timings if it is nil.
func ensureTimings(timings **agentic.TokenTimings) {
	if *timings == nil {
		*timings = &agentic.TokenTimings{}
	}
}

// mergeTimings merges non-zero fields from src into dst.
func mergeTimings(dst, src *agentic.TokenTimings) {
	if src.PromptN > 0 {
		dst.PromptN = src.PromptN
	}
	if src.PredictedN > 0 {
		dst.PredictedN = src.PredictedN
	}
	if src.PromptMs > 0 {
		dst.PromptMs = src.PromptMs
	}
	if src.PredictedMs > 0 {
		dst.PredictedMs = src.PredictedMs
	}
	if src.PromptPerSecond > 0 {
		dst.PromptPerSecond = src.PromptPerSecond
	}
	if src.PredictedPerSecond > 0 {
		dst.PredictedPerSecond = src.PredictedPerSecond
	}
	if src.CacheReadTokens > 0 {
		dst.CacheReadTokens = src.CacheReadTokens
	}
	if src.CacheWriteTokens > 0 {
		dst.CacheWriteTokens = src.CacheWriteTokens
	}
}

func handleFinishReason(reason interface{}) []agentic.Message {
	finishReason, ok := reason.(string)
	if !ok || finishReason == "" {
		return nil
	}
	// All terminal finish reasons signal the end of the assistant's response.
	// "stop" = model finished normally  |  "tool_calls" = model issued tool calls
	// "length" = max_tokens hit  |  "content_filter" = content was filtered
	// In all cases, the provider stream for this assistant response is finished.
	return []agentic.Message{{Type: agentic.End}}
}

func parseTimings(raw map[string]interface{}) *agentic.TokenTimings {
	t := &agentic.TokenTimings{}
	if v, ok := raw["prompt_n"].(float64); ok {
		t.PromptN = int(v)
	}
	if v, ok := raw["predicted_n"].(float64); ok {
		t.PredictedN = int(v)
	}
	if v, ok := raw["prompt_ms"].(float64); ok {
		t.PromptMs = v
	}
	if v, ok := raw["predicted_ms"].(float64); ok {
		t.PredictedMs = v
	}
	if v, ok := raw["prompt_per_second"].(float64); ok {
		t.PromptPerSecond = v
	}
	if v, ok := raw["predicted_per_second"].(float64); ok {
		t.PredictedPerSecond = v
	}
	if t.PromptN == 0 && t.PredictedN == 0 && t.PromptMs == 0 && t.PredictedMs == 0 {
		return nil
	}
	return t
}

// parseOpenAIUsage extracts token counts from OpenAI-compatible usage responses.
//
// Handles ALL known cache token field formats across providers:
//
//	OpenAI:        usage.prompt_tokens_details.cached_tokens
//	OpenRouter:    usage.prompt_tokens_details.cached_tokens, cache_write_tokens
//	GitHub Copilot: usage.prompt_cache_hit_tokens
//	llama.cpp:     usage.tokens_cached  (also at root level, handled in parseRootFields)
//	OpenAI Responses: usage.input_tokens_details.cached_tokens
//	Together AI:  usage.prompt_tokens_details.cached_tokens
//	DeepSeek:     usage.prompt_tokens_details.cached_tokens
//
// Reference for prompt usage calculation logic. See external documentation for details on chunk usage parsing.
func parseOpenAIUsage(raw map[string]interface{}) *agentic.TokenTimings {
	t := &agentic.TokenTimings{}

	rawPromptN := readRawPromptTokens(raw, t)
	readCacheReadTokens(raw, t)
	completionCacheTokens := readCompletionCacheTokens(raw)
	readCacheWriteTokens(raw, t)
	computePromptN(rawPromptN, t)
	if completionCacheTokens > 0 {
		t.CacheReadTokens += completionCacheTokens
	}
	applyTimingEstimate(raw, t)

	if rawPromptN == 0 && t.PredictedN == 0 && t.CacheReadTokens == 0 && t.CacheWriteTokens == 0 {
		return nil
	}
	return t
}

// readRawPromptTokens reads prompt_tokens (or total_tokens as fallback) and
// completion_tokens, returning the raw prompt token count.
func readRawPromptTokens(raw map[string]interface{}, t *agentic.TokenTimings) int {
	var rawPromptN int
	if v, ok := raw["prompt_tokens"].(float64); ok {
		rawPromptN = int(v)
	}
	if v, ok := raw["completion_tokens"].(float64); ok {
		t.PredictedN = int(v)
	}
	if rawPromptN == 0 && t.PredictedN == 0 {
		if v, ok := raw["total_tokens"].(float64); ok {
			rawPromptN = int(v)
		}
	}
	return rawPromptN
}

// readCacheReadTokens extracts cache read tokens from all known field locations.
func readCacheReadTokens(raw map[string]interface{}, t *agentic.TokenTimings) {
	candidates := []struct {
		detailsKey string
		fieldKey   string
	}{
		{"prompt_tokens_details", "cached_tokens"},
		{"input_tokens_details", "cached_tokens"},
	}
	for _, c := range candidates {
		if cached := readNestedFloat(raw, c.detailsKey, c.fieldKey); cached > 0 && t.CacheReadTokens == 0 {
			t.CacheReadTokens = int(cached)
		}
	}
	for _, key := range []string{"prompt_cache_hit_tokens", "tokens_cached"} {
		if cached := readFloat(raw, key); cached > 0 && t.CacheReadTokens == 0 {
			t.CacheReadTokens = int(cached)
		}
	}
}

// readCompletionCacheTokens extracts completion-side cached_tokens (output reuse).
func readCompletionCacheTokens(raw map[string]interface{}) int {
	if cached := readNestedFloat(raw, "completion_tokens_details", "cached_tokens"); cached > 0 {
		return int(cached)
	}
	return 0
}

// readCacheWriteTokens extracts cache write tokens from prompt_tokens_details.
func readCacheWriteTokens(raw map[string]interface{}, t *agentic.TokenTimings) {
	if writeTokens := readNestedFloat(raw, "prompt_tokens_details", "cache_write_tokens"); writeTokens > 0 {
		t.CacheWriteTokens = int(writeTokens)
	}
}

// computePromptN sets PromptN = rawPromptN - cacheRead - cacheWrite.
func computePromptN(rawPromptN int, t *agentic.TokenTimings) {
	if rawPromptN <= 0 {
		return
	}
	t.PromptN = rawPromptN - t.CacheReadTokens - t.CacheWriteTokens
	if t.PromptN < 0 {
		t.PromptN = 0
	}
}

// applyTimingEstimate computes PredictedMs from time_per_output_token_ms.
func applyTimingEstimate(raw map[string]interface{}, t *agentic.TokenTimings) {
	if v, ok := raw["time_per_output_token_ms"].(float64); ok && v > 0 && t.PredictedN > 0 {
		t.PredictedMs = v * float64(t.PredictedN)
	}
}

// readFloat reads a top-level float64 field.
func readFloat(raw map[string]interface{}, key string) float64 {
	if v, ok := raw[key].(float64); ok {
		return v
	}
	return 0
}

// readNestedFloat reads a float64 from a nested map field.
func readNestedFloat(raw map[string]interface{}, mapKey, fieldKey string) float64 {
	m, ok := raw[mapKey].(map[string]interface{})
	if !ok {
		return 0
	}
	return readFloat(m, fieldKey)
}

func parsePromptProgress(raw map[string]interface{}) *agentic.PromptProgress {
	p := &agentic.PromptProgress{}
	if v, ok := raw["total"].(float64); ok {
		p.Total = int(v)
	}
	if v, ok := raw["cache"].(float64); ok {
		p.Cache = int(v)
	}
	if v, ok := raw["processed"].(float64); ok {
		p.Processed = int(v)
	}
	if v, ok := raw["time_ms"].(float64); ok {
		p.TimeMs = int(v)
	}
	if p.Total == 0 && p.Processed == 0 {
		return nil
	}
	return p
}
