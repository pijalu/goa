// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

type mockObserver struct {
	events []agentic.OutputEvent
}

func (m *mockObserver) OnEvent(event agentic.OutputEvent) {
	m.events = append(m.events, event)
}

func TestNewOutputBus(t *testing.T) {
	bus := NewOutputBus()
	if bus == nil {
		t.Fatal("NewOutputBus returned nil")
	}
	if bus.state != agentic.StateIdle {
		t.Errorf("initial state should be StateIdle, got %v", bus.state)
	}
}

func TestOutputBus_AddRemoveObserver(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}

	bus.AddObserver(obs)
	if len(bus.observers) != 1 {
		t.Errorf("expected 1 observer, got %d", len(bus.observers))
	}

	bus.RemoveObserver(obs)
	if len(bus.observers) != 0 {
		t.Errorf("expected 0 observers, got %d", len(bus.observers))
	}
}

func TestOutputBus_SendContent(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Type:    agentic.Content,
		Role:    agentic.Assistant,
		Content: "Hello",
		Delta:   false,
	})

	if len(obs.events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(obs.events), obs.events)
	}
	if obs.events[0].Type != agentic.EventStateChange {
		t.Errorf("first event should be state_change, got %s", obs.events[0].Type)
	}
	if obs.events[0].State != agentic.StateContent {
		t.Errorf("first event state should be StateContent, got %v", obs.events[0].State)
	}
	if obs.events[1].Type != agentic.EventContent {
		t.Errorf("second event should be content, got %s", obs.events[1].Type)
	}
	if obs.events[1].Text != "Hello" {
		t.Errorf("content text should be 'Hello', got %s", obs.events[1].Text)
	}
	if obs.events[2].Type != agentic.EventStateChange {
		t.Errorf("third event should be state_change, got %s", obs.events[2].Type)
	}
	if obs.events[2].State != agentic.StateIdle {
		t.Errorf("third event state should be StateIdle, got %v", obs.events[2].State)
	}
}

func TestOutputBus_SendDeltaContent(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "The ", Delta: true})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "answer", Delta: true})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: " is 27", Delta: false})

	if len(obs.events) != 5 {
		t.Fatalf("expected 5 events, got %d: %+v", len(obs.events), obs.events)
	}
	if obs.events[1].Text != "The " {
		t.Errorf("event 1 text = %q, want 'The '", obs.events[1].Text)
	}
	if obs.events[4].State != agentic.StateIdle {
		t.Errorf("final event state = %v, want StateIdle", obs.events[4].State)
	}
}

func TestOutputBus_SendThinkingToContent(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Thinking: "Let me think...", Delta: false})
	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "Result: 42", Delta: false})

	if len(obs.events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(obs.events), obs.events)
	}
	if obs.events[0].State != agentic.StateThinking {
		t.Errorf("first state should be thinking, got %v", obs.events[0].State)
	}
	if obs.events[3].State != agentic.StateContent {
		t.Errorf("fourth event should be state_change to content, got %v", obs.events[3].State)
	}
}

func TestOutputBus_SendToolCall(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Type:       agentic.ToolCall,
		ToolName:   "calc",
		ToolInput:  `{"a":1}`,
		ToolCallID: "call_1",
	})

	if len(obs.events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(obs.events), obs.events)
	}
	if obs.events[0].Type != agentic.EventStateChange || obs.events[0].State != agentic.StateToolCall {
		t.Errorf("first event should be state_change to tool_call")
	}
	if obs.events[1].Type != agentic.EventToolCall {
		t.Errorf("second event should be tool_call")
	}
	if obs.events[1].ToolName != "calc" {
		t.Errorf("tool name = %q, want 'calc'", obs.events[1].ToolName)
	}
	if obs.events[2].Type != agentic.EventStateChange || obs.events[2].State != agentic.StateIdle {
		t.Errorf("third event should be state_change to idle")
	}
}

func TestOutputBus_SendToolResult(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Type:       agentic.Content,
		Role:       agentic.ToolRole,
		Content:    "15",
		ToolCallID: "call_1",
		Delta:      false,
	})

	if len(obs.events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(obs.events), obs.events)
	}
	if obs.events[0].State != agentic.StateToolResult {
		t.Errorf("first state should be tool_result, got %v", obs.events[0].State)
	}
	if obs.events[1].Type != agentic.EventContent || obs.events[1].Text != "15" {
		t.Errorf("content event should have text '15'")
	}
}

func TestOutputBus_SendEnd(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{Type: agentic.End})

	if len(obs.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(obs.events))
	}
	if obs.events[0].Type != agentic.EventEnd {
		t.Errorf("expected end event, got %s", obs.events[0].Type)
	}
}

func TestOutputBus_Close(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "test", Delta: true})
	bus.Close()

	if len(obs.events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(obs.events))
	}
	last := obs.events[len(obs.events)-1]
	if last.Type != agentic.EventEnd {
		t.Errorf("last event should be end, got %s", last.Type)
	}
}

func TestOutputBus_EmptyMessageSkipped(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{})

	if len(obs.events) != 0 {
		t.Errorf("empty message should produce no events, got %d", len(obs.events))
	}
}

func TestOutputBus_MultipleObservers(t *testing.T) {
	bus := NewOutputBus()
	obs1 := &mockObserver{}
	obs2 := &mockObserver{}
	bus.AddObserver(obs1)
	bus.AddObserver(obs2)

	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "Hello", Delta: false})

	if len(obs1.events) != len(obs2.events) {
		t.Errorf("observers received different number of events: %d vs %d", len(obs1.events), len(obs2.events))
	}
}

func TestOutputBus_PanicRecovery(t *testing.T) {
	bus := NewOutputBus()
	panicker := &panicObserver{}
	normal := &mockObserver{}

	bus.AddObserver(panicker)
	bus.AddObserver(normal)

	bus.Send(agentic.Message{Type: agentic.Content, Role: agentic.Assistant, Content: "test", Delta: false})

	if len(normal.events) == 0 {
		t.Error("normal observer should have received events despite panicker")
	}
}

type panicObserver struct{}

func (p *panicObserver) OnEvent(event agentic.OutputEvent) {
	panic("intentional panic")
}

func TestOutputBus_SendTokenStats(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Type:    agentic.Content,
		Role:    agentic.Assistant,
		Content: "Hello",
		Timings: &agentic.TokenTimings{
			PromptN:            10,
			PredictedN:         5,
			PredictedPerSecond: 25.0,
		},
	})

	foundStats := false
	for _, e := range obs.events {
		if e.Type == agentic.EventTokenStats {
			foundStats = true
			if e.Timings == nil {
				t.Fatal("expected Timings in token_stats event")
			}
			if e.Timings.PromptN != 10 {
				t.Errorf("expected PromptN=10, got %d", e.Timings.PromptN)
			}
		}
	}
	if !foundStats {
		t.Error("expected EventTokenStats to be emitted")
	}
}

func TestOutputBus_SendProgress(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Type: agentic.Content,
		Role: agentic.Assistant,
		PromptProgress: &agentic.PromptProgress{
			Total:     24,
			Processed: 12,
		},
	})

	foundProgress := false
	for _, e := range obs.events {
		if e.Type == agentic.EventProgress {
			foundProgress = true
			if e.PromptProgress == nil {
				t.Fatal("expected PromptProgress in progress event")
			}
			if e.PromptProgress.Total != 24 {
				t.Errorf("expected Total=24, got %d", e.PromptProgress.Total)
			}
		}
	}
	if !foundProgress {
		t.Error("expected EventProgress to be emitted")
	}
}

func TestOutputBus_StatsOnlyMessageNotSkipped(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Timings: &agentic.TokenTimings{PromptN: 10, PredictedN: 5},
	})

	if len(obs.events) != 1 {
		t.Fatalf("expected 1 event for stats-only message, got %d: %+v", len(obs.events), obs.events)
	}
	if obs.events[0].Type != agentic.EventTokenStats {
		t.Errorf("expected EventTokenStats, got %s", obs.events[0].Type)
	}
}

func TestOutputBus_ContentAndStats(t *testing.T) {
	bus := NewOutputBus()
	obs := &mockObserver{}
	bus.AddObserver(obs)

	bus.Send(agentic.Message{
		Type:    agentic.Content,
		Role:    agentic.Assistant,
		Content: "Result",
		Timings: &agentic.TokenTimings{PromptN: 10, PredictedN: 5},
	})

	foundContent := false
	foundStats := false
	for _, e := range obs.events {
		if e.Type == agentic.EventContent && e.Text == "Result" {
			foundContent = true
		}
		if e.Type == agentic.EventTokenStats {
			foundStats = true
		}
	}
	if !foundContent {
		t.Error("expected content event")
	}
	if !foundStats {
		t.Error("expected token_stats event")
	}
}
