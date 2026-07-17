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

// TestHTTPTransportHeaderTimeoutFiresOnHang reproduces the bugs.md
// "stuck in sending" hang: the server accepts the connection but never writes
// a response header. The connection-phase timeout must abort the request
// instead of letting it hang forever.
func TestHTTPTransportHeaderTimeoutFiresOnHang(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // never write headers until the test ends
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	tr := &HTTPTransport{Client: server.Client()}
	start := time.Now()
	_, err := tr.Do(context.Background(), &TransportRequest{
		Method:  "POST",
		URL:     server.URL,
		Body:    []byte(`{}`),
		Timeout: int64(150 * time.Millisecond / time.Millisecond),
	})
	require.Error(t, err, "hung header phase must fail, not block forever")
	assert.Less(t, time.Since(start), 3*time.Second, "timeout should fire promptly")
}

// TestHTTPTransportHeaderTimeoutAllowsSlowStream is the slow-local-model
// guard: headers arrive quickly, then the body streams at a pace slower than
// the connection-phase timeout. The read must NOT be killed — only the header
// phase is bounded.
func TestHTTPTransportHeaderTimeoutAllowsSlowStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Stream three chunks 150ms apart — total body time ~450ms, well
		// beyond the 100ms connection-phase timeout.
		for i := 0; i < 3; i++ {
			time.Sleep(150 * time.Millisecond)
			_, _ = w.Write([]byte("data: chunk\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	tr := &HTTPTransport{Client: server.Client()}
	resp, err := tr.Do(context.Background(), &TransportRequest{
		Method:  "POST",
		URL:     server.URL,
		Body:    []byte(`{}`),
		Timeout: int64(100 * time.Millisecond / time.Millisecond),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "slow streaming body must survive the header timeout")
	_ = resp.Body.Close()
	assert.Equal(t, 3, strings.Count(string(body), "data: chunk"))
}

func TestParseSSE(t *testing.T) {
	input := `event: delta
data: {"text":"hello"}

event: delta
data: {"text":" world"}

`
	var events []SSEEvent
	err := ParseSSE(io.NopCloser(strings.NewReader(input)), func(e SSEEvent) bool {
		events = append(events, e)
		return true
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "delta", events[0].Event)
	assert.Equal(t, `{"text":"hello"}`, events[0].Data)
}

// errReader returns (0, err) on every Read, simulating a connection that died
// mid-stream (e.g. an idle-timeout firing ErrStreamIdle).
type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }

// TestParseSSE_PropagatesReadError is the regression test for the silent-
// failure bug: a read error (idle timeout, connection drop) MUST be returned,
// not swallowed. Previously ParseSSE ignored scanner.Err() and the openai-
// completions parser finalized the turn as if the model had finished cleanly,
// ending with no content, no tool calls, and no retry.
func TestParseSSE_PropagatesReadError(t *testing.T) {
	sentinel := io.ErrClosedPipe
	err := ParseSSE(&errReader{err: sentinel}, func(SSEEvent) bool { return true })
	assert.ErrorIs(t, err, sentinel)
}

// TestParseSSE_LargeLine verifies the buffer was raised past the 64KB default
// so a large SSE data line (big tool argument / content chunk) is not silently
// truncated with bufio.ErrTooLong.
func TestParseSSE_LargeLine(t *testing.T) {
	big := strings.Repeat("x", 200*1024) // 200KB, well over the 64KB default
	input := "data: " + big + "\n\n"
	var got string
	err := ParseSSE(strings.NewReader(input), func(e SSEEvent) bool {
		got = e.Data
		return true
	})
	require.NoError(t, err)
	assert.Len(t, got, 200*1024)
}

// TestHTTPRequestSummaryTracing verifies that the HTTP log captures a
// redaction-safe request summary, the request-body tail, and the response
// tail (carrying finish_reason) — the three pieces needed to confirm whether
// a tool result was sent back to the model.
func TestHTTPRequestSummaryTracing(t *testing.T) {
	// Response is an SSE stream whose finish_reason lives at the END.
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"length\"}]}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stream))
	}))
	defer server.Close()

	log := NewHTTPLog(4)
	tr := &HTTPTransport{Client: server.Client(), Log: log}

	// Request body ends with a tool result (the scenario we must be able to
	// detect: "tool result was sent back").
	reqBody := []byte(`{"model":"deepseek-v4-flash","stream":true,` +
		`"messages":[` +
		`{"role":"system","content":"sys"},` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"c1","function":{"name":"read"}}]},` +
		`{"role":"tool","tool_call_id":"c1","content":"file contents"}` +
		`]}`)

	resp, err := tr.Do(context.Background(), &TransportRequest{
		Method: "POST",
		URL:    server.URL,
		Body:   reqBody,
	})
	require.NoError(t, err)
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	entries := log.Snapshot()
	require.Len(t, entries, 1)
	e := entries[0]

	require.NotNil(t, e.RequestSummary)
	assert.Equal(t, "deepseek-v4-flash", e.RequestSummary.Model)
	assert.True(t, e.RequestSummary.Stream)
	assert.Equal(t, 4, e.RequestSummary.MessageCount)
	assert.Equal(t, "tool", e.RequestSummary.LastRole)
	assert.True(t, e.RequestSummary.LastIsToolResult, "last message should be a tool result")
	assert.Equal(t, 1, e.RequestSummary.ToolCallBlocks)
	assert.Equal(t, 1, e.RequestSummary.ToolResultBlocks)

	// Request body tail captured (truncated to last N bytes).
	assert.Contains(t, e.RequestBody, "file contents")

	// Response tail captured and finish_reason extracted from the END.
	assert.Contains(t, e.ResponseTail, "finish_reason")
	assert.Equal(t, "length", e.FinishReason)
}

func TestSummarizeRequestBodyMalformed(t *testing.T) {
	s := summarizeRequestBody([]byte("not json"))
	assert.Equal(t, 0, s.MessageCount)
	assert.Nil(t, requestSummaryPtr(nil))
}

func TestRollingTail(t *testing.T) {
	tail := &rollingTail{cap: 8}
	tail.write([]byte("0123456789ABCDEF")) // 16 bytes, >> cap
	assert.Equal(t, "89ABCDEF", string(tail.bytes()))
}

func TestTruncateTail(t *testing.T) {
	assert.Equal(t, "short", truncateTail("short", 10))
	assert.Equal(t, "...56789", truncateTail("0123456789", 5))
}
