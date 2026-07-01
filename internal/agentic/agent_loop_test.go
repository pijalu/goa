// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"
	"time"
)

// TestAgent_DoesNotLoopOnPlainTextResponse verifies that a single plain-text
// response (no tool calls) ends the turn after one stream. Previously, the
// outer processTurnWithStream loop never broke on a successful no-tool-call
// round, so the agent kept re-streaming until the context window exploded or
// the user cancelled.
func TestAgent_DoesNotLoopOnPlainTextResponse(t *testing.T) {
	p := textEventProvider("Hello, this is a single response.")
	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are a test assistant.",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := agent.Run(ctx, "Hi")
	if err != nil {
		t.Fatalf("Run did not complete in time: %v", err)
	}

	endCount := 0
	for _, ev := range obs.Events() {
		if ev.Type == EventEnd {
			endCount++
		}
	}
	if endCount != 1 {
		t.Fatalf("expected exactly 1 EventEnd, got %d", endCount)
	}

	agent.mu.Lock()
	assistantCount := 0
	for _, m := range agent.history {
		if m.Role == Assistant {
			assistantCount++
		}
	}
	agent.mu.Unlock()
	if assistantCount != 1 {
		t.Fatalf("expected exactly 1 assistant message in history, got %d", assistantCount)
	}
}
