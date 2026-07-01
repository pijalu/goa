// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

// newInMemoryClient builds a StdioClient wired to an in-memory pipe pair so
// tests can drive the reader without spawning a real server process. stdoutR
// is what the server "writes"; writes to it become frames the client reads.
func newInMemoryClient(t *testing.T) (*StdioClient, *pipe, *safeBuffer) {
	t.Helper()
	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()
	c := &StdioClient{
		stdin:   stdinW,
		stdout:  stdoutR,
		pending: make(map[int]chan rpcResponse),
	}
	if err := c.startReaderLoop(); err != nil {
		t.Fatalf("startReaderLoop: %v", err)
	}
	// Capture what the client sends on stdin (the requests) so the fake
	// server loop can reply. Also drain stdinR so writes do not block.
	serverIn := &safeBuffer{}
	go func() {
		_, _ = io.Copy(serverIn, stdinR)
	}()
	return c, &pipe{r: stdoutR, w: stdoutW}, serverIn
}

type pipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

// safeBuffer is a goroutine-safe bytes.Buffer for capturing client requests.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestStdioClient_NotificationBeforeResponse writes a notification frame and
// a response in a single Write, asserting the notification is dispatched and
// the response is routed to the right waiter without losing buffered data.
func TestStdioClient_NotificationBeforeResponse(t *testing.T) {
	c, p, _ := newInMemoryClient(t)
	var notifMu sync.Mutex
	gotNotifs := []string{}
	c.SetNotificationHandler(func(method string, params json.RawMessage) {
		notifMu.Lock()
		gotNotifs = append(gotNotifs, method)
		notifMu.Unlock()
	})

	// Start the call in a goroutine; it sends a request then waits.
	type res struct {
		v   string
		err error
	}
	resCh := make(chan res, 1)
	go func() {
		out, err := c.call(context.Background(), "tools/list", map[string]any{})
		resCh <- res{string(out), err}
	}()

	// Give the request a moment to be written, then push a notification plus
	// the matching response in one Write (single flush).
	time.Sleep(50 * time.Millisecond)
	frame := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"p":1}}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"read"}]}}` + "\n"
	if _, err := p.w.Write([]byte(frame)); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("call returned error: %v", r.err)
		}
		if !bytes.Contains([]byte(r.v), []byte("read")) {
			t.Errorf("response missing tool: %q", r.v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call timed out — response not demultiplexed")
	}

	notifMu.Lock()
	defer notifMu.Unlock()
	if len(gotNotifs) != 1 || gotNotifs[0] != "notifications/progress" {
		t.Errorf("notifications = %v, want [notifications/progress]", gotNotifs)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestStdioClient_ConcurrentCallsDemuxed fires 100 concurrent Call's, each
// with a random per-call timeout, against a fake server that echoes each
// requested id back. It asserts no deadlock and that the client closes
// cleanly with no leaked goroutines.
func TestStdioClient_ConcurrentCallsDemuxed(t *testing.T) {
	c, p, _ := newInMemoryClient(t)

	// Fake server: read newline-delimited requests from the client's stdin
	// (captured via a tee) and respond. Because we capture stdin in a buffer
	// rather than a live stream, we instead drive responses from a responder
	// goroutine that answers a known id range.
	const n = 100
	go runResponder(c, p.w, n)

	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			timeout := time.Duration(200+rand.Intn(800)) * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_, _ = c.call(ctx, "ping", map[string]any{}) // errors from timeouts are fine
		}()
	}
	wg.Wait()

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Give the reader goroutine a moment to exit after Close.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	// We expect at most a small bounded increase (responder goroutine exits on
	// close). A large leak (one goroutine per stuck Call) would blow this.
	if after > before+5 {
		t.Errorf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestStdioClient_CallCancelledUnblocks asserts that cancelling a Call's ctx
// unblocks it promptly (via context.AfterFunc) without disturbing other
// in-flight calls or the reader goroutine.
func TestStdioClient_CallCancelledUnblocks(t *testing.T) {
	c, p, _ := newInMemoryClient(t)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, "slow", map[string]any{})
		done <- err
	}()

	time.Sleep(50 * time.Millisecond) // let the request register
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from cancelled call")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled call did not unblock (AfterFunc not wired)")
	}
	// A subsequent call must still work — the reader goroutine survived.
	go func() {
		// Answer only id 2.
		time.Sleep(30 * time.Millisecond)
		c.mu.Lock()
		ids := make([]int, 0)
		for id := range c.pending {
			ids = append(ids, id)
		}
		c.mu.Unlock()
		for _, id := range ids {
			_, _ = p.w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(id) + `,"result":{"ok":true}}` + "\n"))
		}
	}()
	if _, err := c.call(context.Background(), "next", map[string]any{}); err != nil {
		t.Errorf("subsequent call failed: %v", err)
	}
}

func runResponder(c *StdioClient, w io.Writer, n int) {
	answered := make(map[int]bool)
	for {
		if c.closed.Load() {
			return
		}
		pending := collectPendingIDs(c, answered)
		for _, id := range pending {
			answered[id] = true
			resp := `{"jsonrpc":"2.0","id":` + itoa(id) + `,"result":{"ok":true}}` + "\n"
			_, _ = w.Write([]byte(resp))
		}
		if len(answered) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func collectPendingIDs(c *StdioClient, answered map[int]bool) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := make([]int, 0, len(c.pending))
	for id := range c.pending {
		if !answered[id] {
			pending = append(pending, id)
		}
	}
	return pending
}

func itoa(i int) string {
	// tiny int->string to avoid pulling strconv formatting quirks
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
