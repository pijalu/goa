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

// collapseModel is an OpenAI-completions model for wire tests.
var collapseModel = schema.Model{
	ID:       "test-model",
	Name:     "test-model",
	Api:      schema.ApiOpenAICompletions,
	Provider: schema.ProviderOpenAI,
	BaseURL:  "https://api.openai.com/v1",
}

func collapseToolSchema() schema.ToolSchema {
	return schema.ToolSchema{
		Name:        "read",
		Description: "read a file",
		InputSchema: map[string]any{"type": "object"},
	}
}

// TestNoTools_CollapseOpenAICompletions verifies P7: a NoTools context omits
// the tools array and forces tool_choice "none".
func TestNoTools_CollapseOpenAICompletions(t *testing.T) {
	profile := schema.ResolveProfile(collapseModel)
	body, err := ForAPI(schema.ApiOpenAICompletions).BuildRequest(collapseModel, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{collapseToolSchema()},
		NoTools:  true,
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	_, hasTools := req["tools"]
	assert.False(t, hasTools, "NoTools request must omit the tools array")
	assert.Equal(t, "none", req["tool_choice"], "NoTools request must set tool_choice none")
}

// TestNoTools_CollapseKeepsToolsForNormalRequests verifies the collapse is
// opt-in: without NoTools the tools array is present (byte-identical behavior).
func TestNoTools_CollapseKeepsToolsForNormalRequests(t *testing.T) {
	profile := schema.ResolveProfile(collapseModel)
	body, err := ForAPI(schema.ApiOpenAICompletions).BuildRequest(collapseModel, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{collapseToolSchema()},
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Contains(t, req, "tools", "normal request must keep the tools array")
	_, hasChoice := req["tool_choice"]
	assert.False(t, hasChoice, "normal request must not set tool_choice")
}

// TestNoTools_CollapseMistral verifies the Mistral-conversations builder
// honors NoTools.
func TestNoTools_CollapseMistral(t *testing.T) {
	model := schema.Model{
		ID: "mistral-test", Name: "mistral-test", Api: schema.ApiMistralConversations,
		Provider: schema.ProviderMistral, BaseURL: "https://api.mistral.ai",
	}
	profile := schema.ResolveProfile(model)
	body, err := ForAPI(schema.ApiMistralConversations).BuildRequest(model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{collapseToolSchema()},
		NoTools:  true,
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	_, hasTools := req["tools"]
	assert.False(t, hasTools, "NoTools request must omit the tools array")
	assert.Equal(t, "none", req["tool_choice"])
}

// TestNoTools_CollapseResponses verifies the Responses builder omits both
// the tools array and the tool_choice key: a request carrying no tools cannot
// yield tool calls, so "none" is redundant — and strict Responses upstreams
// (opencode Zen, 2026-09-02) hard-400 on any tool_choice other than "auto".
func TestNoTools_CollapseResponses(t *testing.T) {
	model := schema.Model{
		ID: "resp-test", Name: "resp-test", Api: schema.ApiOpenAIResponses,
		Provider: schema.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
	}
	profile := schema.ResolveProfile(model)
	body, err := ForAPI(schema.ApiOpenAIResponses).BuildRequest(model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{collapseToolSchema()},
		NoTools:  true,
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	_, hasTools := req["tools"]
	assert.False(t, hasTools, "NoTools request must omit the tools array")
	_, hasChoice := req["tool_choice"]
	assert.False(t, hasChoice, "NoTools request must omit tool_choice (strict upstreams 400 on \"none\")")
}

// TestNoTools_CollapseAnthropic verifies the Anthropic builder omits tools
// and sets tool_choice {"type":"none"}.
func TestNoTools_CollapseAnthropic(t *testing.T) {
	model := schema.Model{
		ID: "anthropic-test", Name: "anthropic-test", Api: schema.ApiAnthropicMessages,
		Provider: schema.ProviderAnthropic, BaseURL: "https://api.anthropic.com/v1",
	}
	profile := schema.ResolveProfile(model)
	body, err := ForAPI(schema.ApiAnthropicMessages).BuildRequest(model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{collapseToolSchema()},
		NoTools:  true,
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	_, hasTools := req["tools"]
	assert.False(t, hasTools, "NoTools request must omit the tools array")
	assert.Equal(t, map[string]any{"type": "none"}, req["tool_choice"])
}

// TestNoTools_CollapseGoogle verifies the Google builder omits tools when
// NoTools is set (absence of tools = no tool calls possible).
func TestNoTools_CollapseGoogle(t *testing.T) {
	model := schema.Model{
		ID: "gemini-test", Name: "gemini-test", Api: schema.ApiGoogleGenerativeAI,
		Provider: schema.ProviderGoogle, BaseURL: "https://generativelanguage.googleapis.com/v1beta",
	}
	profile := schema.ResolveProfile(model)
	body, err := ForAPI(schema.ApiGoogleGenerativeAI).BuildRequest(model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{collapseToolSchema()},
		NoTools:  true,
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	_, hasTools := req["tools"]
	assert.False(t, hasTools, "NoTools request must omit the tools array")
}
