// SPDX-License-Identifier: GPL-3.0-or-later

package orchestrator

import (
	"sync"
	"sync/atomic"
	"time"
)

// durableBufferSize bounds the in-flight durable-event queue. It is large
// enough to absorb a streaming burst; the writer drains it in batches.
const durableBufferSize = 1024

// durableFlushInterval caps how long a buffered event may wait on disk.
// Buffered writes reach the file quickly in practice (the writer flushes
// after each batch); this ticker is the backstop for quiet periods.
const durableFlushInterval = 100 * time.Millisecond

// flusher is the optional Flush capability the sink uses when the store
// supports it (e.g. *FileEventStore). Checked by type assertion so the
// EventStore interface stays minimal.
type flusher interface {
	Flush()
}

// durableSink persists events to an EventStore on a dedicated goroutine so
// the producer (the agent's streaming observer, which fires per token) never
// waits on disk I/O. This is the R1 fix: "complete processing must never
// limit the agent's ability to consume tokens".
//
// submit is strictly non-blocking. If the queue fills because the writer fell
// behind, the event is counted via Overflow and dropped from the durable log
// rather than stalling the stream. Under normal operation the writer keeps up
// (batched, buffered appends are very cheap) and Overflow stays zero.
//
// Ordering is preserved: a single FIFO channel feeds a single writer, so
// events reach the store in emit order.
type durableSink struct {
	store    EventStore
	flush    flusher
	ch       chan Event
	overflow atomic.Int64
	quit     chan struct{}
	done     chan struct{}
	closeOnce sync.Once
}

// newDurableSink starts the writer goroutine for store. Returns nil if store
// is nil (the runtime then skips durable persistence entirely).
func newDurableSink(store EventStore) *durableSink {
	if store == nil {
		return nil
	}
	s := &durableSink{
		store: store,
		flush: flusherOf(store),
		ch:    make(chan Event, durableBufferSize),
		quit:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go s.loop()
	return s
}

// submit enqueues an event for durable persistence without blocking. Counts
// overflow instead of waiting when the queue is saturated.
func (s *durableSink) submit(evt Event) {
	select {
	case s.ch <- evt:
	default:
		s.overflow.Add(1)
	}
}

// Overflow returns the number of durable events dropped because the writer
// could not keep up. Zero in normal operation; a non-zero value signals the
// disk path is the bottleneck (not the stream reader).
func (s *durableSink) Overflow() int64 { return s.overflow.Load() }

// close signals the writer to drain remaining events, flush, and exit. It is
// idempotent and safe to call from any goroutine.
func (s *durableSink) close() {
	s.closeOnce.Do(func() {
		close(s.quit)
	})
	<-s.done
}

// loop is the single writer. It batches immediately-available events on each
// wake-up, flushes, and also flushes on a timer so quiet writes do not sit in
// the buffer past durableFlushInterval.
func (s *durableSink) loop() {
	defer close(s.done)
	ticker := time.NewTicker(durableFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case evt := <-s.ch:
			_ = s.store.Append(evt)
			s.drainBatch()
			if s.flush != nil {
				s.flush.Flush()
			}
		case <-ticker.C:
			if s.flush != nil {
				s.flush.Flush()
			}
		case <-s.quit:
			s.drainAll()
			if s.flush != nil {
				s.flush.Flush()
			}
			return
		}
	}
}

// drainBatch pulls events that are already queued so they share one wake-up,
// up to a per-batch cap to bound the writer's time in the hot path.
func (s *durableSink) drainBatch() {
	for i := 0; i < 128; i++ {
		select {
		case evt := <-s.ch:
			_ = s.store.Append(evt)
		default:
			return
		}
	}
}

// drainAll empties the queue completely; used during shutdown.
func (s *durableSink) drainAll() {
	for {
		select {
		case evt := <-s.ch:
			_ = s.store.Append(evt)
		default:
			return
		}
	}
}

// flusherOf returns the store's Flush capability, or nil if unsupported.
func flusherOf(store EventStore) flusher {
	if f, ok := store.(flusher); ok {
		return f
	}
	return nil
}
