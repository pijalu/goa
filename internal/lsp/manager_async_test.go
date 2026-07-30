// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package lsp

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedFactory blocks every spawn on a channel so tests control exactly when
// (and whether) the "server start" completes — simulating a slow npx download
// or a wedged server (bugs.md "Read stuck").
type gatedFactory struct {
	mu      sync.Mutex
	gate    chan struct{}
	entered chan struct{} // signaled (non-blocking) when a spawn enters the factory
	started int
	sink    *syncBuffer
}

func newGatedFactory(sink *syncBuffer) *gatedFactory {
	return &gatedFactory{gate: make(chan struct{}), entered: make(chan struct{}, 1), sink: sink}
}

func (f *gatedFactory) factory() func(ctx context.Context, cfg ServerConfig) (*Server, error) {
	return func(ctx context.Context, cfg ServerConfig) (*Server, error) {
		f.mu.Lock()
		f.started++
		f.mu.Unlock()
		select {
		case f.entered <- struct{}{}:
		default: // a previous spawn already signaled
		}
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &Server{client: newLoopbackClient(f.sink)}, nil
	}
}

func (f *gatedFactory) spawnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// awaitEntered blocks until the first spawn has entered the factory, failing
// the test when no spawn is kicked within the timeout. Asserting on
// spawnCount without this edge races the async spawn goroutine: under load
// (go test ./...) the goroutine may not be scheduled before the counting
// goroutine runs, reporting a spurious "0 spawns".
func (f *gatedFactory) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-f.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn never entered the factory within 2s")
	}
}

// release lets all blocked and future spawns complete.
func (f *gatedFactory) release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.gate: // already closed
	default:
		close(f.gate)
	}
}

// hangConn answers no request ever: reads block until close. It simulates a
// server that accepts its stdio pipe but never speaks — the pre-fix
// Initialize-with-Background-ctx wedge.
type hangConn struct {
	closed chan struct{}
	once   sync.Once
}

func newHangConn() *hangConn { return &hangConn{closed: make(chan struct{})} }

func (c *hangConn) Read(p []byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *hangConn) Write(p []byte) (int, error) { return len(p), nil }

func (c *hangConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *hangConn) LocalAddr() net.Addr                { return nil }
func (c *hangConn) RemoteAddr() net.Addr               { return nil }
func (c *hangConn) SetDeadline(t time.Time) error      { return nil }
func (c *hangConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *hangConn) SetWriteDeadline(t time.Time) error { return nil }

// hangFactory returns servers whose conn never answers Initialize.
func hangFactory() func(ctx context.Context, cfg ServerConfig) (*Server, error) {
	return func(ctx context.Context, cfg ServerConfig) (*Server, error) {
		conn := newHangConn()
		client := NewClient(conn)
		go client.ReadNotifications(context.Background())
		return &Server{client: client}, nil
	}
}

// startedAsyncManager builds a started Manager with a controllable factory.
func startedAsyncManager(t *testing.T, factory func(ctx context.Context, cfg ServerConfig) (*Server, error)) *Manager {
	t.Helper()
	m := NewManager(t.TempDir(), WithServers(testSpecs))
	m.serverFactory = factory
	m.resolve = func(spec *ServerSpec, binDir string, installAllowed bool) ([]string, bool) {
		return spec.Command, len(spec.Command) > 0
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.Close(ctx)
	})
	return m
}

// TestManager_TouchNeverBlocksOnSpawn is the bugs.md "Read stuck" regression:
// a DidChange touch must return immediately even when the server spawn hangs
// forever (cold npx download / wedged server) — pre-fix the touch parked on
// the synchronous spawn for its full duration (55s observed, potentially ∞).
func TestManager_TouchNeverBlocksOnSpawn(t *testing.T) {
	gated := newGatedFactory(&syncBuffer{})
	mgr := startedAsyncManager(t, gated.factory())
	defer gated.release() // let cleanup Close proceed

	// Fire several concurrent touches: ALL must return promptly, and exactly
	// ONE spawn may be kicked (single-flight).
	const goroutines = 8
	start := time.Now()
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			errs[g] = mgr.DidChange(context.Background(), "file.go", "package main\n")
		}(g)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("touches blocked for %s on a hanging spawn (want immediate)", elapsed)
	}
	for g, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "still starting") {
			t.Errorf("touch %d: expected 'still starting' error, got %v", g, err)
		}
	}
	// The spawn runs on its own goroutine (clientFor kicks it async): wait
	// until it has entered the factory before counting. While that first
	// spawn is parked on the gate, m.spawning[key] stays set, so no second
	// spawn can be kicked — observing entry then counting 1 proves the
	// single-flight.
	gated.awaitEntered(t)
	if n := gated.spawnCount(); n != 1 {
		t.Errorf("expected single-flight (1 spawn), got %d", n)
	}
}

// TestManager_WaitClientForSpawn verifies the query path waits (bounded by
// ctx) for an in-flight spawn, and that a touch AFTER the spawn completes
// opens the document (self-healing after the dropped starting-phase touch).
func TestManager_WaitClientForSpawn(t *testing.T) {
	gated := newGatedFactory(&syncBuffer{})
	sink := gated.sink
	mgr := startedAsyncManager(t, gated.factory())

	// Kick the spawn via a dropped touch.
	_ = mgr.DidChange(context.Background(), "main.go", "package main\n")

	// A waiter with a short ctx times out while the spawn is still gated.
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	if c := mgr.waitClientFor(short, "main.go"); c != nil {
		cancel()
		t.Fatal("expected nil client while spawn is gated")
	}
	cancel()

	// Release the spawn: a fresh waiter gets the client.
	gated.release()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := mgr.waitClientFor(ctx, "main.go")
	if c == nil {
		t.Fatal("expected client after spawn release")
	}

	// The touch dropped during startup is healed by the next touch: didOpen
	// reaches the server now.
	if err := mgr.DidChange(context.Background(), "main.go", "package main\n"); err != nil {
		t.Fatalf("didChange after spawn: %v", err)
	}
	if !strings.Contains(sink.String(), `"method":"textDocument/didOpen"`) {
		t.Errorf("expected didOpen after spawn completed, wire:\n%s", sink.String())
	}
}

// TestManager_SpawnHandshakeTimeout verifies a server that accepts its pipe
// but never answers Initialize is marked broken after the handshake timeout
// instead of wedging the caller forever.
func TestManager_SpawnHandshakeTimeout(t *testing.T) {
	orig := spawnHandshakeTimeout
	spawnHandshakeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { spawnHandshakeTimeout = orig })

	mgr := startedAsyncManager(t, hangFactory())

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c := mgr.waitClientFor(ctx, "main.go"); c != nil {
		t.Fatal("expected nil client for a server that never answers Initialize")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("handshake was not bounded: %s", elapsed)
	}

	// The key is now broken: touches fail fast with the broken error.
	err := mgr.DidChange(context.Background(), "main.go", "package main\n")
	if err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Errorf("expected 'failed to start' after handshake timeout, got %v", err)
	}
}
