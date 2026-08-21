// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zaiModel builds the z.ai GLM coding-plan model used by the caching tests.
func zaiModel() schema.Model {
	return schema.Model{
		ID:        "glm-5.2",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderZai,
		BaseURL:   "https://api.z.ai/api/coding/paas/v4",
		Reasoning: true,
	}
}

// TestZaiPrefixStableAcrossTurns verifies the provider-caching invariant for
// z.ai (GLM-5.2 quota burned rapidly): z.ai uses server-side
// automatic prefix caching, so the request prefix (system prompt + prior
// messages + tools) must serialize byte-identically across turns. A turn-2
// body must contain turn-1's messages array verbatim as its prefix — any
// churn (reordering, per-turn jitter) would bust the provider cache and bill
// the full context every round.
func TestZaiPrefixStableAcrossTurns(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	model := zaiModel()
	profile := schema.ResolveProfile(model)
	opts := schema.StreamOptions{SessionID: "sess-1"}
	tools := []schema.ToolSchema{
		{Name: "read", Description: "read a file"},
		{Name: "bash", Description: "run a command"},
	}

	// Turn 1: system + first user message.
	ctx1 := schema.Context{
		SystemPrompt: "You are a coding agent.",
		Messages:     []schema.Message{schema.NewUserMessage("hello")},
		Tools:        tools,
	}
	body1, err := p.BuildRequest(model, ctx1, opts, profile)
	require.NoError(t, err)

	// Turn 2: same prefix + assistant reply + next user message.
	ctx2 := schema.Context{
		SystemPrompt: "You are a coding agent.",
		Messages: []schema.Message{
			schema.NewUserMessage("hello"),
			schema.NewAssistantMessage([]schema.ContentBlock{{Type: schema.ContentBlockText, Text: "hi! how can I help?"}}),
			schema.NewUserMessage("read README.md"),
		},
		Tools: tools,
	}
	body2, err := p.BuildRequest(model, ctx2, opts, profile)
	require.NoError(t, err)

	// The serialized messages array of turn 1 must appear verbatim inside
	// turn 2 as its leading entries — the prefix z.ai's cache matches on.
	var req1, req2 map[string]any
	require.NoError(t, json.Unmarshal(body1, &req1))
	require.NoError(t, json.Unmarshal(body2, &req2))

	msgs1, err := json.Marshal(req1["messages"])
	require.NoError(t, err)
	msgs2, err := json.Marshal(req2["messages"])
	require.NoError(t, err)

	// Strip the closing ']' from msgs1: the resulting prefix must open msgs2.
	prefix := bytes.TrimSuffix(msgs1, []byte("]"))
	require.True(t, bytes.HasPrefix(msgs2, append(prefix, ',')),
		"turn-2 messages must extend turn-1 verbatim (prefix cache stability)\nturn1: %s\nturn2: %s", msgs1, msgs2)

	// The tools array must be byte-identical across turns.
	tools1, err := json.Marshal(req1["tools"])
	require.NoError(t, err)
	tools2, err := json.Marshal(req2["tools"])
	require.NoError(t, err)
	require.JSONEq(t, string(tools1), string(tools2), "tools must not churn between turns")

	// Same model/options must not inject per-turn jitter at the top level:
	// with the retention unset (not long) no cache-identity fields are sent.
	// (The zai catalog default IS long — see TestZaiLongRetentionSendsCacheKey
	// for the affinity policy — but this test pins serialization stability
	// for callers that do not opt in.)
	_, hasKey := req2["prompt_cache_key"]
	assert.False(t, hasKey, "no prompt_cache_key without long retention")
	_, hasRetention := req2["prompt_cache_retention"]
	assert.False(t, hasRetention, "no prompt_cache_retention without long retention")
}

// TestZaiLongRetentionSendsCacheKey pins the cache-affinity policy
// (bugs.md 2026-08-19): under long retention — the zai catalog default since
// content-keyed routing was observed evicting cached prefixes mid-session
// (2026-08-19 debug exports) — the OpenAI-style prompt_cache_key carries the
// session cache identity so z.ai can pin the conversation to one cache
// shard. z.ai was live-probed: HTTP 200 with prompt_cache_key and
// prompt_cache_retention present. The key must be clamped to OpenAI's
// 64-character limit because goa's cache identities are longer.
func TestZaiLongRetentionSendsCacheKey(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	model := zaiModel()
	profile := schema.ResolveProfile(model)
	identity := "goa_" + strings.Repeat("a1b2c3d4", 12) // 4 + 96 > 64 chars
	opts := schema.StreamOptions{
		SessionID:      identity,
		CacheRetention: schema.CacheRetentionLong,
	}
	ctx := schema.Context{
		SystemPrompt: "sys",
		Messages:     []schema.Message{schema.NewUserMessage("hi")},
	}

	body, err := p.BuildRequest(model, ctx, opts, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))

	want := identity[:64]
	assert.Equal(t, want, req["prompt_cache_key"],
		"long retention must send the (clamped) session cache identity")
	assert.Equal(t, "24h", req["prompt_cache_retention"],
		"long retention on a supporting provider must request 24h retention")

	// The identity fields must not perturb the cached prefix: messages and
	// tools stay byte-identical to a short-retention build.
	shortOpts := opts
	shortOpts.CacheRetention = schema.CacheRetentionShort
	shortBody, err := p.BuildRequest(model, ctx, shortOpts, profile)
	require.NoError(t, err)
	var shortReq map[string]any
	require.NoError(t, json.Unmarshal(shortBody, &shortReq))
	assert.JSONEq(t, string(mustJSON(t, shortReq["messages"])), string(mustJSON(t, req["messages"])),
		"cache identity fields must not alter the message prefix")
	assert.JSONEq(t, string(mustJSON(t, shortReq["tools"])), string(mustJSON(t, req["tools"])),
		"cache identity fields must not alter the tools schema")
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// TestZaiSameBodyIsDeterministic verifies two builds of the identical
// (model, context, options) produce byte-identical bodies — the minimum
// requirement for any provider-side prefix cache hit.
func TestZaiSameBodyIsDeterministic(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	model := zaiModel()
	profile := schema.ResolveProfile(model)
	ctx := schema.Context{
		SystemPrompt: "sys",
		Messages:     []schema.Message{schema.NewUserMessage("hi")},
		Tools:        []schema.ToolSchema{{Name: "read", Description: "read"}},
	}
	opts := schema.StreamOptions{SessionID: "sess-1"}

	body1, err := p.BuildRequest(model, ctx, opts, profile)
	require.NoError(t, err)
	body2, err := p.BuildRequest(model, ctx, opts, profile)
	require.NoError(t, err)
	require.True(t, bytes.Equal(body1, body2), "identical inputs must marshal byte-identically")
}

// TestZaiUsageParsesCachedTokens verifies z.ai's OpenAI-style usage block
// (usage.prompt_tokens_details.cached_tokens) is read as CacheReadTokens —
// the signal the cache-forensics journal and /stats use to prove provider
// caching is working (and the field whose absence means the cache never hits).
func TestZaiUsageParsesCachedTokens(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	// z.ai streams usage in a terminal chunk with an empty choices array.
	sse := "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":5000,\"completion_tokens\":42,\"total_tokens\":5042,\"prompt_tokens_details\":{\"cached_tokens\":4800}}}\n\n" +
		"data: [DONE]\n\n"

	stream := schema.NewAssistantMessageEventStream(16)
	go p.ParseResponse(strings.NewReader(sse), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.NotNil(t, result.Usage, "terminal usage chunk must surface the usage")
	assert.Equal(t, 4800, result.Usage.CacheReadTokens, "z.ai cached_tokens must parse as CacheReadTokens")
	assert.Equal(t, 42, result.Usage.OutputTokens)
}
