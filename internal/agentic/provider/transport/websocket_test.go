// SPDX-License-Identifier: GPL-3.0-or-later

package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebSocketTransportConnectFailureClearError verifies a dead WebSocket
// endpoint surfaces a clear, descriptive error. (Previously this returned a
// never-handled SSEFallbackError; that fallback was dead code — SSEEndpoint was
// never configured anywhere — and has been removed.)
func TestWebSocketTransportConnectFailureClearError(t *testing.T) {
	tr := &WebSocketTransport{HeaderTimeout: 100 * time.Millisecond}
	_, err := tr.Do(context.Background(), &TransportRequest{
		Method: "POST",
		URL:    "ws://localhost:1/socket",
		Body:   []byte(`{}`),
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "websocket connect failed"),
		"want a clear connect-failed error, got %q", err.Error())
}

func TestWebSocketTransportSessionAffinity(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))
		}
	}))
	defer server.Close()

	tr := &WebSocketTransport{}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket"
	resp1, err := tr.Do(context.Background(), &TransportRequest{Method: "POST", URL: url, Headers: map[string]string{"X-Session-ID": "s1"}, Body: []byte(`{"a":1}`)})
	require.NoError(t, err)
	body1, _ := io.ReadAll(resp1.Body)
	assert.Equal(t, `{"a":1}`, string(body1))

	resp2, err := tr.Do(context.Background(), &TransportRequest{Method: "POST", URL: url, Headers: map[string]string{"X-Session-ID": "s1"}, Body: []byte(`{"a":2}`)})
	require.NoError(t, err)
	body2, _ := io.ReadAll(resp2.Body)
	assert.Equal(t, `{"a":2}`, string(body2))
}

// TestWebSocketTransportIdleGuard verifies a server that accepts the request
// but never sends a message is aborted with ErrWSStreamIdle once the idle
// deadline fires, instead of hanging the stream forever.
func TestWebSocketTransportIdleGuard(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		// Read the request but never respond.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, _ = conn.ReadMessage()
		hold := make(chan struct{})
		<-hold // block until the test process tears the server down
	}))
	defer server.Close()

	tr := &WebSocketTransport{IdleTimeout: 150 * time.Millisecond}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket"
	resp, err := tr.Do(context.Background(), &TransportRequest{Method: "POST", URL: url, Body: []byte(`{}`)})
	require.NoError(t, err)

	start := time.Now()
	_, readErr := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	require.Error(t, readErr, "expected idle stream to error")
	assert.True(t, errors.Is(readErr, ErrWSStreamIdle),
		"want ErrWSStreamIdle, got %q (unwrap chain: %v)", readErr, readErr)
	// Must return roughly within the idle deadline, not hang for seconds.
	assert.Less(t, elapsed, 2*time.Second, "idle guard did not fire promptly: %v", elapsed)
}

// TestWebSocketTransport_ConcurrentSameSessionSerializes verifies that two
// concurrent requests on the same session ID serialize on the connection's
// inUse lock rather than corrupting the WebSocket framing. Both must complete
// with their own echoed body intact.
func TestWebSocketTransport_ConcurrentSameSessionSerializes(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	tr := &WebSocketTransport{}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/socket"

	doOne := func(body string) (string, error) {
		resp, err := tr.Do(context.Background(), &TransportRequest{
			Method: "POST", URL: url,
			Headers: map[string]string{"X-Session-ID": "shared"},
			Body:    []byte(body),
		})
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, rerr := io.ReadAll(resp.Body)
		return string(b), rerr
	}

	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	for i, body := range []string{`{"a":1}`, `{"b":2}`} {
		wg.Add(1)
		go func(idx int, b string) {
			defer wg.Done()
			results[idx], errs[idx] = doOne(b)
		}(i, body)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d errored", i)
	}
	// Each response must equal its own request body, not a corrupted mix.
	assert.Contains(t, []string{`{"a":1}`, `{"b":2}`}, results[0])
	assert.Contains(t, []string{`{"a":1}`, `{"b":2}`}, results[1])
	assert.NotEqual(t, results[0], results[1], "two concurrent streams must be distinct")
}

// TestWebSocketConnection_RecordFailureIsRaceFree exercises the failure-counter
// mutation under concurrent callers (run with -race).
func TestWebSocketConnection_RecordFailureIsRaceFree(t *testing.T) {
	conn := &WebSocketConnection{}
	var wg sync.WaitGroup
	for i := 0; i <50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = conn.recordFailure()
		}()
	}
	wg.Wait()
	assert.Equal(t, 50, conn.failures, "with the mutex, all increments are counted")
}
