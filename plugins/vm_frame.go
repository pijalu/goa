// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import (
	"sync"
	"time"
)

// frameRetry is the back-off enterFrame sleeps between attempts while another
// frame is live on the same runtime. Short enough that a waiting entry point
// lands promptly, long enough not to spin.
const frameRetry = time.Millisecond

// frameState serializes JavaScript execution for ONE runtime.
//
// Goja runtimes are single-goroutine: two frames on one runtime corrupt each
// other's values (symptom seen in the wild — a value truthy for `!tok` yet
// undefined for `tok.accessToken`, i.e. the z.ai coding-plan fetcher's
// credential read). Serialization is therefore per runtime, NOT global: a
// plugin parked on a slow HTTP hop must not freeze every other plugin's JS,
// which is why the old process-wide mutex (and its release-during-the-hop
// escape hatch) was wrong in both directions.
//
// A blocking bridge call (goa.http.fetch, goa.auth.oauthToken, goa.ui.confirm)
// simply keeps its frame for the duration: same-runtime frames wait, other
// runtimes run unimpeded. No lock is dropped mid-frame, so there is no window
// in which a second frame can slip in.
type frameState struct {
	mu sync.Mutex
	n  int
}

// enter blocks until no frame is live, then marks one live and returns the
// release func. Use it for entry points that must run (commands, tools,
// lifecycle/observer dispatch, script load).
func (f *frameState) enter() func() {
	for {
		f.mu.Lock()
		if f.n == 0 {
			f.n++
			f.mu.Unlock()
			return f.leave
		}
		f.mu.Unlock()
		time.Sleep(frameRetry)
	}
}

// tryEnter is the non-blocking form: ok=false means another frame is live and
// the caller must skip (or re-arm later) instead of waiting. Use it for the
// UI-facing paths — segment render and hotkeys — where waiting would stall the
// render loop or the keystroke.
func (f *frameState) tryEnter() (func(), bool) {
	f.mu.Lock()
	if f.n != 0 {
		f.mu.Unlock()
		return nil, false
	}
	f.n++
	f.mu.Unlock()
	return f.leave, true
}

func (f *frameState) leave() {
	f.mu.Lock()
	f.n--
	f.mu.Unlock()
}

// busy reports whether a frame is live on this runtime.
func (f *frameState) busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n > 0
}

// FrameGate admits a callback into a runtime's frame discipline. *JSBridge
// implements it; scheduler timers take one so a shared Scheduler can serve
// plugins with different runtimes.
type FrameGate interface {
	// TryEnter acquires a frame slot, or reports false when one is live.
	TryEnter() (release func(), ok bool)
}

// enterFrame acquires this runtime's frame, waiting for a live one to drain.
func (b *JSBridge) enterFrame() func() { return b.frames.enter() }

// tryEnterFrame acquires this runtime's frame without waiting.
func (b *JSBridge) tryEnterFrame() (func(), bool) { return b.frames.tryEnter() }

// TryEnter implements FrameGate.
func (b *JSBridge) TryEnter() (func(), bool) { return b.frames.tryEnter() }
