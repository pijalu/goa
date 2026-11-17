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

// hubSlot is the per-attachment record: what the client may do, and which
// plane it asked to be served. The mode travels with the client so a detached
// or dropped driver is never miscounted as a viewer (or the reverse) — the
// reason a slot frees correctly. The plane travels with it because the two
// planes ship different documents for the same screen: a cells frame sent to a
// blocks client (or vice versa) would corrupt its view, so every frame is
// fanned out per plane.
type hubSlot struct {
	mode  AttachMode
	plane Plane
}

// Hub is the session-scoped fan-out between the VirtualTerminal and the
// attached transports. It is deliberately dumb: no buffering policy of its own,
// no per-client state beyond the attached set — those live in the transport,
// which knows what "behind" means for its socket. The Hub's single job is to
// hand every published frame to every attached client of the frame's plane
// without ever blocking.
type Hub struct {
	mu sync.Mutex
	// clients maps every attachment to its slot (mode + plane).
	clients map[Client]hubSlot

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
		clients:    make(map[Client]hubSlot),
		maxClients: maxClients,
		maxTotal:   maxClients * hubViewerFactor,
		slowLimit:  DefaultSlowClientLimit,
	}
}

// Attach registers a client for the given plane and reports what it may do.
// The detach func is idempotent; it also runs for a refused client, where its
// only job is to close the socket the caller already opened.
//
// The first maxClients attachments drive; the next maxClients watch. Past that
// the hub refuses rather than growing without bound. The driver cap spans
// planes: a driver is a driver whether it watches cells or blocks.
func (h *Hub) Attach(c Client, plane Plane) (detach func(), mode AttachMode) {
	h.mu.Lock()
	if h.closed || len(h.clients) >= h.maxTotal {
		h.mu.Unlock()
		return func() { _ = c.Close() }, AttachRefused
	}
	mode = AttachViewer
	if h.driversLocked() < h.maxClients {
		mode = AttachDriver
	}
	h.clients[c] = hubSlot{mode: mode, plane: plane}
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
	for _, slot := range h.clients {
		if slot.mode == AttachDriver {
			n++
		}
	}
	return n
}

// Publish fans a frame out to every attached client of the frame's plane,
// dropping the ones that report themselves too far behind. Slow clients are
// closed, not waited on. Clients on the OTHER plane never see this frame: the
// two planes describe the same screen in different documents, and a document
// of the wrong plane would desync its client.
//
// The frame is encoded ONCE here, before the fan-out: the hub is the only place
// that knows how many clients a frame is for, so it is the only place that can
// avoid paying the JSON encode (and the per-run allocation that goes with it)
// once per attached browser. With nobody on the plane nothing is encoded at all.
func (h *Hub) Publish(plane Plane, f *Frame) {
	if f == nil {
		return
	}
	h.mu.Lock()
	if h.closed || len(h.clients) == 0 {
		h.mu.Unlock()
		return
	}
	audience := make([]Client, 0, len(h.clients))
	for c, slot := range h.clients {
		if slot.plane == plane {
			audience = append(audience, c)
		}
	}
	if len(audience) == 0 {
		h.mu.Unlock()
		return
	}
	payload, err := EncodePayload(f)
	if err != nil {
		h.mu.Unlock()
		return
	}
	slow := make([]Client, 0, 1)
	for _, c := range audience {
		if !c.Send(payload) {
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

// Broadcast delivers a control message to every attached client, regardless of
// plane: controls are plane-agnostic (rotation, read-only notices, shutdown).
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

// Clients reports how many clients are attached (drivers and viewers, all planes).
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// HasClients reports whether any client is attached on any plane.
func (h *Hub) HasClients() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.closed && len(h.clients) > 0
}

// HasClientsFor reports whether a frame of the given plane would reach anyone.
// The VirtualTerminal consults it per plane before building a frame, so a
// screen nobody on that plane is watching does not pay for its diff, run
// collapse and encode.
func (h *Hub) HasClientsFor(plane Plane) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	for _, slot := range h.clients {
		if slot.plane == plane {
			return true
		}
	}
	return false
}

// Drivers reports how many attached clients may type.
func (h *Hub) Drivers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.driversLocked()
}

// Close detaches every client and refuses further attachments. Before the
// sockets close, every client is told the session is over: a page that
// receives the bye shows "closed" and stops its reconnect machinery instead
// of retrying a server that is intentionally gone.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	clients := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = make(map[Client]hubSlot)
	h.mu.Unlock()
	bye := Control{Kind: CtrlBye, Text: "session ended"}
	for _, c := range clients {
		_ = c.SendControl(bye)
		_ = c.Close()
	}
}
