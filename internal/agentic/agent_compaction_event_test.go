// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// compactEvents returns only the EventCompact events from the observer.
func compactEvents(obs *mockEventObserver) []OutputEvent {
	var out []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventCompact {
			out = append(out, e)
		}
	}
	return out
}

// bigToolHistory builds a history with large elidable tool payloads before the
// preserve window so tool_elision does visible work. The tool results sit at
// indices 2, 4 and 6 — inside the elision boundary for a 9-message history
// (count boundary = 9 − preserve*3 = 3 with the default preserve=2, and the
// forced floor keeps the two newest messages, so indices < 7 are elidable
// under force). Six trailing messages guarantee enough tail.
func bigToolHistory() []Message {
	return []Message{
		{Type: Content, Role: System, Content: "You are helpful."},                     // 0
		{Type: Content, Role: User, Content: "step 1"},                                 // 1
		{Type: Content, Role: ToolRole, ToolCallID: "c1", Content: strings.Repeat("x", 2000)}, // 2
		{Type: Content, Role: User, Content: "step 2"},                                 // 3
		{Type: Content, Role: ToolRole, ToolCallID: "c2", Content: strings.Repeat("y", 2000)}, // 4
		{Type: Content, Role: User, Content: "step 3"},                                 // 5
		{Type: Content, Role: ToolRole, ToolCallID: "c3", Content: strings.Repeat("z", 2000)}, // 6
		{Type: Content, Role: User, Content: "step 4"},                                 // 7
		{Type: Content, Role: Assistant, Content: "ok"},                                // 8
	}
}

// TestEnforceContextCeiling_EmitsCompactEvent is the regression test for
// bugs.md "context compressions are invisible": the reactive ceiling enforcer
// dropped messages with zero observable trace. It must now emit exactly one
// structured EventCompact with Strategy="ceiling".
func TestEnforceContextCeiling_EmitsCompactEvent(t *testing.T) {
	mk := func(role Role, n int) Message {
		return Message{Type: Content, Role: role, Content: strings.Repeat("x", n)}
	}
	a := &Agent{
		cfg: Config{Model: provider.Model{ContextWindow: 100}}, // hardCeiling = 95 tokens
		history: []Message{
			mk(System, 4),
			mk(User, 200),
			mk(User, 200),
			mk(User, 200),
		},
	}
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.enforceContextCeiling()

	evs := compactEvents(obs)
	if len(evs) != 1 {
		t.Fatalf("expected exactly one EventCompact, got %d (%v)", len(evs), obs.Events())
	}
	ci := evs[0].Compaction
	if ci == nil {
		t.Fatal("ceiling EventCompact must carry the structured Compaction payload")
	}
	if ci.Strategy != "ceiling" {
		t.Errorf("Strategy = %q, want ceiling", ci.Strategy)
	}
	if ci.Removed <= 0 {
		t.Errorf("Removed = %d, want > 0 (messages were dropped)", ci.Removed)
	}
	if ci.AfterPct >= ci.BeforePct {
		t.Errorf("AfterPct (%d) must be < BeforePct (%d) after a cut", ci.AfterPct, ci.BeforePct)
	}
}

// TestEnforceContextCeiling_NoEventWhenUnderCeiling verifies no phantom event
// fires when the ceiling enforcer has nothing to drop.
func TestEnforceContextCeiling_NoEventWhenUnderCeiling(t *testing.T) {
	a := &Agent{
		cfg: Config{Model: provider.Model{ContextWindow: 1_000_000}},
		history: []Message{
			{Type: Content, Role: System, Content: "sys"},
			{Type: Content, Role: User, Content: "hi"},
			{Type: Content, Role: Assistant, Content: "hello"},
		},
	}
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.enforceContextCeiling()

	if n := len(compactEvents(obs)); n != 0 {
		t.Errorf("expected no EventCompact under ceiling, got %d", n)
	}
}

// TestCompressToolElision_EmitsCompactEvent verifies the elision path emits a
// structured event through compressHistoryWith when it mutates tool payloads.
func TestCompressToolElision_EmitsCompactEvent(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 100000,
			Strategy:  CompressionToolElision,
		},
	})
	agent.SetHistory(bigToolHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.compressHistoryWith(context.Background(), CompressionToolElision, true); err != nil {
		t.Fatalf("compressHistoryWith: %v", err)
	}

	evs := compactEvents(obs)
	if len(evs) != 1 {
		t.Fatalf("expected exactly one EventCompact, got %d", len(evs))
	}
	if evs[0].Compaction == nil || evs[0].Compaction.Strategy != "elision" {
		t.Errorf("Strategy = %+v, want elision", evs[0].Compaction)
	}
}

// TestCompressToolElision_NoEventWhenNothingToDo verifies elision emits no
// event when the history has nothing elidable before the preserve window.
func TestCompressToolElision_NoEventWhenNothingToDo(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 100000,
			Strategy:  CompressionToolElision,
		},
	})
	agent.SetHistory([]Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "hi"},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	// force=false with a cold cache: the count boundary is 1, nothing elides.
	if err := agent.compressHistoryWith(context.Background(), CompressionToolElision, false); err != nil {
		t.Fatalf("compressHistoryWith: %v", err)
	}
	if n := len(compactEvents(obs)); n != 0 {
		t.Errorf("expected no EventCompact when nothing elides, got %d", n)
	}
}

// TestCompressSelective_EmitsCompactEvent verifies the selective path emits a
// structured event when it drops messages.
func TestCompressSelective_EmitsCompactEvent(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           100000,
			Strategy:            CompressionSelective,
			PreserveRecentTurns: 1,
		},
	})
	var h []Message
	h = append(h, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 10; i++ {
		h = append(h, Message{Type: Content, Role: User, Content: strings.Repeat("a", 20)})
		h = append(h, Message{Type: Content, Role: Assistant, Content: strings.Repeat("b", 20)})
	}
	agent.SetHistory(h)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.compressHistoryWith(context.Background(), CompressionSelective, false); err != nil {
		t.Fatalf("compressHistoryWith: %v", err)
	}

	evs := compactEvents(obs)
	if len(evs) != 1 {
		t.Fatalf("expected exactly one EventCompact, got %d", len(evs))
	}
	ci := evs[0].Compaction
	if ci == nil || ci.Strategy != "selective" {
		t.Fatalf("Strategy = %+v, want selective", ci)
	}
	if ci.Removed <= 0 {
		t.Errorf("Removed = %d, want > 0", ci.Removed)
	}
}

// TestCompressHybrid_EmitsSingleCompactEvent is the regression test against
// double-emit: hybrid's elision+selective path must emit exactly one event
// (strategy "hybrid"), not one per internal step.
func TestCompressHybrid_EmitsSingleCompactEvent(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           500,
			Thresholds:          CompressionThresholds{HardPercent: 15},
			Strategy:            CompressionHybrid,
			PreserveRecentTurns: 1,
		},
	})
	var h []Message
	h = append(h, Message{Type: Content, Role: System, Content: "You are helpful."})
	for i := 0; i < 20; i++ {
		h = append(h, Message{Type: Content, Role: User, Content: strings.Repeat("a", 20)})
		h = append(h, Message{Type: Content, Role: Assistant, Content: strings.Repeat("b", 20)})
	}
	agent.SetHistory(h)
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.compressHybrid(context.Background()); err != nil {
		t.Fatalf("compressHybrid: %v", err)
	}

	evs := compactEvents(obs)
	if len(evs) != 1 {
		t.Fatalf("expected exactly one EventCompact from hybrid (no double-emit), got %d", len(evs))
	}
	if evs[0].Compaction == nil || evs[0].Compaction.Strategy != "hybrid" {
		t.Errorf("Strategy = %+v, want hybrid", evs[0].Compaction)
	}
}

// TestCompressHistoryWith_MicroEmitsOnce verifies the micro path emits exactly
// one event after the internal emission moved to the caller.
func TestCompressHistoryWith_MicroEmitsOnce(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:        100000,
			ThresholdPercent: 80,
			Strategy:         CompressionMicro,
			MicroCompaction:  DefaultMicroCompactionConfig,
		},
	})
	agent.SetHistory(bigToolHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	if err := agent.compressHistoryWith(context.Background(), CompressionMicro, true); err != nil {
		t.Fatalf("compressHistoryWith micro: %v", err)
	}

	evs := compactEvents(obs)
	if len(evs) != 1 {
		t.Fatalf("expected exactly one EventCompact from micro, got %d", len(evs))
	}
	if evs[0].Compaction == nil || evs[0].Compaction.Strategy != "micro" {
		t.Errorf("Strategy = %+v, want micro", evs[0].Compaction)
	}
}

// TestCompressOverflowRecovery_EmitsOverflowEvent verifies the overflow
// recovery path emits a structured "overflow" event when the cheap steps free
// enough (no summarize escalation).
func TestCompressOverflowRecovery_EmitsOverflowEvent(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "You are helpful.",
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           100000,
			PreserveRecentTurns: 1,
		},
	})
	agent.SetHistory(bigToolHistory())
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	// With a generous window the post-elision/selective estimate sits far
	// below the escalation level, so no summarize is attempted and the cheap
	// work is reported as "overflow".
	agent.compressOverflowRecovery(context.Background())

	evs := compactEvents(obs)
	if len(evs) != 1 {
		t.Fatalf("expected exactly one EventCompact from overflow recovery, got %d", len(evs))
	}
	if evs[0].Compaction == nil || evs[0].Compaction.Strategy != "overflow" {
		t.Errorf("Strategy = %+v, want overflow", evs[0].Compaction)
	}
}
