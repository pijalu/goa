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

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebSocketTransportFallbackOnNoServer(t *testing.T) {
	tr := &WebSocketTransport{
		HeaderTimeout: 100 * time.Millisecond,
		SSEEndpoint:   "http://example.com/sse",
	}
	_, err := tr.Do(context.Background(), &TransportRequest{
		Method: "POST",
		URL:    "ws://localhost:1/socket",
		Body:   []byte(`{}`),
	})
	require.Error(t, err)
	assert.IsType(t, &SSEFallbackError{}, err)
}

func TestWebSocketTransportSessionAffinity(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
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
