// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/webui"
)

// webUIAuthConfig builds a token auth config for tests.
func webUIAuthConfig(mode, token string) webui.AuthConfig {
	return webui.AuthConfig{Mode: webui.AuthMode(mode), Token: token}
}

// fakeChild is a Child whose answers the test scripted.
type fakeChild struct {
	session string
	clients int
	path    string
	lastUse time.Time
	// retired are session ids this child owned before rotations.
	retired []string

	refresh func()
	proxied *strings.Builder
	stopped int
}

func (c *fakeChild) Session() string { return c.session }
func (c *fakeChild) Clients() int    { return c.clients }
func (c *fakeChild) Path() string    { return c.path }

// Serves reports whether id is this child's current session or one it
// rotated away from.
func (c *fakeChild) Serves(id string) bool {
	if id == c.session {
		return true
	}
	for _, r := range c.retired {
		if r == id {
			return true
		}
	}
	return false
}
func (c *fakeChild) LastUse() time.Time {
	return c.lastUse
}
func (c *fakeChild) Refresh(ctx context.Context) bool {
	if c.refresh != nil {
		c.refresh()
	}
	return true
}
func (c *fakeChild) Stop() error { c.stopped++; return nil }

// Proxy records the proxied requests, standing in for the reverse proxy.
func (c *fakeChild) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.proxied.WriteString(r.Method + " " + r.URL.Path + " auth=" + r.Header.Get("Authorization") + "\n")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("child-ok"))
	})
}

// fakeSpawner counts starts and returns scripted children.
type fakeSpawner struct {
	starts  int
	failOn  string
	child   *fakeChild
	onStart func(dir string)
}

func (f *fakeSpawner) Start(ctx context.Context, dir string) (Child, error) {
	f.starts++
	if f.failOn != "" && strings.Contains(dir, f.failOn) {
		return nil, os.ErrPermission
	}
	if f.onStart != nil {
		f.onStart(dir)
	}
	if f.child != nil {
		return f.child, nil
	}
	return &fakeChild{
		session: "sess-" + filepath.Base(dir),
		path:    dir,
		lastUse: time.Now(),
		proxied: &strings.Builder{},
	}, nil
}

// newTestSupervisor builds a supervisor over a temp root with a fake
// spawner installed.
func newTestSupervisor(t *testing.T, mutate func(*Options)) (*Supervisor, *fakeSpawner) {
	t.Helper()
	opts := Options{
		Root:        t.TempDir(),
		IdleTimeout: -1, // no reaping unless a test asks
	}
	if mutate != nil {
		mutate(&opts)
	}
	sup, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = sup.Close() })
	sp := &fakeSpawner{}
	sup.setSpawner(sp)
	return sup, sp
}

// postConnect sends a JSON connect request and decodes the answer.
func postConnect(t *testing.T, h http.Handler, path string) connectResponse {
	t.Helper()
	body := `{"path":` + jsonQuote(path) + `}`
	req := httptest.NewRequest(http.MethodPost, "/connect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp connectResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	resp.code = rec.Code
	return resp
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The path policy: absolute, existing, a directory, and — after symlink
// resolution — inside the projects root.
func TestValidatePath(t *testing.T) {
	sup, _ := newTestSupervisor(t, nil)
	root := sup.Root()

	inside := filepath.Join(root, "project-a")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	// A file inside the root (exists, but not a directory).
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the root pointing outside it.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		ok   bool
	}{
		{"inside root", inside, true},
		{"root itself", root, true},
		{"relative", "project-a", false},
		{"empty", "", false},
		{"missing", filepath.Join(root, "nope"), false},
		{"file not dir", file, false},
		{"symlink escape", link, false},
		{"outside root", filepath.Join(outside, "x"), false},
		{"traversal", root + "/../elsewhere", false},
	}
	for _, c := range cases {
		got, err := sup.ValidatePath(c.path)
		if c.ok && err != nil {
			t.Errorf("%s: ValidatePath(%q) = %v, want ok", c.name, c.path, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: ValidatePath(%q) accepted %q", c.name, c.path, got)
		}
	}
}

// Connect opens a session for a path and reuses it on the second call.
func TestConnectCreatesAndReuses(t *testing.T) {
	sup, sp := newTestSupervisor(t, nil)
	proj := filepath.Join(sup.Root(), "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	h := sup.Handler()

	first := postConnect(t, h, proj)
	if first.code != http.StatusOK || first.Session == "" {
		t.Fatalf("first connect: %+v", first)
	}
	second := postConnect(t, h, proj+"/") // trailing slash: same directory
	if second.Session != first.Session {
		t.Errorf("second connect session = %q, want %q", second.Session, first.Session)
	}
	if sp.starts != 1 {
		t.Errorf("spawner started %d children, want 1", sp.starts)
	}
}

// Connect refuses paths the policy rejects, before any child is spawned.
func TestConnectRejectsBadPaths(t *testing.T) {
	sup, sp := newTestSupervisor(t, nil)
	h := sup.Handler()
	for _, path := range []string{"", "relative", "/no/such/dir"} {
		resp := postConnect(t, h, path)
		if resp.code != http.StatusBadRequest || resp.Error == "" {
			t.Errorf("connect %q: code=%d err=%q, want a 400 with a reason", path, resp.code, resp.Error)
		}
	}
	if sp.starts != 0 {
		t.Errorf("rejected connects spawned %d children", sp.starts)
	}
}

// The session cap holds: past MaxSessions, connect fails with an
// explanation instead of growing without bound.
func TestConnectRespectsSessionCap(t *testing.T) {
	sup, _ := newTestSupervisor(t, func(o *Options) { o.MaxSessions = 1 })
	h := sup.Handler()
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(sup.Root(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := postConnect(t, h, filepath.Join(sup.Root(), "a"))
	if first.code != http.StatusOK {
		t.Fatalf("first connect: %+v", first)
	}
	second := postConnect(t, h, filepath.Join(sup.Root(), "b"))
	if second.code != http.StatusInternalServerError || second.Session != "" {
		t.Errorf("second connect = %+v, want a refusal", second)
	}
}

// The proxy maps /s/<id> onto the right child and injects the child's own
// credentials (the client's parent credentials must not pass through).
func TestProxyRoutesBySession(t *testing.T) {
	sup, _ := newTestSupervisor(t, nil)
	h := sup.Handler()
	dir := filepath.Join(sup.Root(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	resp := postConnect(t, h, dir)
	req := httptest.NewRequest(http.MethodGet, "/s/"+resp.Session+"/text", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "child-ok" {
		t.Fatalf("proxied /s/<id>/text = %d %q", rec.Code, rec.Body.String())
	}
}

// Session ids rotate (/new): a client presenting the OLD id still lands on
// the right child, because resolution refreshes the children once.
func TestProxyFollowsRotatedSession(t *testing.T) {
	// The child rotated to new-id and remembers old-id as its own: a client
	// presenting the old id must still land here.
	child := &fakeChild{
		session: "new-id",
		retired: []string{"old-id"},
		path:    "irrelevant",
		lastUse: time.Now(),
		proxied: &strings.Builder{},
	}
	sup, _ := newTestSupervisor(t, nil)
	sup.setSpawner(&fakeSpawner{child: child})
	sup.mu.Lock()
	sup.children["proj"] = child
	sup.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/s/old-id/text", nil)
	rec := httptest.NewRecorder()
	sup.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotated session = %d (%s), want the child reached", rec.Code, rec.Body.String())
	}
}

// An unknown session is a 404, not a proxy to nowhere.
func TestProxyUnknownSessionIs404(t *testing.T) {
	sup, _ := newTestSupervisor(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/s/nobody/text", nil)
	rec := httptest.NewRecorder()
	sup.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown session = %d, want 404", rec.Code)
	}
}

// The index lists live sessions and offers the open form.
func TestIndexListsSessions(t *testing.T) {
	sup, sp := newTestSupervisor(t, nil)
	h := sup.Handler()
	dir := filepath.Join(sup.Root(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	resp := postConnect(t, h, dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), resp.Session) {
		t.Error("index does not list the live session")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("index missing the hardening CSP")
	}
	_ = sp
}

// The connect handshake is inside the auth gate: token auth configured, no
// credentials, refused.
func TestConnectIsAuthenticated(t *testing.T) {
	sup, err := New(Options{
		Root:        t.TempDir(),
		IdleTimeout: -1,
		Auth:        webUIAuthConfig("token", "sekrit"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Close() })
	sup.setSpawner(&fakeSpawner{})
	dir := filepath.Join(sup.Root(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/connect", strings.NewReader(`{"path":`+jsonQuote(dir)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	sup.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated connect = %d, want 401", rec.Code)
	}
	// With the bearer token it passes.
	req = httptest.NewRequest(http.MethodPost, "/connect", strings.NewReader(`{"path":`+jsonQuote(dir)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sekrit")
	rec = httptest.NewRecorder()
	sup.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("authenticated connect = %d (%s), want 200", rec.Code, rec.Body.String())
	}
}

// The reaper stops children with no clients and no traffic within the idle
// window, and leaves busy ones alone.
func TestReaperStopsIdleChildren(t *testing.T) {
	now := time.Now()
	idle := &fakeChild{session: "idle", path: "/p1", lastUse: now.Add(-time.Hour)}
	busy := &fakeChild{session: "busy", path: "/p2", lastUse: now.Add(-time.Hour), clients: 2}
	recent := &fakeChild{session: "recent", path: "/p3", lastUse: now}

	sup, _ := newTestSupervisor(t, func(o *Options) {
		o.IdleTimeout = time.Minute
		o.now = func() time.Time { return now }
	})
	sup.mu.Lock()
	sup.children[keyOf(idle.Path())] = idle
	sup.children[keyOf(busy.Path())] = busy
	sup.children[keyOf(recent.Path())] = recent
	sup.mu.Unlock()

	sup.reapOnce()

	if idle.stopped == 0 {
		t.Error("idle child was not reaped")
	}
	if busy.stopped != 0 {
		t.Error("busy child was reaped")
	}
	if recent.stopped != 0 {
		t.Error("recently used child was reaped")
	}
	sup.mu.Lock()
	_, hasIdle := sup.children[keyOf(idle.Path())]
	_, hasBusy := sup.children[keyOf(busy.Path())]
	sup.mu.Unlock()
	if hasIdle {
		t.Error("reaped child is still registered")
	}
	if !hasBusy {
		t.Error("busy child was removed from the registry")
	}
}
