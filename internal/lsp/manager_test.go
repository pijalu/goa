// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

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
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &req); err == nil && req.ID != 0 {
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, req.ID)
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
	m.resolve = func(spec *ServerSpec, binDir string, installAllowed bool) ([]string, bool) {
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
	ctx := context.Background()
	if err := mgr.OpenDocument(ctx, "main.go", "package main"); err != nil {
		t.Fatalf("open go: %v", err)
	}
	if err := mgr.OpenDocument(ctx, "app.py", "print(1)"); err != nil {
		t.Fatalf("open py: %v", err)
	}
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
	ctx := context.Background()
	// Two Go files in the same root share ONE gopls client.
	if err := mgr.OpenDocument(ctx, "a.go", "package main"); err != nil {
		t.Fatalf("open a.go: %v", err)
	}
	if err := mgr.OpenDocument(ctx, "b.go", "package main"); err != nil {
		t.Fatalf("open b.go: %v", err)
	}
	if len(rec.configs) != 1 {
		t.Errorf("expected 1 gopls client for two go files, got %d", len(rec.configs))
	}
}

func TestManager_DidChangeIncrementsVersion(t *testing.T) {
	sink := &syncBuffer{}
	mgr := startedManager(t, t.TempDir(), &fakeServerRecorder{}, sink, testSpecs)
	ctx := context.Background()
	if err := mgr.OpenDocument(ctx, "main.go", "package main"); err != nil {
		t.Fatalf("open: %v", err)
	}
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
	mgr.resolve = func(spec *ServerSpec, binDir string, installAllowed bool) ([]string, bool) {
		return nil, false // simulate unresolvable server
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := mgr.OpenDocument(context.Background(), "f.xyz", "x"); err == nil {
		t.Fatal("expected error for unavailable server")
	}
	if len(rec.configs) != 0 {
		t.Errorf("unavailable server should not spawn, got %d configs", len(rec.configs))
	}
}

func itoa(i int) string {
	return string(rune('0' + i))
}
