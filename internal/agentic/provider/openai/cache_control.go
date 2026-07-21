// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// cacheControl is the Anthropic-style cache control marker used by some
// OpenAI-compatible servers (OpenRouter, LM Studio, Ollama, etc.).
type cacheControl struct {
	Type string  `json:"type"`
	TTL  *string `json:"ttl,omitempty"`
}

// newCacheControl builds an Anthropic-style cache control marker when the
// retention setting and provider compatibility allow it.
func newCacheControl(retention provider.CacheRetention, supportsLong bool) *cacheControl {
	if !provider.ShouldApplyCacheControl(retention, supportsLong) {
		return nil
	}
	cc := &cacheControl{Type: "ephemeral"}
	if retention == provider.CacheRetentionLong && supportsLong {
		ttl := "1h"
		cc.TTL = &ttl
	}
	return cc
}

// applyCacheControl adds Anthropic-style cache_control markers to the
// system prompt, the last tool definition, and the FIRST user conversation
// message. This works across providers that accept Anthropic-style markers.
//
// The conversation marker is deliberately pinned to the FIRST user message
// (the session's opening turn) instead of the last: llama.cpp-style servers
// (LM Studio, Ollama) do automatic longest-prefix caching — any marker that
// moves between requests rewrites that history message's bytes and kills the
// prefix match at that point, forcing a full re-parse of everything after it
// (bugs.md "cache-hit-first": a moving marker was caught in the request
// capture diverging between rounds while the message text was identical).
// Pinned to the opening turn, every request stays a strict append of the
// previous one and the whole history is cache-served.
func applyCacheControl(messages []map[string]interface{}, tools []map[string]interface{}, cc *cacheControl) {
	if cc == nil {
		return
	}
	addCacheControlToSystemPrompt(messages, cc)
	addCacheControlToLastTool(tools, cc)
	addCacheControlToFirstConversationMessage(messages, cc)
}

func addCacheControlToSystemPrompt(messages []map[string]interface{}, cc *cacheControl) {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "system" || role == "developer" {
			addCacheControlToTextContent(msg, cc)
			return
		}
	}
}

// addCacheControlToFirstConversationMessage pins the conversation breakpoint
// to the FIRST user message (see applyCacheControl for why it must not move).
func addCacheControlToFirstConversationMessage(messages []map[string]interface{}, cc *cacheControl) {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "user" {
			if addCacheControlToTextContent(msg, cc) {
				return
			}
		}
	}
}

func addCacheControlToLastTool(tools []map[string]interface{}, cc *cacheControl) {
	if len(tools) == 0 {
		return
	}
	tools[len(tools)-1]["cache_control"] = cc
}

// addCacheControlToTextContent converts string content into the array form
// and attaches cache_control to the last text block. It returns true if a
// marker was attached.
func addCacheControlToTextContent(msg map[string]interface{}, cc *cacheControl) bool {
	content, ok := msg["content"]
	if !ok {
		return false
	}

	if s, ok := content.(string); ok {
		if s == "" {
			return false
		}
		msg["content"] = []map[string]interface{}{
			{"type": "text", "text": s, "cache_control": cc},
		}
		return true
	}

	parts, ok := content.([]map[string]interface{})
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

// promptCacheKey returns a session-derived key for providers that support
// prompt caching. For OpenAI's official API it is sent whenever caching is
// enabled; for local servers (LM Studio, Ollama) it is sent whenever a
// session ID is available to improve slot/cache affinity.
func promptCacheKey(model provider.Model, opts provider.StreamOptions, compat provider.OpenAICompletionsCompat) string {
	if opts.CacheRetention == provider.CacheRetentionNone && !isLocalProvider(model.Provider, model.BaseURL) {
		return ""
	}
	if opts.SessionID == "" {
		return ""
	}
	isOpenAI := strings.Contains(model.BaseURL, "api.openai.com")
	supportsLong := provider.ToBool(compat.SupportsLongCacheRetention, false)
	if isOpenAI || (opts.CacheRetention == provider.CacheRetentionLong && supportsLong) || isLocalProvider(model.Provider, model.BaseURL) {
		return clampOpenAIPromptCacheKey(opts.SessionID)
	}
	return ""
}

// promptCacheRetention returns the OpenAI prompt_cache_retention value for
// long cache retention on supported providers.
func promptCacheRetention(opts provider.StreamOptions, compat provider.OpenAICompletionsCompat) string {
	if opts.CacheRetention != provider.CacheRetentionLong {
		return ""
	}
	if !provider.ToBool(compat.SupportsLongCacheRetention, false) {
		return ""
	}
	return "24h"
}

// isLocalProvider reports whether the endpoint is a local server (LM Studio
// or Ollama) where prompt_cache_key improves cache affinity across slots.
func isLocalProvider(prov provider.Provider, baseURL string) bool {
	p := strings.ToLower(string(prov))
	u := strings.ToLower(baseURL)
	return p == "lm-studio" || p == "ollama" ||
		strings.Contains(u, "localhost:1234") || strings.Contains(u, "127.0.0.1:1234") ||
		strings.Contains(u, "localhost:11434") || strings.Contains(u, "127.0.0.1:11434")
}

// openAIPromptCacheKeyMaxLen is OpenAI's documented maximum length for
// prompt_cache_key.
const openAIPromptCacheKeyMaxLen = 64

func clampOpenAIPromptCacheKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	if len(runes) <= openAIPromptCacheKeyMaxLen {
		return key
	}
	return string(runes[:openAIPromptCacheKeyMaxLen])
}
