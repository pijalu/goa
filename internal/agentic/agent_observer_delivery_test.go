// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shrinkDeliverTimeout sets a small observer delivery deadline for the test.
func shrinkDeliverTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := observerDeliverTimeout
	observerDeliverTimeout = d
	t.Cleanup(func() { observerDeliverTimeout = prev })
}

// newEmitOnlyAgent builds an agent that only needs the observer plumbing.
func newEmitOnlyAgent() *Agent {
	return NewAgent(Config{
		Model:        provider.Model{ID: "emit-test"},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
}

// blockingObserver never returns from OnEvent until released — the wedged
// observer scenario (TUI/session recorder stall).
type blockingObserver struct {
	release chan struct{}
}

func (b *blockingObserver) OnEvent(OutputEvent) {
	<-b.release // wedge forever (until test cleanup)
}

// TestEmitEvent_WedgedObserverDetached is the F1a regression test: emitEvent
// runs on the stream-consumer goroutine; a synchronous OnEvent that never
// returns used to hang the turn forever with the stall watchdogs unable to
// help (they only close the stream, which this goroutine is not reading).
// Delivery must be bounded by observerDeliverTimeout and the wedged observer
// detached, while healthy observers keep receiving events.
func TestEmitEvent_WedgedObserverDetached(t *testing.T) {
	shrinkDeliverTimeout(t, 100*time.Millisecond)

	a := newEmitOnlyAgent()
	// Blocks until the cleanup closes the channel (closed ⇒ receive returns
	// immediately, so the wedge must use an OPEN channel).
	wedged := &blockingObserver{release: make(chan struct{})}
	t.Cleanup(func() { close(wedged.release) })

	healthy := &mockEventObserver{}
	a.AddObserver(wedged)
	a.AddObserver(healthy)

	start := time.Now()
	a.emitEvent(OutputEvent{Type: EventProgress, Text: "first"})
	firstDur := time.Since(start)
	assert.Less(t, firstDur, time.Second, "first emit must be bounded by the delivery deadline")

	require.Eventually(t, func() bool { return len(healthy.Events()) == 1 },
		time.Second, 5*time.Millisecond, "healthy observer must still receive the event")

	// Wedged observer is detached now: subsequent emits must be instant.
	start = time.Now()
	a.emitEvent(OutputEvent{Type: EventProgress, Text: "second"})
	secondDur := time.Since(start)
	assert.Less(t, secondDur, 50*time.Millisecond, "detached observer must not be contacted again")

	require.Eventually(t, func() bool { return len(healthy.Events()) == 2 },
		time.Second, 5*time.Millisecond, "healthy observer keeps receiving after the wedge")
}

// TestEmitEvent_SynchronousForHealthyObserver guards the delivery contract:
// healthy observers keep synchronous semantics — the event is observable
// immediately after emitEvent returns (existing tests and callers rely on it).
func TestEmitEvent_SynchronousForHealthyObserver(t *testing.T) {
	a := newEmitOnlyAgent()
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.emitEvent(OutputEvent{Type: EventProgress, Text: "now"})
	require.Len(t, obs.Events(), 1, "event must be fully delivered before emitEvent returns")
	assert.Equal(t, "now", obs.Events()[0].Text)
}

// TestEmitEvent_PreservesOrder verifies per-observer ordering is not lost by
// the deadline-bounded delivery.
func TestEmitEvent_PreservesOrder(t *testing.T) {
	a := newEmitOnlyAgent()
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	for i := 0; i < 10; i++ {
		a.emitEvent(OutputEvent{Type: EventProgress, Text: strings.Repeat("x", i+1)})
	}
	events := obs.Events()
	require.Len(t, events, 10)
	for i, e := range events {
		assert.Len(t, e.Text, i+1, "events must arrive in emission order")
	}
}

// panicObserver (declared in agent_history_ctx_test.go) panics on every
// event — the recover must isolate it without stalling the delivery or
// affecting other observers.

// TestEmitEvent_PanickingObserverIsolated verifies a panicking observer does
// not detach (panic ≠ wedge), does not affect other observers, and does not
// crash the emitter.
func TestEmitEvent_PanickingObserverIsolated(t *testing.T) {
	shrinkDeliverTimeout(t, 500*time.Millisecond)

	a := newEmitOnlyAgent()
	a.AddObserver(&panicObserver{})
	healthy := &mockEventObserver{}
	a.AddObserver(healthy)

	a.emitEvent(OutputEvent{Type: EventProgress, Text: "one"})
	a.emitEvent(OutputEvent{Type: EventProgress, Text: "two"})

	require.Len(t, healthy.Events(), 2, "panicking observer must not affect others")
}

// TestRemoveObserver_SynchronousWithinOnEvent covers the lock-order hazard:
// an observer removing itself from inside OnEvent must not deadlock (stop()
// never takes the delivery ordering lock).
func TestRemoveObserver_SynchronousWithinOnEvent(t *testing.T) {
	a := newEmitOnlyAgent()
	var remove func()
	var once sync.Once
	var got int
	selfRemoving := OutputObserverFunc(func(OutputEvent) {
		once.Do(func() {
			got++
			remove() // re-entrant removal from inside OnEvent
		})
	})
	remove = a.AddObserver(selfRemoving)

	done := make(chan struct{})
	go func() {
		a.emitEvent(OutputEvent{Type: EventProgress, Text: "x"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: removing an observer from inside its OnEvent hung emitEvent")
	}
	assert.Equal(t, 1, got)

	// Removed observer receives nothing further.
	a.emitEvent(OutputEvent{Type: EventProgress, Text: "y"})
	assert.Equal(t, 1, got)
}
