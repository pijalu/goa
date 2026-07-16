// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"context"
	"iter"
	"sync"
	"sync/atomic"
)

// AssistantMessageEvent is a single event emitted during LLM streaming.
// The Type field determines which other fields are populated.
type AssistantMessageEvent struct {
	// Type discriminates the event kind.
	Type EventType
	// ContentIndex identifies which content block this event belongs to.
	ContentIndex int
	// Delta is the incremental text/thinking/tool-arguments fragment.
	Delta string
	// Content is the accumulated text for *_end events.
	Content string
	// Partial holds a snapshot of the message being built (populated on *_start).
	Partial *AssistantMessage
	// ToolCall is populated on toolcall_end events.
	ToolCall *ContentBlock
	// Message is populated on "done" and "error" events.
	Message *Message
	// StopReason is populated on "done" and "error" events.
	StopReason StopReason
	// Error holds any error associated with an "error" event.
	Error error
}

// AssistantMessage holds a partial or complete assistant response accumulated
// from streaming events.
type AssistantMessage struct {
	Content    []ContentBlock
	Usage      *Usage
	StopReason StopReason
}

// Clone returns a deep copy of the AssistantMessage.
func (m *AssistantMessage) Clone() *AssistantMessage {
	if m == nil {
		return nil
	}
	cp := &AssistantMessage{
		Usage:      nil,
		StopReason: m.StopReason,
	}
	if m.Usage != nil {
		u := *m.Usage
		cp.Usage = &u
	}
	if len(m.Content) > 0 {
		cp.Content = make([]ContentBlock, len(m.Content))
		copy(cp.Content, m.Content)
	}
	return cp
}

// AssistantMessageEventStream is a push-based event stream that emits typed
// AssistantMessageEvents as the LLM responds.
type AssistantMessageEventStream struct {
	events     chan AssistantMessageEvent
	done       chan struct{}
	result     *AssistantMessage
	err        error
	hardStop   atomic.Bool
	terminated bool
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewAssistantMessageEventStream creates a new event stream with the given
// buffer size.
func NewAssistantMessageEventStream(bufSize int) *AssistantMessageEventStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &AssistantMessageEventStream{
		events: make(chan AssistantMessageEvent, bufSize),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Push adds an event to the stream. Returns false if the stream has been
// terminated.
func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.events <- event:
		return true
	case <-s.done:
		return false
	}
}

func (s *AssistantMessageEventStream) nextEvent() (AssistantMessageEvent, bool) {
	select {
	case event, ok := <-s.events:
		if ok {
			return event, true
		}
		return AssistantMessageEvent{}, false
	default:
	}
	select {
	case event, ok := <-s.events:
		return event, ok
	case <-s.done:
		if s.hardStop.Load() {
			return AssistantMessageEvent{}, false
		}
		return s.drainRemaining()
	}
}

// End signals graceful completion.
func (s *AssistantMessageEventStream) End(result *AssistantMessage) {
	if s.terminate(termination{result: result}) {
		s.cancel()
	}
}

// CloseWithError terminates the stream with an error.
func (s *AssistantMessageEventStream) CloseWithError(err error) {
	if s.terminate(termination{hard: true, err: err}) {
		s.cancel()
	}
}

// Cancel prematurely terminates the stream without draining remaining events.
func (s *AssistantMessageEventStream) Cancel() {
	if s.terminate(termination{hard: true}) {
		s.cancel()
	}
}

type termination struct {
	hard   bool
	result *AssistantMessage
	err    error
}

func (s *AssistantMessageEventStream) terminate(t termination) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminated {
		return false
	}
	s.terminated = true
	if t.hard {
		s.hardStop.Store(true)
	}
	s.result = t.result
	s.err = t.err
	close(s.done)
	return true
}

// Seq returns an iterator over all events in the stream.
func (s *AssistantMessageEventStream) Seq() iter.Seq[AssistantMessageEvent] {
	return s.SeqCtx(context.Background())
}

// SeqCtx returns an iterator over all events in the stream that respects
// context cancellation. The iterator checks ctx.Err() between events so
// callers can bound the total iteration time without relying on the
// byte-level idle timeout (which is reset by every byte, including SSE
// keep-alive comments and empty lines).
func (s *AssistantMessageEventStream) SeqCtx(ctx context.Context) iter.Seq[AssistantMessageEvent] {
	return func(yield func(AssistantMessageEvent) bool) {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			event, ok := s.nextEvent()
			if !ok {
				return
			}
			if !yield(event) {
				return
			}
		}
	}
}

func (s *AssistantMessageEventStream) drainRemaining() (AssistantMessageEvent, bool) {
	for {
		select {
		case event, ok := <-s.events:
			return event, ok
		default:
			return AssistantMessageEvent{}, false
		}
	}
}

// Result returns the final accumulated assistant message after the stream
// completes.
func (s *AssistantMessageEventStream) Result() *AssistantMessage {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// UpdateResult updates the Usage field of the terminal result.
func (s *AssistantMessageEventStream) UpdateResult(usage *Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result != nil {
		s.result.Usage = usage
	}
}

// Err returns any error that terminated the stream.
func (s *AssistantMessageEventStream) Err() error {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// ErrNoWait returns the error without blocking.
func (s *AssistantMessageEventStream) ErrNoWait() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
