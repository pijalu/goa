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

// zaiToolStreamModel is the z.ai coding-plan endpoint (GLM). pi sends
// "tool_stream": true for GLM-4.6+; goa never did (bugs.md: the
// ZaiToolStream compat flag was declared but hardcoded false and never read).
func zaiToolStreamModel() schema.Model {
	return schema.Model{
		ID:       "glm-5.2",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderZai,
		BaseURL:  "https://api.z.ai/api/coding/paas/v4",
	}
}

// buildReq is the shared body builder for the tool_stream payload tests.
func buildReq(t *testing.T, model schema.Model, ctx schema.Context) map[string]any {
	t.Helper()
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	body, err := p.BuildRequest(model, ctx, schema.StreamOptions{}, schema.ResolveProfile(model))
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

// TestZaiSendsToolStreamWithTools pins the wire payload for z.ai: a request
// carrying tools must send the top-level "tool_stream": true so GLM opens its
// dedicated tool-call SSE channel.
func TestZaiSendsToolStreamWithTools(t *testing.T) {
	req := buildReq(t, zaiToolStreamModel(), schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	})
	assert.Equal(t, true, req["tool_stream"], "z.ai request with tools must send tool_stream:true")
	assert.NotEmpty(t, req["tools"])
}

// TestZaiOmitsToolStreamWithoutTools verifies the field is never sent on a
// tools-less request (it is meaningless there and pi omits it too).
func TestZaiOmitsToolStreamWithoutTools(t *testing.T) {
	req := buildReq(t, zaiToolStreamModel(), schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
	})
	_, ok := req["tool_stream"]
	assert.False(t, ok, "no tools -> no tool_stream")
}

// TestZaiOmitsToolStreamOnNoToolsCollapse verifies the final-step collapse
// (P7: tools omitted, tool_choice "none") also drops tool_stream — the field
// must never appear without a tools array.
func TestZaiOmitsToolStreamOnNoToolsCollapse(t *testing.T) {
	req := buildReq(t, zaiToolStreamModel(), schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
		NoTools:  true,
	})
	_, hasTools := req["tools"]
	require.False(t, hasTools, "collapse must omit tools")
	_, ok := req["tool_stream"]
	assert.False(t, ok, "tool_stream must not ride along without a tools array")
}

// TestZaiApiSendsToolStream covers the general (non-coding) z.ai endpoint,
// whose provider id has no dedicated variant profile: the capability is
// resolved from the catalog entry.
func TestZaiApiSendsToolStream(t *testing.T) {
	model := schema.Model{
		ID:       "glm-5.2",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderZaiApi,
		BaseURL:  "https://api.z.ai/api/paas/v4",
	}
	req := buildReq(t, model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	})
	assert.Equal(t, true, req["tool_stream"])
}

// TestZaiToolStreamByURLForCustomProvider verifies a custom provider id pointed
// at a z.ai host still gets the field (endpoint capability, not id capability).
func TestZaiToolStreamByURLForCustomProvider(t *testing.T) {
	model := schema.Model{
		ID:       "glm-5.2",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("custom"),
		BaseURL:  "https://api.z.ai/api/coding/paas/v4",
	}
	req := buildReq(t, model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
		Tools:    []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	})
	assert.Equal(t, true, req["tool_stream"], "z.ai host behind a custom provider id must still get tool_stream")
}

// TestNonZaiProvidersOmitToolStream verifies providers without the capability
// are unaffected (the field is unknown to them).
func TestNonZaiProvidersOmitToolStream(t *testing.T) {
	cases := map[string]schema.Model{
		"openai": {
			ID: "gpt-4o", Api: schema.ApiOpenAICompletions,
			Provider: schema.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
		},
		"deepseek": {
			ID: "deepseek-v4-flash", Api: schema.ApiOpenAICompletions,
			Provider: schema.ProviderDeepSeek, BaseURL: "https://api.deepseek.com",
		},
		"opencode-go": {
			ID: "deepseek-v4-flash", Api: schema.ApiOpenAICompletions,
			Provider: schema.ProviderOpenCodeGo, BaseURL: "https://opencode.ai/zen/go/v1",
		},
	}
	for name, model := range cases {
		t.Run(name, func(t *testing.T) {
			req := buildReq(t, model, schema.Context{
				Messages: []schema.Message{schema.NewUserMessage("hi")},
				Tools:    []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
			})
			_, ok := req["tool_stream"]
			assert.False(t, ok, "%s must not receive tool_stream", name)
		})
	}
}
