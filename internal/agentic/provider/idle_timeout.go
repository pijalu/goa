// SPDX-License-Identifier: GPL-3.0-or-later

//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrStreamIdle is returned when no bytes are received from a streaming LLM
// response for longer than the configured idle timeout.
var ErrStreamIdle = errors.New("stream idle timeout: no data received from LLM")

// DefaultStreamIdleTimeout is the maximum time to wait between bytes from a
// streaming LLM response before treating the connection as stalled.
//
// Five minutes, not two: it doubles as the shipped execution.activity_timeout
// fallback, and a reasoning model legitimately emits no bytes at all while it
// thinks — a captured z.ai export-A request ran 80.7s of pure silence before
// its first token (bugs.md #2). A two-minute window killed healthy reasoning
// turns, so the default now clears a long thinking phase with margin. Callers
// that need a tighter bound pin providers[].idle_timeout (per stream) or
// execution.activity_timeout (per agent).
const DefaultStreamIdleTimeout = 5 * time.Minute

// eventStallNumerator / eventStallDenominator split one silence budget in two:
// the event-level stall watchdog fires at this fraction of the byte-idle
// budget, and the byte-level guard keeps the remainder as backstop.
const (
	eventStallNumerator   = 3
	eventStallDenominator = 4
)

// EventStallTimeout returns the event-level stall budget for a byte-idle
// budget: strictly inside it, never equal to it.
//
// Two guards used to be armed on the SAME budget — the byte-level
// idleTimeoutReader (reset by every byte, keep-alive comments included) and the
// agent's event watchdog (reset only by mapped events) — so a stream that went
// completely silent raced them: whichever timer the scheduler woke first
// decided the error the user saw, and the byte guard could pre-empt the
// watchdog that owns the warn-then-retry path (bugs.md #2).
//
// The event watchdog now owns the silence window because it is the guard with
// the recovery semantics (it knows whether the round already delivered a
// complete answer). The byte guard remains the outer backstop for total
// silence, keeping the full byte budget. A non-positive budget falls back to
// DefaultStreamIdleTimeout, matching the reader's own default.
func EventStallTimeout(byteBudget time.Duration) time.Duration {
	if byteBudget <= 0 {
		byteBudget = DefaultStreamIdleTimeout
	}
	stall := byteBudget * eventStallNumerator / eventStallDenominator
	if stall <= 0 {
		// Sub-nanosecond budgets: keep the split strictly inside by
		// construction rather than collapsing both guards onto zero.
		return byteBudget
	}
	return stall
}

// NewIdleTimeoutReader wraps r so that Read returns ErrStreamIdle if no data
// arrives within timeout. A zero or negative timeout disables the guard and
// returns r unchanged.
func NewIdleTimeoutReader(r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if timeout <= 0 {
		return r
	}
	return newIdleTimeoutReader(r, timeout)
}

// idleTimeoutReader guards a stream body against silent hangs by enforcing a
// maximum gap between received bytes.
//
// A single long-lived reader goroutine services every Read() call (one goroutine
// for the reader's lifetime), eliminating the goroutine-per-Read churn and the
// per-Read buffer allocation of the previous implementation. The reader reads
// into a private scratch buffer (grown as needed, reused across reads) and
// copies into the caller's slice before delivering the result, so a timed-out
// Read() can never observe the reader writing to its slice concurrently.
type idleTimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration

	readReq chan []byte     // Read() -> loop: a buffer to fill
	readRes chan readResult // loop -> Read(): the filled result
	closeMu sync.Mutex
	closed  bool
	closeCh chan struct{} // closed once by Close()
	done    chan struct{} // closed when the loop exits
}

type readResult struct {
	n   int
	err error
}

func newIdleTimeoutReader(r io.ReadCloser, timeout time.Duration) *idleTimeoutReader {
	it := &idleTimeoutReader{
		r:       r,
		timeout: timeout,
		readReq: make(chan []byte),
		readRes: make(chan readResult, 1),
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go it.loop()
	return it
}

// loop is the single long-lived reader goroutine. It reads exactly one chunk
// per request, into a reused scratch buffer, and stops once the underlying
// reader reports a terminal error (EOF/close) — there is nothing left to serve.
func (it *idleTimeoutReader) loop() {
	defer close(it.done)
	var scratch []byte
	for {
		select {
		case buf := <-it.readReq:
			if cap(scratch) < len(buf) {
				scratch = make([]byte, len(buf))
			}
			tmp := scratch[:len(buf)]
			n, err := it.r.Read(tmp)
			if n > 0 {
				copy(buf, tmp[:n])
			}
			// Deliver. readRes has capacity 1 and at most one Read() is in
			// flight at a time, so this never blocks in normal operation; the
			// closeCh branch is the shutdown escape hatch.
			select {
			case it.readRes <- readResult{n: n, err: err}:
			case <-it.closeCh:
				return
			}
			if err != nil {
				// Terminal: the stream is over. Stop reading so we do not
				// read ahead of the consumer; subsequent Read() calls see
				// it.done closed and return EOF.
				return
			}
		case <-it.closeCh:
			return
		}
	}
}

// Read delegates to the underlying reader but aborts with ErrStreamIdle if no
// bytes are received before the timeout expires. The timeout resets on every
// successful read, so only stalled connections are terminated.
func (it *idleTimeoutReader) Read(p []byte) (int, error) {
	select {
	case it.readReq <- p:
	case <-it.done:
		// The loop exited after a terminal error; nothing more to read.
		return 0, io.EOF
	}

	timer := time.NewTimer(it.timeout)
	defer timer.Stop()

	select {
	case res := <-it.readRes:
		return res.n, res.err
	case <-it.closeCh:
		return 0, io.EOF
	case <-timer.C:
		// Closing the body unblocks the reader goroutine and lets the SSE
		// parser surface the stall as a stream error instead of hanging forever.
		_ = it.Close()
		return 0, ErrStreamIdle
	}
}

// Close closes the underlying reader. It is safe to call multiple times.
func (it *idleTimeoutReader) Close() error {
	it.closeMu.Lock()
	defer it.closeMu.Unlock()
	if it.closed {
		return nil
	}
	it.closed = true
	close(it.closeCh)
	return it.r.Close()
}
