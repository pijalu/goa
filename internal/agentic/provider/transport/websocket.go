// SPDX-License-Identifier: GPL-3.0-or-later

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

	// wsStreamIdleTimeout is the maximum gap between WebSocket messages before
	// the stream is treated as stalled. It mirrors the SSE idle guard so a
	// half-open WS connection (proxy timeout, dropped route) cannot hang a
	// whole agent turn. The agent classifies the resulting error as transient
	// (its message contains "idle timeout") and retries.
	wsStreamIdleTimeout = 2 * time.Minute
)

// ErrWSStreamIdle is returned when no WebSocket message arrives within the
// idle timeout. Its text contains "idle timeout" so the agent's transient-
// error classifier treats it as retryable, matching SSE semantics.
var ErrWSStreamIdle = errors.New("websocket stream idle timeout: no message received within deadline")

// WebSocketConnection wraps a gorilla websocket connection with metadata.
type WebSocketConnection struct {
	conn      *websocket.Conn
	createdAt time.Time

	// mu guards lastUsed and failures.
	mu       sync.Mutex
	lastUsed time.Time
	failures int

	// inUse is held for the lifetime of a single request stream (write request
	// + read all response messages). A WebSocket connection is a framed,
	// non-multiplexed protocol: two concurrent streams sharing one conn would
	// interleave Read/Write frames and corrupt the protocol. Holding this lock
	// serializes per-connection use; a concurrent Do that resolves to the same
	// pooled conn blocks until the in-flight stream finishes.
	inUse sync.Mutex
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

// recordFailure increments the failure counter under mu and returns the new
// count. Callers decide whether the count exceeds the threshold to evict the
// connection.
func (c *WebSocketConnection) recordFailure() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	return c.failures
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
	// IdleTimeout bounds the gap between received messages. Zero falls back to
	// wsStreamIdleTimeout. A stalled connection is closed with ErrWSStreamIdle
	// so the agent can retry instead of hanging.
	IdleTimeout time.Duration
}

// Do implements Transport.
//
// A single connection serves one request stream at a time: Do takes the
// connection's inUse lock for the whole stream (write + background read), so
// concurrent requests on the same session serialize rather than corrupt the
// WebSocket framing. The background reader (copyMessages) releases the lock.
func (t *WebSocketTransport) Do(ctx context.Context, req *TransportRequest) (*TransportResponse, error) {
	pool := t.pool()
	sessionID := req.Headers["X-Session-ID"]

	conn, err := t.acquireConnection(ctx, pool, sessionID, req.URL)
	if err != nil {
		// Handshake / dial failure: surface a clear, descriptive error. The
		// agent classifies dial/header failures as transient and retries.
		return nil, fmt.Errorf("websocket connect failed: %w", err)
	}

	// Serialize per-connection use (see WebSocketConnection.inUse).
	conn.inUse.Lock()

	if err := conn.conn.WriteMessage(websocket.TextMessage, req.Body); err != nil {
		conn.inUse.Unlock() // no reader goroutine started; release here.
		t.removeOnFailure(pool, sessionID, conn)
		return nil, fmt.Errorf("websocket write failed: %w", err)
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

// idleTimeout returns the configured message-idle deadline, defaulting to
// wsStreamIdleTimeout.
func (t *WebSocketTransport) idleTimeout() time.Duration {
	if t.IdleTimeout > 0 {
		return t.IdleTimeout
	}
	return wsStreamIdleTimeout
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

// removeOnFailure bumps the connection's failure counter and, if it exceeds
// the threshold or has no session affinity, evicts it from the pool. The
// failure counter is mutated under conn.mu to avoid the data race with the
// background reader goroutine.
func (t *WebSocketTransport) removeOnFailure(pool *WebSocketPool, sessionID string, conn *WebSocketConnection) {
	if conn.recordFailure() >= t.maxFailures() {
		if sessionID != "" {
			pool.Remove(sessionID)
		}
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

// copyMessages reads frames from the WebSocket into the pipe until the stream
// ends. It enforces an idle deadline (so a stalled connection surfaces
// ErrWSStreamIdle instead of hanging forever) and releases the connection's
// inUse lock when done so the next request on that session can proceed.
func (t *WebSocketTransport) copyMessages(conn *WebSocketConnection, pw *io.PipeWriter, sessionID string, pool *WebSocketPool) {
	defer pw.Close()
	defer conn.inUse.Unlock()

	idle := t.idleTimeout()
	for {
		if idle > 0 {
			_ = conn.conn.SetReadDeadline(time.Now().Add(idle))
		}
		_, msg, err := conn.conn.ReadMessage()
		if err != nil {
			if isDeadlineTimeout(err) {
				_ = pw.CloseWithError(ErrWSStreamIdle)
				return
			}
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

// isDeadlineTimeout reports whether err is a network read-deadline timeout
// (gorilla surfaces a *net.OpError whose Timeout() is true when the
// SetReadDeadline fires).
func isDeadlineTimeout(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	// Some close paths wrap the timeout in a generic close error whose text
	// mentions the deadline; match defensively.
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}
