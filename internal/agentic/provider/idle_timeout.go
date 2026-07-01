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
const DefaultStreamIdleTimeout = 2 * time.Minute

// NewIdleTimeoutReader wraps r so that Read returns ErrStreamIdle if no data
// arrives within timeout. A zero or negative timeout disables the guard and
// returns r unchanged.
func NewIdleTimeoutReader(r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if timeout <= 0 {
		return r
	}
	return &idleTimeoutReader{r: r, timeout: timeout}
}

// idleTimeoutReader guards a stream body against silent hangs by enforcing a
// maximum gap between received bytes.
type idleTimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration

	closeMu sync.Mutex
	closed  bool
}

// Read delegates to the underlying reader but aborts with ErrStreamIdle if no
// bytes are received before the timeout expires. The timeout resets on every
// successful read, so only stalled connections are terminated.
func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}

	done := make(chan result, 1)
	go func(buf []byte) {
		// Read into a private buffer first so a late timeout cannot race with
		// the caller's slice after we have already returned.
		tmp := make([]byte, len(buf))
		n, err := r.r.Read(tmp)
		if n > 0 {
			copy(buf, tmp[:n])
		}
		done <- result{n: n, err: err}
	}(p)

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case res := <-done:
		return res.n, res.err
	case <-timer.C:
		// Closing the body unblocks the reader goroutine and lets the SSE
		// parser surface the stall as a stream error instead of hanging forever.
		_ = r.Close()
		return 0, ErrStreamIdle
	}
}

// Close closes the underlying reader. It is safe to call multiple times.
func (r *idleTimeoutReader) Close() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.r.Close()
}
