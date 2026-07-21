// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestBuildParams_LMStudio_SendsCacheKeyAndMarkers(t *testing.T) {
	model := provider.Model{
		ID:       "qwen/qwen3.5-9b",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderLMStudio,
		BaseURL:  "http://localhost:1234/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages: []provider.Message{
			provider.NewUserMessage("Hello"),
		},
		Tools: []provider.ToolSchema{{
			Name:        "read",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionShort, SessionID: "session-xyz"}

	body := buildParams(model, ctx, opts, compat)

	if got, want := body["prompt_cache_key"], "session-xyz"; got != want {
		t.Errorf("prompt_cache_key = %q, want %q", got, want)
	}

	messages, ok := body["messages"].([]map[string]interface{})
	if !ok || len(messages) == 0 {
		t.Fatalf("expected messages, got %+v", body["messages"])
	}
	if !hasCacheControl(messages[0]) {
		t.Errorf("expected cache_control on system message")
	}
	if !hasCacheControl(messages[len(messages)-1]) {
		t.Errorf("expected cache_control on last message")
	}

	tools, ok := body["tools"].([]map[string]interface{})
	if !ok || len(tools) == 0 {
		t.Fatalf("expected tools, got %+v", body["tools"])
	}
	if !hasCacheControl(tools[len(tools)-1]) {
		t.Errorf("expected cache_control on last tool")
	}
}

func TestBuildParams_OpenAI_SendsCacheKey(t *testing.T) {
	model := provider.Model{
		ID:       "gpt-4o-mini",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderOpenAI,
		BaseURL:  "https://api.openai.com/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
	}
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionShort, SessionID: "openai-session"}

	body := buildParams(model, ctx, opts, compat)

	if got, want := body["prompt_cache_key"], "openai-session"; got != want {
		t.Errorf("prompt_cache_key = %q, want %q", got, want)
	}
	if _, ok := body["prompt_cache_retention"]; ok {
		t.Error("expected prompt_cache_retention to be omitted for short retention")
	}
}

func TestBuildParams_LongRetention_SendsRetention(t *testing.T) {
	model := provider.Model{
		ID:       "gpt-4o-mini",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderOpenAI,
		BaseURL:  "https://api.openai.com/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
	}
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionLong, SessionID: "long-session"}

	body := buildParams(model, ctx, opts, compat)

	if got, want := body["prompt_cache_key"], "long-session"; got != want {
		t.Errorf("prompt_cache_key = %q, want %q", got, want)
	}
	if got, want := body["prompt_cache_retention"], "24h"; got != want {
		t.Errorf("prompt_cache_retention = %q, want %q", got, want)
	}
}

func TestBuildParams_CacheRetentionNone_OmitsCacheFields(t *testing.T) {
	model := provider.Model{
		ID:       "qwen/qwen3.5-9b",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderLMStudio,
		BaseURL:  "http://localhost:1234/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
		Tools: []provider.ToolSchema{{
			Name:        "read",
			Description: "Read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
	}
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionNone, SessionID: "none-session"}

	body := buildParams(model, ctx, opts, compat)

	// Local providers still receive the session key for backend affinity.
	if got, want := body["prompt_cache_key"], "none-session"; got != want {
		t.Errorf("prompt_cache_key = %q, want %q", got, want)
	}
	if _, ok := body["prompt_cache_retention"]; ok {
		t.Error("expected prompt_cache_retention to be omitted when cacheRetention is none")
	}

	messages, ok := body["messages"].([]map[string]interface{})
	if !ok || len(messages) == 0 {
		t.Fatalf("expected messages, got %+v", body["messages"])
	}
	if hasCacheControl(messages[0]) {
		t.Error("expected no cache_control on system message when cacheRetention is none")
	}
}

func TestBuildParams_CacheRetentionNone_NonLocal_OmitsCacheKey(t *testing.T) {
	model := provider.Model{
		ID:       "gpt-4o-mini",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderOpenAI,
		BaseURL:  "https://api.openai.com/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
	}
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionNone, SessionID: "none-session"}

	body := buildParams(model, ctx, opts, compat)

	if _, ok := body["prompt_cache_key"]; ok {
		t.Error("expected prompt_cache_key to be omitted for non-local provider when cacheRetention is none")
	}
}

func TestBuildParams_PromptCacheKeyClamped(t *testing.T) {
	model := provider.Model{
		ID:       "qwen/qwen3.5-9b",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderLMStudio,
		BaseURL:  "http://localhost:1234/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
	}
	sessionID := strings.Repeat("x", 80)
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionShort, SessionID: sessionID}

	body := buildParams(model, ctx, opts, compat)

	got, ok := body["prompt_cache_key"].(string)
	if !ok {
		t.Fatalf("expected string prompt_cache_key, got %T", body["prompt_cache_key"])
	}
	if len([]rune(got)) != 64 {
		t.Errorf("prompt_cache_key rune length = %d, want 64", len([]rune(got)))
	}
}

func TestBuildParams_NonLocalNoSessionID_OmitsCacheKey(t *testing.T) {
	model := provider.Model{
		ID:       "custom-model",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderTogether,
		BaseURL:  "https://api.together.xyz/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)

	ctx := provider.Context{
		SystemPrompt: "You are helpful",
		Messages:     []provider.Message{provider.NewUserMessage("Hello")},
	}
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionShort}

	body := buildParams(model, ctx, opts, compat)

	if _, ok := body["prompt_cache_key"]; ok {
		t.Error("expected prompt_cache_key to be omitted for non-local provider without session")
	}
}

func hasCacheControl(v map[string]interface{}) bool {
	content, ok := v["content"]
	if ok {
		switch c := content.(type) {
		case []map[string]interface{}:
			for _, part := range c {
				if _, ok := part["cache_control"]; ok {
					return true
				}
			}
		}
	}
	_, ok = v["cache_control"]
	return ok
}

func TestClampOpenAIPromptCacheKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"short", "short"},
		{strings.Repeat("x", 64), strings.Repeat("x", 64)},
		{strings.Repeat("x", 65), strings.Repeat("x", 64)},
		{strings.Repeat("é", 64), strings.Repeat("é", 64)},
		{strings.Repeat("é", 65), strings.Repeat("é", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := clampOpenAIPromptCacheKey(tt.input)
			if got != tt.want {
				t.Errorf("clampOpenAIPromptCacheKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCacheControlMarker_Marshaling(t *testing.T) {
	cc := newCacheControl(provider.CacheRetentionLong, true)
	if cc == nil {
		t.Fatal("expected cache control marker")
	}
	b, err := json.Marshal(cc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(b), `{"type":"ephemeral","ttl":"1h"}`; got != want {
		t.Errorf("marshaled = %s, want %s", got, want)
	}

	ccShort := newCacheControl(provider.CacheRetentionShort, false)
	if ccShort == nil {
		t.Fatal("expected cache control marker for short retention")
	}
	b2, _ := json.Marshal(ccShort)
	if got, want := string(b2), `{"type":"ephemeral"}`; got != want {
		t.Errorf("short marshaled = %s, want %s", got, want)
	}

	if newCacheControl(provider.CacheRetentionNone, true) != nil {
		t.Error("expected nil cache control for none retention")
	}
}

// TestBuildParams_CacheMarkerPinnedAcrossRounds is the regression for the
// moving-breakpoint cache bust found in the LM Studio request capture
// (bugs.md "cache-hit-first"): the conversation cache_control marker must be
// pinned to the FIRST user message so request N stays a byte-prefix of
// request N+1. With the marker on the last message it moved every round,
// rewriting one history message's bytes and killing llama.cpp's
// longest-prefix cache match at that point.
func TestBuildParams_CacheMarkerPinnedAcrossRounds(t *testing.T) {
	model := provider.Model{
		ID:       "qwythos-9b-v2",
		Api:      provider.ApiOpenAICompletions,
		Provider: provider.ProviderLMStudio,
		BaseURL:  "http://localhost:1234/v1/chat/completions",
	}
	compat := provider.ResolveOpenAICompat(model)
	opts := provider.StreamOptions{CacheRetention: provider.CacheRetentionShort, SessionID: "s"}

	round1 := provider.Context{
		SystemPrompt: "sys",
		Messages:     []provider.Message{provider.NewUserMessage("turn one")},
	}
	round2 := provider.Context{
		SystemPrompt: "sys",
		Messages: []provider.Message{
			provider.NewUserMessage("turn one"),
			{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "reply"}}},
			provider.NewUserMessage("turn two"),
		},
	}

	m1 := buildParams(model, round1, opts, compat)["messages"].([]map[string]interface{})
	m2 := buildParams(model, round2, opts, compat)["messages"].([]map[string]interface{})

	if len(m2) != len(m1)+2 {
		t.Fatalf("round2 should append 2 messages, got %d -> %d", len(m1), len(m2))
	}
	for i := range m1 {
		b1, _ := json.Marshal(m1[i])
		b2, _ := json.Marshal(m2[i])
		if string(b1) != string(b2) {
			t.Errorf("message %d changed between rounds (prefix cache bust):\n  r1=%s\n  r2=%s", i, b1, b2)
		}
	}
	// The marker must sit on the FIRST user message in both rounds.
	if !hasCacheControl(m2[1]) {
		t.Errorf("first user message must carry the pinned cache marker")
	}
	if hasCacheControl(m2[len(m2)-1]) {
		t.Errorf("last message must NOT carry a fresh cache marker (it would move each round)")
	}
}
