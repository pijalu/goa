// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import (
	"context"
	"sync"

	"github.com/pijalu/goa/tui"
)

// ReplayRequest describes one scrollback replay: emit the target view's
// committed-but-not-yet-physically-emitted canvas rows [From, To) into the
// real terminal scrollback. The Canvas is the target's fully-composed scene
// (cullFloor 0) captured on the command loop at switch time.
type ReplayRequest struct {
	// AgentID identifies the view being switched to; the result carries it
	// back so the command loop can attribute the watermark.
	AgentID string
	// Canvas is the target view's full composed canvas at switch time.
	Canvas []string
	// From is the first row to emit (the view's saved watermark: rows
	// [0, From) are already physically present in scrollback).
	From int
	// To is the end of the emission (the view's natural window top: rows
	// [From, To) scroll off, rows [To, ...) form the visible window).
	To int
	// Width/Height are the terminal geometry the canvas was composed at.
	Width, Height int
}

// ReplayResult reports one finished (or cancelled/failed) replay. Exactly one
// result is produced per executed run, delivered on the channel returned by
// Results(). The command loop applies it under a.apply.
type ReplayResult struct {
	// AgentID echoes the request's view id.
	AgentID string
	// Emitted is the absolute canvas row the emission reached (== To on
	// success; the next un-emitted row on cancel/error). Rows [request.From,
	// Emitted) are now physically present in the terminal scrollback, so the
	// view's saved watermark may advance to Emitted — never beyond.
	Emitted int
	// Err is the cancellation cause or the terminal write error, nil on
	// success. The error is contained to the replay; the main UI stays live.
	Err error
}

// ReplayRunner is the dedicated goroutine that emits a switched-to agent's
// committed transcript rows into the real terminal scrollback (plan T3).
//
//   - Ownership split: the runner owns ONLY scrollback emission (via
//     tui.Compositor.ReplayScrollback, which writes under the compositor
//     lock per chunk). The render loop keeps owning the live visible window;
//     the two never write the same region, and the runner restores the
//     scroll region + homes the cursor before the render loop resumes.
//   - Serialization: ONE goroutine executes replays, so at most one replay
//     emits at a time and bytes never interleave across replays.
//   - Cancel + coalesce: Submit cancels any in-flight replay (context) and
//     replaces any pending request, so a burst of tab switches collapses to
//     the latest target — never two concurrent replays, never a stale
//     backlog.
//   - R1: the runner never mutates live compositor or view state; it emits
//     precomputed bytes for the request's canvas rows and hands the final
//     watermark back over the results channel as a single message per run.
//   - Failure isolation: a write error or cancellation is reported on the
//     result and otherwise contained to the runner.
type ReplayRunner struct {
	comp      *tui.Compositor
	chunkRows int

	ctx    context.Context
	cancel context.CancelFunc

	// pending is the latest submitted request not yet picked up by the loop,
	// guarded by mu (latest-wins coalescing). wake (cap 1) signals the loop
	// that pending may be non-empty; a Submit never blocks on it.
	pending *ReplayRequest
	wake    chan struct{}
	results chan ReplayResult // one message per executed run

	mu        sync.Mutex // guards pending and runCancel
	runCancel context.CancelFunc

	wg sync.WaitGroup // the single run goroutine
}

// NewReplayRunner starts the runner goroutine against the given compositor.
// chunkRows bounds one terminal write; values < 1 use the default (64 rows).
// Call Close to stop the goroutine (typically from the app's shutdown path).
func NewReplayRunner(comp *tui.Compositor, chunkRows int) *ReplayRunner {
	if chunkRows < 1 {
		chunkRows = 64
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &ReplayRunner{
		comp:      comp,
		chunkRows: chunkRows,
		ctx:       ctx,
		cancel:    cancel,
		wake:      make(chan struct{}, 1),
		results:   make(chan ReplayResult, 4),
	}
	r.wg.Add(1)
	go r.loop()
	return r
}

// Results returns the channel the runner delivers exactly one ReplayResult
// per executed run on. The receiver must apply each result on the command
// loop (a.apply): every run — including cancelled ones — may have physically
// emitted rows that the view's saved watermark must account for.
func (r *ReplayRunner) Results() <-chan ReplayResult { return r.results }

// Submit schedules a replay, cancelling any in-flight run and replacing any
// pending request (coalescing to the latest target). A request with an empty
// range (From >= To) is dropped without disturbing an in-flight run.
//
// Submit never blocks: the pending slot is a mutex-guarded variable, so the
// drain-then-send race of a cap-1 channel (two concurrent Submits could
// interleave between draining the slot and blocking on the send) cannot wedge
// the caller when the loop is busy or its result channel is full.
func (r *ReplayRunner) Submit(req ReplayRequest) {
	if req.From >= req.To {
		return
	}
	r.mu.Lock()
	if r.runCancel != nil {
		r.runCancel() // supersede the in-flight replay
	}
	r.pending = &req // atomic latest-wins replace
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default: // wake token already queued; the loop drains all pending work
	}
}

// Cancel stops the in-flight replay (if any) without scheduling a new one —
// used when the transcript is reset (/new) or the UI shuts down.
func (r *ReplayRunner) Cancel() {
	r.mu.Lock()
	if r.runCancel != nil {
		r.runCancel()
	}
	r.pending = nil // drop any pending request: its transcript is gone
	r.mu.Unlock()
}

// Close cancels the runner and blocks until the goroutine exits.
func (r *ReplayRunner) Close() {
	r.cancel()
	r.mu.Lock()
	if r.runCancel != nil {
		r.runCancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// loop is the single runner goroutine: take the latest request, run it to
// completion/cancellation, report exactly one result, repeat.
func (r *ReplayRunner) loop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		}
		// Drain everything submitted while we were running: each iteration
		// pops the latest pending request, so a burst of Submits collapses to
		// the newest target per drain pass.
		for r.runPending() {
			if r.ctx.Err() != nil {
				return
			}
		}
	}
}

// runPending pops the latest pending request, if any, and executes it. It
// reports whether a request was executed (false = queue empty).
func (r *ReplayRunner) runPending() bool {
	r.mu.Lock()
	req := r.pending
	r.pending = nil
	r.mu.Unlock()
	if req == nil {
		return false
	}
	r.run(*req)
	return true
}

// run executes one replay request and reports the result. The request's
// context is cancelled by Submit (supersede) or Close; cancellation is
// checked between chunks inside Compositor.ReplayScrollback.
func (r *ReplayRunner) run(req ReplayRequest) {
	ctx, cancel := context.WithCancel(r.ctx)
	r.mu.Lock()
	r.runCancel = cancel
	r.mu.Unlock()

	emitted, err := r.comp.ReplayScrollback(ctx, req.Canvas, req.From, req.To, req.Width, req.Height, r.chunkRows)

	cancel()
	r.mu.Lock()
	r.runCancel = nil
	r.mu.Unlock()

	res := ReplayResult{AgentID: req.AgentID, Emitted: emitted, Err: err}
	select {
	case r.results <- res:
	case <-r.ctx.Done():
	}
}
