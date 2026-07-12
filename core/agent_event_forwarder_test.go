// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventForwarder_DeliversAllInOrder verifies every pushed event reaches
// the destination channel, in order.
func TestEventForwarder_DeliversAllInOrder(t *testing.T) {
	dst := make(chan event.AgentEvent, 1)
	f := newEventForwarder(dst)
	defer f.close()

	const n = 200
	for i := 0; i < n; i++ {
		f.push(event.AgentEvent{Event: agentic.OutputEvent{Text: fmt.Sprintf("%d", i)}})
	}

	// Drain and verify order.
	for i := 0; i < n; i++ {
		select {
		case got := <-dst:
			assert.Equal(t, fmt.Sprintf("%d", i), got.Event.Text, "out of order or dropped event")
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

// TestEventForwarder_PushNeverBlocks is the core guarantee of 1.2: push must
// return promptly even when the consumer channel is full and never drained
// (simulating a stalled TUI). A direct blocking send would deadlock here; the
// forwarder's unbounded queue must not.
func TestEventForwarder_PushNeverBlocks(t *testing.T) {
	// A destination that nobody drains.
	dst := make(chan event.AgentEvent, 4)
	f := newEventForwarder(dst)
	defer f.close()

	// Push far more than the destination capacity; push must not block.
	const n = 5000
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			f.push(event.AgentEvent{Event: agentic.OutputEvent{Text: "x"}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("push blocked: forwarder does not decouple the producer")
	}
}

// TestEventForwarder_ConcurrentPushersIsRaceFree hammers push from many
// goroutines; run with -race.
func TestEventForwarder_ConcurrentPushersIsRaceFree(t *testing.T) {
	dst := make(chan event.AgentEvent, 16)
	f := newEventForwarder(dst)

	const workers, perWorker = 8, 250
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				f.push(event.AgentEvent{Event: agentic.OutputEvent{Text: "x"}})
			}
		}()
	}
	wg.Wait()

	// Drain everything; close once we've seen the expected count.
	got := 0
	expected := workers * perWorker
	deadline := time.After(3 * time.Second)
	for got < expected {
		select {
		case <-dst:
			got++
		case <-deadline:
			f.close()
			t.Fatalf("delivered %d of %d events", got, expected)
		}
	}
	require.Equal(t, expected, got, "every pushed event must be delivered")
	f.close()
}

// TestEventForwarder_CloseStopsGoroutine verifies close unblocks and exits.
func TestEventForwarder_CloseStopsGoroutine(t *testing.T) {
	dst := make(chan event.AgentEvent, 4)
	f := newEventForwarder(dst)

	// Push some events nobody will consume, then close.
	f.push(event.AgentEvent{Event: agentic.OutputEvent{Text: "1"}})
	f.push(event.AgentEvent{Event: agentic.OutputEvent{Text: "2"}})

	done := make(chan struct{})
	go func() {
		f.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not return")
	}
}
