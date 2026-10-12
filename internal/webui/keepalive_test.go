// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The keepalive policy is the difference between a browser tab that stays
// attached and one that is dropped every few minutes for the crime of not being
// typed into: a viewer sends nothing for minutes, so only a server-side ping
// (and the pong it provokes) keeps the read deadline from expiring.
//
// These tests run the production policy at millisecond scale, so the shipped
// ping/pong path — not a copy of it — is what they exercise.

// dialKeepaliveServer starts a server whose liveness policy is scaled to test
// time and returns a raw client connection plus the server behind it.
func dialKeepaliveServer(t *testing.T, ka Keepalive) (*websocket.Conn, *Server) {
	t.Helper()
	vt := NewVirtualTerminal(40, 10)
	srv := NewServer(vt, 40, 10, ServerOptions{
		Addr:      "127.0.0.1:0",
		SessionID: func() string { return "sess-1" },
		Keepalive: ka,
		Logger:    discardLogger(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close()
	})
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts.URL, "sess-1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, srv
}

// An idle client must survive well past the read deadline: the pings the server
// sends are what keep it alive. Without them the deadline expires and the
// connection is closed under a user who is simply watching.
func TestKeepalive_IdleClientSurvivesPastReadDeadline(t *testing.T) {
	ka := Keepalive{PingInterval: 20 * time.Millisecond, ReadTimeout: 120 * time.Millisecond}
	conn, srv := dialKeepaliveServer(t, ka)

	pings := make(chan struct{}, 16)
	conn.SetPingHandler(func(string) error {
		select {
		case pings <- struct{}{}:
		default:
		}
		// A real browser answers every ping; the pong handler on the server is
		// what extends the deadline.
		return conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second))
	})

	// Read for 6 read-deadlines' worth of silence. Any close surfaces here.
	deadline := time.Now().Add(6 * ka.ReadTimeout)
	_ = conn.SetReadDeadline(deadline)
	closed := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				closed <- err
				return
			}
		}
	}()

	select {
	case err := <-closed:
		t.Fatalf("idle client was dropped after %v (keepalive %+v): %v", 6*ka.ReadTimeout, ka, err)
	case <-time.After(5 * ka.ReadTimeout):
	}
	select {
	case <-pings:
	default:
		t.Fatal("server never pinged an idle client: nothing extends the read deadline")
	}
	if srv.hub.Clients() != 1 {
		t.Errorf("attached = %d, want the idle client still attached", srv.hub.Clients())
	}
}

// A client that stops answering must be dropped: an unresponsive half-open
// socket would otherwise hold a client slot forever.
func TestKeepalive_DropsClientThatNeverPongs(t *testing.T) {
	ka := Keepalive{PingInterval: 20 * time.Millisecond, ReadTimeout: 5 * time.Second}
	conn, srv := dialKeepaliveServer(t, ka)

	// Deliberately never read: gorilla only answers a ping from its read pump,
	// so a client that never reads never pongs.
	waitFor(t, func() bool { return srv.hub.Clients() == 0 })

	// The server closed the socket, so the client's next reads drain whatever
	// was already buffered and then fail. A timeout instead would mean the
	// connection is still open and only the hub forgot about it.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if websocket.IsCloseError(err) || !isTimeout(err) {
				return
			}
			t.Fatalf("client that never ponged was left connected: %v", err)
		}
	}
}

// isTimeout reports whether err is a deadline expiry (net.Error timeouts).
func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	t, ok := err.(timeout)
	return ok && t.Timeout()
}
