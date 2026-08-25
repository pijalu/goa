// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInitOptions_TypeScriptWorkspace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "node_modules", "typescript", "lib")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	ts := filepath.Join(path, "tsserver.js")
	if err := os.WriteFile(ts, []byte("// tsserver"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := initOptions(&ServerSpec{Dynamic: &DynamicSpec{TypeScriptServer: true}}, root)
	server, ok := opts["tsserver"].(map[string]any)
	if !ok || server["path"] != ts {
		t.Fatalf("options=%#v, want tsserver path %q", opts, ts)
	}
}

// fakeServerRecorder records the ServerConfig a spawned server was given.
type fakeServerRecorder struct {
	mu      sync.Mutex
	configs []ServerConfig
}

func (r *fakeServerRecorder) factory(sink *syncBuffer) func(ctx context.Context, cfg ServerConfig) (*Server, error) {
	return func(ctx context.Context, cfg ServerConfig) (*Server, error) {
		r.mu.Lock()
		r.configs = append(r.configs, cfg)
		r.mu.Unlock()
		// Auto-responding conn: each client request immediately gets a success
		// response so Initialize/Initialized (and any query) complete without a
		// real subprocess.
		return &Server{client: newLoopbackClient(sink)}, nil
	}
}

// syncBuffer is a goroutine-safe byte sink for the wire payload.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	b.mu.Unlock()
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// loopbackConn answers every JSON-RPC request written to it with a success
// response on the read side, so a *Client's request/response cycle completes.
type loopbackConn struct {
	sink    *syncBuffer
	pending chan []byte
}

func newLoopbackClient(sink *syncBuffer) *Client {
	c := &loopbackConn{sink: sink, pending: make(chan []byte, 128)}
	client := NewClient(c)
	go client.ReadNotifications(context.Background())
	return client
}

func (c *loopbackConn) Read(p []byte) (int, error) {
	body, ok := <-c.pending
	if !ok {
		return 0, io.EOF
	}
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	return copy(p, msg), nil
}

func (c *loopbackConn) Write(p []byte) (int, error) {
	c.sink.Write(p)
	// Parse the request id from the JSON body (after the headers) and queue a
	// matching success response. Notifications (no id) produce no response.
	body := string(p)
	if idx := strings.Index(body, "\r\n\r\n"); idx >= 0 {
		body = body[idx+4:]
	}
	var req struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(body), &req); err == nil && req.ID != 0 {
		result := `{}`
		switch req.Method {
		case "textDocument/implementation", "workspace/symbol", "textDocument/prepareCallHierarchy", "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls":
			result = `[]`
		case "textDocument/diagnostic":
			result = `{"kind":"full","resultId":"test-result","items":[]}`
		}
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
		c.pending <- []byte(resp)
	}
	return len(p), nil
}

func (c *loopbackConn) Close() error                       { close(c.pending); return nil }
func (c *loopbackConn) LocalAddr() net.Addr                { return nil }
func (c *loopbackConn) RemoteAddr() net.Addr               { return nil }
func (c *loopbackConn) SetDeadline(t time.Time) error      { return nil }
func (c *loopbackConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *loopbackConn) SetWriteDeadline(t time.Time) error { return nil }

// startedManager returns a started multi-server Manager backed by fake servers.
// Command resolution is bypassed: each spec's Command[0] is used verbatim so
// tests need no real binaries.
func startedManager(t *testing.T, dir string, rec *fakeServerRecorder, sink *syncBuffer, specs []ServerSpec) *Manager {
	t.Helper()
	m := NewManager(dir, WithServers(specs))
	m.serverFactory = rec.factory(sink)
	m.resolve = func(spec *ServerSpec, workspace, binDir string, installAllowed bool) ([]string, bool) {
		return spec.Command, len(spec.Command) > 0
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return m
}

var testSpecs = []ServerSpec{
	{ID: "gopls", Extensions: []string{".go"}, Markers: []string{"go.mod"}, Command: []string{"gopls"}, LanguageID: "go"},
	{ID: "pyright", Extensions: []string{".py"}, Markers: []string{"pyproject.toml"}, Command: []string{"pyright-langserver"}, LanguageID: "python"},
}

// waitReady waits (bounded) for the server handling path to finish its
// asynchronous spawn. Touches (OpenDocument/DidChange) are non-blocking by
// design — tests that need a READY server synchronize here first.
func waitReady(t *testing.T, m *Manager, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c := m.waitClientFor(ctx, path); c == nil {
		t.Fatalf("server for %s did not start", path)
	}
}

// openSync opens path after its server is ready (test convenience for the
// production non-blocking OpenDocument).
func openSync(t *testing.T, m *Manager, path, text string) {
	t.Helper()
	waitReady(t, m, path)
	if err := m.OpenDocument(context.Background(), path, text); err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
}

func TestManager_StartAndClose(t *testing.T) {
	rec := &fakeServerRecorder{}
	sink := &syncBuffer{}
	mgr := startedManager(t, t.TempDir(), rec, sink, testSpecs)
	if !mgr.Started() {
		t.Error("expected manager to be started")
	}
	if err := mgr.Close(context.Background()); err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestManager_NotStarted(t *testing.T) {
	mgr := NewManager(t.TempDir(), WithServers(testSpecs))
	if err := mgr.OpenDocument(context.Background(), "main.go", "package main"); err == nil {
		t.Error("expected error when not started")
	}
}

func TestManager_SupportsPath(t *testing.T) {
	mgr := startedManager(t, t.TempDir(), &fakeServerRecorder{}, &syncBuffer{}, testSpecs)
	if !mgr.SupportsPath("main.go") || !mgr.SupportsPath("app.py") {
		t.Fatal("expected Go and Python support")
	}
	if mgr.SupportsPath("notes.txt") {
		t.Fatal("unexpected text-file support")
	}
}

func TestManager_PullDiagnosticsPreservesCleanState(t *testing.T) {
	dir := t.TempDir()
	mgr := startedManager(t, dir, &fakeServerRecorder{}, &syncBuffer{}, testSpecs)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := mgr.PullDiagnostics(ctx, "main.go")
	if err != nil {
		t.Fatalf("pull diagnostics: %v", err)
	}
	if !snap.Published || len(snap.Diagnostics) != 0 {
		t.Fatalf("expected explicit clean report, got %+v", snap)
	}
}

func TestManager_NavigationQueries(t *testing.T) {
	dir := t.TempDir()
	mgr := startedManager(t, dir, &fakeServerRecorder{}, &syncBuffer{}, testSpecs)
	openSync(t, mgr, "main.go", "package main")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := mgr.Implementation(ctx, "main.go", 0, 0); err != nil {
		t.Fatalf("implementation: %v", err)
	}
	if _, err := mgr.WorkspaceSymbols(ctx, "main.go", "main"); err != nil {
		t.Fatalf("workspace symbols: %v", err)
	}
	if got, err := mgr.PrepareCallHierarchy(ctx, "main.go", 0, 0); err != nil || got == nil {
		t.Fatalf("prepare hierarchy: %v %#v", err, got)
	}
	if got, err := mgr.IncomingCalls(ctx, "main.go", 0, 0); err != nil || got == nil {
		t.Fatalf("incoming calls: %v %#v", err, got)
	}
	if got, err := mgr.OutgoingCalls(ctx, "main.go", 0, 0); err != nil || got == nil {
		t.Fatalf("outgoing calls: %v %#v", err, got)
	}
}

func TestManager_NoServerForExtension(t *testing.T) {
	rec := &fakeServerRecorder{}
	mgr := startedManager(t, t.TempDir(), rec, &syncBuffer{}, testSpecs)
	// .txt matches no spec: OpenDocument must report no server, not spawn.
	if err := mgr.OpenDocument(context.Background(), "notes.txt", "hi"); err == nil {
		t.Error("expected no-server error for .txt")
	}
	if len(rec.configs) != 0 {
		t.Errorf("no server should have spawned, got %d", len(rec.configs))
	}
}

func TestManager_PerFileServerSelection(t *testing.T) {
	rec := &fakeServerRecorder{}
	mgr := startedManager(t, t.TempDir(), rec, &syncBuffer{}, testSpecs)
	// Touches kick the async spawns; wait for both servers to come up.
	_ = mgr.OpenDocument(context.Background(), "main.go", "package main")
	_ = mgr.OpenDocument(context.Background(), "app.py", "print(1)")
	waitReady(t, mgr, "main.go")
	waitReady(t, mgr, "app.py")
	if len(rec.configs) != 2 {
		t.Fatalf("expected 2 spawned servers (gopls + pyright), got %d", len(rec.configs))
	}
	got := map[string]bool{}
	for _, cfg := range rec.configs {
		got[cfg.Command] = true
	}
	if !got["gopls"] || !got["pyright-langserver"] {
		t.Errorf("spawned servers = %v, want gopls and pyright-langserver", got)
	}
}

func TestManager_LazySpawnReusesClient(t *testing.T) {
	rec := &fakeServerRecorder{}
	dir := t.TempDir()
	mgr := startedManager(t, dir, rec, &syncBuffer{}, testSpecs)
	// Two Go files in the same root share ONE gopls client.
	openSync(t, mgr, "a.go", "package main")
	openSync(t, mgr, "b.go", "package main")
	if len(rec.configs) != 1 {
		t.Errorf("expected 1 gopls client for two go files, got %d", len(rec.configs))
	}
}

func TestManager_DidChangeIncrementsVersion(t *testing.T) {
	sink := &syncBuffer{}
	mgr := startedManager(t, t.TempDir(), &fakeServerRecorder{}, sink, testSpecs)
	ctx := context.Background()
	openSync(t, mgr, "main.go", "package main")
	for i := 0; i < 3; i++ {
		if err := mgr.DidChange(ctx, "main.go", "package main\n"); err != nil {
			t.Fatalf("didChange %d: %v", i, err)
		}
	}
	out := sink.String()
	for want := 2; want <= 4; want++ {
		needle := `"version":` + strings.Repeat(" ", 0) + itoa(want)
		if !strings.Contains(out, needle) {
			t.Errorf("expected DidChange to send %s", needle)
		}
	}
}

// TestManager_UnavailableServerMarkedBroken verifies a server that fails to
// resolve is marked broken and not retried (and the op returns an error).
func TestManager_UnavailableServerMarkedBroken(t *testing.T) {
	specs := []ServerSpec{
		{ID: "nope", Extensions: []string{".xyz"}, Command: []string{"whatever"}},
	}
	rec := &fakeServerRecorder{}
	mgr := NewManager(t.TempDir(), WithServers(specs))
	mgr.serverFactory = rec.factory(&syncBuffer{})
	mgr.resolve = func(spec *ServerSpec, workspace, binDir string, installAllowed bool) ([]string, bool) {
		return nil, false // simulate unresolvable server
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// First touch kicks the async spawn (reports "starting"), which resolves
	// to broken; the next touch reports the failure.
	if err := mgr.OpenDocument(context.Background(), "f.xyz", "x"); err == nil {
		t.Fatal("expected error for unavailable server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c := mgr.waitClientFor(ctx, "f.xyz"); c != nil {
		t.Fatal("expected no client for unavailable server")
	}
	if err := mgr.OpenDocument(context.Background(), "f.xyz", "x"); err == nil {
		t.Fatal("expected broken-server error after spawn resolved")
	}
	if len(rec.configs) != 0 {
		t.Errorf("unavailable server should not spawn, got %d configs", len(rec.configs))
	}
}

func itoa(i int) string {
	return string(rune('0' + i))
}
