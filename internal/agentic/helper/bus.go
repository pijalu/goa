// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// OutputBus receives raw agent messages, normalizes them into events,
// and broadcasts to all registered observers.
type OutputBus struct {
	observers []agentic.OutputObserver
	state     agentic.OutputState
	mu        sync.RWMutex
}

// NewOutputBus creates a new OutputBus starting in the idle state.
func NewOutputBus() *OutputBus {
	return &OutputBus{
		state: agentic.StateIdle,
	}
}

// AddObserver registers an observer to receive events.
func (b *OutputBus) AddObserver(o agentic.OutputObserver) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.observers = append(b.observers, o)
}

// RemoveObserver unregisters an observer.
func (b *OutputBus) RemoveObserver(o agentic.OutputObserver) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, obs := range b.observers {
		if obs == o {
			b.observers = append(b.observers[:i], b.observers[i+1:]...)
			return
		}
	}
}

// Send converts a raw agent message into events and broadcasts them.
func (b *OutputBus) Send(msg agentic.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	target := b.stateFor(msg)

	// Check if message carries stats (these are emitted even for otherwise "empty" messages)
	hasStats := msg.Timings != nil || msg.PromptProgress != nil

	if msg.Type == agentic.End {
		b.transitionTo(agentic.StateIdle)
		b.emit(agentic.OutputEvent{Type: agentic.EventEnd})
		return
	}

	if target == agentic.StateIdle && !hasStats {
		return // skip empty messages without stats
	}

	b.transitionTo(target)

	switch target {
	case agentic.StateThinking, agentic.StateContent, agentic.StateToolResult:
		text := msg.Thinking
		if text == "" {
			text = msg.Content
		}
		b.emit(agentic.OutputEvent{
			Type:       agentic.EventContent,
			State:      target,
			Role:       msg.Role,
			Text:       text,
			IsDelta:    msg.Delta,
			ToolCallID: msg.ToolCallID,
		})
	case agentic.StateToolCall:
		b.emit(agentic.OutputEvent{
			Type:       agentic.EventToolCall,
			State:      target,
			ToolName:   msg.ToolName,
			ToolInput:  msg.ToolInput,
			ToolCallID: msg.ToolCallID,
		})
		b.transitionTo(agentic.StateIdle) // tool calls are atomic
	}

	// Final message: reset to idle
	if !msg.Delta && target != agentic.StateToolCall {
		b.transitionTo(agentic.StateIdle)
	}

	// Emit token stats if present (stateless event)
	if msg.Timings != nil {
		b.emit(agentic.OutputEvent{
			Type:    agentic.EventTokenStats,
			Timings: msg.Timings,
		})
	}

	// Emit progress if present (stateless event)
	if msg.PromptProgress != nil {
		b.emit(agentic.OutputEvent{
			Type:           agentic.EventProgress,
			PromptProgress: msg.PromptProgress,
		})
	}
}

// Close signals the end of the output stream to all observers.
func (b *OutputBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transitionTo(agentic.StateIdle)
	b.emit(agentic.OutputEvent{Type: agentic.EventEnd})
}

// stateFor maps a raw message to its logical output state.
func (b *OutputBus) stateFor(msg agentic.Message) agentic.OutputState {
	switch msg.Type {
	case agentic.End:
		return agentic.StateIdle
	case agentic.ToolCall:
		return agentic.StateToolCall
	}
	if msg.Thinking != "" {
		return agentic.StateThinking
	}
	if msg.Content != "" {
		if msg.Role == agentic.ToolRole {
			return agentic.StateToolResult
		}
		return agentic.StateContent
	}
	return agentic.StateIdle
}

// transitionTo updates state and emits a state_change event when the state changes.
func (b *OutputBus) transitionTo(target agentic.OutputState) {
	if target != b.state {
		b.state = target
		b.emit(agentic.OutputEvent{
			Type:  agentic.EventStateChange,
			State: target,
		})
	}
}

// emit broadcasts an event to all observers, recovering from panics.
func (b *OutputBus) emit(event agentic.OutputEvent) {
	for _, obs := range b.observers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Observer panicked; continue with remaining observers.
				}
			}()
			obs.OnEvent(event)
		}()
	}
}
