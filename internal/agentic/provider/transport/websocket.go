// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// HeaderTimeoutError is returned when a WebSocket handshake exceeds the
// configured header timeout.
type HeaderTimeoutError struct {
	URL string
}

func (e *HeaderTimeoutError) Error() string {
	return fmt.Sprintf("websocket header timeout for %s", e.URL)
}

const (
	maxConnectionAge   = 55 * time.Minute
	defaultIdleTimeout = 5 * time.Minute
	maxMessageSize     = 1 << 20 // 1 MiB
	maxStreamFailures  = 3
)

// WebSocketConnection wraps a gorilla websocket connection with metadata.
type WebSocketConnection struct {
	conn      *websocket.Conn
	createdAt time.Time
	lastUsed  time.Time
	failures  int
	mu        sync.Mutex
}

// IsExpired reports whether the connection has exceeded its maximum age.
func (c *WebSocketConnection) IsExpired() bool {
	return time.Since(c.createdAt) > maxConnectionAge
}

// IsIdle reports whether the connection has been idle longer than the timeout.
func (c *WebSocketConnection) IsIdle(timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = defaultIdleTimeout
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastUsed) > timeout
}

// WebSocketPool manages reusable WebSocket connections keyed by session ID.
type WebSocketPool struct {
	dialer      *websocket.Dialer
	mu          sync.RWMutex
	connections map[string]*WebSocketConnection
}

// NewWebSocketPool creates a connection pool.
func NewWebSocketPool() *WebSocketPool {
	return &WebSocketPool{
		dialer:      &websocket.Dialer{HandshakeTimeout: 20 * time.Second},
		connections: make(map[string]*WebSocketConnection),
	}
}

// Get returns an existing connection for the session or nil.
func (p *WebSocketPool) Get(sessionID string) *WebSocketConnection {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connections[sessionID]
}

// Put stores a connection in the pool.
func (p *WebSocketPool) Put(sessionID string, conn *WebSocketConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connections[sessionID] = conn
}

// Remove removes a connection from the pool.
func (p *WebSocketPool) Remove(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.connections, sessionID)
}

// SSEFallbackError indicates WebSocket streaming failed and a fallback to
// Server-Sent Events should be attempted.
type SSEFallbackError struct {
	Reason string
}

func (e *SSEFallbackError) Error() string {
	return fmt.Sprintf("websocket fallback to sse: %s", e.Reason)
}

// MessageTooBigError is returned when a WebSocket message exceeds the
// configured maximum size.
type MessageTooBigError struct {
	Size int
}

func (e *MessageTooBigError) Error() string {
	return fmt.Sprintf("websocket message too big: %d", e.Size)
}

// WebSocketTransport executes WebSocket requests with session affinity.
type WebSocketTransport struct {
	Pool           *WebSocketPool
	HeaderTimeout  time.Duration
	StreamFailures int
	SSEEndpoint    string
}

// Do implements Transport for WebSocket.
func (t *WebSocketTransport) Do(ctx context.Context, req *TransportRequest) (*TransportResponse, error) {
	pool := t.pool()
	sessionID := req.Headers["X-Session-ID"]

	conn, err := t.acquireConnection(ctx, pool, sessionID, req.URL)
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) || isHeaderTimeout(err, t.HeaderTimeout) || isDialFailure(err) {
			return nil, t.fallbackError("handshake failed")
		}
		return nil, err
	}

	if err := conn.conn.WriteMessage(websocket.TextMessage, req.Body); err != nil {
		t.removeOnFailure(pool, sessionID, conn)
		if conn.failures >= t.maxFailures() {
			return nil, t.fallbackError("max stream failures reached")
		}
		return nil, err
	}
	conn.mu.Lock()
	conn.lastUsed = time.Now()
	conn.mu.Unlock()

	return t.streamResponse(conn, sessionID, pool), nil
}

func (t *WebSocketTransport) pool() *WebSocketPool {
	if t.Pool != nil {
		return t.Pool
	}
	return NewWebSocketPool()
}

func (t *WebSocketTransport) maxFailures() int {
	if t.StreamFailures > 0 {
		return t.StreamFailures
	}
	return maxStreamFailures
}

func (t *WebSocketTransport) fallbackError(reason string) error {
	if t.SSEEndpoint != "" {
		return &SSEFallbackError{Reason: reason}
	}
	return errors.New(reason)
}

func (t *WebSocketTransport) acquireConnection(ctx context.Context, pool *WebSocketPool, sessionID, url string) (*WebSocketConnection, error) {
	conn := pool.Get(sessionID)
	if conn != nil && !conn.IsExpired() && !conn.IsIdle(t.HeaderTimeout) {
		return conn, nil
	}
	return t.dialConnection(ctx, pool, sessionID, url)
}

func (t *WebSocketTransport) dialConnection(ctx context.Context, pool *WebSocketPool, sessionID, url string) (*WebSocketConnection, error) {
	dialer := pool.dialer
	if dialer == nil {
		dialer = &websocket.Dialer{HandshakeTimeout: 20 * time.Second}
	}
	if t.HeaderTimeout > 0 {
		dialer.HandshakeTimeout = t.HeaderTimeout
	}

	wsConn, resp, err := dialer.DialContext(ctx, url, http.Header{})
	if err != nil {
		if ctx.Err() != nil {
			return nil, &HeaderTimeoutError{URL: url}
		}
		return nil, err
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	wsConn.SetReadLimit(maxMessageSize)
	conn := &WebSocketConnection{
		conn:      wsConn,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}
	if sessionID != "" {
		pool.Put(sessionID, conn)
	}
	return conn, nil
}

func (t *WebSocketTransport) removeOnFailure(pool *WebSocketPool, sessionID string, conn *WebSocketConnection) {
	conn.failures++
	if sessionID != "" {
		pool.Remove(sessionID)
	}
}

func (t *WebSocketTransport) streamResponse(conn *WebSocketConnection, sessionID string, pool *WebSocketPool) *TransportResponse {
	pr, pw := io.Pipe()
	go t.copyMessages(conn, pw, sessionID, pool)
	return &TransportResponse{
		StatusCode: 200,
		Body:       pr,
		Headers:    map[string]string{"X-Transport": "websocket"},
	}
}

func (t *WebSocketTransport) copyMessages(conn *WebSocketConnection, pw *io.PipeWriter, sessionID string, pool *WebSocketPool) {
	defer pw.Close()
	for {
		_, msg, err := conn.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
				_ = pw.CloseWithError(&MessageTooBigError{Size: len(msg)})
				return
			}
			t.removeOnFailure(pool, sessionID, conn)
			return
		}
		conn.mu.Lock()
		conn.lastUsed = time.Now()
		conn.mu.Unlock()
		if _, err := pw.Write(msg); err != nil {
			return
		}
	}
}

func isHeaderTimeout(err error, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "timeout")
}

func isDialFailure(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
