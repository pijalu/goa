// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"sync"

	"github.com/pijalu/goa/internal/event"
)

// eventForwarder decouples a producer that must never block from a bounded
// consumer channel.
//
// It exists to keep the agent's streaming goroutine off the app's bounded event
// bus. The agent observer (OnEvent) runs on the LLM stream goroutine; a direct
// blocking send into the bus (cap 1024) would stall token generation whenever
// the TUI falls behind (slow render, scroll, editor open). push() is always
// non-blocking and O(1); a single forwarder goroutine drains the queue into the
// destination channel at the consumer's own pace.
//
// The queue is deliberately unbounded: boundedness would reintroduce the stall
// (just at a different depth), while dropping events would corrupt the rendered
// conversation (lost deltas, tool calls, EventEnd). A persistently slow TUI is a
// separate correctness bug; the stream must not be the victim of it. For
// well-behaved consumers the queue is effectively always empty.
type eventForwarder struct {
	mu     sync.Mutex
	queue  []event.AgentEvent
	wake   chan struct{}
	stopCh chan struct{}
	done   chan struct{}
}

// newEventForwarder starts a forwarder draining into dst and returns it.
func newEventForwarder(dst chan<- event.AgentEvent) *eventForwarder {
	f := &eventForwarder{
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go f.run(dst)
	return f
}

// push enqueues ev without blocking. It is safe for concurrent callers.
func (f *eventForwarder) push(ev event.AgentEvent) {
	f.mu.Lock()
	madeNonEmpty := len(f.queue) == 0
	f.queue = append(f.queue, ev)
	f.mu.Unlock()
	// Only signal when transitioning empty→non-empty; the run loop drains to
	// empty before waiting, so a single in-flight wake token suffices.
	if madeNonEmpty {
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
}

// run is the forwarder goroutine. It waits on wake, drains the whole queue
// into dst, and repeats until stop.
func (f *eventForwarder) run(dst chan<- event.AgentEvent) {
	defer close(f.done)
	for {
		select {
		case <-f.wake:
		case <-f.stopCh:
			return
		}
		for {
			f.mu.Lock()
			if len(f.queue) == 0 {
				f.mu.Unlock()
				break
			}
			batch := f.queue
			f.queue = nil
			f.mu.Unlock()
			for _, ev := range batch {
				select {
				case dst <- ev:
				case <-f.stopCh:
					return
				}
			}
		}
	}
}

// close stops the forwarder and blocks until the goroutine has exited, so no
// in-flight send remains. Events still queued at close time are discarded
// (used only at shutdown). Safe to call at most once.
func (f *eventForwarder) close() {
	close(f.stopCh)
	<-f.done
}
