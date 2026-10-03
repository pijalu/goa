// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SSE + POST is the fallback transport (spec §11.2): when the WebSocket cannot be
// established — blocked by a proxy or an extension, downgraded, or simply
// refused — the page switches to a one-way event stream for output and plain
// POSTs for input. EventSource and fetch exist in every browser that runs the
// page, so the fallback survives exactly the failure WS cannot: a network path
// that forbids the Upgrade dance but serves ordinary HTTP.
//
// The degradation is invisible beyond a slightly higher input latency: both
// transports speak the same FrameCodec wire format, so app.js renders frames
// from either without a second code path.

const (
	// sseKeepalive is how often a comment is written to an idle stream. Proxies
	// and load balancers drop connections they consider dead; a comment is a
	// valid event-stream payload that carries no data, so it keeps the stream
	// open without waking the client.
	sseKeepalive = 20 * time.Second
	// sseRetry is the reconnection delay advertised to EventSource. The
	// browser honours it verbatim, so a server restart costs one wait, not the
	// browser's default backoff.
	sseRetry = 2000
	// sseQueue is the per-client frame buffer. Same newest-wins policy as the
	// WebSocket client: a browser on a slow link must never accumulate a
	// backlog that shows the session seconds behind (spec §7.6).
	sseQueue = 8
)

// sseClient is a Client over one Server-Sent Events stream. Frames are encoded
// with the shared FrameCodec and written as `data: {json}\n\n`; the writer owns
// the ResponseWriter for the life of the stream.
type sseClient struct {
	codec  *FrameCodec
	writer sseWriter

	frames  chan *Frame
	control chan Control
	prelude chan string
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once

	consecutive atomic.Int64
	slowLimit   int64
	readOnly    atomic.Bool
	closed      atomic.Bool
}

// sseWriter is the minimal half of http.ResponseWriter an event stream needs:
// a byte sink plus an explicit flush (an SSE event must reach the browser the
// moment it is written). Keeping it an interface (rather than the concrete
// writer) is what lets the client be unit-tested without a real HTTP round trip.
type sseWriter interface {
	Write(p []byte) (int, error)
	Flush()
}

var _ Client = (*sseClient)(nil)

// newSSEClient wraps one event stream. The stream's headers must already be
// written (the handler does that before attaching).
func newSSEClient(w sseWriter, readOnly bool) *sseClient {
	c := &sseClient{
		codec:     NewFrameCodec(),
		writer:    w,
		frames:    make(chan *Frame, sseQueue),
		control:   make(chan Control, 4),
		prelude:   make(chan string, 1),
		done:      make(chan struct{}),
		stopped:   make(chan struct{}),
		slowLimit: DefaultSlowClientLimit,
	}
	c.readOnly.Store(readOnly)
	go c.writeLoop()
	return c
}

// SetReadOnly switches the client between driving and mirroring.
// Open queues the stream preamble — the reconnection delay the browser should
// honour. From here the writer goroutine owns the ResponseWriter for the life of
// the stream: the handler must not write to it itself, because two flushes
// racing on one http.ResponseWriter is a data race, not a cosmetic issue.
func (c *sseClient) Open(retryMS int) error {
	select {
	case c.prelude <- fmt.Sprintf("retry: %d\n\n", retryMS):
		return nil
	case <-c.done:
		return fmt.Errorf("webui: sse client closed")
	}
}

func (c *sseClient) SetReadOnly(v bool) { c.readOnly.Store(v) }

// ReadOnly reports the current mode (the hub and the handler consult it).
func (c *sseClient) ReadOnly() bool { return c.readOnly.Load() }

// Send queues a frame, dropping the oldest when the queue is full. It returns
// false once the client has missed slowLimit frames in a row, which is the hub's
// signal to drop it — identical to the WebSocket policy.
func (c *sseClient) Send(f *Frame) bool {
	if f == nil || c.closed.Load() {
		return false
	}
	for {
		select {
		case <-c.frames:
			if c.consecutive.Add(1) >= c.slowLimit {
				return false
			}
		default:
			select {
			case c.frames <- f:
				c.consecutive.Store(0)
				return true
			case <-c.done:
				return false
			}
		}
	}
}

// SendControl queues an out-of-band control message.
func (c *sseClient) SendControl(ctrl Control) error {
	if c.closed.Load() {
		return fmt.Errorf("webui: sse client closed")
	}
	select {
	case c.control <- ctrl:
		return nil
	case <-c.done:
		return fmt.Errorf("webui: sse client closed")
	default:
		return nil // best-effort: never block the publisher
	}
}

// Close shuts the stream down and waits for the writer goroutine to leave the
// ResponseWriter before returning. Idempotent.
//
// The wait is not optional: net/http finishes the response the instant the
// handler returns, and a write that is still in flight would then be racing the
// server's own cleanup. Every detach path goes through here, so the stream's
// last write always happens while the handler is still on the stack.
func (c *sseClient) Close() error {
	c.once.Do(func() {
		c.closed.Store(true)
		close(c.done)
	})
	<-c.stopped
	return nil
}

// writeLoop owns the stream: frames first, then controls, with a keepalive
// comment whenever nothing has been written for sseKeepalive.
func (c *sseClient) writeLoop() {
	timer := time.NewTimer(sseKeepalive)
	defer timer.Stop()
	defer close(c.stopped)
	for {
		select {
		case <-c.done:
			return
		case s := <-c.prelude:
			if !c.writeRaw(s) {
				return
			}
			resetTimer(timer, sseKeepalive)
		case f := <-c.frames:
			if !c.writeFrame(f) {
				return
			}
			resetTimer(timer, sseKeepalive)
		case ctrl := <-c.control:
			if !c.writeControl(ctrl) {
				return
			}
			resetTimer(timer, sseKeepalive)
		case <-timer.C:
			if !c.writeRaw(": keepalive\n\n") {
				return
			}
			resetTimer(timer, sseKeepalive)
		}
	}
}

// resetTimer re-arms a timer that has already fired or been stopped.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// writeFrame emits one frame plus its scrollback batch as two events.
func (c *sseClient) writeFrame(f *Frame) bool {
	data, err := c.codec.EncodeFrame(f)
	if err != nil {
		return true // an unencodable frame must not kill the stream
	}
	if !c.writeEvent(data) {
		return false
	}
	if len(f.Scrollback) == 0 {
		return true
	}
	batch, err := c.codec.EncodeScrollback(f.Seq, f.Scrollback)
	if err != nil {
		return true
	}
	return c.writeEvent(batch)
}

func (c *sseClient) writeControl(ctrl Control) bool {
	data, err := c.codec.EncodeControl(ctrl)
	if err != nil {
		return true
	}
	return c.writeEvent(data)
}

// writeEvent writes one `data:`-framed event and flushes it. Splitting every
// line keeps a payload containing newlines from corrupting the stream framing.
func (c *sseClient) writeEvent(payload []byte) bool {
	var b strings.Builder
	for _, line := range strings.Split(string(payload), "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return c.writeRaw(b.String())
}

// writeRaw is the single choke point for stream writes: one error check, so
// every message type gets the same failure handling.
func (c *sseClient) writeRaw(s string) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	if _, err := c.writer.Write([]byte(s)); err != nil {
		return false
	}
	c.writer.Flush()
	return true
}

// handleEvents is GET /events: the SSE output stream. It attaches an sseClient
// to the hub exactly like the WebSocket handler, so both transports feed the
// same fan-out and an SSE browser is just another viewer.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("s")
	if id != "" && !s.knownSession(id) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	// Defeats proxy buffering, which is the other classic reason a stream
	// looks "hung": without it an intermediary can hold every event back.
	h.Set("X-Accel-Buffering", "no")

	// A write deadline is what keeps Close's wait bounded: a peer that stops
	// reading cannot pin the writer goroutine on a blocked socket. It is the
	// same budget as the keepalive, which is far longer than a small event needs.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(sseKeepalive)); err != nil {
		s.log.Printf("webui: sse write deadline: %v", err)
	}

	readOnly := s.opts.ReadOnly
	client := newSSEClient(streamWriter{w: w, f: flusher}, readOnly)
	detach, hubReadOnly := s.hub.Attach(client)
	// Detach first (it stops new frames), then Close (it joins the writer) —
	// LIFO order, which is what keeps the last write inside the handler.
	defer client.Close()
	defer detach()
	if hubReadOnly {
		readOnly = true
	}
	client.SetReadOnly(readOnly)

	// Open queues the reconnection hint. Every later write — the first frame
	// included — goes through the same writer goroutine, so the handler never
	// touches the ResponseWriter again.
	if err := client.Open(sseRetry); err != nil {
		s.log.Printf("webui: sse open: %v", err)
	}

	// EventSource reconnects on its own, replaying the URL it was opened with, so
	// the page carries the revision it holds in ?since= and a resumed stream that
	// is already current opens silently. A joining client (no since) or one that
	// fell behind gets the authoritative screen.
	if f, due := s.term.Resync(sinceQuery(r)); due {
		client.Send(f)
	}
	if readOnly {
		reason := "server is read-only"
		if hubReadOnly {
			reason = "viewer limit reached"
		}
		_ = client.SendControl(Control{Kind: CtrlReadOnly, Text: reason})
	}

	// The handler owns the connection until the client goes away or the server
	// stops. The writer goroutine has the socket, so this loop only waits.
	s.serveStream(r, client)
}

// sinceQuery reads the ?since= resume hint from a stream request. A missing,
// unparsable or negative value means "I hold nothing" (0), which always asks
// for the full screen rather than silently trusting a number the server could
// not read.
func sinceQuery(r *http.Request) uint64 {
	v, err := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// serveStream blocks until the request context is cancelled (browser closed the
// tab, or the server shut down) or the client was dropped as too slow.
func (s *Server) serveStream(r *http.Request, c *sseClient) {
	done := make(chan struct{})
	go func() {
		select {
		case <-r.Context().Done():
		case <-c.done:
		}
		close(done)
	}()
	<-done
}

// streamWriter adapts a ResponseWriter plus its Flusher to sseWriter.
type streamWriter struct {
	w io.Writer
	f http.Flusher
}

func (s streamWriter) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s streamWriter) Flush()                      { s.f.Flush() }

// ------------------------------------------------------------------ POST paths

// handleInput is POST /input: the fallback input path (spec §11.2/§11.3). It
// accepts both the JSON the page's SSE mode posts and the
// application/x-www-form-urlencoded body the no-JS page's form submits, so one
// endpoint serves a JavaScript client and a browser with scripting disabled.
//
// A form post is answered with a 303 back to the page that posted, so the
// browser re-renders the updated screen without a client-side round trip — the
// whole point of the no-JS path.
func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	in, err := readPostBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.knownSession(r.URL.Query().Get("s")) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if !s.opts.ReadOnly {
		s.term.Input(in.data)
		// The no-JS page has no keydown handler, so the buttons report the key
		// they stand for in the form body. It is encoded server-side with the
		// shared table for the same reason /key is: one byte table, one test.
		if in.key != "" {
			s.term.Input(string(EncodeKey(KeyEvent{Key: in.key})))
		}
	}
	if in.form {
		redirectBack(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleResize is POST /resize: the fallback geometry path. Same two body
// shapes as /input, because the same page code posts both.
func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	cols, rows, form, err := readResizeBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.knownSession(r.URL.Query().Get("s")) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	// Geometry is never a privilege: a read-only viewer still gets a correctly
	// sized screen, exactly as it would over the WebSocket.
	s.term.Resize(cols, rows)
	if form {
		redirectBack(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleKey is POST /key: the fallback counterpart of the WebSocket key
// message. It reports what was pressed and the *server* turns it into terminal
// bytes with the shared KeyEncoder, so the byte table still lives in exactly one
// tested place — the page never grows a second encoder just because it lost its
// socket (spec §7.5).
func (s *Server) handleKey(w http.ResponseWriter, r *http.Request) {
	var m clientMsg
	if err := decodeJSONPost(r, &m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.knownSession(r.URL.Query().Get("s")) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if !s.opts.ReadOnly {
		s.term.Input(string(EncodeKey(m.Key)))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// postMaxBytes bounds a fallback POST. A keystroke paste is kilobytes; 1 MiB
// matches the WebSocket read limit so the two paths accept the same input.
const postMaxBytes = MaxFrameBytes

// formKeyField is the form field the no-JS page's key buttons set. A browser
// posts the name/value of only the button that was pressed, so "Send" leaves it
// empty (insert the text) and "⏎" reports "Enter" (insert, then submit).
const formKeyField = "key"

// postPayload is one decoded fallback POST: the literal text the browser typed
// or pasted, the key its button stood for, and whether it must be answered with
// a redirect rather than JSON.
type postPayload struct {
	data string
	key  string
	form bool
}

// readPostBody extracts the payload from either supported POST encoding.
func readPostBody(r *http.Request) (postPayload, error) {
	if isFormPost(r) {
		if err := r.ParseForm(); err != nil {
			return postPayload{form: true}, fmt.Errorf("webui: bad form body: %w", err)
		}
		return postPayload{
			data: r.PostFormValue("data"),
			key:  r.PostFormValue(formKeyField),
			form: true,
		}, nil
	}
	var m clientMsg
	if err := decodeJSONPost(r, &m); err != nil {
		return postPayload{}, err
	}
	return postPayload{data: m.Data}, nil
}

// readResizeBody extracts the geometry from either supported POST encoding.
// A non-positive dimension is a bug or an attack, never a legitimate client.
func readResizeBody(r *http.Request) (cols, rows int, form bool, err error) {
	if isFormPost(r) {
		if err := r.ParseForm(); err != nil {
			return 0, 0, true, fmt.Errorf("webui: bad form body: %w", err)
		}
		cols, err = strconv.Atoi(r.PostFormValue("cols"))
		if err != nil {
			return 0, 0, true, fmt.Errorf("webui: bad cols")
		}
		rows, err = strconv.Atoi(r.PostFormValue("rows"))
		if err != nil {
			return 0, 0, true, fmt.Errorf("webui: bad rows")
		}
		return cols, rows, true, nil
	}
	var m clientMsg
	if err := decodeJSONPost(r, &m); err != nil {
		return 0, 0, false, err
	}
	return m.Cols, m.Rows, false, nil
}

// isFormPost reports whether the request carries a urlencoded form body — the
// shape a plain HTML form submits with scripting disabled.
func isFormPost(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

// decodeJSONPost reads a bounded JSON body into v.
func decodeJSONPost(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, postMaxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("webui: bad JSON body: %w", err)
	}
	return nil
}

// redirectBack sends a form poster back where it came from, defaulting to the
// session page. A 303 is required rather than a 302 so the browser follows with
// a GET — a repost would resend the input.
func redirectBack(w http.ResponseWriter, r *http.Request) {
	target := r.Referer()
	if target == "" {
		target = "/s/" + r.URL.Query().Get("s")
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
