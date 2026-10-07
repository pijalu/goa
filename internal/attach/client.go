// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pijalu/goa/internal/webui"
)

// DetachKey is the raw byte sequence that detaches the local terminal from
// the remote session (Ctrl+]): the session keeps running on the server, the
// terminal is restored, and attach exits. The chord is free in the engine's
// keymap — nothing else binds it — which is what makes a byte the client may
// keep for itself.
const DetachKey = "\x1d"

// Options configures an attach session.
type Options struct {
	// Server is the `goa server` base URL ("http://127.0.0.1:8080"; a bare
	// host:port is accepted and defaulted to http).
	Server string
	// Session is the session id to attach to. Empty means "ask the server":
	// a supervisor (or single-session server) resolves it via /connect (when
	// Path is set) or /healthz.
	Session string
	// Path is a project directory to open on a multi-project server. Empty
	// attaches to an existing session as-is.
	Path string
	// Plane names the rendering plane to request ("cells" for a native
	// terminal). The server's default answers a client that names none.
	Plane string
	// Token is the bearer token for --server-auth=token.
	Token string
	// User and Password are the --server-auth=basic credentials.
	User, Password string
	// ReconnectDelay is the first sleep between reconnect attempts; zero
	// uses the production default (it doubles up to a 5s cap). Exposed for
	// tests.
	ReconnectDelay time.Duration
}

// Terminal is the local half of an attach session: everything a real TTY
// needs to offer. *tui.ProcessTerminal implements it — the client reuses the
// production raw-mode, key-debouncing and Kitty-negotiation logic unchanged,
// because the bytes it forwards are the bytes a local session would read.
type Terminal interface {
	Start(onInput func(string), onResize func())
	Stop()
	Size() (cols, rows int)
	WriteString(s string)
}

// SessionEnded reports an intentional end: the server told the client the
// session is over. Distinct from a transport error so Run stops retrying.
type SessionEnded struct{ Reason string }

func (e *SessionEnded) Error() string { return "session ended: " + e.Reason }

func asSessionEnded(err error) *SessionEnded {
	if e, ok := err.(*SessionEnded); ok {
		return e
	}
	return nil
}

// pendingInputLimit bounds keystrokes buffered while the socket is down, the
// same bound the server's virtual terminal keeps for a not-yet-wired engine:
// a bounded hold loses the overflow instead of growing without end.
const pendingInputLimit = 4096

// Client drives one attach session: it owns the socket, the screen and the
// local terminal wiring. The terminal is started once for the client's
// lifetime (a re-Start per reconnect would stack raw-mode stdin readers);
// reconnects re-attach the socket and repaint the screen only.
type Client struct {
	opts   Options
	term   Terminal
	screen *Screen

	// conn is the live socket; nil while disconnected. Guarded by wmu, which
	// also serializes its writes.
	wmu  sync.Mutex
	conn *websocket.Conn

	// sends carries outbound documents to the writer goroutine; bounded like
	// every other queue on this path.
	sends chan []byte
	// pendingInput holds keystrokes typed while disconnected, replayed on
	// the next attach. Guarded by pmu.
	pmu          sync.Mutex
	pendingInput []byte

	// done is closed when the session is over (bye, detach, or cancelled
	// context): the writer goroutine exits, and the read loop's transport
	// errors stop being errors.
	done chan struct{}
	// detached marks an intentional user exit so Run can distinguish it from
	// a socket failure worth retrying. Atomic: finish runs on the input
	// goroutine while Run's retry loop reads it.
	detached atomic.Bool

	// base is the server's http(s) base URL, resolved once.
	base string
	// session is the id the socket attaches to; a rotation control updates
	// it so a reconnect lands on the session's new id.
	session string
	// lastSeq is the revision the screen holds — the reconnect hello's hint.
	lastSeq uint64
	// readOnly is set when the server granted a viewer attachment.
	readOnly bool
}

// Run attaches and serves until the session ends (server bye), the user
// detaches (Ctrl+]), or ctx is cancelled. The terminal is restored on every
// exit path.
func Run(ctx context.Context, opts Options, term Terminal) error {
	c, err := newClient(opts, term)
	if err != nil {
		return err
	}
	defer c.term.Stop()

	// Cancellation must reach the blocking read: closing the socket is what
	// unblocks it.
	go func() {
		<-ctx.Done()
		c.finish(ctx.Err())
	}()

	// The local keyboard is wired once: raw bytes go to the socket (and into
	// the pending buffer while disconnected); the detach chord is the one
	// sequence the client keeps for itself.
	term.Start(func(in string) {
		if in == DetachKey {
			c.finish(nil)
			return
		}
		c.sendInput(in)
	}, func() {
		c.sendResize()
	})

	return c.serve(ctx)
}

// newClient resolves the session to attach, builds the screen at the local
// geometry and starts the writer goroutine.
func newClient(opts Options, term Terminal) (*Client, error) {
	c := &Client{
		opts:    opts,
		term:    term,
		sends:   make(chan []byte, 64),
		done:    make(chan struct{}),
		session: opts.Session,
	}
	base, err := baseURL(opts.Server)
	if err != nil {
		return nil, err
	}
	c.base = base
	if c.session == "" {
		// No session named: ask the server. A multi-project supervisor
		// resolves Path into (or reuses) a session; a single-session server
		// answers /healthz with the live session id.
		c.session, err = resolveSession(base, opts)
		if err != nil {
			return nil, err
		}
	}
	cols, rows := term.Size()
	c.screen = NewScreen(term, cols, rows)
	go c.writeLoop()
	return c, nil
}

// serve runs the attach-retry loop: one attach at a time, reconnecting on
// transport failures with backoff. Handshake refusals, goodbyes, detaches
// and cancellation all end the loop with their own answer.
func (c *Client) serve(ctx context.Context) error {
	delay := c.opts.ReconnectDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	for {
		err := c.attachAndServe(ctx, c.base)
		if end := asSessionEnded(err); end != nil {
			return end
		}
		if err == nil || c.detached.Load() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if asHandshakeError(err) != nil {
			// The upgrade itself was refused (credentials, capacity,
			// unknown session). Retrying cannot heal a refusal: report it.
			return err
		}
		// The socket died mid-session; the session keeps running server-side
		// — that is the point of attach. Say so (stderr is not the screen:
		// raw mode leaves it alone) and re-attach.
		fmt.Fprintf(os.Stderr, "goa attach: connection lost (%v) — reconnecting…\n", err)
		if err := sleepCtx(ctx, delay); err != nil {
			return err
		}
		delay = backoff(delay)
	}
}

// sleepCtx waits for d, reporting cancellation.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// backoffCap bounds the reconnect delay.
const backoffCap = 5 * time.Second

// backoff doubles the reconnect delay up to the cap.
func backoff(d time.Duration) time.Duration {
	d *= 2
	if d > backoffCap {
		return backoffCap
	}
	return d
}

// finish ends the session: idempotent, safe from any goroutine. reason ==
// nil means an intentional local exit (detach). Closing the live socket is
// what unblocks the read loop — a closed done channel alone would leave it
// in ReadMessage until the server spoke again.
func (c *Client) finish(reason error) {
	if reason == nil {
		c.detached.Store(true)
	}
	c.wmu.Lock()
	conn := c.conn
	c.conn = nil
	c.wmu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

// writeLoop owns every socket write for the client's lifetime. While
// disconnected the queue drains into the void (keystrokes land in the
// pending buffer on the input side instead).
func (c *Client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case data := <-c.sends:
			c.wmu.Lock()
			conn := c.conn
			if conn != nil {
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if conn.WriteMessage(websocket.TextMessage, data) != nil {
					_ = conn.Close()
				}
			}
			c.wmu.Unlock()
		}
	}
}

// attachAndServe dials, renders until the socket dies, and reports why it
// ended. A SessionEnded return stops Run's retry loop.
func (c *Client) attachAndServe(ctx context.Context, base string) (err error) {
	conn, err := c.dial(ctx, base)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	c.conn = conn
	c.wmu.Unlock()

	defer func() {
		c.wmu.Lock()
		c.conn = nil
		c.wmu.Unlock()
		_ = conn.Close()
	}()

	// Repaint from the authoritative snapshot; announce what we hold so a
	// reconnect that lost nothing is answered with silence instead of a
	// second full paint. The server sends its attach frame right after the
	// upgrade regardless — the hello decides whether a resync follows.
	c.screen.Start()
	c.sendResize()
	_ = c.queue(map[string]any{"t": webui.MsgHello, "since": c.lastSeq})
	c.replayPending()

	return c.readLoop(conn)
}

// readLoop decodes server messages until the socket dies or the session
// ends. It is the only goroutine touching the screen. Frames render the
// moment they arrive; a transcript batch arrives as the NEXT message and is
// applied by Screen.Scrollback (scroll the physical screen, repaint the rows
// the scroll moved).
func (c *Client) readLoop(conn *websocket.Conn) error {
	codec := webui.NewFrameCodec()
	for {
		// The conn arrives as a parameter, not through the field: finish
		// nils the field from another goroutine while this read blocks, and
		// closing the socket (not re-reading the field) is what unblocks it.
		_, data, err := conn.ReadMessage()
		if err != nil {
			if c.isDone() {
				return nil // detach or context: not an error
			}
			return err
		}
		if err := c.dispatch(codec, data); err != nil {
			return err
		}
	}
}

// dispatch routes one server message. A bye returns the session's end (the
// caller stops reading); every other message is answered inline.
func (c *Client) dispatch(codec *webui.FrameCodec, data []byte) error {
	var probe struct {
		T string `json:"t"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return nil
	}
	switch probe.T {
	case webui.MsgFrame:
		c.onFrame(codec, data)
	case webui.MsgScrollback:
		c.onScrollback(codec, data)
	case webui.MsgControl, webui.CtrlBye, webui.CtrlReadOnly, webui.CtrlSessionRotated:
		return c.onControl(codec, data)
	}
	return nil
}

// onFrame renders one frame and remembers its revision (the reconnect
// hello's hint).
func (c *Client) onFrame(codec *webui.FrameCodec, data []byte) {
	f, err := codec.DecodeFrame(data)
	if err != nil || f == nil {
		return
	}
	c.lastSeq = f.Seq
	c.render(f)
}

// onScrollback applies the transcript batch that rode the last frame.
func (c *Client) onScrollback(codec *webui.FrameCodec, data []byte) {
	_, batch, err := codec.DecodeScrollback(data)
	if err != nil {
		return
	}
	c.screen.Scrollback(batch)
}

// onControl applies one control message; a bye returns the session's end.
func (c *Client) onControl(codec *webui.FrameCodec, data []byte) error {
	ctrl, err := codec.DecodeControl(data)
	if err != nil {
		return nil
	}
	if end := c.control(ctrl); end != nil {
		c.finish(end)
		return end
	}
	return nil
}

// render hands one frame to the screen. A read-only attachment is not ours
// to draw on: the viewer notice rides the window title instead.
func (c *Client) render(f *webui.Frame) {
	if c.readOnly {
		c.screen.setTitle("goa attach [read-only] — " + f.Title)
		return
	}
	c.screen.Frame(f, nil)
}

// control applies one control message; a bye ends the session.
func (c *Client) control(ctrl webui.Control) error {
	switch ctrl.Kind {
	case webui.CtrlBye:
		return &SessionEnded{Reason: ctrl.Text}
	case webui.CtrlReadOnly:
		c.readOnly = true
		c.screen.setTitle("goa attach [read-only]")
	case webui.CtrlSessionRotated:
		if ctrl.Session != "" {
			c.session = ctrl.Session
		}
	}
	return nil
}

// sendInput forwards raw terminal bytes. Live socket → straight out;
// otherwise (or when the queue refuses) they are held bounded and replayed
// on the next attach, so a short reconnect does not eat typing.
func (c *Client) sendInput(in string) {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	if len(c.pendingInput) == 0 && c.connHeld() {
		if c.queue(map[string]any{"t": webui.MsgInput, "data": in}) == nil {
			return
		}
	}
	if room := pendingInputLimit - len(c.pendingInput); room > 0 {
		if len(in) > room {
			in = in[:room]
		}
		c.pendingInput = append(c.pendingInput, in...)
	}
}

// replayPending flushes keystrokes held while disconnected, oldest first.
// Held bytes leave the buffer only when the socket accepted them.
func (c *Client) replayPending() {
	c.pmu.Lock()
	defer c.pmu.Unlock()
	if len(c.pendingInput) == 0 || !c.connHeld() {
		return
	}
	data := string(c.pendingInput)
	if c.queue(map[string]any{"t": webui.MsgInput, "data": data}) == nil {
		c.pendingInput = nil
	}
}

// connHeld reports whether a socket is live. Callers hold pmu; taking wmu
// here too would invert nothing (wmu never waits on pmu), and the peek is
// what decides hold-vs-buffer.
func (c *Client) connHeld() bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.conn != nil
}

// sendResize reports the local geometry to the server.
func (c *Client) sendResize() {
	cols, rows := c.term.Size()
	_ = c.queue(map[string]any{"t": webui.MsgResize, "cols": cols, "rows": rows})
}

// HandshakeError reports that the server refused the upgrade itself — bad
// credentials, viewer limit, unknown session. Unlike a transport drop it is
// final: no reconnect can heal a refusal.
type HandshakeError struct {
	URL    string
	Status int
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("connect %s: refused (status %d)", e.URL, e.Status)
}

func asHandshakeError(err error) *HandshakeError {
	if e, ok := err.(*HandshakeError); ok {
		return e
	}
	return nil
}

// dial opens the WebSocket, presenting the credentials and the plane request.
func (c *Client) dial(ctx context.Context, base string) (*websocket.Conn, error) {
	wsURL := wsScheme(base) + "/ws?s=" + url.QueryEscape(c.session) + "&plane=" + url.QueryEscape(c.opts.Plane)
	hdr := http.Header{}
	c.opts.addAuth(hdr)
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := d.DialContext(ctx, wsURL, hdr)
	if err != nil {
		if resp != nil {
			return nil, &HandshakeError{URL: wsURL, Status: resp.StatusCode}
		}
		return nil, fmt.Errorf("connect %s: %w", wsURL, err)
	}
	return conn, nil
}

// queue marshals and hands one outbound document to the writer goroutine. It
// drops rather than blocks: the read loop's error path is what notices a
// dead socket.
func (c *Client) queue(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case <-c.done:
		return websocket.ErrCloseSent
	default:
	}
	select {
	case c.sends <- data:
		return nil
	default:
		return fmt.Errorf("send queue full")
	}
}

func (c *Client) isDone() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}
