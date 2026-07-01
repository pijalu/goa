// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestForegroundOrchestrator_SingleConsumerDeliversAllEvents codifies the
// invariant the app relies on: a single reader of Events() receives every
// emitted message in order. This is the contract broken by the old
// per-command routeOrchEvents helper, which spawned a new competing reader on
// every /pair, /reviewer, /orchestrate call and round-robin-lost events.
func TestForegroundOrchestrator_SingleConsumerDeliversAllEvents(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	const n = 50
	var seen []string
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < n; i++ {
			select {
			case msg, ok := <-orch.Events():
				if !ok {
					return
				}
				seen = append(seen, msg.Content)
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	for i := 0; i < n; i++ {
		orch.emit("unit", "user", eventMarker(i))
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for single consumer to drain events")
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("single consumer received %d/%d events (competing consumers detected)", len(seen), n)
	}
	for i, got := range seen {
		if got != eventMarker(i) {
			t.Fatalf("event %d = %q, want %q (ordering lost)", i, got, eventMarker(i))
		}
	}
}

// (The previous competing-consumer sentinel test was removed: with a
// buffered channel and fast emitters, one goroutine can drain everything
// before the others schedule, so the split it asserted was non-deterministic.
// The single-consumer test above is the real invariant the app relies on.)

func eventMarker(i int) string { return "evt-" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
