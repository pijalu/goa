// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// wsQueue is the per-client frame buffer. Newest-frame-wins: a client that
// cannot keep up skips stale frames instead of accumulating a backlog that
// would show the session seconds behind (spec §7.6).
const wsQueue = 8

// Client→server message types (spec §20).
const (
	MsgInput  = "input"
	MsgKey    = "key"
	MsgResize = "resize"
	MsgHello  = "hello"
)

// clientMsg is the inbound envelope. Only one field is meaningful per type.
type clientMsg struct {
	T    string `json:"t"`
	Data string `json:"data,omitempty"`
	// Key carries the normalized keydown for MsgKey. It is inlined rather
	// than a string so the page never has to reimplement the encoder: it
	// reports what was pressed and the server decides the bytes.
	Key   KeyEvent `json:"key,omitempty"`
	Cols  int      `json:"cols,omitempty"`
	Rows  int      `json:"rows,omitempty"`
	Since uint64   `json:"since,omitempty"`
}

// writeTimeout bounds one socket write, so a peer that stops reading cannot pin
// the writer goroutine (and with it the hub's Publish) on a blocked socket.
const writeTimeout = 10 * time.Second

// missedPongLimit is how many consecutive pings may go unanswered before the
// client is considered gone (spec §11.1: drop after two missed pongs).
const missedPongLimit = 2

// ClientHandlers are the callbacks a transport hands inbound browser events to.
// They are a struct rather than three positional funcs so adding a message
// type (paste, upload, session rotation) does not change every call site.
type ClientHandlers struct {
	// Input receives raw terminal bytes (paste, no-JS form posts).
	Input func(string)
	// Key receives one normalized key event; the transport encodes it with
	// KeyEncoder so the byte table lives in one tested place.
	Key func(KeyEvent)
	// Resize receives the browser's window geometry in cells.
	Resize func(cols, rows int)
	// Hello receives the last frame seq a reconnecting client saw. The grid is
	// authoritative, so nothing has to be replayed.
	Hello func(since uint64)
}

// wsClient is a Client over one WebSocket connection. It owns a writer
// goroutine so Publish never blocks on the socket; reads happen on the
// handler goroutine.
type wsClient struct {
	conn    *websocket.Conn
	codec   *FrameCodec
	frames  chan *Payload
	control chan Control
	done    chan struct{}
	once    sync.Once

	// consecutive counts frames dropped back-to-back; at slowLimit the client
	// is declared too slow and the hub closes it.
	consecutive atomic.Int64
	slowLimit   int64

	// keepalive is the liveness policy: pingEvery drives the writer's ticker,
	// readTimeout the read deadline the pong handler extends. missedPongs
	// counts unanswered pings.
	keepalive   Keepalive
	pingEvery   time.Duration
	readTimeout time.Duration
	missedPongs atomic.Int64

	readOnly atomic.Bool
	closed   atomic.Bool
}

var _ Client = (*wsClient)(nil)

// NewWSClient wraps an upgraded connection and starts its writer goroutine.
// When readOnly is set, inbound input is ignored (over-capacity viewer).
func NewWSClient(conn *websocket.Conn, readOnly bool, ka Keepalive) *wsClient {
	c := &wsClient{
		conn:        conn,
		codec:       NewFrameCodec(),
		frames:      make(chan *Payload, wsQueue),
		control:     make(chan Control, 4),
		done:        make(chan struct{}),
		slowLimit:   DefaultSlowClientLimit,
		keepalive:   ka,
		pingEvery:   ka.ping(),
		readTimeout: ka.read(),
		readOnly:    atomic.Bool{},
	}
	c.readOnly.Store(readOnly)
	go c.writeLoop()
	return c
}

// SetReadOnly switches the client between driving and mirroring. A hub that
// is already full calls this after attaching, so an over-capacity viewer is
// told it is read-only instead of silently typing into a session another
// browser is driving.
func (c *wsClient) SetReadOnly(v bool) { c.readOnly.Store(v) }

// Send queues an already-encoded payload, dropping the oldest one when the
// queue is full. It returns false once the client has missed slowLimit frames
// in a row.
func (c *wsClient) Send(p *Payload) bool {
	if p == nil || c.closed.Load() {
		return false
	}
	// Newest-wins: make room by discarding the stale head.
	for {
		select {
		case <-c.frames:
			if c.consecutive.Add(1) >= c.slowLimit {
				return false
			}
		default:
			select {
			case c.frames <- p:
				c.consecutive.Store(0)
				return true
			case <-c.done:
				return false
			}
		}
	}
}

// SendControl queues a control message.
func (c *wsClient) SendControl(ctrl Control) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	select {
	case c.control <- ctrl:
		return nil
	case <-c.done:
		return net.ErrClosed
	default:
		return nil // control messages are best-effort; never block the publisher
	}
}

// Close shuts the writer down and closes the connection. Idempotent.
func (c *wsClient) Close() error {
	var err error
	c.once.Do(func() {
		// Let a farewell that was just queued ("viewer limit reached", "slow
		// client") reach the socket before it dies: the client is told why it
		// was dropped, which is the difference between a page that reports the
		// reason and one that silently reconnects.
		c.flushControl(closeGracePeriod)
		c.closed.Store(true)
		close(c.done)
		err = c.conn.Close()
	})
	return err
}

// closeGracePeriod bounds how long Close waits for a queued control message to
// be picked up by the writer goroutine.
const closeGracePeriod = 250 * time.Millisecond

// flushControl waits (bounded) until the writer has taken every queued control
// message. It is what makes a goodbye arrive instead of racing the close.
func (c *wsClient) flushControl(wait time.Duration) {
	deadline := time.Now().Add(wait)
	for len(c.control) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

// ReadLoop reads inbound client messages until the socket dies, dispatching
// them through the supplied callbacks. It blocks; the handler runs it inline.
func (c *wsClient) ReadLoop(h ClientHandlers) {
	c.conn.SetReadLimit(MaxFrameBytes)
	c.armReadDeadline()
	// A pong is the proof of life the ping ticker waits for, so it both clears
	// the missed-pong count and pushes the deadline out.
	c.conn.SetPongHandler(func(string) error {
		c.missedPongs.Store(0)
		c.armReadDeadline()
		return nil
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		// Any inbound message is proof of life too: a client that is typing is
		// plainly there, whatever the pong timing says.
		c.missedPongs.Store(0)
		c.armReadDeadline()
		var m clientMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue // drop malformed input, keep the socket
		}
		c.dispatch(m, h)
	}
}

// armReadDeadline (re)sets the read deadline from the keepalive policy.
func (c *wsClient) armReadDeadline() {
	_ = c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
}

// dispatch routes one inbound message. Split out of ReadLoop so the read loop
// stays a plain transport loop and the routing policy is testable on its own.
func (c *wsClient) dispatch(m clientMsg, h ClientHandlers) {
	switch m.T {
	case MsgInput, MsgKey:
		c.dispatchInput(m, h)
	case MsgResize:
		if h.Resize != nil && m.Cols > 0 && m.Rows > 0 {
			h.Resize(m.Cols, m.Rows)
		}
	case MsgHello:
		if h.Hello != nil {
			h.Hello(m.Since)
		}
	}
}

// dispatchInput routes the two input-carrying messages. They share one gate so
// a read-only viewer is refused identically whether it sends raw bytes or a
// key event.
func (c *wsClient) dispatchInput(m clientMsg, h ClientHandlers) {
	if c.readOnly.Load() {
		return
	}
	switch m.T {
	case MsgInput:
		if h.Input != nil {
			h.Input(m.Data)
		}
	case MsgKey:
		if h.Key != nil {
			h.Key(m.Key)
		}
	}
}

// writeLoop owns every write to the connection: frames, then controls, then the
// keepalive ping, until the client goes away.
//
// Whatever makes it stop — a dead socket, two unanswered pings, a Close from
// another goroutine — the connection is torn down on the way out. Leaving it
// open would strand the read loop (and with it the client's hub slot) on a
// socket nobody is writing to any more.
func (c *wsClient) writeLoop() {
	defer c.closeOnWriterExit()
	ping := time.NewTicker(c.pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-c.done:
			return
		case f := <-c.frames:
			if !c.writeFrame(f) {
				return
			}
		case ctrl := <-c.control:
			if !c.writeControl(ctrl) {
				return
			}
		case <-ping.C:
			if !c.sendPing() {
				return
			}
		}
	}
}

// closeOnWriterExit closes the socket when the writer stopped on its own. A
// Close already in progress (or done) owns the teardown, so this is a no-op
// then.
func (c *wsClient) closeOnWriterExit() {
	if c.closed.Load() {
		return
	}
	_ = c.Close()
}

// sendPing writes one ping, giving up on the client once missedPongLimit pings
// in a row went unanswered. A dead peer is not detected by a write — the TCP
// buffer accepts it happily — so the missing pongs are the only signal, and
// closing here is what stops a half-open socket from holding a client slot
// forever.
func (c *wsClient) sendPing() bool {
	if c.missedPongs.Add(1) > missedPongLimit {
		return false
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteMessage(websocket.PingMessage, nil) == nil
}

// writeFrame sends one payload: the frame document, followed by its scrollback
// batch when the screen scrolled. Both were encoded once by the hub, so this
// only writes bytes. Returns false when the socket died.
func (c *wsClient) writeFrame(p *Payload) bool {
	if !c.write(p.Data) {
		return false
	}
	if len(p.Scrollback) == 0 {
		return true
	}
	return c.write(p.Scrollback)
}

func (c *wsClient) writeControl(ctrl Control) bool {
	data, err := c.codec.EncodeControl(ctrl)
	if err != nil {
		return true
	}
	return c.write(data)
}

// write is the single choke point for socket writes: one deadline, one error
// check, so every message type gets the same failure handling.
func (c *wsClient) write(data []byte) bool {
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteMessage(websocket.TextMessage, data) == nil
}
