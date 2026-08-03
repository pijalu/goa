// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// loopScriptProvider returns scripted per-call text responses, one entry per
// Stream call. Calls beyond the script get a default tail response.
type loopScriptProvider struct {
	api    provider.Api
	mu     sync.Mutex
	script []string
	calls  int
}

func (p *loopScriptProvider) API() provider.Api { return p.api }

func (p *loopScriptProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.mu.Unlock()
	text := "script exhausted"
	if idx < len(p.script) {
		text = p.script[idx]
	}
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: text})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: text}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *loopScriptProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

func (p *loopScriptProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newLoopScriptAgent(t *testing.T, script []string) (*Agent, *loopScriptProvider) {
	t.Helper()
	p := &loopScriptProvider{
		api:    provider.Api(fmt.Sprintf("test-loopscript-%d", testProviderCounter.Add(1))),
		script: script,
	}
	provider.RegisterApiProvider(p)
	return NewAgent(Config{Model: testModel(p.api), SystemPrompt: "test"}), p
}

// TestRunawayLoopLatch_ResetLoopStopRecovers is the regression test for the
// bricked session: after the guardrail latches, a genuine new user input
// (ResetLoopStop, called by AgentManager for human turns and by the goal
// driver for runaway resumes) must clear the latch so the session proceeds.
func TestRunawayLoopLatch_ResetLoopStopRecovers(t *testing.T) {
	agent, p := newLoopScriptAgent(t, []string{
		"same answer", "same answer", "same answer", "recovery answer",
	})
	ctx := context.Background()

	if err := agent.Run(ctx, "turn 1"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := agent.Run(ctx, "turn 2"); err != nil {
		t.Fatalf("turn 2 (first repeat = warning only): %v", err)
	}
	if err := agent.Run(ctx, "turn 3"); err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("turn 3 = %v, want runaway loop detected error", err)
	}

	// Latched: the next turn is rejected WITHOUT contacting the provider.
	if err := agent.Run(ctx, "turn 4"); err == nil || !strings.Contains(err.Error(), "session stopped due to a runaway loop") {
		t.Fatalf("latched turn = %v, want session-stopped error", err)
	}
	if got := p.Calls(); got != 3 {
		t.Fatalf("provider calls = %d, want 3 (latched turn must not call the LLM)", got)
	}

	// A genuine new user message resets the latch: the session recovers.
	agent.ResetLoopStop()
	out, err := agent.RunAndCollect(ctx, "fresh prompt")
	if err != nil {
		t.Fatalf("turn after ResetLoopStop: %v", err)
	}
	if !strings.Contains(out, "recovery answer") {
		t.Fatalf("output = %q, want %q", out, "recovery answer")
	}
}

// TestRunawayLoopLatch_AutoExpires verifies the cooldown backstop: a latch
// older than loopStopCooldown no longer rejects turns, even without an
// explicit ResetLoopStop.
func TestRunawayLoopLatch_AutoExpires(t *testing.T) {
	agent, p := newLoopScriptAgent(t, []string{
		"same", "same", "same", "after expiry",
	})
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := agent.Run(ctx, fmt.Sprintf("turn %d", i+1)); err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
	}
	if err := agent.Run(ctx, "turn 3"); err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("turn 3 = %v, want runaway loop detected error", err)
	}

	// Backdate the latch beyond the cooldown.
	agent.mu.Lock()
	agent.loopStoppedAt = time.Now().Add(-loopStopCooldown - time.Minute)
	agent.mu.Unlock()

	out, err := agent.RunAndCollect(ctx, "later prompt")
	if err != nil {
		t.Fatalf("turn after cooldown expiry: %v", err)
	}
	if !strings.Contains(out, "after expiry") {
		t.Fatalf("output = %q, want %q", out, "after expiry")
	}
	if got := p.Calls(); got != 4 {
		t.Fatalf("provider calls = %d, want 4", got)
	}
	agent.mu.Lock()
	if agent.loopStopped {
		t.Fatal("latch still set after cooldown expiry")
	}
	if agent.assistantRepeatCount != 0 {
		t.Fatalf("repeat count = %d after expiry+progress, want 0", agent.assistantRepeatCount)
	}
	agent.mu.Unlock()
}

// TestCheckProgressLoop_SkipsStaleAssistantMessage is the regression test for
// the false strike: a turn that produced NO new assistant message (stream
// error, retry, pause) must not compare the stale last message against
// itself. Only messages appended at/after turnStartHistoryLen count.
func TestCheckProgressLoop_SkipsStaleAssistantMessage(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})

	seed := []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "q"},
		{Type: Content, Role: Assistant, Content: "same"},
	}
	agent.mu.Lock()
	agent.history = append([]Message(nil), seed...)
	agent.lastAssistantHash = agent.hashAssistantMessage(seed[2])
	// Turn starts AFTER the last assistant message and appends nothing
	// (e.g. the stream errored): the stale message must not score a strike.
	agent.turnStartHistoryLen = len(agent.history)
	agent.mu.Unlock()

	if err := agent.checkProgressLoop(); err != nil {
		t.Fatalf("stale assistant message scored a strike: %v", err)
	}
	agent.mu.Lock()
	if agent.assistantRepeatCount != 0 {
		t.Fatalf("repeat count = %d after stale check, want 0", agent.assistantRepeatCount)
	}
	if agent.loopStopped {
		t.Fatal("latch set on stale message")
	}
	agent.mu.Unlock()

	// A NEW identical message appended during the turn DOES count (hint).
	agent.mu.Lock()
	agent.turnStartHistoryLen = len(agent.history)
	agent.history = append(agent.history, Message{Type: Content, Role: Assistant, Content: "same"})
	agent.mu.Unlock()
	if err := agent.checkProgressLoop(); err != nil {
		t.Fatalf("first genuine repeat = %v, want warning hint only", err)
	}
	agent.mu.Lock()
	if agent.assistantRepeatCount != 1 {
		t.Fatalf("repeat count = %d after first genuine repeat, want 1", agent.assistantRepeatCount)
	}
	agent.mu.Unlock()

	// A second NEW identical message latches.
	agent.mu.Lock()
	agent.turnStartHistoryLen = len(agent.history)
	agent.history = append(agent.history, Message{Type: Content, Role: Assistant, Content: "same"})
	agent.mu.Unlock()
	err := agent.checkProgressLoop()
	if err == nil || !strings.Contains(err.Error(), "runaway loop detected") {
		t.Fatalf("second genuine repeat = %v, want runaway loop detected", err)
	}
	agent.mu.Lock()
	if !agent.loopStopped {
		t.Fatal("latch not set after second genuine repeat")
	}
	if agent.loopStoppedAt.IsZero() {
		t.Fatal("loopStoppedAt not recorded at latch time")
	}
	agent.mu.Unlock()
}

// TestClear_ResetsLoopGuardrail ensures starting a new conversation also
// clears the runaway-loop latch and counters.
func TestClear_ResetsLoopGuardrail(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test"})
	agent.mu.Lock()
	agent.loopStopped = true
	agent.loopStoppedAt = time.Now()
	agent.assistantRepeatCount = 2
	agent.lastAssistantHash = "abc"
	agent.mu.Unlock()

	agent.Clear()

	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loopStopped || !agent.loopStoppedAt.IsZero() || agent.assistantRepeatCount != 0 || agent.lastAssistantHash != "" {
		t.Fatal("Clear did not reset runaway-loop guardrail state")
	}
}
