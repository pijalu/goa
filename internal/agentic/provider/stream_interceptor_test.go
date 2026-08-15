// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyStreamInterceptorsOrder verifies the waterfall semantics: the first
// interceptor is the outermost wrapper and the last runs closest to the
// terminal handler.
func TestApplyStreamInterceptorsOrder(t *testing.T) {
	var order []string
	mk := func(name string) StreamInterceptor {
		return func(next StreamHandler) StreamHandler {
			return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
				order = append(order, "enter:"+name)
				s, err := next(ctx, req)
				order = append(order, "exit:"+name)
				return s, err
			}
		}
	}
	terminal := func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
		order = append(order, "terminal")
		return nil, nil
	}
	handler := ApplyStreamInterceptors(terminal, mk("a"), mk("b"))
	_, _ = handler(context.Background(), &StreamRequest{})
	assert.Equal(t, []string{"enter:a", "enter:b", "terminal", "exit:b", "exit:a"}, order,
		"first interceptor is outermost, last runs closest to the terminal handler")
}

// TestStreamInterceptorObservesAndTagsCall is the P12 acceptance test: a test
// interceptor can observe the resolved request (model, URL, wire body) and tag
// it (add a header) before the transport executes, and the tag must reach the
// transport.
func TestStreamInterceptorObservesAndTagsCall(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)

	capt := &captureTransport{}
	transport.SetDefault(capt)

	var (
		gotModel   string
		gotURL     string
		gotBodyHas bool
	)
	tagKey := "X-Goa-Test-Tag"
	tagValue := "intercepted"
	interceptor := func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
			gotModel = string(req.Model.ID)
			gotURL = req.URL
			gotBodyHas = strings.Contains(string(req.Body), `"messages"`)
			req.Headers[tagKey] = tagValue
			return next(ctx, req)
		}
	}

	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	stream, err := streamWithInterceptors(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test", Headers: map[string]string{}},
		interceptor)
	require.NoError(t, err)
	require.NoError(t, stream.Err())
	_ = stream.Result()

	assert.Equal(t, "gpt-4o", gotModel)
	assert.Equal(t, "http://example.com/v1/chat/completions", gotURL)
	assert.True(t, gotBodyHas, "interceptor must see the resolved wire body")
	require.NotNil(t, capt.req, "transport should have received a request")
	assert.Equal(t, tagValue, capt.req.Headers[tagKey], "interceptor tag must reach the transport")
}

// TestStreamInterceptorObservesEventStream proves an interceptor can observe
// the event stream: after delegating, it reads the terminal usage from the
// stream result — the same consumption pattern used by cache forensics and
// metrics on the seam.
func TestStreamInterceptorObservesEventStream(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)

	transport.SetDefault(&mockTransport{
		status: 200,
		header: map[string]string{"Content-Type": "text/event-stream"},
		body: `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n" +
			`data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":200,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":100}}}` + "\n\n" +
			`data: [DONE]` + "\n\n",
	})

	usageCh := make(chan *schema.Usage, 1)
	interceptor := func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
			stream, err := next(ctx, req)
			if err != nil || stream == nil {
				return stream, err
			}
			go func() {
				usageCh <- streamResultUsage(stream)
			}()
			return stream, nil
		}
	}

	model := schema.Model{
		ID:       "kimi-k2",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderKimiCode,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	stream, err := streamWithInterceptors(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{SessionID: "sess-1"},
		interceptor)
	require.NoError(t, err)
	require.NoError(t, stream.Err())
	_ = stream.Result()

	select {
	case usage := <-usageCh:
		require.NotNil(t, usage)
		// InputTokens is the NET prompt (prompt_tokens minus cached tokens).
		assert.Equal(t, 100, usage.InputTokens)
		assert.Equal(t, 5, usage.OutputTokens)
		assert.Equal(t, 100, usage.CacheReadTokens)
	case <-time.After(2 * time.Second):
		t.Fatal("interceptor did not observe the stream usage")
	}
}

// TestMetricsInterceptorPreservesOnResponse is the metrics parity test: the
// historical StreamOptions.OnResponse metrics callback must still fire with
// the response status and headers after the transport round-trip when going
// through the canonical chain (GenericStream).
func TestMetricsInterceptorPreservesOnResponse(t *testing.T) {
	ResetCacheForensics()
	defer ResetCacheForensics()
	old := transport.Default()
	defer transport.SetDefault(old)

	transport.SetDefault(&mockTransport{
		status: 200,
		header: map[string]string{"Content-Type": "text/event-stream", "X-RateLimit-Remaining": "42"},
		body:   `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n",
	})

	var gotStatus int
	var gotHeaders map[string]string
	opts := schema.StreamOptions{
		MaxTokens: 10,
		OnResponse: func(status int, headers map[string]string) {
			gotStatus = status
			gotHeaders = headers
		},
	}

	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	stream, err := GenericStream(model, schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, opts)
	require.NoError(t, err)
	require.NoError(t, stream.Err())
	_ = stream.Result()

	assert.Equal(t, 200, gotStatus)
	require.NotNil(t, gotHeaders)
	assert.Equal(t, "42", gotHeaders["X-RateLimit-Remaining"])
}

// TestMetricsInterceptorFiresOnErrorStatus keeps the parity guarantee for the
// failure side: OnResponse is invoked for HTTP error statuses too (the
// transport round-trip completed, so the metrics observation point fires).
func TestMetricsInterceptorFiresOnErrorStatus(t *testing.T) {
	ResetCacheForensics()
	defer ResetCacheForensics()
	old := transport.Default()
	defer transport.SetDefault(old)

	transport.SetDefault(&mockTransport{
		status: 429,
		header: map[string]string{"Content-Type": "application/json"},
		body:   `{"error":{"message":"rate limited","type":"rate_limit"}}`,
	})

	var gotStatus int
	opts := schema.StreamOptions{
		MaxTokens: 10,
		OnResponse: func(status int, _ map[string]string) {
			gotStatus = status
		},
	}

	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	stream, err := GenericStream(model, schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}, opts)
	require.NoError(t, err)
	require.Error(t, stream.Err(), "429 must terminate the stream with an error")
	_ = stream.Result()

	assert.Equal(t, 429, gotStatus)
}

// TestRegisterStreamInterceptorAppendsToCanonicalChain verifies the extension
// API: an interceptor registered via RegisterStreamInterceptor is applied by
// GenericStream.
func TestRegisterStreamInterceptorAppendsToCanonicalChain(t *testing.T) {
	ResetCacheForensics()
	defer ResetCacheForensics()
	old := transport.Default()
	defer transport.SetDefault(old)
	defer func() {
		// Remove the registered interceptor so later tests see the canonical
		// chain only. Since Register only appends, rebuild from the known
		// canonical consumers.
		streamInterceptorsMu.Lock()
		defer streamInterceptorsMu.Unlock()
		streamInterceptorsList = append([]StreamInterceptor(nil), canonicalStreamInterceptors...)
	}()

	capt := &captureTransport{}
	transport.SetDefault(capt)

	const tag = "X-Goa-Registered-Tag"
	const tagVal = "via-register"
	RegisterStreamInterceptor(func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, req *StreamRequest) (*schema.AssistantMessageEventStream, error) {
			if req.Headers == nil {
				req.Headers = make(map[string]string)
			}
			req.Headers[tag] = tagVal
			return next(ctx, req)
		}
	})

	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
		BaseURL:  "http://example.com/v1/chat/completions",
	}
	stream, err := GenericStream(model,
		schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}},
		schema.StreamOptions{MaxTokens: 10, APIKey: "sk-test", Headers: map[string]string{}})
	require.NoError(t, err)
	require.NoError(t, stream.Err())
	_ = stream.Result()

	require.NotNil(t, capt.req)
	assert.Equal(t, tagVal, capt.req.Headers[tag],
		"an interceptor registered via RegisterStreamInterceptor must apply to GenericStream")
}

// TestStreamInterceptorsSnapshotIsIsolated verifies the canonical-chain
// accessor returns a snapshot safe from later registration mutations, and that
// RegisterStreamInterceptor rejects nil.
func TestStreamInterceptorsSnapshotIsIsolated(t *testing.T) {
	before := StreamInterceptors()
	RegisterStreamInterceptor(func(next StreamHandler) StreamHandler { return next })
	after := StreamInterceptors()
	require.Len(t, after, len(before)+1)
	require.Panics(t, func() { RegisterStreamInterceptor(nil) })
	// The snapshot taken before the mutations is unchanged.
	assert.Len(t, before, len(after)-1)

	// Restore the canonical chain.
	streamInterceptorsMu.Lock()
	defer streamInterceptorsMu.Unlock()
	streamInterceptorsList = append([]StreamInterceptor(nil), canonicalStreamInterceptors...)
}
