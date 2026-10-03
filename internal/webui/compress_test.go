// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// Compression has two halves, and both are about the same thing: the page and
// the frame stream are the only things a browser pulls from this server, and
// both are overwhelmingly text that compresses by an order of magnitude.
//
//   - HTTP responses (the shell, the stylesheet, the script, the text mirror)
//     get Content-Encoding: gzip when the client says it can decode one.
//   - The WebSocket gets permessage-deflate, which compresses each frame
//     independently and therefore survives the "flush now" semantics a live
//     screen needs — an HTTP-level gzip on a streaming socket would buffer the
//     whole session behind the compressor.
//
// Two things must NOT be compressed: the SSE stream (a compressor holds bytes
// back until its window fills, which is exactly the latency SSE exists to
// avoid) and the WebSocket upgrade (the extension does that job, per message).

// gunzip decompresses a gzipped response body, failing the test on bad bytes.
func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("open gzip body: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	return string(out)
}

// get issues a request against the server's handler and returns the recorder.
func get(t *testing.T, srv *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// A browser that accepts gzip must get the stylesheet compressed, and the bytes
// must survive the round trip intact — a broken compressor is worse than none.
func TestGzip_AssetIsCompressedWhenAccepted(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)

	rec := get(t, srv, "/assets/app.js", map[string]string{"Accept-Encoding": "gzip"})
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding; a shared cache would serve the wrong body", vary)
	}
	want, _, ok := srv.page.Asset("app.js")
	if !ok {
		t.Fatal("asset missing from the embedded FS")
	}
	if got := gunzip(t, rec.Body.Bytes()); got != string(want) {
		t.Errorf("gunzipped asset differs from the embedded file (%d vs %d bytes)", len(got), len(want))
	}
	if rec.Body.Len() >= len(want) {
		t.Errorf("compressed asset is %d bytes, uncompressed %d — no saving", rec.Body.Len(), len(want))
	}
}

// A client that does not accept gzip must get the plain asset, uncompressed and
// unlabelled: Content-Encoding is a promise about bytes that were not transformed.
func TestGzip_ClientWithoutSupportGetsPlainBytes(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)

	rec := get(t, srv, "/assets/app.js", nil)
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q for a client that did not ask for gzip", got)
	}
	want, _, _ := srv.page.Asset("app.js")
	if rec.Body.String() != string(want) {
		t.Error("plain response body is not the raw asset")
	}
}

// A client that lists several codings still gets gzip, which is the one we
// implement; identity is the fallback and needs no label.
func TestGzip_NegotiationPicksGzipFromAnyList(t *testing.T) {
	for _, accept := range []string{"gzip", "gzip, deflate, br", "deflate, gzip;q=0.8", "br, gzip"} {
		srv, _, _ := newEngineServer(t, 40, 4)
		rec := get(t, srv, "/assets/app.css", map[string]string{"Accept-Encoding": accept})
		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Errorf("Accept-Encoding %q → Content-Encoding %q, want gzip", accept, got)
		}
	}
}

// The HTML shell compresses too — it is the one document every page load pays
// for, and it carries the theme block.
func TestGzip_SessionPageIsCompressed(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)

	rec := get(t, srv, "/s/sess-1", map[string]string{"Accept-Encoding": "gzip"})
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip on the shell", got)
	}
	if body := gunzip(t, rec.Body.Bytes()); !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("gunzipped shell is not the page: %.80q", body)
	}
}

// The no-JS fallback page compresses on the same terms: a browser with scripting
// off is usually an ancient one on a slow link, which is the case compression
// helps most.
func TestGzip_PlainPageIsCompressed(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)

	rec := get(t, srv, "/s/sess-1/page", map[string]string{"Accept-Encoding": "gzip"})
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip on the no-JS page", got)
	}
	if body := gunzip(t, rec.Body.Bytes()); !strings.Contains(body, "http-equiv=\"refresh\"") {
		t.Error("gunzipped no-JS page lost its polling refresh")
	}
}

// The text mirror is a screenful of mostly spaces: ideal for compression, and
// curl users who ask for gzip get it.
func TestGzip_TextMirrorIsCompressed(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	vt.WriteString("\x1b[1;1Hhello")

	rec := get(t, srv, "/s/sess-1/text", map[string]string{"Accept-Encoding": "gzip"})
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip on /text", got)
	}
	if body := gunzip(t, rec.Body.Bytes()); !strings.Contains(body, "hello") {
		t.Errorf("gunzipped mirror lost the screen: %.80q", body)
	}
}

// The event stream must stay uncompressed. gzip holds bytes until its window is
// full, so a compressed SSE stream delivers frames in bursts — the exact failure
// the transport exists to avoid, and one that looks like a hung page.
func TestGzip_EventStreamIsNeverCompressed(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?s=sess-1", nil)
	if err != nil {
		t.Fatalf("build stream request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("event stream was compressed (%q); SSE needs per-event flushes", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}

// The WebSocket upgrade is not an HTTP response body: wrapping the ResponseWriter
// in a compressor here would both corrupt the handshake and duplicate what
// permessage-deflate already does per message.
func TestGzip_WebSocketUpgradeIsNotWrapped(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)

	dialer := websocket.Dialer{EnableCompression: true}
	conn, resp, err := dialer.Dial(wsURL(base, "sess-1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Extensions"); got == "" {
		t.Fatal("the upgrade response advertised no compression extension")
	}
	f := readFrame(t, conn)
	if f == nil {
		t.Fatal("no frame after the handshake")
	}
}

// A client that does not request compression must still get a working socket:
// the negotiation is opt-in per connection, not imposed.
func TestGzip_WebSocketWorksWithoutCompression(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(base, "sess-1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if got := resp.Header.Get("Sec-WebSocket-Extensions"); got != "" {
		t.Errorf("extensions = %q for a client that asked for none", got)
	}
	if f := readFrame(t, conn); f == nil {
		t.Fatal("no frame after the handshake")
	}
}

// Compression must not cost correctness: frames delivered over a compressed
// socket must decode to the same screen an uncompressed one would.
func TestGzip_CompressedSocketCarriesRealFrames(t *testing.T) {
	conn, vt, _ := dialCompressedTestServer(t)

	first := readFrame(t, conn)
	if !first.Full {
		t.Fatalf("first frame over a compressed socket is not full: %+v", first)
	}

	vt.WriteString("\x1b[1;1Hcompressed hello")
	got := readFrame(t, conn)
	if !patchTextContains(got, "compressed hello") {
		t.Errorf("delta over a compressed socket lost its content: %q", patchText(got))
	}
}

// dialCompressedTestServer starts a server and dials it with a
// compression-capable client, failing the test if the extension was not
// negotiated — every test that uses it depends on the socket really being
// compressed.
func dialCompressedTestServer(t *testing.T) (*websocket.Conn, *VirtualTerminal, chan string) {
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

	dialer := websocket.Dialer{EnableCompression: true}
	conn, resp, err := dialer.Dial(wsURL(ts.URL, "sess-1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if !strings.Contains(resp.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate") {
		t.Fatalf("compression was not negotiated: extensions = %q",
			resp.Header.Get("Sec-WebSocket-Extensions"))
	}
	return conn, vt, inputs
}
