// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Regression for goa-export-20261015-170840: a goal-driven session was stopped
// with
//
//	runaway loop detected: the assistant repeated the same response 3
//	consecutive times without progress (repeated: "(empty response)")
//
// even though every one of those three turns executed real, successful tool
// work (dozens of read/search/bash calls, edits) and only its FINAL provider
// response was empty — the provider "empty stop right after tool results"
// quirk the premature-stop auto-continue exists for (agent_stream_heal.go).
//
// Three such endings in a row latched the guardrail across goal continuation
// turns (goal continuations deliberately keep the guardrail state —
// core/agentmanager_turns.go) and paused a healthy goal with a message that
// blames the model for repeating itself.
//
// An empty response carries no content to repeat: when the turn it ends made
// observable progress (a tool call whose result landed without an execution
// error), it must not accumulate a repetition strike.
func TestCheckProgressLoop_EmptyEndingAfterToolWorkDoesNotLatch(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})

	// One goal-style turn: a real tool call with a successful result, then an
	// empty assistant message (the provider returned nothing at the turn end).
	turnEndingEmptyAfterToolWork := func(n int) []Message {
		msgs := toolTurn([3]string{fmt.Sprintf("call_%d", n), "read", fmt.Sprintf(`{"path":"f%d.go"}`, n)})
		return append(msgs, Message{Type: Content, Role: Assistant})
	}

	for turn := 1; turn <= 3; turn++ {
		if err := runGuardrailTurn(t, agent, turnEndingEmptyAfterToolWork(turn)...); err != nil {
			t.Fatalf("turn %d ending with an empty reply after successful tool work was stopped: %v", turn, err)
		}
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loopStopped {
		t.Fatal("runaway-loop latch set although every turn made tool progress")
	}
	if agent.assistantRepeatCount != 0 {
		t.Fatalf("repeat count = %d, want 0 (empty endings after tool progress are not repeats)", agent.assistantRepeatCount)
	}
}

// The original rule stays intact: a turn that produced nothing at all — no
// content, no thinking, no tool calls, and no tool work anywhere in the turn —
// still strikes, and three of them still latch (plan rule 3).
func TestCheckProgressLoop_EmptyNoToolTurnsLatchUnchanged(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	empty := func() []Message {
		return []Message{{Type: Content, Role: Assistant}}
	}
	if err := runGuardrailTurn(t, agent, empty()...); err != nil {
		t.Fatalf("empty turn 1: %v", err)
	}
	if err := runGuardrailTurn(t, agent, empty()...); err != nil {
		t.Fatalf("empty turn 2 (first repeat) = %v, want warning only", err)
	}
	err := runGuardrailTurn(t, agent, empty()...)
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("empty turn 3 = %v, want runaway loop detected", err)
	}
}

// A turn whose tool calls ALL failed is not progress: the existing failing-turn
// rule must keep striking (empty endings included).
func TestCheckProgressLoop_EmptyEndingAfterFailedToolsStillLatches(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	failing := func() []Message {
		msgs := singleCallTurn("c1", "bash", `{"cmd":"make test"}`, "Error: exit 1")
		return append(msgs, Message{Type: Content, Role: Assistant})
	}
	if err := runGuardrailTurn(t, agent, failing()...); err != nil {
		t.Fatalf("failing turn 1: %v", err)
	}
	if err := runGuardrailTurn(t, agent, failing()...); err != nil {
		t.Fatalf("failing turn 2 (first repeat) = %v, want warning only", err)
	}
	err := runGuardrailTurn(t, agent, failing()...)
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("failing turn 3 = %v, want runaway loop detected", err)
	}
}

// toolThenEmptyProvider reproduces the export's provider behaviour within one
// turn: the first round of the turn issues a real tool call, every later round
// ends cleanly with NO events at all (empty stop right after tool results).
// ResetTurn arms the next turn's tool round the way a fresh goal turn would.
type toolThenEmptyProvider struct {
	api       provider.Api
	toolRound atomic.Bool
	turn      atomic.Int32
}

func (p *toolThenEmptyProvider) API() provider.Api { return p.api }

// ResetTurn arms a tool round for the next turn and returns the turn number.
func (p *toolThenEmptyProvider) ResetTurn() int {
	p.toolRound.Store(true)
	return int(p.turn.Add(1))
}

func (p *toolThenEmptyProvider) Stream(_ provider.Model, _ provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	if p.toolRound.CompareAndSwap(true, false) {
		arg := fmt.Sprintf(`{"arg":"turn-%d"}`, p.turn.Load())
		go func() {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    fmt.Sprintf("call_%d", p.turn.Load()),
					ToolName:      "mock_tool",
					ToolArguments: arg,
				},
			})
			result.End(&provider.AssistantMessage{
				Content: []provider.ContentBlock{{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    fmt.Sprintf("call_%d", p.turn.Load()),
					ToolName:      "mock_tool",
					ToolArguments: arg,
				}},
				StopReason: provider.StopReasonToolCall,
			})
		}()
		return result, nil
	}
	go func() {
		// Empty stop right after tool results: no text, no thinking, no calls.
		result.End(&provider.AssistantMessage{StopReason: provider.StopReasonEndTurn})
	}()
	return result, nil
}

func (p *toolThenEmptyProvider) StreamSimple(m provider.Model, c provider.Context, o provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(m, c, provider.BuildSimpleOptions(m, o))
}

func registerToolThenEmptyProvider() *toolThenEmptyProvider {
	id := testProviderCounter.Add(1)
	p := &toolThenEmptyProvider{api: provider.Api(fmt.Sprintf("test-tool-then-empty-%d", id))}
	provider.RegisterApiProvider(p)
	return p
}

// End-to-end reproduction of the export: three goal continuation turns, each
// doing real tool work and ending with an empty provider reply, must not stop
// the session — and each such turn must report itself as a silent stop so the
// goal driver pauses the goal with the real reason (provider produced no reply)
// instead of blaming a runaway model loop.
func TestRun_GoalTurnsEndingEmptyAfterToolWorkDoNotStopSession(t *testing.T) {
	p := registerToolThenEmptyProvider()
	agent := newAgentWithMockTool(p.API(), 50)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for turn := 1; turn <= 3; turn++ {
		p.ResetTurn()
		if err := agent.Run(ctx, fmt.Sprintf("Continue working toward the active goal (turn %d)", turn)); err != nil {
			t.Fatalf("turn %d stopped the session: %v", turn, err)
		}
		if !agent.LastTurnSilentStop() {
			t.Fatalf("turn %d ended with an empty reply but did not report a silent stop", turn)
		}
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loopStopped {
		t.Fatal("runaway-loop latch set although every turn made tool progress")
	}
}

// The empty reply must be visible, never silent: the user (and the goal driver)
// must be told the provider returned nothing, with a hint about what to do.
func TestRun_EmptyReplyAfterToolWorkSurfacesNotice(t *testing.T) {
	p := registerToolThenEmptyProvider()
	agent := newAgentWithMockTool(p.API(), 50)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p.ResetTurn()
	if err := agent.Run(ctx, "Continue working toward the active goal"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	found := false
	for _, e := range obs.Events() {
		if e.Type == EventContent && e.Role == System && strings.Contains(e.Text, "empty reply") {
			found = true
			if e.Metadata["category"] != "system-notification" {
				t.Errorf("notice missing system-notification category, got %v", e.Metadata)
			}
			if e.Metadata["transient"] == "true" {
				t.Error("notice must be non-transient so it survives the turn")
			}
		}
	}
	if !found {
		t.Error("expected a system notification explaining the empty reply")
	}
}
