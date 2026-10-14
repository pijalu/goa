// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zenGoCaptureTransport records the wire body of the opencode-go request and
// answers with a Park-style reasoning reply (reasoning_content then content),
// the shape the gateway returns for cat-jam-free.
type zenGoCaptureTransport struct {
	req  *transport.TransportRequest
	body string
}

func (c *zenGoCaptureTransport) Do(_ context.Context, req *transport.TransportRequest) (*transport.TransportResponse, error) {
	c.req = req
	c.body = string(req.Body)
	resp := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"ok"},"finish_reason":null}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"
	return &transport.TransportResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/event-stream"},
		Body:       io.NopCloser(strings.NewReader(resp)),
	}, nil
}

// The full export path, no network: a cat-jam-free model on the OpenCode Go
// gateway goes through the real pipeline (profile resolution → hooks →
// protocol builder → transport) and the wire body must not carry the "thinking"
// object the upstream rejects with HTTP 400 "[unsupported_parameter]
// Unrecognized request argument supplied: thinking." (exports
// goa-export-20261013-220109 and -222008). The reference client never sends it
// on these surfaces either: models.dev declares the zen gateways with npm
// "@ai-sdk/openai-compatible", which OpenCode maps to its openaiChat protocol
// (reasoning_effort only — packages/core/src/variant.ts).
func TestGenericStream_OpenCodeGoCatJamFreeIsEffortOnly(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &zenGoCaptureTransport{}
	transport.SetDefault(capt)

	model := schema.Model{
		ID:        "cat-jam-free",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderOpenCodeGo,
		BaseURL:   "https://opencode.ai/zen/go/v1/chat/completions",
		Reasoning: true,
	}
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("teqst")}}
	// No explicit level: the main-agent path leaves Reasoning empty and the
	// protocol layer applies the profile/fallback default ("medium").
	stream, err := GenericStream(model, ctx, schema.StreamOptions{APIKey: "test-key"})
	require.NoError(t, err)
	_ = stream.Result()

	require.NotNil(t, capt.req, "the request must reach the transport")
	assert.Equal(t, "https://opencode.ai/zen/go/v1/chat/completions", capt.req.URL)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(capt.body), &body))
	assert.NotContains(t, body, "thinking",
		"the strict upstream rejects the thinking object in every spelling")
	assert.Equal(t, "medium", body["reasoning_effort"],
		"reasoning is controlled by reasoning_effort on the zen gateways")
	assert.Equal(t, "cat-jam-free", body["model"])
	// The reasoning_content echo requirement stays: the upstream 400s with
	// "missing_required_parameter messages[1].reasoning_content" without it.
	assert.True(t, schema.ResolveProfile(model).Compat.RequiresReasoningContentOnAssistantMessages)
}

// A model that explicitly cannot reason gets no reasoning parameters at all,
// not even the "disabled" spelling (same upstreams reject both).
func TestGenericStream_NonReasoningModelSendsNoReasoningFields(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &zenGoCaptureTransport{}
	transport.SetDefault(capt)

	model := schema.Model{
		ID: "cat-jam-free", Api: schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenCodeGo,
		BaseURL:  "https://opencode.ai/zen/go/v1/chat/completions",
	}
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("teqst")}}
	stream, err := GenericStream(model, ctx, schema.StreamOptions{Reasoning: schema.ThinkingOff, APIKey: "test-key"})
	require.NoError(t, err)
	_ = stream.Result()

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(capt.body), &body))
	assert.NotContains(t, body, "thinking")
	assert.NotContains(t, body, "reasoning_effort")
}

// The adaptive-parameter mechanism on the wire (review finding F6): when the
// agent's 400 handler decides an endpoint rejected a request field, the field
// must be gone from the built body — the protocol builders stay unaware, so
// this is the only place that can prove the deletion reaches the wire.
func TestGenericStream_SuppressedParamsLeaveTheWireBody(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)
	capt := &zenGoCaptureTransport{}
	transport.SetDefault(capt)

	model := schema.Model{
		ID: "cat-jam-free", Api: schema.ApiOpenAICompletions,
		Provider:  schema.ProviderOpenCodeGo,
		BaseURL:   "https://opencode.ai/zen/go/v1/chat/completions",
		Reasoning: true,
	}
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("teqst")}}
	stream, err := GenericStream(model, ctx, schema.StreamOptions{
		APIKey:         "test-key",
		SuppressParams: []string{"thinking", "reasoning_effort"},
	})
	require.NoError(t, err)
	_ = stream.Result()

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(capt.body), &body))
	assert.NotContains(t, body, "thinking")
	assert.NotContains(t, body, "reasoning_effort")
	// Everything else the request needs is untouched.
	assert.Equal(t, "cat-jam-free", body["model"])
	assert.Contains(t, body, "messages")
}

// A suppression name the request does not carry must not damage the body, and
// a body that is not a JSON object must be passed through untouched.
func TestSuppressBodyParams_LeavesUnrelatedBodiesAlone(t *testing.T) {
	body := []byte(`{"model":"m","thinking":{"type":"enabled"}}`)

	assert.Equal(t, string(body), string(suppressBodyParams(body, nil)))
	assert.Equal(t, string(body), string(suppressBodyParams(body, []string{"absent"})))

	notAnObject := []byte(`[1,2,3]`)
	assert.Equal(t, string(notAnObject), string(suppressBodyParams(notAnObject, []string{"thinking"})))

	without := suppressBodyParams(body, []string{"thinking"})
	assert.NotContains(t, string(without), "thinking")
	assert.Contains(t, string(without), `"model":"m"`)
}
