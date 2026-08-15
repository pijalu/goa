// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveOpenAICompat_ReasoningContentFlagPropagation is the regression
// test for the thinking-mode 400: the opencode variant profile
// carries requires_reasoning_content_on_assistant_messages=true, but
// resolveOpenAICompat dropped it, so proxied DeepSeek models 400'd with
// "reasoning_content in the thinking mode must be passed back".
func TestResolveOpenAICompat_ReasoningContentFlagPropagation(t *testing.T) {
	profile := schema.VariantProfile{
		Compat: schema.CompatFlags{
			RequiresReasoningContentOnAssistantMessages: true,
		},
	}
	compat := resolveOpenAICompat(schema.Model{}, profile)
	assert.True(t, compat.RequiresReasoningContentOnAssistantMessages,
		"profile flag must reach the serializer compat")

	compat = resolveOpenAICompat(schema.Model{}, schema.VariantProfile{})
	assert.False(t, compat.RequiresReasoningContentOnAssistantMessages,
		"flag defaults off without a profile requirement")
}

// TestConvertAssistantMessage_ReasoningContent pins both serialization
// behaviors: thinking blocks always serialize as reasoning_content; the flag
// additionally injects an empty reasoning_content on assistant messages
// without thinking (DeepSeek requires the key on EVERY assistant message in
// thinking mode); without the flag the key stays absent.
func TestConvertAssistantMessage_ReasoningContent(t *testing.T) {
	thinkingMsg := schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentBlock{
			{Type: schema.ContentBlockThinking, Thinking: "chain of thought"},
			{Type: schema.ContentBlockText, Text: "answer"},
		},
	}
	plainMsg := schema.Message{
		Role:    schema.RoleAssistant,
		Content: []schema.ContentBlock{{Type: schema.ContentBlockText, Text: "answer"}},
	}

	// Thinking always serializes, flag or not.
	out := convertAssistantMessage(thinkingMsg, openAICompletionsCompat{})
	assert.Equal(t, "chain of thought", out["reasoning_content"])

	// Flag on: plain assistant message gets an empty reasoning_content key.
	out = convertAssistantMessage(plainMsg, openAICompletionsCompat{RequiresReasoningContentOnAssistantMessages: true})
	rc, ok := out["reasoning_content"]
	require.True(t, ok, "flag on: reasoning_content key must be present")
	assert.Equal(t, "", rc)

	// Flag off: the key stays absent (pinned — providers that reject unknown
	// fields rely on this).
	out = convertAssistantMessage(plainMsg, openAICompletionsCompat{})
	_, ok = out["reasoning_content"]
	assert.False(t, ok, "flag off: reasoning_content must be absent")
}

// TestResolveProfile_OpencodeCarriesReasoningContentFlag verifies the
// embedded opencode variant (zen proxy, DeepSeek-class models) resolves with
// the requirement set for a proxied deepseek model id unknown to the models
// registry.
func TestResolveProfile_OpencodeCarriesReasoningContentFlag(t *testing.T) {
	profile := schema.ResolveProfile(schema.Model{
		ID:       "deepseek-v4-flash-free",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("opencode"),
		BaseURL:  "https://opencode.ai/zen",
	})
	assert.True(t, profile.Compat.RequiresReasoningContentOnAssistantMessages,
		"embedded opencode variant must require reasoning_content passback")
}

// TestSummarizeRequestIsStrictAppendOfConversation proves the CA1 wire
// contract on the DeepSeek route WITHOUT any provider call: the summarize
// request body (conversation system prompt + tools + history, with the
// instruction appended as the final user message — the shape
// agentic.summarizeHistory builds) serializes to a byte-prefix of the next
// conversation turn's body, minus only the diverging final message. On
// DeepSeek's automatic prefix caching, everything up to that final message
// is a cache hit; the pre-fix shape (summarizer system prompt, no tools)
// missed from token 0.
func TestSummarizeRequestIsStrictAppendOfConversation(t *testing.T) {
	model := schema.Model{
		ID:       "deepseek-v4-flash",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderDeepSeek,
		BaseURL:  "https://api.deepseek.com",
	}
	profile := schema.ResolveProfile(model)
	opts := schema.StreamOptions{}
	tools := []schema.ToolSchema{{
		Name:        "bash",
		Description: "run a command",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
	}}
	history := []schema.Message{
		schema.NewUserMessage("first question"),
		schema.NewAssistantMessage([]schema.ContentBlock{{Type: schema.ContentBlockText, Text: "first answer"}}),
		schema.NewUserMessage("second question"),
		schema.NewAssistantMessage([]schema.ContentBlock{{Type: schema.ContentBlockText, Text: "second answer"}}),
	}
	const systemPrompt = "You are helpful."

	p := ForAPI(schema.ApiOpenAICompletions)
	build := func(msgs []schema.Message) map[string]any {
		body, err := p.BuildRequest(model, schema.Context{
			SystemPrompt: systemPrompt,
			Messages:     msgs,
			Tools:        tools,
		}, opts, profile)
		require.NoError(t, err)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		return req
	}

	// Conversation request: prefix + next user turn.
	conversation := build(append(append([]schema.Message{}, history...), schema.NewUserMessage("third question")))
	// Summarize request (post-CA1 shape): prefix + instruction.
	summarize := build(append(append([]schema.Message{}, history...), schema.NewUserMessage(
		"Summarize the conversation above concisely, preserving key facts, decisions, and context.")))

	convMsgs, ok := conversation["messages"].([]any)
	require.True(t, ok)
	sumMsgs, ok := summarize["messages"].([]any)
	require.True(t, ok)
	require.Equal(t, len(convMsgs), len(sumMsgs),
		"both requests must carry system + history + one trailing user message")

	// Tools arrays must be identical — a tools change realigns every
	// subsequent token and would forfeit the cached prefix.
	assert.Equal(t, conversation["tools"], summarize["tools"], "tools must serialize identically")

	// Every message except the final one must serialize byte-identically:
	// same leading system message, same history — only the trailing user
	// message (turn prompt vs. instruction) diverges.
	marshal := func(v any) string { b, err := json.Marshal(v); require.NoError(t, err); return string(b) }
	for i := 0; i < len(convMsgs)-1; i++ {
		require.Equal(t, marshal(convMsgs[i]), marshal(sumMsgs[i]),
			"message %d must be byte-identical across conversation and summarize requests", i)
	}
	require.NotEqual(t, marshal(convMsgs[len(convMsgs)-1]), marshal(sumMsgs[len(sumMsgs)-1]),
		"only the final user message may differ")

	// The shared leading message is the conversation's own system prompt —
	// the pre-fix failure was a swapped-in summarizer system prompt here.
	first, ok := sumMsgs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "system", first["role"])
	assert.Equal(t, systemPrompt, first["content"])

	// And the trailing summarize message is the user-role instruction.
	last, ok := sumMsgs[len(sumMsgs)-1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user", last["role"])
	assert.Contains(t, last["content"], "Summarize the conversation above")
}
