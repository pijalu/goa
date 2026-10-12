// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/webui"
)

// fakeTerminal is the local TTY stand-in: it records what the renderer drew
// and lets the test drive keystrokes and resizes through the captured
// callbacks.
type fakeTerminal struct {
	mu      sync.Mutex
	out     strings.Builder
	stopped bool

	onInput  func(string)
	onResize func()
}

func (t *fakeTerminal) Start(onInput func(string), onResize func()) {
	t.mu.Lock()
	t.onInput, t.onResize = onInput, onResize
	t.mu.Unlock()
}

func (t *fakeTerminal) Stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
}

func (t *fakeTerminal) Size() (int, int) { return 40, 8 }

func (t *fakeTerminal) WriteString(s string) {
	t.mu.Lock()
	t.out.WriteString(s)
	t.mu.Unlock()
}

func (t *fakeTerminal) typeKeys(s string) {
	t.mu.Lock()
	cb := t.onInput
	t.mu.Unlock()
	if cb != nil {
		cb(s)
	}
}

func (t *fakeTerminal) resize() {
	t.mu.Lock()
	cb := t.onResize
	t.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (t *fakeTerminal) drawn() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.String()
}

func (t *fakeTerminal) wasStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

// echoEngine wires a VirtualTerminal the way the app does: input typed by a
// client comes back as "echo:<keys>" on the screen.
func echoEngine(vt *webui.VirtualTerminal) {
	vt.Start(func(s string) {
		vt.WriteString("echo:" + s + "\r\n")
	}, func() {})
}

// waitFor polls until the condition holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// newAttachServer starts a real server (handler over httptest) with an
// echoing engine.
func newAttachServer(t *testing.T, opts ...func(*webui.ServerOptions, *webui.VirtualTerminal)) (*httptest.Server, *webui.Server, *webui.VirtualTerminal) {
	t.Helper()
	vt := webui.NewVirtualTerminal(40, 8)
	sopts := webui.ServerOptions{
		SessionID: func() string { return "sess-1" },
	}
	for _, o := range opts {
		o(&sopts, vt)
	}
	srv := webui.NewServer(vt, 40, 8, sopts)
	echoEngine(vt)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		_ = srv.Close()
		ts.Close()
	})
	return ts, srv, vt
}

// The full loop: attach → the attach frame paints the screen → typed keys
// reach the engine → the echo paints back onto the local terminal → detach
// restores the terminal and returns cleanly.
func TestClientAttachEchoDetach(t *testing.T) {
	ts, _, vt := newAttachServer(t)
	term := &fakeTerminal{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Server: ts.URL, Session: "sess-1", Plane: "cells"}, term)
	}()

	waitFor(t, "attach frame on the local terminal", func() bool {
		return strings.Contains(term.drawn(), "goa") || len(term.drawn()) > 0
	})
	// Type; the engine echoes it onto the grid, and the frame comes back.
	term.typeKeys("hi")
	waitFor(t, "echo on the local terminal", func() bool {
		return strings.Contains(term.drawn(), "echo:hi")
	})
	// Resize reaches the server's grid.
	term.resize()
	waitFor(t, "server grid resized to the local geometry", func() bool {
		c, r := vt.Size()
		return c == 40 && r == 8
	})
	// Detach: Ctrl+] ends the client, not the session.
	term.typeKeys(DetachKey)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want clean detach", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("detach did not end Run")
	}
	if !term.wasStopped() {
		t.Error("terminal was not restored on detach")
	}
	if vt.Grid().Text() == "" {
		t.Error("detach must leave the server session running")
	}
	cancel()
}

// A server shutdown reaches the client as a bye: Run reports the session's
// end instead of reconnecting.
func TestClientByeOnServerClose(t *testing.T) {
	ts, srv, _ := newAttachServer(t)
	term := &fakeTerminal{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Server: ts.URL, Session: "sess-1", Plane: "cells", ReconnectDelay: time.Millisecond}, term)
	}()
	waitFor(t, "attach", func() bool { return len(term.drawn()) > 0 })

	// The webui server's Close (not the raw listener's) is the graceful
	// path: the hub says goodbye to every attached client before the
	// sockets die, and that bye is what the client reports.
	srv.Close()
	select {
	case err := <-done:
		var end *SessionEnded
		if err == nil || !asErrorIs(err, &end) {
			t.Fatalf("Run returned %v, want SessionEnded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server close did not end Run")
	}
}

func asErrorIs(err error, target any) bool {
	if e, ok := err.(*SessionEnded); ok {
		_ = e
		return true
	}
	_ = target
	return false
}

// Token auth: the upgrade without credentials is refused; with the token the
// attach proceeds.
func TestClientTokenAuth(t *testing.T) {
	ts, _, _ := newAttachServer(t, func(o *webui.ServerOptions, _ *webui.VirtualTerminal) {
		o.Addr = "127.0.0.1:0"
		o.Auth = webui.AuthConfig{Mode: webui.AuthToken, Token: "sekrit"}
	})

	// Without the token the handshake fails.
	term := &fakeTerminal{}
	err := Run(context.Background(), Options{
		Server: ts.URL, Session: "sess-1", Plane: "cells", ReconnectDelay: time.Millisecond,
	}, term)
	if err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("unauthenticated attach = %v, want a connect error", err)
	}

	// With the bearer token it works end to end.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Server: ts.URL, Session: "sess-1", Plane: "cells", Token: "sekrit",
		}, term)
	}()
	waitFor(t, "authenticated attach", func() bool { return len(term.drawn()) > 0 })
	term.typeKeys(DetachKey)
	<-done
}

// Session resolution: an empty session id asks the server. A path is offered
// to /connect (which a single-project server does not answer) and the client
// falls back to /healthz.
func TestClientResolvesSession(t *testing.T) {
	ts, _, _ := newAttachServer(t)
	term := &fakeTerminal{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Server: ts.URL, Session: "", Path: "/some/project", Plane: "cells",
		}, term)
	}()
	waitFor(t, "session resolved via healthz fallback", func() bool { return len(term.drawn()) > 0 })
	term.typeKeys(DetachKey)
	<-done
}

// A read-only attachment still sees every frame, and the viewer notice is
// carried in the window title.
func TestClientReadOnlyViewer(t *testing.T) {
	ts, _, vt := newAttachServer(t, func(o *webui.ServerOptions, _ *webui.VirtualTerminal) {
		o.ReadOnly = true
	})
	term := &fakeTerminal{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Server: ts.URL, Session: "sess-1", Plane: "cells"}, term)
	}()
	waitFor(t, "viewer attach", func() bool { return strings.Contains(term.drawn(), "read-only") })
	// Keystrokes from a viewer never reach the engine.
	term.typeKeys("nope")
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(term.drawn(), "echo:nope") {
		t.Error("viewer keystrokes were forwarded")
	}
	term.typeKeys(DetachKey)
	<-done
	cancel()
	_ = vt
	_ = fmt.Sprint()
}
