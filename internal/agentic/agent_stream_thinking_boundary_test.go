// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestThinkingBoundaryEvents_CountAsProgress pins the mapping that keeps
// thinking_start / thinking_end out of the unmapped class: goa's own
// responses protocol pushes both around every reasoning summary
// (protocol/openai_responses.go), so treating them as unmapped logged a
// per-turn WARN blaming the provider for a goa-internal event — the same
// self-blame class as EventStart (bugs.md #2) — and kept them from re-arming
// the stall/quiet guards during a long thinking phase.
func TestThinkingBoundaryEvents_CountAsProgress(t *testing.T) {
	a := NewAgent(Config{Logger: NewLogger(Error)})
	unmapped := make(map[provider.EventType]bool)

	for _, typ := range []provider.EventType{provider.EventThinkingStart, provider.EventThinkingEnd} {
		if !a.noteStreamEventProgress(provider.AssistantMessageEvent{Type: typ}, unmapped) {
			t.Errorf("noteStreamEventProgress(%s) = false, want mapped (re-arms the stall/quiet guards)", typ)
		}
	}
	assert.Empty(t, unmapped, "thinking boundary events must never be reported as unmapped")
}

// TestThinkingBoundaryHandler_AbsorbsWithoutMarkingGeneration pins the two
// things the boundary handler must NOT do: terminate the stream, and mark the
// generation started — framing alone must not turn an otherwise empty
// response into a delivered one (the empty-response retry guard in
// agent_turn_lifecycle keys on genSawEvent).
func TestThinkingBoundaryHandler_AbsorbsWithoutMarkingGeneration(t *testing.T) {
	a := NewAgent(Config{Logger: NewLogger(Error)})

	for _, typ := range []provider.EventType{provider.EventThinkingStart, provider.EventThinkingEnd} {
		handler, ok := streamEventHandlers[typ]
		require.True(t, ok, "%s must have a registered handler", typ)

		done, toolCalls, err := handler(a, context.Background(), nil, provider.AssistantMessageEvent{
			Type:    typ,
			Content: "accumulated thinking text",
		})
		assert.False(t, done, "%s must not terminate the stream", typ)
		assert.False(t, toolCalls, "%s encounters no tool calls", typ)
		require.NoError(t, err)
		assert.False(t, a.genSawEvent, "%s must not mark the generation started", typ)
	}
}
