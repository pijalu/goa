// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/webui"
)

// The reconnect promise: keystrokes typed while the socket is down are held
// and replayed when the session comes back. The server here is a swappable
// handler behind one stable listener, so the test can "restart" the server
// without the client noticing a URL change.
func TestClientReplaysKeystrokesTypedOffline(t *testing.T) {
	var handlerMu sync.Mutex
	handler := http.Handler(http.NotFoundHandler())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerMu.Lock()
		h := handler
		handlerMu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	serve := func() *webui.Server {
		vt := webui.NewVirtualTerminal(40, 8)
		vt.Start(func(s string) { vt.WriteString("echo:" + s + "\r\n") }, func() {})
		srv := webui.NewServer(vt, 40, 8, webui.ServerOptions{SessionID: func() string { return "sess-1" }})
		return srv
	}
	first := serve()
	handlerMu.Lock()
	handler = first.Handler()
	handlerMu.Unlock()
	t.Cleanup(func() { _ = first.Close() })

	term := &fakeTerminal{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Server: ts.URL, Session: "sess-1", Plane: "cells", ReconnectDelay: time.Millisecond}, term)
	}()
	waitFor(t, "first attach", func() bool { return len(term.drawn()) > 0 })

	// The socket dies at the TRANSPORT level: the aborting handler kills
	// every new attempt (a 404 would be a handshake refusal — final by
	// design), and CloseClientConnections drops the live one without the
	// graceful goodbye that would end the client.
	handlerMu.Lock()
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})
	handlerMu.Unlock()
	ts.CloseClientConnections()
	time.Sleep(300 * time.Millisecond) // the retry loop settles into failure

	// Keystrokes typed while offline: buffered, not lost.
	term.typeKeys("offline")

	// The server "restarts": a fresh session behind the same listener.
	second := serve()
	handlerMu.Lock()
	handler = second.Handler()
	handlerMu.Unlock()
	t.Cleanup(func() { _ = second.Close() })

	waitFor(t, "offline keystrokes replayed after reconnect", func() bool {
		return strings.Contains(term.drawn(), "echo:offline")
	})
	term.typeKeys(DetachKey)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("detach did not end Run")
	}
}

// One message of each control kind takes effect: rotation updates the
// session a reconnect will use, read-only flips the viewer flag, and a bye
// ends the session.
func TestClientControls(t *testing.T) {
	var w bufWriter
	c := &Client{
		opts:    Options{Server: "127.0.0.1:1"},
		screen:  NewScreen(&w, 40, 8),
		sends:   make(chan []byte, 8),
		done:    make(chan struct{}),
		session: "old",
	}
	codec := webui.NewFrameCodec()

	// Rotation: the reconnect target follows the session.
	data, err := codec.EncodeControl(webui.Control{Kind: webui.CtrlSessionRotated, Session: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.dispatch(codec, data); err != nil {
		t.Fatalf("rotation control: %v", err)
	}
	if c.session != "new" {
		t.Errorf("session after rotation = %q, want new", c.session)
	}

	// Read-only: the viewer flag latches and the notice rides the title.
	data, _ = codec.EncodeControl(webui.Control{Kind: webui.CtrlReadOnly, Text: "viewer limit reached"})
	if err := c.dispatch(codec, data); err != nil {
		t.Fatalf("read-only control: %v", err)
	}
	if !c.readOnly {
		t.Error("read-only flag not set")
	}
	if !strings.Contains(w.b.String(), "goa attach [read-only]") {
		t.Error("read-only notice missing from the title")
	}

	// Bye: the session is over — dispatch reports it and the client marks
	// itself finished.
	data, _ = codec.EncodeControl(webui.Control{Kind: webui.CtrlBye, Text: "session ended"})
	if err := c.dispatch(codec, data); err == nil {
		t.Fatal("bye did not end the session")
	}
	if !c.isDone() {
		t.Error("bye did not close the client")
	}
}

// Keystrokes typed with no live connection are buffered, bounded, and kept
// until a connection accepts them.
func TestSendInputBuffersWhileDisconnected(t *testing.T) {
	c := &Client{opts: Options{Server: "x"}, sends: make(chan []byte, 4), done: make(chan struct{})}

	c.sendInput("hello")
	if string(c.pendingInput) != "hello" {
		t.Fatalf("pending = %q, want hello", c.pendingInput)
	}

	// The bound holds: overflow past pendingInputLimit is dropped, not
	// stored.
	c.sendInput(strings.Repeat("x", pendingInputLimit))
	if got := len(c.pendingInput); got != pendingInputLimit {
		t.Fatalf("pending grew to %d, want the %d bound", got, pendingInputLimit)
	}
}

// replayPending keeps the buffer until a connection is live; queue() is
// what it hands the held bytes to once one is.
func TestReplayPendingFlushesOnce(t *testing.T) {
	c := &Client{
		sends:        make(chan []byte, 8),
		done:         make(chan struct{}),
		pendingInput: []byte("buffered"),
	}
	// No conn: replay keeps the buffer.
	c.replayPending()
	if len(c.pendingInput) != 8 {
		t.Fatalf("replay without a connection consumed the buffer")
	}
	// A queued send is observable on the channel when the writer side is
	// bypassed: queue() is what replayPending calls once a conn exists.
	if err := c.queue(map[string]any{"t": webui.MsgInput, "data": "direct"}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	select {
	case got := <-c.sends:
		var m clientMsgProbe
		if err := json.Unmarshal(got, &m); err != nil || m.Data != "direct" {
			t.Errorf("queued = %q (%v)", m.Data, err)
		}
	default:
		t.Fatal("nothing queued")
	}
}

type clientMsgProbe struct {
	Data string `json:"data"`
}

// A cancelled context surfaces from the sleep between reconnects.
func TestSleepCtxReturnsOnCancel(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("plain sleep = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); err == nil {
		t.Error("cancelled sleep returned nil")
	}
}

// The reconnect delay backs off exponentially to the cap.
func TestBackoff(t *testing.T) {
	d := time.Millisecond
	for i := 0; i < 20 && d < 5*time.Second; i++ {
		d = backoff(d)
	}
	if d != 5*time.Second {
		t.Errorf("backoff settled at %v, want the 5s cap", d)
	}
	if backoff(5*time.Second) != 5*time.Second {
		t.Error("backoff grew past the cap")
	}
}

// HandshakeError reports the URL and status, and is recognised by the
// retry loop's refusal check.
func TestHandshakeErrorMessage(t *testing.T) {
	err := &HandshakeError{URL: "ws://h/ws", Status: 401}
	if got := err.Error(); !strings.Contains(got, "ws://h/ws") || !strings.Contains(got, "401") {
		t.Errorf("error text = %q", got)
	}
	if asHandshakeError(err) == nil {
		t.Error("asHandshakeError did not recognise its own type")
	}
	if asHandshakeError(context.Canceled) != nil {
		t.Error("asHandshakeError matched a non-handshake error")
	}
}

// SessionEnded renders its reason.
func TestSessionEndedError(t *testing.T) {
	e := asSessionEnded(&SessionEnded{Reason: "quit"})
	if e == nil || e.Error() != "session ended: quit" {
		t.Errorf("SessionEnded.Error = %q", e.Error())
	}
}
