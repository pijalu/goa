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

// The browser reports *what was pressed*; the server decides the bytes. This is
// the seam that keeps input fidelity: the page cannot ship a key the encoder
// does not know, and the encoder is unit-tested.
func TestServer_WebSocketKeyMessageReachesEngine(t *testing.T) {
	conn, _, inputs := dialTestServer(t)
	readFrame(t, conn) // drain the initial full frame

	if err := conn.WriteJSON(clientMsg{T: MsgKey, Key: KeyEvent{Key: "a", Ctrl: true}}); err != nil {
		t.Fatalf("write key: %v", err)
	}
	select {
	case in := <-inputs:
		if in != "\x01" {
			t.Errorf("engine input = %q, want %q", in, "\x01")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("key never reached the engine")
	}
}

// Read-only is enforced at the socket: a viewer's keystrokes never reach the
// engine, and the client is told why instead of typing into a void.
func TestServer_ReadOnlyClientInputIsDropped(t *testing.T) {
	conn, _, inputs := dialTestServerOpts(t, ServerOptions{ReadOnly: true})

	// The full frame and the read-only notice race on the same socket (both
	// are queued before the writer goroutine runs), so skip frames until the
	// control arrives — a reconnecting client does the same.
	ctrl := waitForControl(t, conn)
	if ctrl.Kind != CtrlReadOnly || ctrl.Text != "server is read-only" {
		t.Fatalf("control = %+v, want a read_only notice", ctrl)
	}

	if err := conn.WriteJSON(clientMsg{T: MsgKey, Key: KeyEvent{Key: "a"}}); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := conn.WriteJSON(clientMsg{T: MsgInput, Data: "sneaky\r"}); err != nil {
		t.Fatalf("write input: %v", err)
	}
	select {
	case in := <-inputs:
		t.Fatalf("read-only client drove the engine with %q", in)
	case <-time.After(200 * time.Millisecond):
	}
}

// A key the encoder declines (modifier-only press, Meta chord) must not be
// turned into bytes, so the browser keeps its own shortcuts.
func TestServer_UnencodableKeyIsIgnored(t *testing.T) {
	conn, _, inputs := dialTestServer(t)
	readFrame(t, conn)

	if err := conn.WriteJSON(clientMsg{T: MsgKey, Key: KeyEvent{Key: "r", Ctrl: true, Meta: true}}); err != nil {
		t.Fatalf("write key: %v", err)
	}
	select {
	case in := <-inputs:
		t.Fatalf("unencodable key produced %q", in)
	case <-time.After(200 * time.Millisecond):
	}
}

// Paste arrives as raw text in one message and is delivered verbatim, so the
// editor applies its own paste handling exactly as for a terminal paste.
func TestServer_PasteReachesEngineVerbatim(t *testing.T) {
	conn, _, inputs := dialTestServer(t)
	readFrame(t, conn)

	text := "line one\nline two\tindented\n"
	if err := conn.WriteJSON(clientMsg{T: MsgInput, Data: text}); err != nil {
		t.Fatalf("write paste: %v", err)
	}
	select {
	case in := <-inputs:
		if in != text {
			t.Errorf("engine input = %q, want %q", in, text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("paste never reached the engine")
	}
}

// waitForControl reads until the first control message arrives, skipping the
// full frame the server sends to every joining client.
func waitForControl(t *testing.T, conn *websocket.Conn) Control {
	t.Helper()
	codec := NewFrameCodec()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = conn.SetReadDeadline(deadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read message: %v", err)
		}
		// DecodeControl parses any envelope; only a non-empty kind is a
		// control message rather than a frame.
		if ctrl, err := codec.DecodeControl(data); err == nil && ctrl.Kind != "" {
			return ctrl
		}
	}
}

// dialTestServerOpts starts a server with extra options and returns a socket,
// so read-only and normal wiring share one harness.
func dialTestServerOpts(t *testing.T, opts ServerOptions) (*websocket.Conn, *VirtualTerminal, chan string) {
	t.Helper()
	vt := NewVirtualTerminal(40, 10)
	inputs := make(chan string, 4)
	vt.Start(func(s string) { inputs <- s }, func() {})
	opts.SessionID = func() string { return "sess-1" }
	opts.Logger = discardLogger()
	srv := NewServer(vt, 40, 10, opts)
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
	return conn, vt, inputs
}
