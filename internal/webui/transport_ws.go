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
	MsgInput      = "input"
	MsgKey        = "key"
	MsgResize     = "resize"
	MsgHello      = "hello"
	MsgSessionNew = "session_new"
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

// readTimeout bounds how long a silent client may hold the socket before the
// read loop gives up (pings keep it alive).
const readTimeout = 5 * time.Minute

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
	frames  chan *Frame
	control chan Control
	done    chan struct{}
	once    sync.Once

	// consecutive counts frames dropped back-to-back; at slowLimit the client
	// is declared too slow and the hub closes it.
	consecutive atomic.Int64
	slowLimit   int64

	readOnly atomic.Bool
	closed   atomic.Bool
}

var _ Client = (*wsClient)(nil)

// NewWSClient wraps an upgraded connection and starts its writer goroutine.
// When readOnly is set, inbound input is ignored (over-capacity viewer).
func NewWSClient(conn *websocket.Conn, readOnly bool) *wsClient {
	c := &wsClient{
		conn:      conn,
		codec:     NewFrameCodec(),
		frames:    make(chan *Frame, wsQueue),
		control:   make(chan Control, 4),
		done:      make(chan struct{}),
		slowLimit: DefaultSlowClientLimit,
		readOnly:  atomic.Bool{},
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

// Send queues a frame, dropping the oldest one when the queue is full. It
// returns false once the client has missed slowLimit frames in a row.
func (c *wsClient) Send(f *Frame) bool {
	if f == nil || c.closed.Load() {
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
			case c.frames <- f:
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
		c.closed.Store(true)
		close(c.done)
		err = c.conn.Close()
	})
	return err
}

// ReadLoop reads inbound client messages until the socket dies, dispatching
// them through the supplied callbacks. It blocks; the handler runs it inline.
func (c *wsClient) ReadLoop(h ClientHandlers) {
	c.conn.SetReadLimit(MaxFrameBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var m clientMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue // drop malformed input, keep the socket
		}
		c.dispatch(m, h)
	}
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

// writeLoop owns every write to the connection: frames, then controls, until
// the client goes away.
func (c *wsClient) writeLoop() {
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
		}
	}
}

// writeFrame sends one frame, followed by its scrollback batch when the screen
// scrolled. Returns false when the socket died.
func (c *wsClient) writeFrame(f *Frame) bool {
	data, err := c.codec.EncodeFrame(f)
	if err != nil {
		return true // an unencodable frame must not kill the connection
	}
	if !c.write(data) {
		return false
	}
	if len(f.Scrollback) == 0 {
		return true
	}
	batch, err := c.codec.EncodeScrollback(f.Seq, f.Scrollback)
	if err != nil {
		return true
	}
	return c.write(batch)
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
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, data) == nil
}
