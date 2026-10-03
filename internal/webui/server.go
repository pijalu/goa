// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pijalu/goa/tui"
)

// DefaultAddr is the listen address used when none is given. It is loopback
// on purpose: the no-auth web UI is only safe on this machine, so the default
// is the one address where it is.
const DefaultAddr = "127.0.0.1:8080"

// ServerOptions configures the HTTP surface. Phase 0 ships the loopback-safe
// subset; auth (§10) and the SSE/plain transports arrive in phase 3.
type ServerOptions struct {
	// Addr is the listen address ("127.0.0.1:8080" by default).
	Addr string
	// SessionID is the current core.SessionStore id — the /s/<id> namespace.
	SessionID func() string
	// MaxClients caps attached browsers.
	MaxClients int
	// ReadOnly makes every attached browser a viewer: keystrokes and uploads
	// are refused with an explicit notice instead of silently doing nothing,
	// so a shared screen can never be driven by a passer-by.
	ReadOnly bool
	// Auth selects the authentication scheme and its credentials (spec §10).
	// The zero value is AuthNone, which CheckExposure permits on loopback only.
	Auth AuthConfig
	// InsecureNoAuth is the operator's explicit acceptance of serving without
	// credentials on a non-loopback address (--insecure-no-auth).
	InsecureNoAuth bool
	// Logger receives lifecycle messages; nil uses the standard logger.
	Logger *log.Logger
}

// Server wires the virtual terminal, the hub and the HTTP routes together.
//
//	Browser ──/ws──► wsClient ──► Hub ◄── VirtualTerminal ◄── TUI engine
//	                                       │
//	                              CellGrid (tui.TermEmulator)
type Server struct {
	opts ServerOptions
	term *VirtualTerminal
	hub  *Hub
	page *HTMLPage
	mux  *http.ServeMux
	auth Authenticator
	// maxRequestBytes is the body cap the bodyLimit middleware enforces.
	// It is a field (not a constant read) so a test can exercise the
	// middleware's own refusal without allocating a 16 MiB request.
	maxRequestBytes int64
	// handler is the fully wrapped chain — compression, body limit, origin
	// guard, auth, response hardening — the single entry point every caller
	// (Serve, Listen, Handler) goes through.
	handler http.Handler
	http    *http.Server
	log     *log.Logger
	upgr    websocket.Upgrader

	// boundMu guards bound: Listen writes it on the serving goroutine while
	// Addr/URL read it from another (the CLI prints the URL as soon as the
	// socket is up).
	boundMu sync.RWMutex
	bound   string
}

// NewServer builds a server around a virtual terminal sized cols×rows.
//
// It panics on a configuration that would be unsafe to serve (unknown auth
// mode, missing credentials, unauthenticated non-loopback bind). A server that
// refuses to be constructed is better than one that starts and answers: the
// caller here is a CLI whose only remedy is to stop with a message.
func NewServer(term *VirtualTerminal, cols, rows int, opts ServerOptions) *Server {
	if opts.Addr == "" {
		opts.Addr = DefaultAddr
	}
	if opts.SessionID == nil {
		opts.SessionID = func() string { return "" }
	}
	if err := CheckExposure(opts.Addr, opts.Auth, opts.InsecureNoAuth); err != nil {
		panic(fmt.Sprintf("webui: %v", err))
	}
	auth, err := NewAuthenticator(opts.Auth)
	if err != nil {
		panic(fmt.Sprintf("webui: %v", err))
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{
		opts: opts,
		term: term,
		hub:  NewHub(opts.MaxClients),
		page: NewHTMLPage(),
		auth: auth,
		log:  logger,
	}
	term.Resize(cols, rows)
	term.SetSink(s.hub)
	s.upgr = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// Same-origin only: a browser-reachable agent must not accept a
		// cross-origin socket that can drive it (spec §10.3).
		CheckOrigin: sameOrigin,
		// permessage-deflate, negotiated per connection: each frame is deflated
		// on its own, so a screen update reaches the browser the moment it is
		// written. An HTTP-level gzip on a streaming socket would instead buffer
		// the session behind the compressor (spec §12).
		EnableCompression: true,
	}
	s.routes()
	s.maxRequestBytes = MaxRequestBytes
	// One wrapper chain for every entry point: Serve, Listen and Handler all
	// go through it, so a request cannot behave differently depending on which
	// door it came in. Order, outermost first:
	//
	//   securityHeaders — every response, including a 401, carries the CSP
	//   bodyLimit       — no handler can read past the cap
	//   originGuard     — no cross-origin state change reaches a handler
	//   authGate        — no unauthenticated request reaches a handler
	//   gzip            — transport only, once the answer is already decided
	s.handler = s.securityHeaders(s.bodyLimit(s.originGuard(s.authGate(gzipMiddleware(s.mux)))))
	s.http = &http.Server{
		Addr:              opts.Addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Handler exposes the server's handler for tests and for embedding.
func (s *Server) Handler() http.Handler { return s.handler }

// Terminal returns the virtual terminal (the server's screen).
func (s *Server) Terminal() *VirtualTerminal { return s.term }

// Hub returns the fan-out (tests assert attachment counts).
func (s *Server) Hub() *Hub { return s.hub }

func (s *Server) routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	// /s/{path...} covers /s/, /s/<id>, /s/<id>/text and the empty-id forms:
	// before the agent session exists the id is "", and the page must still be
	// reachable at the canonical URL.
	mux.HandleFunc("GET /s/{path...}", s.handleSession)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("POST /input", s.handleInput)
	mux.HandleFunc("POST /key", s.handleKey)
	mux.HandleFunc("POST /resize", s.handleResize)
	mux.HandleFunc("POST /upload", s.handleUpload)
	mux.HandleFunc("GET /assets/{name}", s.handleAsset)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// The login form exists only when there is a token to exchange; Basic auth
	// is negotiated by the browser itself, so it has no page.
	if s.auth != nil && s.auth.LoginPath() != "" {
		mux.HandleFunc("GET "+s.auth.LoginPath(), s.handleLoginGet)
		mux.HandleFunc("POST "+s.auth.LoginPath(), s.handleLoginPost)
	}
	s.mux = mux
}

// Listen binds the configured address without serving yet, so the caller can
// print the real URL (with the resolved port) before traffic starts.
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("webui: listen %s: %w", s.opts.Addr, err)
	}
	s.setBound(ln.Addr().String())
	return ln, nil
}

// setBound records the resolved listen address.
func (s *Server) setBound(addr string) {
	s.boundMu.Lock()
	s.bound = addr
	s.boundMu.Unlock()
}

// boundAddr reads the resolved listen address ("" before Listen binds).
func (s *Server) boundAddr() string {
	s.boundMu.RLock()
	defer s.boundMu.RUnlock()
	return s.bound
}

// URL is the browsable entry point of the live session.
func (s *Server) URL() string { return "http://" + s.boundAddr() + "/s/" + s.opts.SessionID() }

// Serve serves until ctx is cancelled or the listener fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
	}()
	if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	s.hub.Close()
	return nil
}

// ListenAndServe binds the address and serves until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := s.Listen()
	if err != nil {
		return err
	}
	s.log.Printf("goa web UI: %s", s.URL())
	return s.Serve(ctx, ln)
}

// Addr reports the bound address ("" before ListenAndServe binds).
func (s *Server) Addr() string { return s.boundAddr() }

// Close stops the server and detaches every client.
func (s *Server) Close() error {
	s.hub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

// handleRoot is the required 302: the bare host always lands on the current
// session view.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/s/"+s.opts.SessionID(), http.StatusFound)
}

// handleSession serves the session view (/s/<id>) and its plain-text mirror
// (/s/<id>/text). An unknown id 404s with a hint rather than silently
// attaching the viewer to a different conversation.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id, sub := splitSessionPath(r.PathValue("path"))
	// With no live session id there is no room for "/s/<id>/text", so the
	// mirror lives at "/s/text". A real id always wins over the alias.
	if sub == "" && id == textPathAlias && id != s.opts.SessionID() {
		id, sub = "", "text"
	}
	if !s.knownSession(id) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if sub == "text" {
		s.handleText(w, r, id)
		return
	}
	if sub == "page" {
		s.handlePlainPage(w, r, id)
		return
	}
	// ?mode=plain is the same server-rendered fallback reached from the JS page,
	// for a browser whose scripting is off or blocked by policy (spec §11.3).
	if r.URL.Query().Get("mode") == "plain" {
		s.handlePlainPage(w, r, id)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.page.Render(w, s.pageData(id, r)); err != nil {
		s.log.Printf("webui: render page: %v", err)
	}
}

// pageData builds the template payload, pulling the palette straight from the
// TUI theme so the browser renders with the very colours the terminal uses. The
// nonce is the one this response's CSP names, which is what lets the page keep
// its inline theme block and bootstrap line under a policy without
// 'unsafe-inline'.
func (s *Server) pageData(id string, r *http.Request) PageData {
	th := tui.TheTheme
	return PageData{
		SessionID: id,
		Theme:     ThemeName(th),
		ThemeVars: ThemeVars(th),
		Nonce:     nonceOf(r),
	}
}

// textPathAlias is the plain-text mirror URL for a session that has no id yet
// (before the agent session starts).
const textPathAlias = "text"

// splitSessionPath splits the /s/... remainder into (session id, subresource).
// Both may be empty: "/s/" is the page of the current session with no id yet.
func splitSessionPath(path string) (id, sub string) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

// handleText is the plain-text mirror of the current screen (curl, screen
// readers, accessibility).
func (s *Server) handleText(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, s.term.Grid().Text())
}

// handleHealth answers /healthz with a tiny JSON status document.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"session":%q,"clients":%d}`+"\n",
		s.opts.SessionID(), s.hub.Clients())
}

// handleAsset serves the embedded client assets (immutable).
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	data, ctype, ok := s.page.Asset(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

// handleWS upgrades the connection and attaches it as a viewer. The first
// thing a client receives is a full frame (the grid is authoritative, so a
// reconnect never needs a replay).
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("s")
	if id != "" && !s.knownSession(id) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	conn, err := s.upgr.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the error
	}
	// --read-only (or an over-capacity hub) attaches a viewer, not a driver.
	readOnly := s.opts.ReadOnly
	client := NewWSClient(conn, readOnly)
	detach, hubReadOnly := s.hub.Attach(client)
	defer detach()
	if hubReadOnly {
		readOnly = true
	}
	if readOnly {
		client.SetReadOnly(true)
		reason := "server is read-only"
		if hubReadOnly {
			reason = "viewer limit reached"
		}
		_ = client.SendControl(Control{Kind: CtrlReadOnly, Text: reason})
	}
	// A joining client gets the authoritative screen: the grid is the truth, so
	// there is nothing to replay. The frame carries the grid's current revision,
	// which is what lets the client's own hello be answered with "you are
	// current" when nothing changed while it was away.
	client.Send(s.term.FullFrame())
	client.ReadLoop(ClientHandlers{
		Input:  func(in string) { s.term.Input(in) },
		Key:    func(ev KeyEvent) { s.term.Input(string(EncodeKey(ev))) },
		Resize: func(cols, rows int) { s.term.Resize(cols, rows) },
		Hello:  s.resyncer(client),
	})
}

// resyncer answers a client's hello with the revision it announced: nothing when
// the client's view already is the grid's, one authoritative full frame when it
// is not. A client that missed frames — both transports drop the oldest queued
// frame for a client that cannot keep up — uses this to stop drifting instead of
// patching deltas onto a grid that no longer matches the server's.
func (s *Server) resyncer(client Client) func(uint64) {
	return func(since uint64) {
		if f, due := s.term.Resync(since); due {
			client.Send(f)
		}
	}
}

// knownSession reports whether id addresses the live session. An empty id is
// accepted (a browser that loaded before the first message).
func (s *Server) knownSession(id string) bool {
	if id == "" {
		return true
	}
	return id == s.opts.SessionID()
}

// sameOrigin accepts only requests whose Origin host matches the Host header
// (absent Origin — a native client or curl — is allowed).
//
// The port is dropped from both sides: a browser sends Host as "goa.local:8080"
// and Origin as "http://goa.local:8080", so comparing the origin host against
// the raw Host header would refuse the page's own requests — the exact request
// the check exists to allow.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	uHost := hostOf(origin)
	return uHost != "" && strings.EqualFold(uHost, hostOnly(r.Host))
}

// hostOnly strips a trailing :port from a Host header value. It works on the
// bare authority, so it must not go through hostOf (which cuts at the first
// "/" and would empty the value).
func hostOnly(hostport string) string {
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		if _, err := strconv.Atoi(hostport[i+1:]); err == nil {
			return hostport[:i]
		}
	}
	return hostport
}

func hostOf(origin string) string {
	rest := origin
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		if _, err := strconv.Atoi(rest[i+1:]); err == nil {
			rest = rest[:i]
		}
	}
	return rest
}
