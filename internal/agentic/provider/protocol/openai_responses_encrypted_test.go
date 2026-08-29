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

// The Responses protocols request reasoning.encrypted_content (the "include"
// field) only where it can be consumed: STATELESS requests that replay their
// full history. A previous_response_id-chained request keeps the reasoning
// server-side, so requesting encrypted content there is redundant at best —
// and a hard HTTP 400 on strict backends (muse-spark via the OpenCode
// gateways rejects the combination outright, bugs.md 2026-08-29).

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

// TestOpenAIResponses_EncryptedContentOmittedWhenChaining pins the muse-spark
// 400 fix: a chained request (previous_response_id present) must NOT ask for
// reasoning.encrypted_content; the reasoning/text request fields stay.
func TestOpenAIResponses_EncryptedContentOmittedWhenChaining(t *testing.T) {
	req := responsesBody(t, schema.ApiOpenAIResponses, reasoningResponsesModel(schema.ApiOpenAIResponses),
		schema.StreamOptions{SessionID: "session-123"}, schema.VariantProfile{})

	assert.Equal(t, "session-123", req["previous_response_id"])
	assert.Contains(t, req, "reasoning", "reasoning request block must stay")
	assert.Contains(t, req, "text", "verbosity request block must stay")
	assert.NotContains(t, req, "include",
		"chained requests must not request reasoning.encrypted_content (muse upstream 400s on the pair)")
}

// TestOpenAIResponses_EncryptedContentKeptWhenStateless pins the stateless
// half: without chaining the include rides along (full-history replay can
// carry reasoning), matching the existing gpt-5 request pin.
func TestOpenAIResponses_EncryptedContentKeptWhenStateless(t *testing.T) {
	req := responsesBody(t, schema.ApiOpenAIResponses, reasoningResponsesModel(schema.ApiOpenAIResponses),
		schema.StreamOptions{}, schema.VariantProfile{})

	assert.NotContains(t, req, "previous_response_id")
	assert.Contains(t, req, "include")
	assert.Equal(t, []any{"reasoning.encrypted_content"}, req["include"])
}

// TestOpenAIResponses_EncryptedContentCompatTriState covers the per-model
// escape hatch: CompatFlags.SupportsEncryptedContent overrides the default
// stateless-only rule in BOTH directions.
func TestOpenAIResponses_EncryptedContentCompatTriState(t *testing.T) {
	tests := []struct {
		name      string
		flag      *bool
		sessionID string
		want      bool
	}{
		{"explicit false + chained", ptr(false), "session-123", false},
		{"explicit false + stateless", ptr(false), "", false},
		{"explicit true + chained", ptr(true), "session-123", true},
		{"explicit true + stateless", ptr(true), "", true},
		{"nil + chained (default rule)", nil, "session-123", false},
		{"nil + stateless (default rule)", nil, "", true},
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

// TestOpenAIResponses_EncryptedContentFlavorParity keeps the flavors aligned:
// Azure chains exactly like plain (include omitted when chained), while the
// Codex flavor never chains and therefore keeps the include its backend pins.
func TestOpenAIResponses_EncryptedContentFlavorParity(t *testing.T) {
	opts := schema.StreamOptions{SessionID: "session-123"}

	azure := responsesBody(t, schema.ApiAzureOpenAIResponses,
		reasoningResponsesModel(schema.ApiAzureOpenAIResponses), opts, schema.VariantProfile{})
	assert.Equal(t, "session-123", azure["previous_response_id"])
	assert.NotContains(t, azure, "include", "azure chains like plain: no encrypted-content include")

	codex := responsesBody(t, schema.ApiOpenAICodexResponses,
		reasoningResponsesModel(schema.ApiOpenAICodexResponses), opts, schema.VariantProfile{})
	assert.NotContains(t, codex, "previous_response_id", "codex never chains")
	assert.Contains(t, codex, "include", "codex is stateless: keeps the encrypted-content include")
}

// TestOpenAIResponses_MuseSparkChainedRequestOmitsEncryptedContent is the
// wire-level regression for the reported failure: muse-spark-1.2-contributor
// on opencode-go (openai-responses per eecf2f5, profile falls through to the
// default) + a session id must produce a body the muse upstream accepts —
// previous_response_id WITHOUT the encrypted-content include.
func TestOpenAIResponses_MuseSparkChainedRequestOmitsEncryptedContent(t *testing.T) {
	model := schema.Model{
		ID:        "muse-spark-1.2-contributor",
		Provider:  schema.ProviderOpenCodeGo,
		Api:       schema.ApiOpenAIResponses,
		Reasoning: true,
	}
	profile := schema.ResolveProfile(model)

	req := responsesBody(t, schema.ApiOpenAIResponses, model,
		schema.StreamOptions{SessionID: "goa-session-1"}, profile)

	assert.NotContains(t, req, "include",
		"muse upstream 400s on reasoning.encrypted_content + previous_response_id")
	assert.Equal(t, "goa-session-1", req["previous_response_id"])
}
