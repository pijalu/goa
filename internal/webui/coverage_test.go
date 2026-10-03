// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pijalu/goa/tui"
)

func newListeningServer(t *testing.T) *Server {
	t.Helper()
	vt := NewVirtualTerminal(20, 4)
	srv := NewServer(vt, 20, 4, ServerOptions{
		Addr:      "127.0.0.1:0",
		SessionID: func() string { return "s1" },
		Logger:    discardLogger(),
	})
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	for i := 0; i < 100; i++ {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never became ready at %s", url)
}

func TestServer_ListenServeAndClose(t *testing.T) {
	srv := newListeningServer(t)
	if srv.Addr() != "" {
		t.Errorf("Addr before Listen = %q, want empty", srv.Addr())
	}
	ln, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if srv.Addr() == "" || srv.URL() == "" {
		t.Fatalf("Addr/URL after Listen = %q / %q", srv.Addr(), srv.URL())
	}
	if srv.Terminal() == nil || srv.Hub() == nil {
		t.Error("Terminal/Hub accessors returned nil")
	}

	go func() { _ = srv.Serve(t.Context(), ln) }()
	waitHealthy(t, srv.URL()+"/healthz")
	if err := srv.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestServer_ListenAndServe(t *testing.T) {
	srv := newListeningServer(t)
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() { errCh <- srv.ListenAndServe(ctx) }()
	// ListenAndServe binds asynchronously: the URL only exists once it did.
	waitFor(t, func() bool { return srv.Addr() != "" })
	waitHealthy(t, srv.URL()+"/healthz")
	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("ListenAndServe returned %v", err)
	}
}

// waitFor polls cond until it holds (or the test times out).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// Broadcast reaches every attached client; PublishFull ships a full snapshot.
func TestHub_BroadcastAndPublishFull(t *testing.T) {
	vt := NewVirtualTerminal(10, 3)
	hub := NewHub(2)
	vt.SetSink(hub)
	vt.WriteString("hello")

	c := &fakeClient{}
	_, readOnly := hub.Attach(c)
	if readOnly {
		t.Fatal("first client must be the driver")
	}
	hub.Broadcast(Control{Kind: CtrlReadOnly, Text: "note"})
	if len(c.controls) != 1 || c.controls[0].Text != "note" {
		t.Errorf("broadcast controls = %+v", c.controls)
	}

	vt.PublishFull()
	if len(c.frames) == 0 {
		t.Fatal("PublishFull shipped no frame")
	}
	if !c.frames[len(c.frames)-1].Full {
		t.Error("PublishFull must mark the frame full")
	}
	if vt.Seq() == 0 {
		t.Error("Seq must advance with published frames")
	}
	if _, err := vt.EncodeFrame(c.frames[0]); err != nil {
		t.Errorf("EncodeFrame: %v", err)
	}
}

func TestVirtualTerminal_WriteAndStop(t *testing.T) {
	vt := NewVirtualTerminal(10, 3)
	sink := &recordingSink{}
	vt.SetSink(sink)

	n, err := vt.Write([]byte("hi"))
	if err != nil || n != 2 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if n, err := vt.Write(nil); err != nil || n != 0 {
		t.Errorf("empty Write = %d, %v", n, err)
	}
	vt.HideCursor() // twice: the second call is a no-op
	vt.ShowCursor()
	vt.Stop()
	vt.Stop() // idempotent
	if _, err := vt.EncodeFrame(nil); err == nil {
		t.Error("EncodeFrame(nil) must fail")
	}
	// After Stop the sink is detached: no more frames.
	before := len(sink.frames)
	vt.WriteString("more")
	if len(sink.frames) != before {
		t.Error("a stopped terminal must not publish")
	}
}

func TestHTMLPage_AssetHandler(t *testing.T) {
	p := NewHTMLPage()
	rec := httptest.NewRecorder()
	p.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("css status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}

	rec = httptest.NewRecorder()
	p.AssetHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/nope.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", rec.Code)
	}
	if _, _, ok := p.Asset(".."); ok {
		t.Error("path traversal must be rejected")
	}
}

// fitCells pads a short row; it is the guard that keeps the client's grid
// rectangular after a resize raced a diff.
func TestCellGrid_CellsPadShortRows(t *testing.T) {
	short := fitCells([]tui.CellAttrs{{Text: "a"}, {Text: "b"}}, 5)
	if len(short) != 5 {
		t.Fatalf("len = %d, want 5", len(short))
	}
	for i := 2; i < 5; i++ {
		if short[i].Text != " " {
			t.Errorf("pad cell %d = %q, want a space", i, short[i].Text)
		}
	}
}

func TestTuiFlags_DecodedForms(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want tui.AttrFlags
	}{
		{float64(3), tui.AttrBold | tui.AttrItalic},
		{int(3), tui.AttrBold | tui.AttrItalic},
		{uint16(3), tui.AttrBold | tui.AttrItalic},
		{"not a number", 0},
		{nil, 0},
	} {
		if got := tuiFlags(tc.in); got != tc.want {
			t.Errorf("tuiFlags(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// An empty control kind must still produce a decodable message rather than an
// empty "t" field the client cannot route.
func TestFrameCodec_ControlDefaultsToControlType(t *testing.T) {
	data, err := NewFrameCodec().EncodeControl(Control{})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := NewFrameCodec().DecodeControl(data); err != nil {
		t.Errorf("decode: %v", err)
	}
}

func TestPalette256_OutOfRangeAndCubeEdges(t *testing.T) {
	if got := Palette256(-1); got != "" {
		t.Errorf("Palette256(-1) = %q, want empty", got)
	}
	if got := Palette256(300); got != "" {
		t.Errorf("Palette256(300) = %q, want empty", got)
	}
	// 16 is the first cube colour (black) and 231 the last (white).
	if got := Palette256(16); got != "#000000" {
		t.Errorf("Palette256(16) = %q", got)
	}
	if got := Palette256(231); got != "#ffffff" {
		t.Errorf("Palette256(231) = %q", got)
	}
	if got := cubeLevel(9); got != 0 {
		t.Errorf("cubeLevel(9) = %d, want 0", got)
	}
}

func TestHostOf(t *testing.T) {
	tests := []struct{ origin, want string }{
		// The port is dropped: sameOrigin compares against r.Host without it.
		{"http://localhost:8080", "localhost"},
		{"https://goa.dev", "goa.dev"},
		{"http://goa.dev/some/path", "goa.dev"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := hostOf(tc.origin); got != tc.want {
			t.Errorf("hostOf(%q) = %q, want %q", tc.origin, got, tc.want)
		}
	}
}

// A cross-origin socket upgrade must be refused (spec §10.3): a browser page
// from another site must not be able to drive the agent.
func TestServer_RejectsCrossOriginUpgrade(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusSwitchingProtocols {
		t.Fatal("cross-origin upgrade was accepted")
	}
}
