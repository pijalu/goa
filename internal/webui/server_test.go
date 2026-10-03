// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T, cols, rows int) (*Server, *VirtualTerminal) {
	t.Helper()
	vt := NewVirtualTerminal(cols, rows)
	vt.Start(func(string) {}, func() {})
	srv := NewServer(vt, cols, rows, ServerOptions{
		SessionID: func() string { return "sess-1" },
		Logger:    discardLogger(),
	})
	t.Cleanup(func() { _ = srv.Close() })
	return srv, vt
}

func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// The bare host must always land on the current session view.
func TestServer_RedirectsRootToSession(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/s/sess-1" {
		t.Errorf("Location = %q, want /s/sess-1", loc)
	}
}

func TestServer_ServesSessionPage(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`data-session="sess-1"`, "/assets/app.css", "/assets/app.js", `id="grid"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestServer_UnknownSessionIs404(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/other", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Before the agent session exists the id is empty: the canonical URL must
// still serve the page (and the text mirror) rather than 404.
func TestServer_EmptySessionPathIsServed(t *testing.T) {
	vt := NewVirtualTerminal(20, 2)
	srv := NewServer(vt, 20, 2, ServerOptions{
		SessionID: func() string { return "" },
		Logger:    discardLogger(),
	})
	defer func() { _ = srv.Close() }()
	vt.WriteString("\x1b[1;1Hno session yet")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/s/ status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/text", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "no session yet") {
		t.Errorf("text mirror = %d %q", rec.Code, rec.Body.String())
	}
}

func TestSplitSessionPath(t *testing.T) {
	tests := []struct{ path, id, sub string }{
		{"", "", ""},
		{"/", "", ""},
		{"abc", "abc", ""},
		{"abc/text", "abc", "text"},
		{"/abc/", "abc", ""},
	}
	for _, tc := range tests {
		id, sub := splitSessionPath(tc.path)
		if id != tc.id || sub != tc.sub {
			t.Errorf("splitSessionPath(%q) = (%q,%q), want (%q,%q)", tc.path, id, sub, tc.id, tc.sub)
		}
	}
}

func TestServer_Healthz(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var health struct {
		OK      bool   `json:"ok"`
		Session string `json:"session"`
		Clients int    `json:"clients"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("healthz body %q: %v", rec.Body.String(), err)
	}
	if !health.OK || health.Session != "sess-1" || health.Clients != 0 {
		t.Errorf("healthz = %+v", health)
	}
}

func TestServer_ServesEmbeddedAssets(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	for _, tc := range []struct{ name, ctype, want string }{
		{"app.css", "text/css", "--accent"},
		{"app.js", "text/javascript", "WebSocket"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/"+tc.name, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d", tc.name, rec.Code)
			continue
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.ctype) {
			t.Errorf("%s content-type = %q", tc.name, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s body missing %q", tc.name, tc.want)
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/nope.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", rec.Code)
	}
}

func TestServer_TextMirror(t *testing.T) {
	srv, vt := newTestServer(t, 30, 3)
	vt.WriteString("\x1b[1;1Hhello goa")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1/text", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "hello goa") {
		t.Errorf("text mirror = %q", rec.Body.String())
	}
}

// The end-to-end promise of phase 0: a browser attaches, receives the current
// screen, types, and the bytes arrive at the engine's input handler.
func TestServer_WebSocketDeliversFullFrame(t *testing.T) {
	conn, vt, _ := dialTestServer(t)

	f := readFrame(t, conn)
	if !f.Full || f.Cols != 40 || f.Rows != 10 {
		t.Fatalf("first frame = %+v, want a full 40x10 snapshot", f)
	}
	vt.WriteString("\x1b[2;1Hfrom the engine")
	f = readFrame(t, conn)
	if !patchTextContains(f, "from the engine") {
		t.Errorf("patch text = %q, want the engine's row", patchText(f))
	}
}

func TestServer_WebSocketInputReachesEngine(t *testing.T) {
	conn, _, inputs := dialTestServer(t)
	readFrame(t, conn) // drain the initial full frame

	if err := conn.WriteJSON(clientMsg{T: MsgInput, Data: "hello\r"}); err != nil {
		t.Fatalf("write input: %v", err)
	}
	select {
	case in := <-inputs:
		if in != "hello\r" {
			t.Errorf("engine input = %q, want %q", in, "hello\r")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input never reached the engine")
	}
}

func TestServer_WebSocketResizeRepaints(t *testing.T) {
	conn, _, _ := dialTestServer(t)
	readFrame(t, conn)

	if err := conn.WriteJSON(clientMsg{T: MsgResize, Cols: 60, Rows: 20}); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	f := readFrame(t, conn)
	if !f.Full || f.Cols != 60 || f.Rows != 20 {
		t.Errorf("resize frame = %+v, want a full 60x20 frame", f)
	}
}

func TestServer_RejectsCrossOriginSocket(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	hdr := http.Header{}
	hdr.Set("Origin", "http://evil.example")
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(ts.URL, "sess-1"), hdr)
	if err == nil {
		t.Fatal("cross-origin upgrade should have been refused")
	}
	if resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
		t.Errorf("cross-origin socket was upgraded (status %d)", resp.StatusCode)
	}
}

// Excess viewers keep a live screen but lose the keyboard.
func TestHub_CapsClientsAndMarksExcessReadOnly(t *testing.T) {
	hub := NewHub(1)
	first := &fakeClient{}
	_, readOnly := hub.Attach(first)
	if readOnly {
		t.Fatal("first client should not be read-only")
	}
	second := &fakeClient{}
	_, readOnly = hub.Attach(second)
	if !readOnly {
		t.Error("second client should be read-only")
	}
	hub.Publish(&Frame{Seq: 1})
	if len(first.frames) != 1 {
		t.Errorf("first client got %d frames", len(first.frames))
	}
	if hub.Clients() != 1 {
		t.Errorf("attached = %d, want 1", hub.Clients())
	}
	hub.Close()
	if hub.Clients() != 0 || !first.closed {
		t.Error("Close must detach and close clients")
	}
}

// A client that never drains must be dropped, and the publisher must not
// block on it.
func TestHub_DropsSlowClient(t *testing.T) {
	hub := NewHub(2)
	slow := &fakeClient{alwaysSlow: true}
	hub.Attach(slow)
	done := make(chan struct{})
	go func() {
		for i := 0; i < DefaultSlowClientLimit+1; i++ {
			hub.Publish(&Frame{Seq: uint64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow client")
	}
	if hub.Clients() != 0 {
		t.Errorf("slow client still attached (%d)", hub.Clients())
	}
}

func TestHub_DetachIsIdempotent(t *testing.T) {
	hub := NewHub(2)
	c := &fakeClient{}
	detach, _ := hub.Attach(c)
	detach()
	detach()
	if hub.Clients() != 0 {
		t.Errorf("clients = %d, want 0", hub.Clients())
	}
}

// dialTestServer starts a server with a 40x10 screen and returns a connected
// WebSocket client, the terminal behind it, and the channel the terminal
// delivers typed input to.
func dialTestServer(t *testing.T) (*websocket.Conn, *VirtualTerminal, chan string) {
	t.Helper()
	vt := NewVirtualTerminal(40, 10)
	inputs := make(chan string, 4)
	vt.Start(func(s string) { inputs <- s }, func() {})
	srv := NewServer(vt, 40, 10, ServerOptions{
		Addr:      "127.0.0.1:0",
		SessionID: func() string { return "sess-1" },
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
	return conn, vt, inputs
}

// patchText joins every patched row of a frame.
func patchText(f *Frame) string {
	var rows []string
	for _, p := range f.Patches {
		rows = append(rows, RunsText(p.Runs))
	}
	return strings.Join(rows, "\n")
}

func patchTextContains(f *Frame, want string) bool {
	return len(f.Patches) > 0 && strings.Contains(patchText(f), want)
}

func wsURL(httpURL, session string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws?s=" + session
}

func readFrame(t *testing.T, conn *websocket.Conn) *Frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	codec := NewFrameCodec()
	for {
		f, err := codec.DecodeFrame(data)
		if err == nil {
			return f
		}
		t.Fatalf("decode %q: %v", data, err)
	}
}

type fakeClient struct {
	frames     []*Frame
	controls   []Control
	closed     bool
	alwaysSlow bool
}

func (c *fakeClient) Send(f *Frame) bool {
	if c.alwaysSlow {
		return false
	}
	c.frames = append(c.frames, f)
	return true
}

func (c *fakeClient) SendControl(ctrl Control) error {
	c.controls = append(c.controls, ctrl)
	return nil
}

func (c *fakeClient) Close() error {
	c.closed = true
	return nil
}
