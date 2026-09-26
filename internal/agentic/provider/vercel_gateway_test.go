// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProviderDefaultEndpoint_VercelGateway is the routing half of bugs.md
// "Vercel AI Gateway: no way to add an API key": a catalog gateway whose
// configured endpoint is empty must resolve to the gateway's own
// chat-completions URL — never to api.openai.com — and the model id must be
// passed through verbatim (vendor-namespaced ids like stealth/pixel-canary are
// valid ON the gateway, which is exactly why OpenAI answered 400 invalid model
// ID in the export).
func TestProviderDefaultEndpoint_VercelGateway(t *testing.T) {
	model := schema.Model{
		ID:       "stealth/pixel-canary",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("vercel"),
	}
	got := resolveURL(model, schema.ResolveProfile(model))
	assert.Equal(t, "https://ai-gateway.vercel.sh/v1/chat/completions", got)
	assert.NotContains(t, got, "api.openai.com",
		"an empty endpoint must never fall back to another vendor's host")
}

// TestProviderWithoutEndpoint_FailsActionably asserts the second half of the
// same defect: a provider Goa cannot place (no configured endpoint, no catalog
// base URL) must fail with an error naming the provider instead of silently
// POSTing the request to api.openai.com.
func TestProviderWithoutEndpoint_FailsActionably(t *testing.T) {
	model := schema.Model{
		ID:       "some-model",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("acme-unplaceable"),
	}
	opts := schema.StreamOptions{MaxTokens: 16, APIKey: "sk-test"}

	stream, err := GenericStream(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, opts)
	require.Error(t, err, "a provider with no endpoint must fail, not call another vendor")
	require.Nil(t, stream)
	assert.Contains(t, err.Error(), "acme-unplaceable",
		"the error must name the provider that has no endpoint")
	assert.Contains(t, err.Error(), "endpoint",
		"the error must say what is missing")
}

// TestVercelGateway_RequestRouting is the end-to-end acceptance check: with
// only the catalog env var (AI_GATEWAY_API_KEY) available, a vercel request
// must reach the gateway host carrying the catalog model id and the
// Authorization header derived from that env key.
func TestVercelGateway_RequestRouting(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "gw-env-key")
	t.Setenv("HOME", t.TempDir())

	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &captureTransport{}
	transport.SetDefault(capt)

	model := schema.Model{
		ID:       "stealth/pixel-canary",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("vercel"),
	}
	opts := schema.StreamOptions{MaxTokens: 16}

	stream, err := GenericStream(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, opts)
	require.NoError(t, err)
	require.NotNil(t, stream)
	_ = stream.Result() // block until the terminal handler ran

	require.NotNil(t, capt.req, "the request must actually go out")
	assert.True(t, strings.HasPrefix(capt.req.URL, "https://ai-gateway.vercel.sh/"),
		"request URL = %q, want the Vercel AI Gateway host", capt.req.URL)
	assert.Contains(t, string(capt.req.Body), `"model":"stealth/pixel-canary"`,
		"the catalog model id must be passed through unchanged")
	assert.Equal(t, "Bearer gw-env-key", capt.req.Headers["Authorization"],
		"the catalog env var must authenticate the request")
}

// TestProviderRequestWithoutCredential_ActionableError asserts that a request
// that cannot be authenticated fails BEFORE it is sent, naming the provider and
// the three ways to add a key.
func TestProviderRequestWithoutCredential_ActionableError(t *testing.T) {
	t.Setenv("AI_GATEWAY_API_KEY", "")
	t.Setenv("HOME", t.TempDir())

	old := transport.Default()
	defer transport.SetDefault(old)
	transport.SetDefault(&mockTransport{status: 401, body: `{"error":"unauthorized"}`})

	model := schema.Model{
		ID:       "stealth/pixel-canary",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("vercel"),
	}

	stream, err := GenericStream(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{MaxTokens: 16})
	require.Error(t, err, "no credential must fail before the request goes out")
	require.Nil(t, stream)
	msg := err.Error()
	assert.Contains(t, msg, "vercel", "the error must name the provider")
	assert.Contains(t, msg, "/login:vercel:apikey", "…and the login path")
	assert.Contains(t, msg, "api_key", "…and the config key")
	assert.Contains(t, msg, "AI_GATEWAY_API_KEY", "…and the catalog env var")
}
