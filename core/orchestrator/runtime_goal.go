// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import "github.com/pijalu/goa/internal"

func defaultRunID() string {
	return internal.PrefixedHexID("run", 4)
}

// Stop tears down the durable sink, flushing any buffered events to disk.
// It should be called when the runtime is no longer needed (after Run
// returns and any final snapshot/Replay). It is idempotent and does not
// cancel an in-flight run — use Cancel for that. Stopping the sink keeps a
// long-lived process from leaking a writer goroutine per run (R7).
func (r *Runtime) Stop() {
	if r.sink != nil {
		r.sink.close()
	}
}

// Cancel requests the running orchestration to stop. It is safe to call
// multiple times and from any goroutine. If the run has already finished,
// Cancel is a no-op.
func (r *Runtime) Cancel() {
	r.cancelMu.Lock()
	cancel := r.cancel
	r.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Done returns a channel closed when Run finishes (success or error). Allows
// the TUI / command layer to know when to unsubscribe and clear the active
// runtime holder.
func (r *Runtime) Done() <-chan struct{} {
	return r.doneCh
}

// SteerAgent appends a steering message to a specific live handle (by id).
