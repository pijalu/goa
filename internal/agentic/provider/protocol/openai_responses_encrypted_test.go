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

// The Responses protocols never send previous_response_id over SSE: the field
// must reference a server-issued response object ("resp_*"), and Goa replays
// its full history every turn, so a client-side session ID there is a hard
// HTTP 400 on strict upstreams (opencode Zen: "previous_response_id must
// start with resp_", export 2026-09-02). Session affinity rides
// prompt_cache_key only — the exact shape opencode sends to the same
// gateways. With no server-side chaining, reasoning continuity is carried by
// the reasoning.encrypted_content include on every flavor (as Codex always
// did); CompatFlags.SupportsEncryptedContent stays the per-model escape
// hatch for backends that reject encrypted content outright.

func responsesBody(t *testing.T, api schema.Api, model schema.Model, opts schema.StreamOptions, profile schema.VariantProfile) map[string]any {
	t.Helper()
	p := ForAPI(api)
	require.NotNil(t, p)
	body, err := p.BuildRequest(model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}, opts, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

func reasoningResponsesModel(api schema.Api) schema.Model {
	return schema.Model{ID: "gpt-5", Api: api, Reasoning: true}
}

// TestOpenAIResponses_SessionAffinityViaPromptCacheKeyOnly pins the root fix
// for the muse-spark 400: a session ID must never land in
// previous_response_id (strict upstreams demand a server-issued resp_* id);
// affinity is prompt_cache_key only, on every flavor.
func TestOpenAIResponses_SessionAffinityViaPromptCacheKeyOnly(t *testing.T) {
	apis := []struct {
		name string
		api  schema.Api
	}{
		{"plain", schema.ApiOpenAIResponses},
		{"azure", schema.ApiAzureOpenAIResponses},
		{"codex", schema.ApiOpenAICodexResponses},
	}
	for _, tc := range apis {
		t.Run(tc.name, func(t *testing.T) {
			req := responsesBody(t, tc.api, reasoningResponsesModel(tc.api),
				schema.StreamOptions{SessionID: "session-123"}, schema.VariantProfile{})

			assert.NotContains(t, req, "previous_response_id",
				"SSE requests must never send the Goa session ID as previous_response_id")
			assert.Equal(t, "session-123", req["prompt_cache_key"],
				"session affinity rides prompt_cache_key only (opencode parity)")
		})
	}
}

// TestOpenAIResponses_EncryptedContentAllFlavors pins the stateless contract:
// with chaining gone, every flavor requests reasoning.encrypted_content so
// reasoning items ride the full-history replay (matching what Codex already
// did and what opencode sends for reasoning models).
func TestOpenAIResponses_EncryptedContentAllFlavors(t *testing.T) {
	apis := []struct {
		name string
		api  schema.Api
	}{
		{"plain", schema.ApiOpenAIResponses},
		{"azure", schema.ApiAzureOpenAIResponses},
		{"codex", schema.ApiOpenAICodexResponses},
	}
	for _, tc := range apis {
		t.Run(tc.name, func(t *testing.T) {
			for _, sessionID := range []string{"", "session-123"} {
				req := responsesBody(t, tc.api, reasoningResponsesModel(tc.api),
					schema.StreamOptions{SessionID: sessionID}, schema.VariantProfile{})

				assert.Equal(t, []any{"reasoning.encrypted_content"}, req["include"],
					"reasoning model must request encrypted content (session %q)", sessionID)
				assert.Contains(t, req, "reasoning", "reasoning request block must stay")
				assert.Contains(t, req, "text", "verbosity request block must stay")
			}
		})
	}
}

// TestOpenAIResponses_EncryptedContentCompatTriState covers the per-model
// escape hatch: CompatFlags.SupportsEncryptedContent overrides the default
// in BOTH directions, independent of session state.
func TestOpenAIResponses_EncryptedContentCompatTriState(t *testing.T) {
	tests := []struct {
		name      string
		flag      *bool
		sessionID string
		want      bool
	}{
		{"explicit false + session", ptr(false), "session-123", false},
		{"explicit false + no session", ptr(false), "", false},
		{"explicit true + session", ptr(true), "session-123", true},
		{"explicit true + no session", ptr(true), "", true},
		{"nil + session (default)", nil, "session-123", true},
		{"nil + no session (default)", nil, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := schema.VariantProfile{
				Compat: schema.CompatFlags{SupportsEncryptedContent: tc.flag},
			}
			req := responsesBody(t, schema.ApiOpenAIResponses,
				reasoningResponsesModel(schema.ApiOpenAIResponses),
				schema.StreamOptions{SessionID: tc.sessionID}, profile)

			if tc.want {
				assert.Contains(t, req, "include")
			} else {
				assert.NotContains(t, req, "include")
			}
		})
	}
}

// TestOpenAIResponses_NonReasoningModelOmitsReasoningFields pins that the
// include/reasoning blocks stay gated on the model actually reasoning.
func TestOpenAIResponses_NonReasoningModelOmitsReasoningFields(t *testing.T) {
	model := schema.Model{ID: "gpt-5-mini", Api: schema.ApiOpenAIResponses}
	req := responsesBody(t, schema.ApiOpenAIResponses, model,
		schema.StreamOptions{SessionID: "session-123"}, schema.VariantProfile{})

	assert.NotContains(t, req, "include")
	assert.NotContains(t, req, "reasoning")
	assert.NotContains(t, req, "previous_response_id")
}

// TestOpenAIResponses_MuseSparkExportRequestShape is the wire-level
// regression for the 2026-09-02 export failure: muse-spark on opencode-go
// (openai-responses flavor, profile falls through to the default) + the Goa
// session ID from the export must produce a body the Zen upstream accepts —
// no previous_response_id, affinity via prompt_cache_key, encrypted-content
// include for the reasoning model.
func TestOpenAIResponses_MuseSparkExportRequestShape(t *testing.T) {
	model := schema.Model{
		ID:        "muse-spark-1.2-contributor-free",
		Provider:  schema.ProviderOpenCodeGo,
		Api:       schema.ApiOpenAIResponses,
		Reasoning: true,
	}
	profile := schema.ResolveProfile(model)

	req := responsesBody(t, schema.ApiOpenAIResponses, model,
		schema.StreamOptions{SessionID: "1788372570_lom323ve"}, profile)

	assert.NotContains(t, req, "previous_response_id",
		"Zen upstream 400s: previous_response_id must start with resp_ (got the session ID)")
	assert.Equal(t, "1788372570_lom323ve", req["prompt_cache_key"],
		"session affinity must ride prompt_cache_key (opencode parity)")
	assert.Equal(t, []any{"reasoning.encrypted_content"}, req["include"],
		"reasoning continuity must ride the include now that nothing chains server-side")
}
