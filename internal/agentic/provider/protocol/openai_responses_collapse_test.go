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

// TestResponsesNoToolsCollapseKeepsToolSurface pins the fix for the
// 2026-10-05 kimi cache bust AND the strict-upstream constraint from
// 2026-09-02 (opencode Zen "Console": `only "auto" is supported for
// tool_choice`). The final-step text-only collapse (P7) must never mutate the
// cached prompt surface: the tools array stays in the body on the collapse
// round, and strict upstreams keep the previous round's tool_choice — the
// collapse changes no field at all, so the request stays append-only.
// parallel_tool_calls is never dropped either (it is fingerprint-compared by
// the Codex WebSocket reuse path and cannot yield a tool call on its own).
func TestResponsesNoToolsCollapseKeepsToolSurface(t *testing.T) {
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

			assert.Contains(t, m, "tools", "collapse must keep the cached tool surface")
			assert.NotEqual(t, "none", m["tool_choice"],
				"strict Responses upstreams 400 on tool_choice \"none\"")
			if f.flavor == "codex" {
				assert.Equal(t, "auto", m["tool_choice"],
					"codex collapse must repeat the normal round's tool_choice verbatim")
				assert.Equal(t, true, m["parallel_tool_calls"])
			}
		})
	}
}
