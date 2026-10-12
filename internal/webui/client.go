// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Client is one attached viewer (a browser tab). Send must never block the
// publisher: a client that cannot keep up is dropped or skipped, because the
// agent's event stream must not stall behind a slow browser (spec §4.5).
type Client interface {
	// Send queues an already-encoded payload. The Hub encodes a frame once and
	// hands the same Payload to every client, so a transport only writes bytes —
	// it never encodes. It returns false when the client is too far behind to
	// be worth keeping.
	Send(p *Payload) bool
	// SendControl delivers an out-of-band control message.
	SendControl(c Control) error
	// Close detaches the client.
	Close() error
}
