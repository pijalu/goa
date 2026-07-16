// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// k3 (Kimi K3) only emits reasoning_content when the request opts in with
// reasoning_effort. The kimi-code profile previously had thinking_format
// "none", so no reasoning_effort was ever sent and k3 sessions showed no
// thinking. After the fix, a k3 request must carry reasoning_effort:"max"
// (k3 is max-only today; low/high are not yet supported per the Kimi docs).
func TestK3RequestSendsReasoningEffort(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	profile := schema.ResolveProfile(schema.Model{
		ID: "k3", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderKimiCode, Reasoning: true,
	})
	require.Equal(t, "openai", string(profile.Compat.ThinkingFormat),
		"kimi-code profile must use the openai thinking format so reasoning_effort is sent")

	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	body, err := p.BuildRequest(schema.Model{
		ID: "k3", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderKimiCode, Reasoning: true,
	}, ctx, schema.StreamOptions{}, profile)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Equal(t, "max", req["reasoning_effort"],
		"k3 default reasoning effort must be max (k3 is max-only today)")
}

// k3 rejects unknown reasoning_effort values with HTTP 400. A user-configured
// thinking level that k3 does not support (high/medium/low/minimal/xhigh)
// must be mapped to "max" (k3 is max-only); only "none"/"off" disables
// thinking. This guards against the HTTP 400 the Kimi docs warn about.
func TestK3UnsupportedEffortMapsToMax(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	profile := schema.ResolveProfile(schema.Model{
		ID: "k3", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderKimiCode, Reasoning: true,
	})
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}

	for _, level := range []string{"minimal", "low", "medium", "high", "xhigh"} {
		opts := schema.StreamOptions{Reasoning: schema.ThinkingLevel(level)}
		body, err := p.BuildRequest(schema.Model{
			ID: "k3", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderKimiCode, Reasoning: true,
		}, ctx, opts, profile)
		require.NoError(t, err)
		var req map[string]any
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "max", req["reasoning_effort"],
			"thinking level %q must map to max for k3 (max-only), not HTTP-400", level)
	}
}

// The kimi-for-coding model (K2.7 Code, Thinking:ON) shares the kimi-code
// profile. It must ALSO get a reasoning field it accepts — but its behavior
// must not regress to an effort it rejects. Since k3 is the max-only model
// and K2.7 uses Thinking:ON, the openai format's reasoning_effort must be
// acceptable to it (unknown fields are ignored by the server). This test
// documents that the shared profile change is safe for K2.7.
func TestKimiForCodingStillWorks(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	profile := schema.ResolveProfile(schema.Model{
		ID: "kimi-for-coding", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderKimiCode, Reasoning: true,
	})
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	body, err := p.BuildRequest(schema.Model{
		ID: "kimi-for-coding", Api: schema.ApiOpenAICompletions, Provider: schema.ProviderKimiCode, Reasoning: true,
	}, ctx, schema.StreamOptions{}, profile)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	// K2.7 accepts reasoning_effort (Thinking:ON); it must be a value the
	// server maps, not an HTTP-400 trigger.
	effort, _ := req["reasoning_effort"].(string)
	assert.Contains(t, []string{"max", "high", "medium", "low", ""}, effort,
		"kimi-for-coding reasoning_effort must be a server-mappable value")
}

// End-to-end for the plan's after-fix flow: a k3 stream carrying
// reasoning_content must be parsed into EventThinkingDelta events (the
// response half of the k3 thinking fix), kept separate from content.
func TestK3ReasoningContentParsed(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	sse := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"Let me think"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":" about this."}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"The answer is 42."}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
	}, "\n")

	stream := schema.NewAssistantMessageEventStream(16)
	go p.ParseResponse(io.NopCloser(strings.NewReader(sse)), stream)

	var thinking strings.Builder
	var content strings.Builder
	for ev := range stream.Seq() {
		switch ev.Type {
		case schema.EventThinkingDelta:
			thinking.WriteString(ev.Delta)
		case schema.EventTextDelta:
			content.WriteString(ev.Delta)
		}
	}
	assert.Equal(t, "Let me think about this.", thinking.String(), "reasoning_content must be extracted as thinking deltas")
	assert.Equal(t, "The answer is 42.", content.String(), "content must be extracted separately")
}
