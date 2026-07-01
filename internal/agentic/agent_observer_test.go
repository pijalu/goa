// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"sync/atomic"
	"testing"
)

// TestAddObserver_HandleRemovesByID verifies the S3 fix: AddObserver returns a
// remove handle that unregisters exactly that registration by id (not by
// reflect-based observer comparison). Adding the same observer value twice and
// invoking each handle must remove the right entries independently.
func TestAddObserver_HandleRemovesByID(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	h1 := agent.AddObserver(obs)
	h2 := agent.AddObserver(obs) // same value added twice

	if got := len(agent.observers); got != 2 {
		t.Fatalf("expected 2 observers, got %d", got)
	}

	// Removing via the first handle must leave exactly one (the second entry).
	h1()
	if got := len(agent.observers); got != 1 {
		t.Fatalf("after h1(), expected 1 observer, got %d", got)
	}

	// The remaining entry must be the second registration.
	if agent.observers[0].obs != obs {
		t.Errorf("remaining observer is not the second registration")
	}

	h2()
	if got := len(agent.observers); got != 0 {
		t.Errorf("after h2(), expected 0 observers, got %d", got)
	}
}

// TestAddObserver_WrappedObserverRemovableViaHandle verifies that an observer
// wrapped in an adapter (so reflect/value comparison would fail) is still
// removable via its handle — the original equalObservers/reflect design could
// not do this reliably.
func TestAddObserver_WrappedObserverRemovableViaHandle(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	var calls int32
	inner := OutputObserverFunc(func(ev OutputEvent) { atomic.AddInt32(&calls, 1) })
	// Wrap in a fresh adapter — distinct value from `inner`.
	wrapped := OutputObserverFunc(func(ev OutputEvent) { inner.OnEvent(ev) })

	handle := agent.AddObserver(wrapped)
	if got := len(agent.observers); got != 1 {
		t.Fatalf("expected 1 observer, got %d", got)
	}

	handle()
	if got := len(agent.observers); got != 0 {
		t.Errorf("wrapped observer not removable via handle; got %d observers", got)
	}
}

// TestAddObserver_HandleIsIdempotentNoOp ensures calling a handle twice does
// not panic or remove an unrelated entry (ids are unique).
func TestAddObserver_HandleIsIdempotentNoOp(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs1 := &mockEventObserver{}
	obs2 := &mockEventObserver{}
	h1 := agent.AddObserver(obs1)
	_ = agent.AddObserver(obs2)

	h1()
	h1() // second call must be a no-op, not remove obs2

	if got := len(agent.observers); got != 1 {
		t.Errorf("after double h1(), expected 1 observer (obs2), got %d", got)
	}
	if agent.observers[0].obs != obs2 {
		t.Errorf("surviving observer is not obs2")
	}
}

// TestRemoveObserver_StructPointerStillWorks confirms the backwards-compatible
// RemoveObserver path still removes struct-pointer observers (identity ==).
func TestRemoveObserver_StructPointerStillWorks(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	if len(agent.observers) != 1 {
		t.Fatalf("expected 1 observer, got %d", len(agent.observers))
	}

	agent.RemoveObserver(obs)
	if len(agent.observers) != 0 {
		t.Errorf("RemoveObserver failed to remove struct-pointer observer")
	}
}
