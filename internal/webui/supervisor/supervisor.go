// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package supervisor serves many projects from one `goa server` process.
//
// One process per project is the architecture the rest of goa assumes: the
// config cascade, the theme, the tool registry and the session store are all
// rooted at a project directory, and the TUI engine carries process-global
// state (theme, spinner, project dir). Rather than refactor all of that into
// instantiable bundles, the supervisor keeps each project's session in its
// own child `goa server` process — bound to a private Unix socket inside a
// 0700 directory, speaking the ordinary webui protocol — and fronts them
// with one authenticated HTTP surface: an index of live sessions, a
// connect-by-path handshake, and a reverse proxy that maps /s/<id> and
// /ws?s=<id> onto the right child.
//
// The isolation is the feature: a session crash cannot take another project
// down, per-project config/plugins/trust are exact (it IS the ordinary
// single-project server), and every conversation chain keeps its own session
// and cache identity by construction.
package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/webui"
)

// Defaults for the session registry.
const (
	// DefaultMaxSessions caps how many project sessions may live at once.
	DefaultMaxSessions = 8
	// DefaultIdleTimeout is how long a session with no attached client and
	// no proxied request is kept before it is stopped. Its transcript stays
	// on disk in the project, resumable like any session.
	DefaultIdleTimeout = 30 * time.Minute
	// DefaultReapInterval is how often the reaper looks at the children in
	// production.
	DefaultReapInterval = 30 * time.Second

	// DefaultReadyTimeout bounds how long a child may take to wire its
	// session (config load, plugin load, model catalog) before /connect
	// gives up on it.
	DefaultReadyTimeout = 2 * time.Minute
)

// reapInterval is the reaper's tick. A var rather than a constant, like the
// webui Keepalive policy, so a test can shrink it: a reaper that cannot be
// exercised is a reaper that rots.
var reapInterval = DefaultReapInterval

// Options configures a supervisor.
type Options struct {
	// Root is the only directory tree sessions may be opened under
	// (--server-projects-root). Absolute; created if missing.
	Root string
	// Addr is the supervisor's own listen address.
	Addr string
	// Auth is the supervisor's auth configuration. Children always get a
	// fresh random bearer token regardless.
	Auth webui.AuthConfig
	// InsecureNoAuth passes through the explicit loopback waiver.
	InsecureNoAuth bool
	// MaxSessions caps live children (0 = DefaultMaxSessions).
	MaxSessions int
	// IdleTimeout stops a session no client has touched for this long
	// (0 = DefaultIdleTimeout; negative disables reaping).
	IdleTimeout time.Duration
	// ReadyTimeout bounds a child's startup (0 = DefaultReadyTimeout).
	ReadyTimeout time.Duration
	// Binary is the goa executable children run (default: this executable).
	Binary string
	// ServerArgs are extra flags handed to every child (e.g. --server-cells).
	ServerArgs []string
	// Log receives lifecycle messages (nil = standard logger).
	Log *log.Logger
	// now is a seam for tests.
	now func() time.Time
	// spawner is a seam for tests: nil uses the production child spawner.
	spawner Spawner
}

// Health is a child's (or the supervisor's) status document.
type Health struct {
	OK      bool   `json:"ok"`
	Session string `json:"session"`
	Clients int    `json:"clients"`
}

// SessionInfo is one live project session, as the index and /sessions
// surface it.
type SessionInfo struct {
	Session string `json:"session"`
	Path    string `json:"path"`
	Clients int    `json:"clients"`
}

// Child is a running project session the supervisor fronts.
type Child interface {
	// Session is the child's current session id (follows /new rotations).
	Session() string
	// Serves reports whether id is a session this child owns — its current
	// id, or one it rotated away from. A client that presents a retired id
	// must still land on the child that owned it.
	Serves(id string) bool
	// Clients is the child's attached-client count (from its healthz).
	Clients() int
	// Path is the project directory the child runs in.
	Path() string
	// LastUse is when the supervisor last proxied a request to it.
	LastUse() time.Time
	// Refresh re-reads the child's status (session id after rotation,
	// client count). It reports whether the child answered.
	Refresh(ctx context.Context) bool
	// Stop ends the child (SIGTERM, then SIGKILL) and removes its socket.
	Stop() error
}

// Spawner starts a session server for a directory. The production
// implementation spawns a child goa process; tests substitute a fake.
type Spawner interface {
	Start(ctx context.Context, dir string) (Child, error)
}

// Supervisor is the parent: the registry of children plus one hardened HTTP
// surface in front of them.
type Supervisor struct {
	opts  Options
	guard *webui.Guard
	log   *log.Logger
	now   func() time.Time

	mu       sync.Mutex
	children map[string]Child // key: resolved project directory
	sockDir  string
	nextSock int

	// spawnerMu guards spawner: set in tests, read lazily in production.
	spawnerMu sync.Mutex
	spawner   Spawner

	// reaperStop ends the reaper goroutine.
	reaperStop chan struct{}
	reaperOnce sync.Once
}

// New validates the options and builds the supervisor. The root is resolved
// (symlinks and all) once here: every path check compares against the
// resolved form, so a symlink inside the tree cannot point outside it.
func New(opts Options) (*Supervisor, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	guard, err := webui.NewGuard(opts.Auth, 0)
	if err != nil {
		return nil, fmt.Errorf("supervisor: %w", err)
	}
	sockDir, err := makeSocketDir()
	if err != nil {
		return nil, err
	}
	s := &Supervisor{
		opts:       opts,
		guard:      guard,
		log:        opts.Log,
		now:        opts.now,
		children:   make(map[string]Child),
		sockDir:    sockDir,
		reaperStop: make(chan struct{}),
		spawner:    opts.spawner,
	}
	return s, nil
}

// normalizeOptions resolves the projects root to its real form and fills the
// defaults.
func normalizeOptions(opts Options) (Options, error) {
	if opts.Root == "" {
		return opts, fmt.Errorf("supervisor: no projects root (--server-projects-root)")
	}
	abs, err := filepath.Abs(opts.Root)
	if err != nil {
		return opts, fmt.Errorf("supervisor: root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return opts, fmt.Errorf("supervisor: root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return opts, fmt.Errorf("supervisor: root: %w", err)
	}
	opts.Root = resolved
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = DefaultMaxSessions
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = DefaultReadyTimeout
	}
	if opts.Log == nil {
		opts.Log = log.Default()
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	return opts, nil
}

// makeSocketDir creates the 0700 directory the children's Unix sockets live
// in: the directory's file-system permissions are the children's boundary.
func makeSocketDir() (string, error) {
	dir, err := os.MkdirTemp("", "goa-supervisor-")
	if err != nil {
		return "", fmt.Errorf("supervisor: socket dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("supervisor: socket dir: %w", err)
	}
	return dir, nil
}

// setSpawner replaces the child spawner (tests).
func (s *Supervisor) setSpawner(sp Spawner) {
	s.spawnerMu.Lock()
	s.spawner = sp
	s.spawnerMu.Unlock()
}

// currentSpawner returns the configured spawner, defaulting to the
// production child-process spawner.
func (s *Supervisor) currentSpawner() Spawner {
	s.spawnerMu.Lock()
	sp := s.spawner
	s.spawnerMu.Unlock()
	if sp != nil {
		return sp
	}
	return s.processSpawner()
}

// Root reports the resolved projects root.
func (s *Supervisor) Root() string { return s.opts.Root }

// Close stops every child and removes the socket directory.
func (s *Supervisor) Close() error {
	s.reaperOnce.Do(func() { close(s.reaperStop) })
	s.mu.Lock()
	children := s.children
	s.children = make(map[string]Child)
	s.mu.Unlock()
	var firstErr error
	for _, c := range children {
		if err := c.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Handler returns the supervisor's fully hardened HTTP surface: the same
// Guard chain the session server composes, over the supervisor's routes —
// index, connect, sessions, healthz — and a proxy for everything else.
func (s *Supervisor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /connect", s.handleConnect)
	mux.HandleFunc("GET /sessions", s.handleSessions)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// Everything else belongs to a child session: /s/<id>…, /ws?s=<id>,
	// /events, /input, /key, /resize, /upload, /assets. The proxy resolves
	// the session id and 404s when no child claims it.
	mux.HandleFunc("/", s.handleProxy)
	return s.guard.Wrap(mux)
}

// ListenAndServe serves until ctx is cancelled.
func (s *Supervisor) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("supervisor: listen %s: %w", s.opts.Addr, err)
	}
	go s.reapLoop(ctx)
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	idle := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(idle)
	}()
	s.log.Printf("goa web UI (multi-project) ready: http://%s — projects root %s", ln.Addr(), s.opts.Root)
	err = srv.Serve(ln)
	<-idle
	s.Close()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// reapLoop stops children no client has touched for the idle timeout.
func (s *Supervisor) reapLoop(ctx context.Context) {
	if s.opts.IdleTimeout < 0 {
		return // reaping disabled
	}
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.reaperStop:
			return
		case <-ticker.C:
			s.reapOnce()
		}
	}
}

// reapOnce stops every child that is idle: no proxied request within the
// timeout AND no attached client right now. A session with viewers is never
// idle, whatever the clock says; a session alone with a running turn is
// stopped with its transcript intact — the same trade a console Ctrl+C on
// `goa server` makes.
func (s *Supervisor) reapOnce() {
	s.mu.Lock()
	candidates := make([]Child, 0, len(s.children))
	for _, c := range s.children {
		candidates = append(candidates, c)
	}
	s.mu.Unlock()
	for _, c := range candidates {
		idleFor := s.now().Sub(c.LastUse())
		if idleFor < s.opts.IdleTimeout {
			continue
		}
		if c.Clients() > 0 {
			continue
		}
		s.log.Printf("supervisor: reaping idle session %s (%s)", c.Session(), c.Path())
		if err := c.Stop(); err != nil {
			s.log.Printf("supervisor: reap %s: %v", c.Path(), err)
			continue
		}
		s.mu.Lock()
		delete(s.children, keyOf(c.Path()))
		s.mu.Unlock()
	}
}

// keyOf is the registry key for a project path.
func keyOf(path string) string { return filepath.Clean(path) }

// sortedSessions snapshots the children, oldest-registration first.
func (s *Supervisor) sortedSessions() []SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionInfo, 0, len(s.children))
	for _, c := range s.children {
		out = append(out, SessionInfo{Session: c.Session(), Path: c.Path(), Clients: c.Clients()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// handleIndex is the multi-project landing page: live sessions and a form to
// open a path. No scripts, no styling beyond what the shared sheet provides
// — the page exists to be useful, not to widen the CSP.
func (s *Supervisor) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>goa — projects</title></head><body>`)
	b.WriteString("<h1>goa sessions</h1><ul>")
	sessions := s.sortedSessions()
	for _, si := range sessions {
		fmt.Fprintf(&b, `<li><a href="/s/%s">%s</a> — %s (%d client(s))</li>`,
			url.PathEscape(si.Session), htmlEscape(si.Path), htmlEscape(si.Session), si.Clients)
	}
	if len(sessions) == 0 {
		b.WriteString("<li>No live sessions — open a project below.</li>")
	}
	b.WriteString(`</ul><form method="post" action="/connect">` +
		`<label>Project directory <input name="path" required></label> ` +
		`<button type="submit">Open</button></form></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}

// htmlEscape escapes the few characters that matter in text content.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;")
	return r.Replace(s)
}

// handleSessions lists the live project sessions as JSON.
func (s *Supervisor) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.sortedSessions())
}

// handleHealth answers with the supervisor's own status: no session of its
// own (an attach client reads the empty id as "name one explicitly").
func (s *Supervisor) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	n := len(s.children)
	s.mu.Unlock()
	fmt.Fprintf(w, `{"ok":true,"session":"","sessions":%d}`+"\n", n)
}
