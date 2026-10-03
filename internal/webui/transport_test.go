// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests cover the fallback transports (spec §11.2/§11.3): the SSE event
// stream, the POST input/resize endpoints, the server-rendered no-JS page, the
// seq-based reconnect, and the immutable asset cache. The WebSocket primary is
// covered in server_test.go; here we assert that *when the primary is
// unavailable*, a session is still fully drivable.

// recorder collects the input bytes the engine received, thread-safely. It stands
// in for the engine's input handler so a test can assert exactly what a
// browser's keystrokes became.
type recorder struct {
	mu sync.Mutex
	in []string
}

func (r *recorder) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.in = append(r.in, s)
}

func (r *recorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.in, "")
}

// newEngineServer builds a server whose input handler records every byte it
// receives, backed by a real HTTP listener so streaming routes can be read.
func newEngineServer(t *testing.T, cols, rows int) (*Server, *VirtualTerminal, *recorder) {
	t.Helper()
	return newEngineServerOpts(t, cols, rows, ServerOptions{})
}

func newEngineServerOpts(t *testing.T, cols, rows int, opts ServerOptions) (*Server, *VirtualTerminal, *recorder) {
	t.Helper()
	if opts.SessionID == nil {
		opts.SessionID = func() string { return "sess-1" }
	}
	// An ephemeral loopback port, never the default 8080: tests must not share a
	// fixed port, or a still-draining listener from the previous test answers
	// this one's requests (flaky EOFs, and a real bind failure if it lingers).
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	opts.Logger = discardLogger()
	vt := NewVirtualTerminal(cols, rows)
	rec := &recorder{}
	vt.Start(rec.record, func() {})
	srv := NewServer(vt, cols, rows, opts)
	t.Cleanup(func() { _ = srv.Close() })
	return srv, vt, rec
}

// live starts a real listener for srv and returns its base URL, so streaming
// endpoints can be exercised over an actual socket. The listener always gets an
// ephemeral port: sharing the default 8080 across tests makes a request land on
// the previous test's still-draining server.
func live(t *testing.T, srv *Server) string {
	t.Helper()
	if srv.opts.Addr == DefaultAddr {
		srv.opts.Addr = "127.0.0.1:0"
	}
	ln, err := srv.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx, ln) }()
	t.Cleanup(cancel)
	return "http://" + srv.Addr()
}

// sseSession is one open GET /events stream.
type sseSession struct {
	resp   *http.Response
	sc     *bufio.Scanner
	cancel context.CancelFunc
}

// next reads the next `data:` payload from the stream, or fails the test.
func (s *sseSession) next(t *testing.T) string {
	t.Helper()
	for s.sc.Scan() {
		line := s.sc.Text()
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("event stream ended without a data event: %v", s.sc.Err())
	return ""
}

// awaitEvent reads events until one contains substr, or fails the test. Frames
// and controls travel on separate queues so a full frame backlog can never
// delay an out-of-band notice, which means their arrival order is not
// guaranteed — a test that expects a specific order would hang.
func (s *sseSession) awaitEvent(t *testing.T, substr string) {
	t.Helper()
	found := make(chan string, 1)
	go func() {
		for s.sc.Scan() {
			line := s.sc.Text()
			if strings.HasPrefix(line, "data: ") && strings.Contains(line, substr) {
				found <- line
				return
			}
		}
	}()
	select {
	case <-found:
	case <-time.After(3 * time.Second):
		t.Fatalf("stream never delivered an event containing %q", substr)
	}
}

// openSSE attaches to the fallback stream and reads its first frame.
// attachSSE opens a stream and returns the session without reading it. Tests
// that only await a specific event use this: frames and controls travel on
// separate queues, so the first event read may be either.
func attachSSE(t *testing.T, base string) *sseSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?s=sess-1", nil)
	if err != nil {
		cancel()
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET /events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET /events status = %d", resp.StatusCode)
	}
	s := &sseSession{resp: resp, sc: bufio.NewScanner(resp.Body), cancel: cancel}
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})
	return s
}

// openSSE attaches to the fallback stream and reads its first event.
func openSSE(t *testing.T, base string) (*sseSession, string) {
	t.Helper()
	s := attachSSE(t, base)
	return s, s.next(t)
}

// decodeFrame parses one SSE payload back into the wire shape.
func decodeFrame(t *testing.T, payload string) wireFrame {
	t.Helper()
	var msg wireFrame
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("decode event %q: %v", payload, err)
	}
	return msg
}

// postForm submits a urlencoded body the way a plain HTML form does.
func postForm(t *testing.T, srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "/s/sess-1/page")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// postJSON issues a JSON POST to the server mux.
func postJSON(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------- SSE stream

// A joining SSE client must immediately receive the current screen as one full
// frame — the grid is authoritative, so no replay is needed (spec §8/§11.2).
func TestSSE_AttachingClientReceivesFullFrame(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 6)
	vt.WriteString("\x1b[1;1Hsse screen")
	base := live(t, srv)

	_, payload := openSSE(t, base)
	msg := decodeFrame(t, payload)
	if msg.T != MsgFrame {
		t.Fatalf("event type = %q, want %q", msg.T, MsgFrame)
	}
	if !msg.Full {
		t.Error("first frame not marked full")
	}
	if !strings.Contains(payload, "sse screen") {
		t.Errorf("frame does not carry the screen text: %q", payload)
	}
}

// The stream must declare itself an event stream, forbid caching, and defeat
// proxy buffering (the other classic reason a stream looks hung).
func TestSSE_SetsStreamHeaders(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	s, _ := openSSE(t, base)

	if ct := s.resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := s.resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if ab := s.resp.Header.Get("X-Accel-Buffering"); ab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", ab)
	}
}

// The stream must advertise a retry hint so EventSource reconnects on the
// server's terms after a restart, not the browser's default backoff.
func TestSSE_AdvertisesRetry(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events?s=sess-1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	var sawRetry bool
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "retry: ") {
			sawRetry = true
			break
		}
	}
	if !sawRetry {
		t.Error("stream did not advertise a retry hint")
	}
}

// Live output must reach a stream client: writing to the terminal after the
// stream attached has to arrive as a new event.
func TestSSE_DeliversLiveFrames(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 6)
	base := live(t, srv)
	s, first := openSSE(t, base)
	_ = first

	vt.WriteString("\x1b[1;1Hlive update")
	payload := s.next(t)
	if !strings.Contains(payload, "live update") {
		t.Errorf("stream event does not carry the update: %q", payload)
	}
}

// An unknown session id must be refused before the stream opens.
func TestSSE_UnknownSession404(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	resp, err := http.Get(base + "/events?s=other")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// A read-only server must tell an SSE viewer it may not drive, and must count it
// as attached like any other client.
func TestSSE_ReadOnlyViewerIsTold(t *testing.T) {
	srv, _, _ := newEngineServerOpts(t, 40, 4, ServerOptions{ReadOnly: true})
	base := live(t, srv)
	s := attachSSE(t, base)
	s.awaitEvent(t, `"t":"read_only"`)
}

// ------------------------------------------------------------- POST endpoints

// A JSON POST to /input must reach the engine exactly like a WebSocket key.
func TestPostInput_JSONReachesEngine(t *testing.T) {
	srv, _, rec := newEngineServer(t, 40, 4)
	res := postJSON(t, srv, "/input?s=sess-1", `{"t":"input","data":"hello\r"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got := rec.joined(); got != "hello\r" {
		t.Errorf("engine received %q, want %q", got, "hello\r")
	}
}

// The no-JS form posts urlencoded data and must be answered with a 303 back to
// the page (so the browser re-renders) rather than JSON.
func TestPostInput_FormRedirectsBack(t *testing.T) {
	srv, _, rec := newEngineServer(t, 40, 4)
	res := postForm(t, srv, "/input?s=sess-1", url.Values{"data": {"typed message\r"}, "s": {"sess-1"}})

	if res.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "/s/sess-1/page" {
		t.Errorf("Location = %q, want the referring page", loc)
	}
	if got := rec.joined(); got != "typed message\r" {
		t.Errorf("engine received %q, want the form's data", got)
	}
}

// A read-only server must refuse input from the fallback path too.
func TestPostInput_RefusedWhenReadOnly(t *testing.T) {
	srv, _, rec := newEngineServerOpts(t, 40, 4, ServerOptions{ReadOnly: true})
	postJSON(t, srv, "/input?s=sess-1", `{"t":"input","data":"nope"}`)
	if got := rec.joined(); got != "" {
		t.Errorf("read-only server delivered %q to the engine", got)
	}
}

// POST /resize must reach the terminal geometry.
func TestPostResize_ReachesTerminal(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	postJSON(t, srv, "/resize?s=sess-1", `{"t":"resize","cols":100,"rows":30}`)
	cols, rows := vt.Grid().Size()
	if cols != 100 || rows != 30 {
		t.Errorf("grid = %dx%d, want 100x30", cols, rows)
	}
}

// The urlencoded resize body must work too — the no-JS form and the page script
// share these endpoints.
func TestPostResize_FormBody(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	postForm(t, srv, "/resize?s=sess-1", url.Values{"cols": {"90"}, "rows": {"20"}})
	cols, rows := vt.Grid().Size()
	if cols != 90 || rows != 20 {
		t.Errorf("grid = %dx%d, want 90x20", cols, rows)
	}
}

// Geometry is not a privilege: a read-only viewer still gets a correctly sized
// screen over the fallback path.
func TestPostResize_AllowedWhenReadOnly(t *testing.T) {
	srv, vt, _ := newEngineServerOpts(t, 40, 4, ServerOptions{ReadOnly: true})
	postJSON(t, srv, "/resize?s=sess-1", `{"t":"resize","cols":70,"rows":22}`)
	if cols, _ := vt.Grid().Size(); cols != 70 {
		t.Errorf("read-only viewer grid = %d cols, want 70", cols)
	}
}

// A malformed body must be rejected, not panic.
func TestPostInput_RejectsGarbage(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	req := httptest.NewRequest(http.MethodPost, "/input?s=sess-1", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// A non-numeric geometry form must be rejected rather than silently clamped.
func TestPostResize_RejectsBadFormGeometry(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	res := postForm(t, srv, "/resize?s=sess-1", url.Values{"cols": {"wide"}, "rows": {"tall"}})
	if res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.Code)
	}
}

// An unknown session id on the fallback POST must 404.
func TestPostInput_UnknownSession404(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	res := postJSON(t, srv, "/input?s=other", `{"t":"input","data":"x"}`)
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}
}

// ---------------------------------------------------------------- no-JS page

// The no-JS page must render the current screen as text and offer a form that
// posts back to /input — with no <script> tag anywhere (spec §11.3).
func TestPlainPage_RendersScreenAndForm(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 6)
	vt.WriteString("\x1b[1;1Hplain view")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1/page", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"plain view",
		`<meta http-equiv="refresh"`,
		`action="/input?s=sess-1"`,
		"<textarea",
		`name="data"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("no-JS page missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("no-JS page must not contain a <script> tag")
	}
}

// ?mode=plain on the JS page URL must serve the same server-rendered fallback,
// for a browser whose scripting is blocked by policy rather than by the URL.
func TestPlainPage_ModePlainAlias(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1?mode=plain", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<textarea") {
		t.Error("mode=plain did not serve the fallback page")
	}
}

// The no-JS page must escape cell text so terminal output cannot inject HTML.
func TestPlainPage_EscapesCellText(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	vt.WriteString("\x1b[1;1H<script>alert(1)</script>")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1/page", nil))
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("cell text was not HTML-escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the escaped form of the cell text")
	}
}

// A read-only server's no-JS page must hide the form and say why.
func TestPlainPage_ReadOnlyHidesForm(t *testing.T) {
	srv, _, _ := newEngineServerOpts(t, 40, 4, ServerOptions{ReadOnly: true})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1/page", nil))
	body := rec.Body.String()
	if strings.Contains(body, "<textarea") {
		t.Error("read-only no-JS page still shows an input form")
	}
	if !strings.Contains(body, "read-only") {
		t.Error("read-only no-JS page does not explain the mode")
	}
}

// The full checkpoint for the no-JS path: fetch the page, submit its form, and
// the engine receives the typed bytes — with no JavaScript anywhere in the loop.
func TestPlainPage_FormPostReachesEngine(t *testing.T) {
	srv, _, rec := newEngineServer(t, 40, 4)
	res := postForm(t, srv, "/input?s=sess-1", url.Values{"data": {"no-js hello\r"}, "s": {"sess-1"}})

	if got := rec.joined(); got != "no-js hello\r" {
		t.Errorf("engine received %q after form submit", got)
	}
	if res.Code != http.StatusSeeOther {
		t.Errorf("form submit status = %d, want 303 back", res.Code)
	}
	// The reply must land the user back on the page, not on a bare JSON body.
	if loc := res.Header().Get("Location"); loc != "/s/sess-1/page" {
		t.Errorf("form submit redirected to %q, want the referring page", loc)
	}
}

// Cell colours must survive into the server-rendered page: a plain-text-only
// render would lose the styling the terminal shows.
func TestPlainPage_KeepsRunColours(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	vt.WriteString("\x1b[1;1H\x1b[31mred\x1b[0m plain")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/s/sess-1/page", nil))
	if !strings.Contains(rec.Body.String(), "color:") {
		t.Error("no-JS page dropped the run colours")
	}
}

// ------------------------------------------------------------------ reconnect

// A reconnecting client announces the last frame it saw; the server replies
// with a full frame (the grid is authoritative — spec §8). Re-attaching the
// stream is exactly that reconnect, and the frame it gets must re-sync to the
// current grid rather than replay a delta.
func TestReconnect_ResumesFromSeq(t *testing.T) {
	srv, vt, _ := newEngineServer(t, 40, 4)
	base := live(t, srv)
	// A first client sees the screen, then goes away.
	_, firstPayload := openSSE(t, base)
	firstMsg := decodeFrame(t, firstPayload)
	if firstMsg.Seq == 0 {
		t.Fatal("first frame carried no seq")
	}
	// The session advances while nobody is attached.
	vt.WriteString("\x1b[1;1Hmoved on")

	// A reconnecting client re-attaches and must receive the *current* grid in
	// one full frame whose seq is newer than the one it left off at.
	second, payload := openSSE(t, base)
	msg := decodeFrame(t, payload)
	if !msg.Full {
		t.Error("reconnect frame not marked full")
	}
	if msg.Seq <= firstMsg.Seq {
		t.Errorf("reconnect seq = %d, want newer than %d", msg.Seq, firstMsg.Seq)
	}
	if !strings.Contains(payload, "moved on") {
		t.Errorf("reconnect frame does not carry the missed output: %q", payload)
	}
	// The reconnecting stream stays open for further frames.
	if second.resp.StatusCode != http.StatusOK {
		t.Errorf("reconnect stream status = %d", second.resp.StatusCode)
	}
}

// An over-capacity hub must degrade an SSE viewer to read-only rather than
// letting it type into a session another browser owns.
func TestSSE_OverCapacityViewerIsReadOnly(t *testing.T) {
	srv, _, _ := newEngineServerOpts(t, 40, 4, ServerOptions{MaxClients: 1})
	base := live(t, srv)
	attachSSE(t, base) // occupies the only slot
	second := attachSSE(t, base)
	second.awaitEvent(t, "read_only")
}

// --------------------------------------------------------------------- assets

// Assets are embedded and must be served with an immutable cache header so a
// browser never re-fetches app.js/app.css.
func TestAssets_ImmutableCache(t *testing.T) {
	srv, _, _ := newEngineServer(t, 40, 4)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("Cache-Control = %q, want immutable + 1-year max-age", cc)
	}
}
