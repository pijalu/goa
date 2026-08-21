// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

type observerEntry struct {
	obs OutputObserver
	id  uint64
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
	a.observers = append(a.observers, observerEntry{obs: o, id: id})
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
