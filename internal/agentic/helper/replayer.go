// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// Replayer replays recorded events as a mock API provider.
type Replayer struct {
	mu     sync.Mutex
	events []RecordedEvent
	index  int
	loop   bool
}

// NewReplayer creates a new replayer from recorded events.
func NewReplayer(events []RecordedEvent, loop bool) *Replayer {
	eventsCopy := make([]RecordedEvent, len(events))
	copy(eventsCopy, events)

	return &Replayer{
		events: eventsCopy,
		index:  0,
		loop:   loop,
	}
}

// NewReplayerFromFile creates a new replayer by loading events from a file.
func NewReplayerFromFile(filePath string, loop bool) (*Replayer, error) {
	recorder := &Recorder{}
	if err := recorder.Load(filePath); err != nil {
		return nil, fmt.Errorf("load file: %w", err)
	}

	return NewReplayer(recorder.Events(), loop), nil
}

// API returns a unique API type for this mock provider.
func (r *Replayer) API() provider.Api {
	return provider.Api("test-replayer-api")
}

// Stream replays the recorded events as a stream.
func (r *Replayer) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		r.mu.Lock()
		events := make([]RecordedEvent, len(r.events))
		copy(events, r.events)
		r.mu.Unlock()

		for _, event := range events {
			msg := r.eventToMessage(event)
			if msg.Type == agentic.End {
				break
			}
			if msg.Type == agentic.Content && msg.Role == agentic.Assistant && msg.Content != "" {
				result.Push(provider.AssistantMessageEvent{
					Type:         provider.EventTextStart,
					ContentIndex: 0,
				})
				result.Push(provider.AssistantMessageEvent{
					Type:         provider.EventTextDelta,
					ContentIndex: 0,
					Delta:        msg.Content,
				})
				result.Push(provider.AssistantMessageEvent{
					Type:         provider.EventTextEnd,
					ContentIndex: 0,
				})
			}
			if msg.Type == agentic.ToolCall {
				result.Push(provider.AssistantMessageEvent{
					Type: provider.EventToolCallEnd,
					ToolCall: &provider.ContentBlock{
						Type:          provider.ContentBlockToolCall,
						ToolName:      msg.ToolName,
						ToolArguments: msg.ToolInput,
					},
				})
			}
		}
		result.End(&provider.AssistantMessage{
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (r *Replayer) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return r.Stream(model, ctx, base)
}

// eventToMessage converts a RecordedEvent to an agentic.Message.
func (r *Replayer) eventToMessage(event RecordedEvent) agentic.Message {
	switch event.Type {
	case string(agentic.EventContent):
		return agentic.Message{
			Type:    agentic.Content,
			Content: event.Content,
			Role:    agentic.Assistant,
		}
	case string(agentic.EventToolCall):
		return agentic.Message{
			Type:      agentic.ToolCall,
			ToolName:  event.ToolName,
			ToolInput: event.ToolInput,
			Role:      agentic.Assistant,
		}
	case string(agentic.EventToolResult):
		return agentic.Message{
			Type:    agentic.Content,
			Content: event.ToolResult,
			Role:    agentic.ToolRole,
		}
	default:
		return agentic.Message{}
	}
}

// Reset resets the replayer to the beginning.
func (r *Replayer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index = 0
}

// Position returns the current position in the event list.
func (r *Replayer) Position() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.index
}

// Remaining returns the number of events remaining.
func (r *Replayer) Remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events) - r.index
}

// Total returns the total number of events.
func (r *Replayer) Total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// SetPosition sets the current position.
func (r *Replayer) SetPosition(pos int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pos >= 0 && pos <= len(r.events) {
		r.index = pos
	}
}

// IsLooping returns whether the replayer loops.
func (r *Replayer) IsLooping() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loop
}

// SetLooping sets whether the replayer loops.
func (r *Replayer) SetLooping(loop bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loop = loop
}
