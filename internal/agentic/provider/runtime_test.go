// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTransport struct {
	status int
	body   string
	header map[string]string
}

func (m *mockTransport) Do(ctx context.Context, req *transport.TransportRequest) (*transport.TransportResponse, error) {
	return &transport.TransportResponse{
		StatusCode: m.status,
		Headers:    m.header,
		Body:       io.NopCloser(strings.NewReader(m.body)),
	}, nil
}

func TestGenericStreamBuildsPipeline(t *testing.T) {
	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
	}
	ctx := schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}
	opts := schema.StreamOptions{MaxTokens: 100}

	stream, err := GenericStream(model, ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, stream)

	// Wait for the stream to terminate (no URL set, so it ends immediately).
	_ = stream.Result()
}

type captureTransport struct {
	req *transport.TransportRequest
}

func (c *captureTransport) Do(ctx context.Context, req *transport.TransportRequest) (*transport.TransportResponse, error) {
	c.req = req
	return &transport.TransportResponse{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/event-stream"},
		Body:       io.NopCloser(strings.NewReader(`data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n")),
	}, nil
}

func TestGenericStreamSendsOptsHeaders(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)

	capt := &captureTransport{}
	transport.SetDefault(capt)

	model := schema.Model{
		ID:       "kimi-for-coding",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderKimiCode,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	opts := schema.StreamOptions{
		MaxTokens: 10,
		APIKey:    "sk-test",
		Headers:   map[string]string{"User-Agent": "goa/0.1.0-dev", "X-Custom": "custom-value"},
	}
	stream, err := GenericStream(model, schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, opts)
	require.NoError(t, err)
	require.NotNil(t, stream)

	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	_ = stream.Result()

	require.NotNil(t, capt.req, "transport should have received a request")
	assert.Equal(t, "goa/0.1.0-dev", capt.req.Headers["User-Agent"], "User-Agent from opts.Headers should be preserved")
	assert.Equal(t, "custom-value", capt.req.Headers["X-Custom"], "custom header from opts.Headers should be preserved")
	assert.Equal(t, "Bearer sk-test", capt.req.Headers["Authorization"], "auth header should be injected")
}

func TestGenericProviderImplementsApiProvider(t *testing.T) {
	p := NewGenericProvider(schema.ApiOpenAICompletions)
	assert.Equal(t, schema.ApiOpenAICompletions, p.API())

	var _ ApiProvider = p
}

func TestGenericStreamWithMockTransport(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)

	transport.SetDefault(&mockTransport{
		status: 200,
		header: map[string]string{"Content-Type": "text/event-stream"},
		body: `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}` + "\n\n" +
			`data: {"choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}` + "\n\n",
	})

	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	stream, err := GenericStream(model, schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, schema.StreamOptions{MaxTokens: 10})
	require.NoError(t, err)
	require.NotNil(t, stream)

	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, "Hello world", resultText(result))
}

func resultText(result *schema.AssistantMessage) string {
	var text string
	for _, b := range result.Content {
		if b.Type == schema.ContentBlockText {
			text += b.Text
		}
	}
	return text
}
