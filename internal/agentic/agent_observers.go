// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"sync"
	"sync/atomic"
	"time"
)

// observerDeliverTimeout bounds a single OnEvent call. If an observer does not
// return within it, the observer is considered wedged and detached — a
// synchronous observer used to be able to block the stream-consumer goroutine
// forever, beyond the reach of the stall watchdogs (F1a review finding: the
// luna stuck-session hang class). Variable so tests can shrink it.
var observerDeliverTimeout = 5 * time.Second

type observerEntry struct {
	obs OutputObserver
	id  uint64
	// del carries the per-registration delivery state (ordering + wedge
	// flags). Held by pointer so entry copies in emitEvent never copy a mutex.
	del *observerDelivery
}

// observerDelivery serializes events to one observer with a per-observer
// ordering lock and runs OnEvent on a helper goroutine so the caller can
// enforce a deadline. Healthy observers keep full synchronous semantics (the
// event is fully processed before deliver returns); a wedged observer blocks
// at most observerDeliverTimeout once and is then detached.
type observerDelivery struct {
	obs OutputObserver

	mu      sync.Mutex // per-observer ordering
	stopped atomic.Bool
	stalled atomic.Bool
}

// newObserverDelivery creates the delivery state for one registration.
func newObserverDelivery(obs OutputObserver) *observerDelivery {
	return &observerDelivery{obs: obs}
}

// stop marks the registration dead (removal path). Safe from any goroutine,
// including from inside OnEvent itself — it never takes the ordering lock.
func (d *observerDelivery) stop() {
	d.stopped.Store(true)
}

// deliver delivers one event: ordered behind earlier events for this
// observer, bounded by observerDeliverTimeout. On timeout the observer is
// detached (stalled) so all subsequent deliveries skip it instantly.
func (d *observerDelivery) deliver(ev OutputEvent, lg *Logger) {
	if d.stopped.Load() || d.stalled.Load() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped.Load() || d.stalled.Load() {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			_ = recover() // observer panicked; keep delivering
		}()
		d.obs.OnEvent(ev)
	}()
	timer := time.NewTimer(observerDeliverTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		d.stalled.Store(true)
		if lg != nil {
			lg.Log(Warn, "observer %T wedged for %v in OnEvent; detaching it so the stream can continue", d.obs, observerDeliverTimeout)
		}
	}
}

// AddObserver registers an observer to receive output events and returns a
// remove handle. Call the returned func exactly once to unregister that
// specific registration. Using a handle (instead of comparing observer values
// via reflect) makes removal reliable even when the same observer is added
// twice or the observer is wrapped in an adapter.
func (a *Agent) AddObserver(o OutputObserver) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observerCounter++
	id := a.observerCounter
	a.observers = append(a.observers, observerEntry{obs: o, id: id, del: newObserverDelivery(o)})
	return func() { a.removeObserverByID(id) }
}

// RemoveObserver unregisters a previously added observer by value. It is kept
// for backwards compatibility; new code should prefer the remove handle
// returned by AddObserver. Comparison is identity-based (pointer equality);
// function-typed observers cannot be matched this way (comparing two non-nil
// func values panics), so callers using OutputObserverFunc must retain and use
// the AddObserver handle. RemoveObserver is a no-op when no entry matches.
func (a *Agent) RemoveObserver(o OutputObserver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.observers {
		if safeObserverEqual(entry.obs, o) {
			entry.del.stop()
			a.observers = append(a.observers[:i], a.observers[i+1:]...)
			return
		}
	}
}

// removeObserverByID removes the observer entry with the given id (no-op if
// not found). Called by the remove handle returned from AddObserver.
func (a *Agent) removeObserverByID(id uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.observers {
		if entry.id == id {
			entry.del.stop()
			a.observers = append(a.observers[:i], a.observers[i+1:]...)
			return
		}
	}
}

// safeObserverEqual reports whether two OutputObserver values are identical by
// pointer/interface equality. Comparing two non-nil function values panics, so
// the comparison is guarded with a recover; such observers are considered
// non-matching (callers must use the AddObserver handle for them). This avoids
// any dependency on reflect.
func safeObserverEqual(a, b OutputObserver) (eq bool) {
	if a == nil || b == nil {
		return a == b
	}
	defer func() { _ = recover() }()
	return a == b
}

func (a *Agent) transitionTo(target OutputState) {
	if a.emitState != target {
		a.emitState = target
		a.emitEvent(OutputEvent{
			Type:  EventStateChange,
			State: target,
		})
	}
}

// Run starts a new conversation turn with the given user input.
// If the agent is already processing, the input is queued and handled
// after the current turn completes. The system prompt is automatically
// prepended on the first call.
//
// Run blocks until the conversation turn completes or the context is cancelled.
