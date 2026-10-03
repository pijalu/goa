// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Session rotation is what `/new` (and `/clear`) does to the conversation id.
// The browser holds the id it was rendered with, so a rotation must reach the
// page — otherwise every reconnect presents an id the server no longer knows,
// the page falls back to a permanently reconnecting stream, and the tab is dead
// until it is reloaded by hand.

// mutableSession is a session id a test can rotate, standing in for
// core.SessionStore's live id.
type mutableSession struct {
	mu sync.Mutex
	id string
}

func (m *mutableSession) get() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.id
}

func (m *mutableSession) set(id string) {
	m.mu.Lock()
	m.id = id
	m.mu.Unlock()
}

// rotationServer starts a server over a rotatable session id.
func rotationServer(t *testing.T, sess *mutableSession) (*Server, *httptest.Server) {
	t.Helper()
	vt := NewVirtualTerminal(40, 10)
	srv := NewServer(vt, 40, 10, ServerOptions{
		Addr:      "127.0.0.1:0",
		SessionID: sess.get,
		Logger:    discardLogger(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close()
	})
	return srv, ts
}

// readControl waits for the next control message of the given kind.
func readControl(t *testing.T, conn *websocket.Conn, kind string) Control {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q control: %v", kind, err)
		}
		var ctrl Control
		if err := json.Unmarshal(data, &ctrl); err != nil {
			continue // a frame; keep looking
		}
		if ctrl.Kind == kind {
			return ctrl
		}
	}
}

// Every attached browser is told where the session moved the moment it does.
func TestRotation_AttachedClientsAreTold(t *testing.T) {
	sess := &mutableSession{id: "sess-1"}
	srv, ts := rotationServer(t, sess)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(ts.URL, "sess-1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	waitFor(t, func() bool { return srv.hub.Clients() == 1 })

	sess.set("sess-2")
	if !srv.broadcastRotation() {
		t.Fatal("rotation was not detected after the session id changed")
	}
	ctrl := readControl(t, conn, CtrlSessionRotated)
	if ctrl.Session != "sess-2" {
		t.Errorf("rotation control session = %q, want the new id", ctrl.Session)
	}
	if srv.broadcastRotation() {
		t.Error("an unchanged id must not broadcast another rotation")
	}
}

// A page that loaded before the rotation still presents the old id. Its socket
// must be accepted (the screen is the same session) and answered with the new
// location, while an id that never existed still 404s.
func TestRotation_StaleIdReconnectsAndIsRedirected(t *testing.T) {
	sess := &mutableSession{id: "sess-1"}
	srv, ts := rotationServer(t, sess)

	sess.set("sess-2")
	if !srv.broadcastRotation() {
		t.Fatal("rotation was not detected")
	}

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(ts.URL, "sess-1"), nil)
	if err != nil {
		t.Fatalf("stale session id refused: %v (status %v)", err, statusOf(resp))
	}
	t.Cleanup(func() { conn.Close() })
	ctrl := readControl(t, conn, CtrlSessionRotated)
	if ctrl.Session != "sess-2" {
		t.Errorf("redirect session = %q, want sess-2", ctrl.Session)
	}

	if _, resp, err := websocket.DefaultDialer.Dial(wsURL(ts.URL, "never-existed"), nil); err == nil {
		t.Fatal("an unknown session id was accepted")
	} else if statusOf(resp) != http.StatusNotFound {
		t.Errorf("unknown session status = %d, want 404", statusOf(resp))
	}
}

// The page itself must follow the rotation: the control is useless if the
// client only records it.
func TestRotation_ClientNavigatesToTheNewSession(t *testing.T) {
	h := newClientHarness(t)
	doc, err := NewFrameCodec().EncodeControl(Control{Kind: CtrlSessionRotated, Session: "sess-9"})
	if err != nil {
		t.Fatalf("encode control: %v", err)
	}
	h.deliver(t, string(doc))

	got := h.call(t, "__replaced").String()
	if got != "/s/sess-9" {
		t.Fatalf("client navigated to %q, want /s/sess-9", got)
	}
}

// statusOf reads the HTTP status of a refused upgrade (0 when there is none).
func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
