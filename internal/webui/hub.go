// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import "sync"

// DefaultMaxClients caps how many browsers may attach to one session.
const DefaultMaxClients = 8

// DefaultSlowClientLimit is how many consecutive frames a client may fall
// behind before it is dropped ("client too slow").
const DefaultSlowClientLimit = 30

// Hub is the session-scoped fan-out between the VirtualTerminal and the
// attached transports. It is deliberately dumb: no buffering policy of its own,
// no per-client state beyond the attached set — those live in the transport,
// which knows what "behind" means for its socket. The Hub's single job is to
// hand every published frame to every attached client without ever blocking.
type Hub struct {
	mu      sync.Mutex
	clients map[Client]struct{}

	maxClients int
	slowLimit  int
	closed     bool
}

// NewHub creates a hub. maxClients <= 0 means DefaultMaxClients.
func NewHub(maxClients int) *Hub {
	if maxClients <= 0 {
		maxClients = DefaultMaxClients
	}
	return &Hub{
		clients:    make(map[Client]struct{}),
		maxClients: maxClients,
		slowLimit:  DefaultSlowClientLimit,
	}
}

// Attach registers a client. It returns a detach func (idempotent) and
// readOnly=true when the hub is already full: excess viewers get a live but
// input-less screen rather than a broken page.
func (h *Hub) Attach(c Client) (detach func(), readOnly bool) {
	h.mu.Lock()
	if h.closed || len(h.clients) >= h.maxClients {
		h.mu.Unlock()
		// Still deliver frames to the overflow viewer, just never as driver.
		return func() { _ = c.Close() }, true
	}
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
			_ = c.Close()
		})
	}, false
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

// Clients reports how many clients are attached.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Close detaches every client and refuses further attachments.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	clients := make([]Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = make(map[Client]struct{})
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}
