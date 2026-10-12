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

// collapseContext is the context both the normal round and the collapse round
// are built from.
func collapseContext(noTools bool) schema.Context {
	return schema.Context{
		SystemPrompt: "system prompt",
		Messages: []schema.Message{
			schema.NewUserMessage("hi"),
			schema.NewUserMessage("read it"),
		},
		Tools:   []schema.ToolSchema{collapseToolSchema()},
		NoTools: noTools,
	}
}

// boolPtr returns a pointer to v (profile compat flags are tri-state).
func boolPtr(v bool) *bool { return &v }

// buildFlavorBody builds the wire body of one flavor for a collapse/normal
// round. profileMut, when non-nil, tweaks the resolved profile first.
func buildFlavorBody(t *testing.T, api schema.Api, model schema.Model, noTools bool, profileMut func(*schema.VariantProfile)) map[string]any {
	t.Helper()
	profile := schema.ResolveProfile(model)
	if profileMut != nil {
		profileMut(&profile)
	}
	body, err := ForAPI(api).BuildRequest(model, collapseContext(noTools), schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded), "body must be valid JSON: %s", body)
	return decoded
}

// TestCollapse_KeepsToolSurfaceOpenAICompletions verifies the fix for the
// 2026-10-05 kimi cache bust: the final-step text-only collapse keeps the
// tools array (part of the cached prompt) and expresses its intent on
// tool_choice only.
func TestCollapse_KeepsToolSurfaceOpenAICompletions(t *testing.T) {
	body := buildFlavorBody(t, schema.ApiOpenAICompletions, collapseModel, true, nil)
	assert.Contains(t, body, "tools", "collapse round must keep the cached tool surface")
	assert.Equal(t, "none", body["tool_choice"], "collapse round must set tool_choice none")
}

// TestCollapse_KeepsToolSurfaceResponses pins the Responses default: strict
// upstreams reject tool_choice "none", so the collapse round repeats the
// previous round's tool_choice verbatim and the body stays append-only.
func TestCollapse_KeepsToolSurfaceResponses(t *testing.T) {
	model := schema.Model{
		ID: "resp-test", Name: "resp-test", Api: schema.ApiOpenAIResponses,
		Provider: schema.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
	}
	body := buildFlavorBody(t, schema.ApiOpenAIResponses, model, true, nil)
	assert.Contains(t, body, "tools")
	_, hasChoice := body["tool_choice"]
	assert.False(t, hasChoice, "Responses collapse must not introduce tool_choice by default")

	// An upstream that does accept "none" opts in per model.
	opted := buildFlavorBody(t, schema.ApiOpenAIResponses, model, true, func(p *schema.VariantProfile) {
		p.Compat.SupportsToolChoiceNone = boolPtr(true)
	})
	assert.Contains(t, opted, "tools")
	assert.Equal(t, "none", opted["tool_choice"])
}

// TestCollapse_DisallowedToolChoiceKeepsShape verifies the per-model escape
// hatch: with SupportsToolChoiceNone explicitly false the collapse round is
// byte-canonically identical to a normal round — not one prompt-bearing field
// moves (the append-only guarantee in its strongest form).
func TestCollapse_DisallowedToolChoiceKeepsShape(t *testing.T) {
	for _, api := range []schema.Api{
		schema.ApiOpenAICompletions,
		schema.ApiAnthropicMessages,
		schema.ApiGoogleGenerativeAI,
		schema.ApiMistralConversations,
		schema.ApiOpenAIResponses,
	} {
		t.Run(string(api), func(t *testing.T) {
			model := collapseModel
			model.Api = api
			disable := func(p *schema.VariantProfile) { p.Compat.SupportsToolChoiceNone = boolPtr(false) }
			normal := buildFlavorBody(t, api, model, false, disable)
			collapsed := buildFlavorBody(t, api, model, true, disable)
			assert.Equal(t, normal, collapsed,
				"with tool_choice changes disabled the collapse round must add no field at all")
		})
	}
}

// TestCollapse_PreservesCachedPromptSurface is the regression test for the
// 2026-10-05 export: across every flavor, the collapse round may differ from
// the normal round ONLY in prompt-neutral control fields. Any difference in
// the prompt-bearing surface (tools above all) re-bills the whole prompt
// because it moves the provider's prefix-cache divergence point to the front
// of the conversation.
func TestCollapse_PreservesCachedPromptSurface(t *testing.T) {
	flavors := []struct {
		name       string
		api        schema.Api
		controlKey []string // fields the collapse is allowed to touch
	}{
		{"openai-completions", schema.ApiOpenAICompletions, []string{"tool_choice"}},
		{"anthropic-messages", schema.ApiAnthropicMessages, []string{"tool_choice"}},
		{"mistral-conversations", schema.ApiMistralConversations, []string{"tool_choice"}},
		{"google-generative-ai", schema.ApiGoogleGenerativeAI, []string{"toolConfig"}},
		{"openai-responses", schema.ApiOpenAIResponses, nil},
		{"openai-codex-responses", schema.ApiOpenAICodexResponses, nil},
	}
	for _, flavor := range flavors {
		t.Run(flavor.name, func(t *testing.T) {
			model := collapseModel
			model.Api = flavor.api
			normal := buildFlavorBody(t, flavor.api, model, false, nil)
			collapsed := buildFlavorBody(t, flavor.api, model, true, nil)

			allowed := make(map[string]bool, len(flavor.controlKey))
			for _, key := range flavor.controlKey {
				allowed[key] = true
			}
			for key := range changedTopLevelFields(t, normal, collapsed) {
				assert.True(t, allowed[key],
					"collapse changed %q, which is part of the provider's cached prompt — the request must stay append-only", key)
			}
			assert.Equal(t, normal["tools"], collapsed["tools"],
				"the tool surface must be byte-identical between a normal and a collapse round")
			assert.Equal(t, normal["messages"], collapsed["messages"],
				"the collapse round must not touch the message history")
		})
	}
}

// TestCollapse_RealKimiProfileStaysAppendOnly runs the same invariant against
// the shipped kimi-code profile (the provider in the 2026-10-05 export): the
// collapse round must be an append-only continuation and must pin the text-only
// tool_choice — the combination verified live against api.kimi.com.
func TestCollapse_RealKimiProfileStaysAppendOnly(t *testing.T) {
	model := schema.Model{
		ID:        "k3-256k",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderKimiCode,
		BaseURL:   "https://api.kimi.com/coding/v1",
		Reasoning: true,
	}
	profile := schema.ResolveProfile(model)
	require.True(t, profile.Compat.SupportsPromptCache || profile.ID != "",
		"expected the shipped kimi-code profile to resolve (got %+v)", profile.Match)
	useCatalogProfile := func(p *schema.VariantProfile) { *p = profile }

	normal := buildFlavorBody(t, schema.ApiOpenAICompletions, model, false, useCatalogProfile)
	collapsed := buildFlavorBody(t, schema.ApiOpenAICompletions, model, true, useCatalogProfile)

	for key := range changedTopLevelFields(t, normal, collapsed) {
		assert.Equal(t, "tool_choice", key,
			"collapse changed %q on the kimi profile — prompt-bearing fields must not move", key)
	}
	assert.Equal(t, "none", collapsed["tool_choice"])
	assert.Contains(t, collapsed, "tools")
}

// changedTopLevelFields reports which top-level body fields differ between two
// built bodies (canonical JSON comparison, so key order is irrelevant).
func changedTopLevelFields(t *testing.T, a, b map[string]any) map[string]bool {
	t.Helper()
	changed := map[string]bool{}
	for key := range a {
		if !canonicalJSONEqual(t, a[key], b[key]) {
			changed[key] = true
		}
	}
	for key := range b {
		if _, seen := a[key]; !seen {
			changed[key] = true
		}
	}
	return changed
}

func canonicalJSONEqual(t *testing.T, a, b any) bool {
	t.Helper()
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	require.NoError(t, errA)
	require.NoError(t, errB)
	return string(ab) == string(bb)
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

// TestCollapse_Mistral verifies the Mistral-conversations builder honors
// NoTools on the control field only.
func TestCollapse_Mistral(t *testing.T) {
	model := schema.Model{
		ID: "mistral-test", Name: "mistral-test", Api: schema.ApiMistralConversations,
		Provider: schema.ProviderMistral, BaseURL: "https://api.mistral.ai",
	}
	body := buildFlavorBody(t, schema.ApiMistralConversations, model, true, nil)
	assert.Contains(t, body, "tools", "collapse round must keep the cached tool surface")
	assert.Equal(t, "none", body["tool_choice"])
}

// TestCollapse_Anthropic verifies the Anthropic builder keeps tools and sets
// the text-only tool_choice object.
func TestCollapse_Anthropic(t *testing.T) {
	model := schema.Model{
		ID: "anthropic-test", Name: "anthropic-test", Api: schema.ApiAnthropicMessages,
		Provider: schema.ProviderAnthropic, BaseURL: "https://api.anthropic.com/v1",
	}
	body := buildFlavorBody(t, schema.ApiAnthropicMessages, model, true, nil)
	assert.Contains(t, body, "tools", "collapse round must keep the cached tool surface")
	assert.Equal(t, map[string]any{"type": "none"}, body["tool_choice"])
}

// TestCollapse_Google verifies the Google builder keeps tools and expresses the
// collapse on the function-calling config.
func TestCollapse_Google(t *testing.T) {
	model := schema.Model{
		ID: "gemini-test", Name: "gemini-test", Api: schema.ApiGoogleGenerativeAI,
		Provider: schema.ProviderGoogle, BaseURL: "https://generativelanguage.googleapis.com/v1beta",
	}
	body := buildFlavorBody(t, schema.ApiGoogleGenerativeAI, model, true, nil)
	assert.Contains(t, body, "tools", "collapse round must keep the cached tool surface")
	assert.Equal(t, map[string]any{
		"functionCallingConfig": map[string]any{"mode": "NONE"},
	}, body["toolConfig"])
}
