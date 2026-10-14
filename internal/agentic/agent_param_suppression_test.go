// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnsupportedParamOf pins the detection of an endpoint's rejected request
// parameter (review finding F6): a gateway whose upstreams validate strictly
// answers 400 unsupported_parameter naming a field the profile sends, and the
// field is ours to drop.
func TestUnsupportedParamOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "the export's body: structured param plus message",
			err: &testResponseError{status: http.StatusBadRequest, body: `{"error":{"param":"thinking",` +
				`"type":"invalid_request_error","message":"Upstream request failed: [unsupported_parameter] ` +
				`Unrecognized request argument supplied: thinking."}}`},
			want: "thinking",
		},
		{
			name: "structured param with code only",
			err: &testResponseError{status: http.StatusBadRequest, body: `{"error":{"code":"unsupported_parameter",` +
				`"param":"reasoning_effort","message":"nope"}}`},
			want: "reasoning_effort",
		},
		{
			name: "message-only phrasing",
			err: &testResponseError{status: http.StatusBadRequest, body: `{"error":{"message":` +
				`"Unrecognized request argument supplied: thinking."}}`},
			want: "thinking",
		},
		{
			name: "unknown parameter phrasing",
			err:  &testResponseError{status: http.StatusBadRequest, body: `{"error":{"message":"Unknown parameter: thinking"}}`},
			want: "thinking",
		},
		{
			name: "422 is the other shape for the same complaint",
			err:  &testResponseError{status: http.StatusUnprocessableEntity, body: `{"error":{"message":"unsupported parameter: temperature"}}`},
			want: "temperature",
		},
		{
			name: "400 without the marker is a real request error",
			err:  &testResponseError{status: http.StatusBadRequest, body: `{"error":{"param":"model","message":"invalid model id"}}`},
			want: "",
		},
		{
			name: "401 is never a parameter complaint",
			err:  &testResponseError{status: http.StatusUnauthorized, body: `{"error":{"message":"Unrecognized request argument supplied: thinking"}}`},
			want: "",
		},
		{
			name: "500 irrelevant",
			err:  &testResponseError{status: http.StatusInternalServerError, body: "boom"},
			want: "",
		},
		{
			name: "empty body",
			err:  &testResponseError{status: http.StatusBadRequest},
			want: "",
		},
		{
			name: "not an HTTP-shaped error",
			err:  errors.New("connection reset"),
			want: "",
		},
		{
			name: "nil",
			err:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, unsupportedParamOf(tt.err))
		})
	}
}

// TestSuppressUnsupportedParam_IsBounded pins the loop bound: one name per
// attempt, never the same name twice, and a finite cap.
func TestSuppressUnsupportedParam_IsBounded(t *testing.T) {
	rejected := func(param string) error {
		return &testResponseError{status: http.StatusBadRequest, body: fmt.Sprintf(
			`{"error":{"param":%q,"type":"unsupported_parameter","message":"Unrecognized request argument supplied: %s."}}`,
			param, param)}
	}

	opts := provider.StreamOptions{}
	narrowed, param, ok := suppressUnsupportedParam(rejected("thinking"), opts)
	require.True(t, ok)
	assert.Equal(t, "thinking", param)
	assert.Equal(t, []string{"thinking"}, narrowed.SuppressParams)

	// Same complaint, same field: nothing left to drop, so the error surfaces
	// instead of looping.
	_, _, ok = suppressUnsupportedParam(rejected("thinking"), narrowed)
	assert.False(t, ok)

	// A different field buys one more attempt ...
	narrowed, param, ok = suppressUnsupportedParam(rejected("reasoning_effort"), narrowed)
	require.True(t, ok)
	assert.Equal(t, "reasoning_effort", param)

	// ... and the budget is finite.
	narrowed, _, ok = suppressUnsupportedParam(rejected("temperature"), narrowed)
	require.True(t, ok)
	_, _, ok = suppressUnsupportedParam(rejected("top_p"), narrowed)
	assert.False(t, ok, "the suppression budget must bound the narrowing")
	assert.Len(t, narrowed.SuppressParams, maxSuppressedParams)
}

func TestAgent_AdaptiveParamSuppressionRetriesWithoutTheField(t *testing.T) {
	api := provider.Api(fmt.Sprintf("test-suppress-%d", testProviderCounter.Add(1)))
	p := &suppressParamProvider{api: api}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:         testModel(api),
		SystemPrompt:  "test",
		Logger:        NewLogger(Error),
		StreamOptions: provider.StreamOptions{MaxRetries: 2},
	})
	runDrained(t, agent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, agent.Run(ctx, "prompt"))

	require.GreaterOrEqual(t, len(p.suppressedPerCall), 2,
		"the rejected field must be dropped and the request retried")
	assert.False(t, p.suppressedPerCall[0],
		"the first attempt carries the field the profile declares")
	assert.True(t, p.suppressedPerCall[1],
		"the retry must not carry the field the endpoint rejected")

	// A later call in the session (a secondary purpose request) must start from
	// the profile again: suppression belongs to the rejected episode, not to
	// the session, or one strict upstream would silently narrow every request
	// that follows.
	for call, suppressed := range p.suppressedPerCall[2:] {
		assert.Falsef(t, suppressed, "call %d inherited the suppression", call+3)
	}
}

// suppressParamProvider rejects the first request the way the opencode gateway
// does (400 unsupported_parameter thinking) and accepts every request after it,
// recording per call whether "thinking" was suppressed.
type suppressParamProvider struct {
	api               provider.Api
	suppressedPerCall []bool
}

func (p *suppressParamProvider) API() provider.Api { return p.api }

func (p *suppressParamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func (p *suppressParamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	suppressed := false
	for _, name := range opts.SuppressParams {
		if name == "thinking" {
			suppressed = true
		}
	}
	p.suppressedPerCall = append(p.suppressedPerCall, suppressed)

	if len(p.suppressedPerCall) == 1 {
		return nil, &testResponseError{status: http.StatusBadRequest, body: `{"error":{"param":"thinking",` +
			`"type":"invalid_request_error","message":"Upstream request failed: [unsupported_parameter] ` +
			`Unrecognized request argument supplied: thinking."}}`}
	}

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Recovered"})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}
