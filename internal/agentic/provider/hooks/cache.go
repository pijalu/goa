// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

const cacheOrder = 400

// CacheHook applies cache policy to canonical messages.
type CacheHook struct {
	profile schema.VariantProfile
}

// Name returns the hook name.
func (h *CacheHook) Name() string { return "cache" }

// Order returns the hook order.
func (h *CacheHook) Order() int { return cacheOrder }

// Init initializes the hook with the variant profile.
func (h *CacheHook) Init(profile schema.VariantProfile) error {
	h.profile = profile
	return nil
}

// ApplyRequest annotates messages with cache breakpoints according to the
// profile's cache policy.
func (h *CacheHook) ApplyRequest(ctx *RequestContext) error {
	policy := h.profile.CachePolicy
	if policy.Mode == "" {
		policy.Mode = schema.CacheModeNone
	}

	switch policy.Mode {
	case schema.CacheModeAuto:
		ctx.Context.Messages = applyAutoCache(ctx.Context.Messages, policy)
	case schema.CacheModeLong:
		ctx.Context.Messages = applyTailCache(ctx.Context.Messages, policy, 1)
	}

	if policy.AffinityHeader != "" {
		if ctx.Headers == nil {
			ctx.Headers = make(map[string]string)
		}
		ctx.Headers[policy.AffinityHeader] = ctx.Options.SessionID
	}

	return nil
}

// ApplyResponse is a no-op for cache.
func (h *CacheHook) ApplyResponse(ctx *ResponseContext) error { return nil }

// ApplyError is a no-op for cache.
func (h *CacheHook) ApplyError(ctx *ErrorContext) error { return nil }

func applyAutoCache(messages []schema.Message, policy schema.CachePolicy) []schema.Message {
	cap := policy.BreakpointCap
	if cap <= 0 {
		return messages
	}
	out := make([]schema.Message, len(messages))
	copy(out, messages)

	placed := 0
	if policy.Messages.Tools {
		// Cache the first user message after tool definitions (not represented
		// as a separate message here, so we place on the first user message).
		for i := range out {
			if out[i].Role == schema.RoleUser {
				out[i] = markCached(out[i], policy)
				placed++
				break
			}
		}
	}

	if placed < cap && policy.Messages.System {
		for i := range out {
			if out[i].Role == schema.RoleSystem {
				out[i] = markCached(out[i], policy)
				placed++
				break
			}
		}
	}

	if placed < cap {
		out = applyTailCache(out, policy, cap-placed)
	}

	return out
}

func applyTailCache(messages []schema.Message, policy schema.CachePolicy, limit int) []schema.Message {
	out := make([]schema.Message, len(messages))
	copy(out, messages)

	tail := policy.Messages.Tail
	if tail <= 0 {
		tail = limit
	}
	if tail > limit {
		tail = limit
	}

	placed := 0
	for i := len(out) - 1; i >= 0 && placed < tail; i-- {
		if !isCached(out[i]) {
			out[i] = markCached(out[i], policy)
			placed++
		}
	}
	return out
}

func markCached(m schema.Message, policy schema.CachePolicy) schema.Message {
	if policy.Granularity == "content" && len(m.Content) > 0 {
		m.Content[0] = schema.ContentBlock{
			Type:          m.Content[0].Type,
			Text:          m.Content[0].Text,
			Thinking:      m.Content[0].Thinking,
			ToolCallID:    m.Content[0].ToolCallID,
			ToolName:      m.Content[0].ToolName,
			ToolArguments: m.Content[0].ToolArguments,
			ImageData:     m.Content[0].ImageData,
			ImageMimeType: m.Content[0].ImageMimeType,
		}
	}
	if m.Extra == nil {
		m.Extra = make(map[string]interface{})
	}
	m.Extra["cache_control"] = map[string]string{"type": "ephemeral"}
	if policy.TTL != "" {
		m.Extra["cache_ttl"] = policy.TTL
	}
	return m
}

func isCached(m schema.Message) bool {
	if m.Extra == nil {
		return false
	}
	_, ok := m.Extra["cache_control"]
	return ok
}

// SanitizeCacheKey sanitizes a string to be a valid OpenAI prompt cache key.
func SanitizeCacheKey(key string) string {
	key = strings.ToLower(key)
	key = regexp.MustCompile(`[^a-z0-9_-]`).ReplaceAllString(key, "_")
	if len(key) > 128 {
		key = truncateToAllowed(key, 128)
	}
	return key
}

func truncateToAllowed(key string, max int) string {
	sum := sha256.Sum256([]byte(key))
	hex := fmt.Sprintf("%x", sum)
	var b strings.Builder
	b.Grow(max)
	for b.Len() < max {
		b.WriteString(hex)
	}
	return b.String()[:max]
}
