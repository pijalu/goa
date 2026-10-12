// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/pijalu/goa/internal/webui"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The child-process path (processSpawner → processChild) is exercised against
// a FAKE `goa server`: this test binary re-executes itself (TestMain) as a
// tiny HTTP server on the Unix socket the spawner handed it, speaking the
// two documents the real protocol needs (healthz, and an echo for proxied
// requests). That covers the exec/args/token/readiness/stop machinery
// without building the real binary, while the real-binary flow is covered by
// the e2e suite.

const fakeChildEnv = "GOA_SUPERVISOR_FAKE_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(fakeChildEnv) == "1" {
		runFakeChild()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeChildArgs is what the fake child needs from the spawner's command
// line.
type fakeChildArgs struct {
	sock        string
	token       string
	scheme      string
	sess        string
	rotateFile  string
	clientsFile string
	never       bool
}

// parseFakeChildArgs reads the args the spawner constructs, checking the
// pieces the supervisor must always send.
func parseFakeChildArgs(args []string) (*fakeChildArgs, error) {
	out := &fakeChildArgs{scheme: "token"}
	// flagValue reads the argument following a flag ("" at the end).
	flagValue := func(i int) string {
		if i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	setters := map[string]func(*fakeChildArgs, string){
		"--server-addr":       func(a *fakeChildArgs, v string) { a.sock = strings.TrimPrefix(v, webUIUnixPrefix) },
		"--server-auth-token": func(a *fakeChildArgs, v string) { a.token = v },
		"--server-auth":       func(a *fakeChildArgs, v string) { a.scheme = v },
		"--fake-never":        func(a *fakeChildArgs, v string) { a.never = v == "1" },
		"--fake-sess":         func(a *fakeChildArgs, v string) { a.sess = v },
		"--fake-rotate":       func(a *fakeChildArgs, v string) { a.rotateFile = v },
		"--fake-clients":      func(a *fakeChildArgs, v string) { a.clientsFile = v },
	}
	for i, arg := range args {
		if set, ok := setters[arg]; ok {
			set(out, flagValue(i))
		}
	}
	if out.sock == "" || out.token == "" {
		return nil, fmt.Errorf("fake child: missing --server-addr/--server-auth-token")
	}
	if out.scheme != "token" {
		return nil, fmt.Errorf("fake child: unexpected auth scheme %q", out.scheme)
	}
	if out.sess == "" {
		out.sess = "fake-sess-1"
	}
	return out, nil
}

// fakeChildState is the healthz view: the current session id and the client
// count, both driven by marker files the test writes.
type fakeChildState struct {
	mu          sync.Mutex
	sess        string
	rotateFile  string
	clientsFile string
}

// current resolves the session id, rotating once the marker file appears.
func (st *fakeChildState) current() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.rotateFile != "" {
		if _, err := os.Stat(st.rotateFile); err == nil && !strings.HasSuffix(st.sess, "-rotated") {
			st.sess += "-rotated"
		}
	}
	return st.sess
}

// clients reads the scripted attached-client count.
func (st *fakeChildState) clients() int {
	if st.clientsFile == "" {
		return 0
	}
	b, err := os.ReadFile(st.clientsFile)
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(string(b), "%d", &n)
	return n
}

// serveHealthz answers the token-checked status document.
func (st *fakeChildState) serveHealthz(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"session":%q,"clients":%d}`, st.current(), st.clients())
	}
}

// serveEcho answers every other path, so proxied requests are provable.
func serveEcho(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "fake-child-ok %s", r.URL.Path)
	}
}

// runFakeChild impersonates `goa server` for the spawner tests: it parses the
// very args the spawner constructs, serves healthz (token-checked) on the
// Unix socket, echoes every other path, and exits on SIGTERM — exactly the
// lifecycle Stop drives.
func runFakeChild() {
	args, err := parseFakeChildArgs(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if args.never {
		// Simulates a child that never wires its session: serve nothing.
		select {}
	}

	ln, err := net.Listen("unix", args.sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake child: listen: %v\n", err)
		os.Exit(3)
	}
	// A line on stdout: the supervisor pipes child output through its
	// prefixed logger, and the test asserts the line arrived.
	fmt.Println("fake child ready")

	state := &fakeChildState{sess: args.sess, rotateFile: args.rotateFile, clientsFile: args.clientsFile}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", state.serveHealthz(args.token))
	mux.HandleFunc("/", serveEcho(args.token))

	// SIGTERM is the reaper's stop signal: leave gracefully, like the real
	// server's shutdown wiring does.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	go func() {
		<-sig
		os.Exit(0)
	}()
	_ = http.Serve(ln, mux)
}

// webUIUnixPrefix mirrors webui.UnixSocketPrefix (the fake child avoids
// importing the parent package's dependency graph).
const webUIUnixPrefix = "unix://"

// fakeChildProc bundles a spawner-started process child with its fixtures.
type fakeChildProc struct {
	*processChild
	sock     string
	sessFile string // marker file: presence rotates the session id
	clientsF string
	logBuf   *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// startFakeChild spawns a fake child through the REAL spawner path.
func startFakeChild(t *testing.T, mutate func(args *[]string)) *fakeChildProc {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "child.sock")
	sessFile := filepath.Join(dir, "rotate.marker")
	clientsF := filepath.Join(dir, "clients")
	logBuf := &syncBuffer{}

	extra := []string{
		"--fake-sess", "fake-sess-1",
		"--fake-rotate", sessFile,
		"--fake-clients", clientsF,
	}
	if mutate != nil {
		mutate(&extra)
	}
	log := log.New(logBuf, "", 0)
	sp := &processSpawner{
		binary:  os.Args[0],
		extra:   extra,
		sockDir: dir,
		log:     log,
		timeout: 10 * time.Second,
		next:    new(int),
	}
	// The spawner builds its own exec.Command with a nil Env, which makes
	// the child inherit the parent's environment — so setting the fake-mode
	// flag with t.Setenv is how the re-executed test binary becomes the
	// fake child instead of running the test suite again.
	t.Setenv(fakeChildEnv, "1")
	child, err := sp.Start(context.Background(), dir)
	if err != nil {
		t.Fatalf("start fake child: %v", err)
	}
	pc, ok := child.(*processChild)
	if !ok {
		t.Fatalf("spawner returned %T", child)
	}
	// The env above reaches the child because exec inherits the parent's
	// environment (cmd.Env is nil ⇒ os.Environ), and t.Setenv set the flag.
	fc := &fakeChildProc{
		processChild: pc,
		sock:         sock,
		sessFile:     sessFile,
		clientsF:     clientsF,
		logBuf:       logBuf,
	}
	t.Cleanup(func() { _ = fc.Stop() })
	return fc
}

// The full child lifecycle: the spawner execs the fake `goa server`, waits
// for its readiness gate, the proxy speaks HTTP over the Unix socket with
// the child's token injected, a rotation is followed (old id still served),
// the prefixed log carries the child's output, and Stop removes the socket.
func TestProcessChildLifecycle(t *testing.T) {
	fc := startFakeChild(t, nil)

	t.Run("identity", func(t *testing.T) { assertChildIdentity(t, fc) })
	t.Run("proxied request", func(t *testing.T) { assertProxiedRequest(t, fc) })
	t.Run("rotation", func(t *testing.T) { assertRotationFollowed(t, fc) })
	t.Run("clients", func(t *testing.T) { assertClientCount(t, fc) })
	t.Run("child output", func(t *testing.T) {
		waitForCond(t, "child stdout in supervisor log", func() bool {
			return strings.Contains(fc.logBuf.String(), "fake child ready")
		})
	})
	t.Run("stop", func(t *testing.T) { assertStopRemovesSocket(t, fc) })
}

// assertChildIdentity checks the ready child reports its scripted session.
func assertChildIdentity(t *testing.T, fc *fakeChildProc) {
	t.Helper()
	if got := fc.Session(); got != "fake-sess-1" {
		t.Fatalf("session = %q, want fake-sess-1", got)
	}
	if !fc.Serves("fake-sess-1") || fc.Serves("other") {
		t.Errorf("Serves wrong: current=%v other=%v", fc.Serves("fake-sess-1"), fc.Serves("other"))
	}
	if fc.Clients() != 0 {
		t.Errorf("clients = %d, want 0", fc.Clients())
	}
	if fc.Path() == "" {
		t.Error("child path is empty")
	}
}

// assertProxiedRequest proves HTTP reaches the child over its socket with
// the child's own token, and that proxying counts as liveness.
func assertProxiedRequest(t *testing.T, fc *fakeChildProc) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://goa-child/assets/app.js", nil)
	w := newRecorder()
	fc.Proxy().ServeHTTP(w, req)
	if w.code != http.StatusOK || !strings.Contains(w.body, "fake-child-ok /assets/app.js") {
		t.Fatalf("proxied request = %d %q", w.code, w.body)
	}
	if fc.LastUse().Before(time.Now().Add(-time.Minute)) {
		t.Error("proxy did not touch the last-use clock")
	}
}

// assertRotationFollowed makes the child rotate via its marker file and
// checks the supervisor followed — remembering the retired id.
func assertRotationFollowed(t *testing.T, fc *fakeChildProc) {
	t.Helper()
	if err := os.WriteFile(fc.sessFile, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fc.Refresh(context.Background()) {
		t.Fatal("refresh failed")
	}
	if got := fc.Session(); got != "fake-sess-1-rotated" {
		t.Fatalf("session after rotation = %q", got)
	}
	if !fc.Serves("fake-sess-1") {
		t.Error("retired id no longer served")
	}
}

// assertClientCount checks the scripted count flows from healthz.
func assertClientCount(t *testing.T, fc *fakeChildProc) {
	t.Helper()
	if err := os.WriteFile(fc.clientsF, []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = fc.Refresh(context.Background())
	if fc.Clients() != 3 {
		t.Errorf("clients = %d, want 3", fc.Clients())
	}
}

// assertStopRemovesSocket stops the child and pins the cleanup contract.
func assertStopRemovesSocket(t *testing.T, fc *fakeChildProc) {
	t.Helper()
	if err := fc.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(fc.sock); !os.IsNotExist(err) {
		t.Errorf("socket still present after stop (err=%v)", err)
	}
	if err := fc.Stop(); err != nil {
		t.Errorf("second stop: %v", err)
	}
}

// A child that never wires its session must fail Start within the ready
// deadline instead of hanging the connect handshake.
func TestProcessChildNotReadyFails(t *testing.T) {
	dir := t.TempDir()
	sp := &processSpawner{
		binary:  os.Args[0],
		extra:   []string{"--fake-never", "1"},
		sockDir: dir,
		log:     log.New(io.Discard, "", 0),
		timeout: 750 * time.Millisecond,
		next:    new(int),
	}
	t.Setenv(fakeChildEnv, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := sp.Start(ctx, dir); err == nil {
		t.Fatal("start succeeded for a child that never became ready")
	}
}

// The proxy refuses a child that died: a request to a stopped child is a 502
// with a reason, not a hang.
func TestProxyAfterChildDeath(t *testing.T) {
	fc := startFakeChild(t, nil)
	rp := fc.Proxy()
	if err := fc.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	w := newRecorder()
	req, _ := http.NewRequest(http.MethodGet, "http://goa-child/anything", nil)
	rp.ServeHTTP(w, req)
	if w.code != http.StatusBadGateway {
		t.Errorf("dead child = %d, want 502", w.code)
	}
}

// A supervisor-resolved Child that does not implement Proxy() (a bare fake)
// gets the explicit no-proxy answer.
func TestProxyForChildWithoutProxy(t *testing.T) {
	sup, _ := newTestSupervisor(t, nil)
	bare := &bareChild{session: "bare"}
	sup.mu.Lock()
	sup.children["p"] = bare
	sup.mu.Unlock()
	req := httptest.NewRequest(http.MethodGet, "/s/bare/text", nil)
	rec := httptest.NewRecorder()
	sup.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("child without proxy = %d, want 502", rec.Code)
	}
}

// bareChild satisfies Child alone (no Proxy method).
type bareChild struct{ session string }

func (c *bareChild) Session() string                  { return c.session }
func (c *bareChild) Serves(id string) bool            { return id == c.session }
func (c *bareChild) Clients() int                     { return 0 }
func (c *bareChild) Path() string                     { return "/bare" }
func (c *bareChild) LastUse() time.Time               { return time.Now() }
func (c *bareChild) Refresh(ctx context.Context) bool { return true }
func (c *bareChild) Stop() error                      { return nil }

// The connect handshake serves the browser form too: a urlencoded POST gets
// a redirect to the session page, and a rejected path gets the HTML error.
func TestConnectBrowserForm(t *testing.T) {
	sup, sp := newTestSupervisor(t, nil)
	h := sup.Handler()
	dir := filepath.Join(sup.Root(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	form := "path=" + url.QueryEscape(dir)
	req := httptest.NewRequest(http.MethodPost, "/connect", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form connect = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/s/") {
		t.Errorf("redirect = %q, want /s/<id>", loc)
	}
	if sp.starts != 1 {
		t.Errorf("spawns = %d, want 1", sp.starts)
	}

	// A rejected path in a browser gets the HTML error page, not JSON.
	req = httptest.NewRequest(http.MethodPost, "/connect", strings.NewReader("path=relative"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "<html>") {
		t.Errorf("form rejection = %d %q, want an HTML 400", rec.Code, rec.Body.String())
	}
}

// /sessions and /healthz answer with the supervisor's own status.
func TestSessionsAndHealthz(t *testing.T) {
	sup, _ := newTestSupervisor(t, nil)
	h := sup.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	var health struct {
		OK       bool   `json:"ok"`
		Session  string `json:"session"`
		Sessions int    `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("healthz body: %v", err)
	}
	if !health.OK || health.Session != "" || health.Sessions != 0 {
		t.Errorf("healthz = %+v", health)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "[") {
		t.Errorf("sessions = %d %q", rec.Code, rec.Body.String())
	}
}

// The supervisor's own ListenAndServe binds, serves, and stops on ctx.
func TestListenAndServeStopsOnContext(t *testing.T) {
	// Pick a free port, then hand it to the supervisor.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().String()
	_ = ln.Close()

	logBuf := &syncBuffer{}
	sup, err := New(Options{
		Root:        t.TempDir(),
		Addr:        port,
		IdleTimeout: -1,
		Log:         log.New(logBuf, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.ListenAndServe(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + port + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logBuf.String(), "multi-project") {
		t.Errorf("startup banner missing: %q", logBuf.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not stop on context cancellation")
	}
}

// prefixWriter turns a child's stream into log lines, buffering a partial
// last line until its newline arrives.
func TestPrefixWriter(t *testing.T) {
	buf := &syncBuffer{}
	w := &prefixWriter{prefix: "child: ", log: log.New(buf, "", 0)}
	w.Write([]byte("one\ntwo"))
	if strings.Contains(buf.String(), "two") {
		t.Error("partial line was logged early")
	}
	w.Write([]byte("\nthree"))
	out := buf.String()
	for _, want := range []string{"child: one\n", "child: two\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q in %q", want, out)
		}
	}
}

// newToken mints distinct, non-empty tokens.
func TestNewToken(t *testing.T) {
	a, err := newToken()
	if err != nil || a == "" {
		t.Fatalf("token = %q, %v", a, err)
	}
	b, _ := newToken()
	if a == b {
		t.Error("two tokens collided")
	}
}

// --- small helpers the tests above share ---

func newRecorder() *countRecorder { return &countRecorder{header: http.Header{}} }

type countRecorder struct {
	header http.Header
	body   string
	code   int
}

func (r *countRecorder) Header() http.Header         { return r.header }
func (r *countRecorder) Write(b []byte) (int, error) { r.body += string(b); return len(b), nil }
func (r *countRecorder) WriteHeader(code int)        { r.code = code }

func waitForCond(t *testing.T, what string, cond func() bool) {
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

// The production spawner wires the real binary and the shared socket dir.
func TestProcessSpawnerConfig(t *testing.T) {
	sup, _ := newTestSupervisor(t, nil)
	sp, ok := sup.processSpawner().(*processSpawner)
	if !ok {
		t.Fatalf("processSpawner returned %T", sp)
	}
	if sp.binary == "" {
		t.Error("spawner binary is empty")
	}
	if sp.sockDir != sup.sockDir {
		t.Error("spawner does not share the supervisor's socket dir")
	}
	if sp.timeout != sup.opts.ReadyTimeout {
		t.Error("spawner ready timeout does not match the options")
	}
	// An explicit Binary wins over os.Args[0].
	sup2, _ := newTestSupervisor(t, nil)
	sup2.opts.Binary = "/custom/goa"
	sp2 := sup2.processSpawner().(*processSpawner)
	if sp2.binary != "/custom/goa" {
		t.Errorf("explicit binary ignored: %q", sp2.binary)
	}
}

// An unparseable auth scheme is refused before anything binds.
func TestNewRejectsBadAuth(t *testing.T) {
	if _, err := New(Options{Root: t.TempDir(), Auth: webui.AuthConfig{Mode: "bogus"}}); err == nil {
		t.Fatal("New accepted an unknown auth mode")
	}
}

// The reaper loop runs on its ticker and stops with its context: with a
// shrunken interval, an idle child is reaped by the LOOP itself (not just
// by the direct reapOnce call the unit test drives).
func TestReapLoopReapsAndStops(t *testing.T) {
	old := reapInterval
	reapInterval = 10 * time.Millisecond
	t.Cleanup(func() { reapInterval = old })

	now := time.Now()
	idle := &fakeChild{session: "idle", path: "/p1", lastUse: now.Add(-time.Hour)}
	sup, _ := newTestSupervisor(t, func(o *Options) {
		o.IdleTimeout = time.Minute
		o.now = func() time.Time { return now }
	})
	sup.mu.Lock()
	sup.children[keyOf(idle.Path())] = idle
	sup.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go sup.reapLoop(ctx)
	waitForCond(t, "reaper to reap the idle child", func() bool {
		sup.mu.Lock()
		defer sup.mu.Unlock()
		if idle.stops() == 0 {
			return false
		}
		_, present := sup.children[keyOf(idle.Path())]
		return !present
	})
	cancel()
}

// A disabled reaper returns immediately.
func TestReapLoopDisabled(t *testing.T) {
	sup, _ := newTestSupervisor(t, func(o *Options) { o.IdleTimeout = -1 })
	done := make(chan struct{})
	go func() { sup.reapLoop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabled reaper did not return")
	}
}

// A socket directory that cannot be created is a startup error, not a
// supervisor that silently spawns children next to nothing.
func TestNewSocketDirFailure(t *testing.T) {
	// The root must already exist: this test is about the socket dir.
	root := t.TempDir()
	t.Setenv("TMPDIR", "/nonexistent-goa-test-dir")
	if _, err := New(Options{Root: root}); err == nil {
		t.Fatal("New succeeded with an unusable TMPDIR")
	}
}
