// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

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
	addOpenAICacheControlToFirstConversationMessage(messages, cc)
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

// addOpenAICacheControlToFirstConversationMessage pins the conversation
// breakpoint to the FIRST user message (the session's opening turn). It must
// NOT move between requests: llama.cpp-style servers (LM Studio, Ollama) do
// automatic longest-prefix caching, and a marker that jumps to the new last
// message every round rewrites that history message's bytes, killing the
// prefix match at that point and forcing a full re-parse of everything
// after it (cache-hit-first: caught in the LM Studio request
// capture — identical message text, only the marker moved). Pinned to the
// opening turn, every request stays a strict append of the previous one.
func addOpenAICacheControlToFirstConversationMessage(messages []map[string]any, cc *cacheControl) {
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role == "user" {
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
	if opts.CacheRetention == schema.CacheRetentionNone && !isLocalProvider(model.Provider, model.BaseURL) && !compat.SupportsPromptCache {
		return ""
	}
	if promptCacheIdentity(opts) == "" {
		return ""
	}
	isOpenAI := strings.Contains(model.BaseURL, "api.openai.com")
	if isOpenAI || compat.SupportsPromptCache || (opts.CacheRetention == schema.CacheRetentionLong && compat.SupportsLongCacheRetention) || isLocalProvider(model.Provider, model.BaseURL) {
		return ClampOpenAIPromptCacheKey(promptCacheIdentity(opts))
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

// supportsLongCacheRetention reports whether the provider accepts OpenAI's
// prompt_cache_key / prompt_cache_retention fields under long retention.
// It mirrors the provider-layer detection (compat_detect's
// supportsCacheRetention): every provider except the known-rejecting
// gateways. Protocol-local (like isLocalProvider) because this package
// cannot import the provider package without an import cycle — keep the
// exclusion lists in sync when either changes.
func supportsLongCacheRetention(model schema.Model) bool {
	p := strings.ToLower(string(model.Provider))
	u := strings.ToLower(model.BaseURL)
	switch {
	case p == "together" || strings.Contains(u, "api.together.ai") || strings.Contains(u, "api.together.xyz"),
		p == "cloudflare-workers-ai" || strings.Contains(u, "api.cloudflare.com"),
		p == "cloudflare-ai-gateway" || strings.Contains(u, "gateway.ai.cloudflare.com"),
		p == "nvidia" || strings.Contains(u, "integrate.api.nvidia.com"),
		p == "ant-ling" || strings.Contains(u, "api.ant-ling.com"):
		return false
	}
	return true
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
