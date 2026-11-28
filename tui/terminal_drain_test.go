// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"io"
	"sync"
	"testing"
	"time"
)

// TestDrainInputFallback_ExitsOnEOF verifies that the fallback drain loop
// terminates immediately when stdin reaches EOF.
func TestDrainInputFallback_ExitsOnEOF(t *testing.T) {
	oldRead := stdinRead
	oldFile := stdinFile
	defer func() {
		stdinRead = oldRead
		stdinFile = oldFile
	}()

	stdinFile = nil
	stdinRead = func(buf []byte) (int, error) {
		return 0, io.EOF
	}

	done := make(chan struct{})
	go func() {
		drainInputFallback(100, 10)
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(time.Second):
		t.Fatal("drainInputFallback did not exit on EOF")
	}
}

// TestDrainInputFallback_ExitsWhenIdle verifies that the fallback drain loop
// exits after the idle window when no further data arrives.
func TestDrainInputFallback_ExitsWhenIdle(t *testing.T) {
	oldRead := stdinRead
	oldFile := stdinFile
	defer func() {
		stdinRead = oldRead
		stdinFile = oldFile
	}()

	stdinFile = nil
	readCalls := make(chan int, 1)
	unblock := make(chan struct{})
	stdinRead = func(buf []byte) (int, error) {
		readCalls <- 1
		<-unblock
		return 0, io.EOF
	}

	done := make(chan struct{})
	go func() {
		drainInputFallback(1000, 20)
		close(done)
	}()

	// Wait for at least one read call so the drain loop is active.
	select {
	case <-readCalls:
	case <-time.After(time.Second):
		t.Fatal("drainInputFallback did not start reading")
	}

	// The drain loop should exit after the idle window (20ms) elapses.
	select {
	case <-done:
		close(unblock)
	case <-time.After(time.Second):
		close(unblock)
		t.Fatal("drainInputFallback did not exit after idle window")
	}
}

// blockingReadMock replaces stdinRead with a reader whose every call blocks
// until the test sends the result it must return. It records the call count
// and the maximum number of concurrently in-flight reads.
type blockingReadMock struct {
	mu          sync.Mutex
	calls       int
	inflight    int
	maxInflight int
	gates       chan chan stdinReadResult
}

func newBlockingReadMock() *blockingReadMock {
	return &blockingReadMock{gates: make(chan chan stdinReadResult, 16)}
}

func (m *blockingReadMock) read(buf []byte) (int, error) {
	reply := make(chan stdinReadResult)
	m.mu.Lock()
	m.calls++
	m.inflight++
	if m.inflight > m.maxInflight {
		m.maxInflight = m.inflight
	}
	m.mu.Unlock()
	m.gates <- reply
	r := <-reply
	m.mu.Lock()
	m.inflight--
	m.mu.Unlock()
	return r.n, r.err
}

func (m *blockingReadMock) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *blockingReadMock) maxConcurrent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxInflight
}

// nextGate waits (bounded) for the next read call and returns the channel
// used to release it with a result.
func (m *blockingReadMock) nextGate(t *testing.T) chan<- stdinReadResult {
	t.Helper()
	select {
	case g := <-m.gates:
		return g
	case <-time.After(2 * time.Second):
		t.Fatal("expected a read call")
		return nil
	}
}

// TestDrainInputFallback_ReleasesPollerAfterDrainWindow is the regression
// test for bug W5: after the drain window ends with a read still blocked
// (no input), the stdin poller must issue no further reads once that read
// completes (released), and repeated drains must never stack up concurrent
// blocked readers. The pre-fix per-drain poller goroutine kept looping on
// stdin after its drain exited, failing both assertions.
func TestDrainInputFallback_ReleasesPollerAfterDrainWindow(t *testing.T) {
	oldRead := stdinRead
	oldFile := stdinFile
	defer func() {
		stdinRead = oldRead
		stdinFile = oldFile
	}()

	stdinFile = nil
	mock := newBlockingReadMock()
	stdinRead = mock.read

	// First drain: its read blocks (no input); the drain must exit on the
	// idle window while the read is still blocked.
	done := make(chan struct{})
	go func() {
		drainInputFallback(400, 40)
		close(done)
	}()

	gate1 := mock.nextGate(t)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		gate1 <- stdinReadResult{0, io.EOF}
		t.Fatal("drainInputFallback did not exit after drain window with blocked read")
	}

	// The drain is over but its read is still blocked. Release it with DATA
	// (the leak case: the pre-fix orphaned poller would loop and read again;
	// the shared poller must park instead).
	gate1 <- stdinReadResult{4, nil}
	time.Sleep(3 * 40 * time.Millisecond)
	if got := mock.callCount(); got != 1 {
		t.Fatalf("poller issued %d reads after the drain ended; want 1 (released)", got)
	}

	// Two more drains while the poller is idle: each performs exactly one
	// read, and no two reads are ever in flight concurrently.
	for i := 0; i < 2; i++ {
		done := make(chan struct{})
		go func() {
			drainInputFallback(400, 40)
			close(done)
		}()
		g := mock.nextGate(t)
		g <- stdinReadResult{0, io.EOF} // EOF ends this drain immediately
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("drainInputFallback did not exit on EOF")
		}
	}

	if got := mock.callCount(); got != 3 {
		t.Fatalf("expected 3 total read calls, got %d", got)
	}
	if got := mock.maxConcurrent(); got > 1 {
		t.Fatalf("%d concurrent blocked reads; the shared poller must allow at most 1", got)
	}
}

// TestDrainInputFallback_ConcurrentDrains verifies that two overlapping
// drains are both served (serially) by the shared poller without deadlock.
func TestDrainInputFallback_ConcurrentDrains(t *testing.T) {
	oldRead := stdinRead
	oldFile := stdinFile
	defer func() {
		stdinRead = oldRead
		stdinFile = oldFile
	}()

	stdinFile = nil
	mock := newBlockingReadMock()
	stdinRead = mock.read

	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() {
		drainInputFallback(2000, 500)
		close(done1)
	}()
	go func() {
		drainInputFallback(2000, 500)
		close(done2)
	}()

	// Serve both reads with EOF; both drains must terminate.
	g1 := mock.nextGate(t)
	g1 <- stdinReadResult{0, io.EOF}
	g2 := mock.nextGate(t)
	g2 <- stdinReadResult{0, io.EOF}

	for _, d := range []chan struct{}{done1, done2} {
		select {
		case <-d:
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent drain did not terminate")
		}
	}

	if got := mock.maxConcurrent(); got > 1 {
		t.Fatalf("%d concurrent blocked reads; the shared poller must serialize reads", got)
	}
}
