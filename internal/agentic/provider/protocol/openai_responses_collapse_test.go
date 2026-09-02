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

// TestResponsesNoToolsCollapseOmitsToolChoice pins the fix for the force-stop /
// recovery-round 400 on strict Responses upstreams (opencode Zen "Console":
// `only "auto" is supported for tool_choice`, 2026-09-02). The final-step
// text-only collapse (P7) must not send tool_choice "none"; a request carrying
// no tools cannot yield tool calls, so the collapse is expressed by omitting
// both the tools array and the tool_choice key entirely, and by dropping
// parallel_tool_calls. This holds for every Responses flavor (plain, codex,
// azure share buildResponsesBody).
func TestResponsesNoToolsCollapseOmitsToolChoice(t *testing.T) {
	flavors := []struct {
		name   string
		api    schema.Api
		flavor string
	}{
		{name: "plain", api: schema.ApiOpenAIResponses, flavor: ""},
		{name: "codex", api: schema.ApiOpenAICodexResponses, flavor: "codex"},
	}
	for _, f := range flavors {
		t.Run(f.name, func(t *testing.T) {
			model := schema.Model{ID: "muse-spark", Api: f.api, Provider: schema.ProviderOpenAI}
			ctx := schema.Context{
				SystemPrompt: "s",
				NoTools:      true,
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentBlock{{Type: schema.ContentBlockText, Text: "hi"}}},
				},
				Tools: []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
			}
			profile := schema.ResolveProfile(model)

			body, err := buildResponsesBody(model, ctx, schema.StreamOptions{}, profile, f.flavor)
			require.NoError(t, err)
			var m map[string]any
			require.NoError(t, json.Unmarshal(body, &m))

			_, hasChoice := m["tool_choice"]
			assert.False(t, hasChoice, "NoTools collapse must omit tool_choice (strict upstreams 400 on \"none\")")
			_, hasTools := m["tools"]
			assert.False(t, hasTools, "NoTools collapse must omit the tools array")
			_, hasParallel := m["parallel_tool_calls"]
			assert.False(t, hasParallel, "NoTools collapse must drop parallel_tool_calls")
		})
	}
}
