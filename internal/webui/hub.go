// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import "sync"

// DefaultMaxClients caps how many browsers may *drive* one session.
const DefaultMaxClients = 8

// hubViewerFactor is how many read-only viewers are admitted per driver slot.
// The cap is on drivers (the browsers that can type), not on attachments: a
// shared screen is useful to more than eight people, but every attachment is a
// socket, a goroutine and a frame queue, so the total stays bounded.
const hubViewerFactor = 2

// DefaultSlowClientLimit is how many consecutive frames a client may fall
// behind before it is dropped ("client too slow").
const DefaultSlowClientLimit = 30

// AttachMode is what the hub granted an attaching client. It is an explicit
// value rather than a bool because the three outcomes differ: a driver may
// type, a viewer watches but may not, and a refused client is closed (there is
// no screen to give it, and a frozen one would be a lie).
type AttachMode int

const (
	// AttachRefused means the hub is closed or past every capacity.
	AttachRefused AttachMode = iota
	// AttachDriver means the client may type into the session.
	AttachDriver
	// AttachViewer means the client receives every frame but is read-only.
	AttachViewer
)

// String names the mode (test messages, logs).
func (m AttachMode) String() string {
	switch m {
	case AttachDriver:
		return "driver"
	case AttachViewer:
		return "viewer"
	default:
		return "refused"
	}
}

// ReadOnly reports whether the mode forbids input.
func (m AttachMode) ReadOnly() bool { return m != AttachDriver }

// Hub is the session-scoped fan-out between the VirtualTerminal and the
// attached transports. It is deliberately dumb: no buffering policy of its own,
// no per-client state beyond the attached set — those live in the transport,
// which knows what "behind" means for its socket. The Hub's single job is to
// hand every published frame to every attached client without ever blocking.
type Hub struct {
	mu sync.Mutex
	// clients maps every attachment to what it was granted. The mode travels
	// with the client so a detached or dropped driver is never miscounted as a
	// viewer (or the reverse) — the reason a slot frees correctly.
	clients map[Client]AttachMode

	maxClients int
	maxTotal   int
	slowLimit  int
	closed     bool
}

// NewHub creates a hub. maxClients <= 0 means DefaultMaxClients.
func NewHub(maxClients int) *Hub {
	if maxClients <= 0 {
		maxClients = DefaultMaxClients
	}
	return &Hub{
		clients:    make(map[Client]AttachMode),
		maxClients: maxClients,
		maxTotal:   maxClients * hubViewerFactor,
		slowLimit:  DefaultSlowClientLimit,
	}
}

// Attach registers a client and reports what it may do. The detach func is
// idempotent; it also runs for a refused client, where its only job is to close
// the socket the caller already opened.
//
// The first maxClients attachments drive; the next maxClients watch. Past that
// the hub refuses rather than growing without bound.
func (h *Hub) Attach(c Client) (detach func(), mode AttachMode) {
	h.mu.Lock()
	if h.closed || len(h.clients) >= h.maxTotal {
		h.mu.Unlock()
		return func() { _ = c.Close() }, AttachRefused
	}
	mode = AttachViewer
	if h.driversLocked() < h.maxClients {
		mode = AttachDriver
	}
	h.clients[c] = mode
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
			_ = c.Close()
		})
	}, mode
}

// driversLocked counts the attachments currently allowed to type. The attached
// set is tiny (tens of entries at most), so a count on demand is cheaper than
// maintaining an invariant that a drop path could break.
func (h *Hub) driversLocked() int {
	n := 0
	for _, mode := range h.clients {
		if mode == AttachDriver {
			n++
		}
	}
	return n
}

// Publish fans a frame out to every attached client, dropping the ones that
// report themselves too far behind. Slow clients are closed, not waited on.
func (h *Hub) Publish(f *Frame) {
	if f == nil {
		return
	}
	h.mu.Lock()
	if h.closed || len(h.clients) == 0 {
		h.mu.Unlock()
		return
	}
	slow := make([]Client, 0, 1)
	for c := range h.clients {
		if !c.Send(f) {
			slow = append(slow, c)
		}
	}
	for _, c := range slow {
		// Removing the client from the set is all that is needed: the driver
		// count is derived from the set, so a dropped driver frees its slot.
		delete(h.clients, c)
	}
	h.mu.Unlock()
	for _, c := range slow {
		_ = c.SendControl(Control{Kind: CtrlBye, Text: "slow_client"})
		_ = c.Close()
	}
}

// Broadcast delivers a control message to every attached client.
func (h *Hub) Broadcast(ctrl Control) {
	h.mu.Lock()
	clients := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.SendControl(ctrl)
	}
}

// Clients reports how many clients are attached (drivers and viewers).
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Drivers reports how many attached clients may type.
func (h *Hub) Drivers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.driversLocked()
}

// Close detaches every client and refuses further attachments.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	clients := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = make(map[Client]AttachMode)
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}
