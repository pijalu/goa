// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPTransportDo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tr := &HTTPTransport{Client: server.Client()}
	resp, err := tr.Do(context.Background(), &TransportRequest{
		Method:  "POST",
		URL:     server.URL,
		Headers: map[string]string{"Authorization": "Bearer token"},
		Body:    []byte(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := ReadAll(resp)
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(body))
}

func TestWebSocketPoolSessionAffinity(t *testing.T) {
	pool := NewWebSocketPool()
	conn := &WebSocketConnection{createdAt: time.Now(), lastUsed: time.Now()}
	pool.Put("session-1", conn)

	got := pool.Get("session-1")
	require.Equal(t, conn, got)

	pool.Remove("session-1")
	require.Nil(t, pool.Get("session-1"))
}

func TestWebSocketConnectionExpired(t *testing.T) {
	conn := &WebSocketConnection{createdAt: time.Now().Add(-60 * time.Minute), lastUsed: time.Now()}
	assert.True(t, conn.IsExpired())
}

func TestMapCodexEvent(t *testing.T) {
	events, err := MapCodexEvent([]byte(`{"type":"response.output_text.delta","delta":"hello"}`))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, schema.EventTextDelta, events[0].Type)
	assert.Equal(t, "hello", events[0].Delta)
}

func TestHTTPTransportTimeoutKeepsBodyAlive(t *testing.T) {
	// A slow streaming endpoint sends headers immediately and the first data
	// chunk after a short delay. Before the transport fix, the per-request
	// timeout context was cancelled when Do() returned, which closed the
	// response body and caused the caller to read EOF instead of the stream.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("data: hello\n\n"))
	}))
	defer server.Close()

	tr := &HTTPTransport{Client: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resp, err := tr.Do(ctx, &TransportRequest{
		Method:  "POST",
		URL:     server.URL,
		Body:    []byte(`{}`),
		Timeout: int64(5 * time.Second / time.Millisecond),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Read the streamed body. With the buggy defer cancel(), this returned
	// an empty result because the body was closed as soon as Do() returned.
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Contains(t, string(body), "hello")
}

func TestParseSSE(t *testing.T) {
	input := `event: delta
data: {"text":"hello"}

event: delta
data: {"text":" world"}

`
	var events []SSEEvent
	ParseSSE(io.NopCloser(strings.NewReader(input)), func(e SSEEvent) bool {
		events = append(events, e)
		return true
	})
	require.Len(t, events, 2)
	assert.Equal(t, "delta", events[0].Event)
	assert.Equal(t, `{"text":"hello"}`, events[0].Data)
}
