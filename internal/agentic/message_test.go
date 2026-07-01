// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "testing"

func TestMessage_DeltaField(t *testing.T) {
	msg := Message{
		Type:    Content,
		Role:    Assistant,
		Content: "Hello",
		Delta:   true,
	}

	if !msg.Delta {
		t.Error("expected Delta to be true")
	}
	if msg.Content != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", msg.Content)
	}
}

func TestMessage_ThinkingField(t *testing.T) {
	msg := Message{
		Type:     Content,
		Role:     Assistant,
		Content:  "The answer is 42",
		Thinking: "Let me calculate...",
	}

	if msg.Thinking != "Let me calculate..." {
		t.Errorf("expected 'Let me calculate...', got '%s'", msg.Thinking)
	}
}

func TestMessage_FinalMessage(t *testing.T) {
	msg := Message{
		Type:    Content,
		Role:    Assistant,
		Content: "Complete answer",
		Delta:   false, // final message
	}

	if msg.Delta {
		t.Error("expected Delta to be false for final message")
	}
}

func TestMessage_ToolCallWithDelta(t *testing.T) {
	msg := Message{
		Type:      ToolCall,
		ToolName:  "calculator",
		ToolInput: `{"a":10,"b":20}`,
		Delta:     true,
	}

	if !msg.Delta {
		t.Error("expected Delta to be true")
	}
	if msg.Type != ToolCall {
		t.Errorf("expected ToolCall, got %s", msg.Type)
	}
}

func TestMessage_EndMessage(t *testing.T) {
	msg := Message{
		Type: End,
	}

	if msg.Type != End {
		t.Errorf("expected End, got %s", msg.Type)
	}
}

func TestMessage_TokenTimings(t *testing.T) {
	msg := Message{
		Type:    Content,
		Role:    Assistant,
		Content: "Hello",
		Timings: &TokenTimings{
			PromptN:            18,
			PredictedN:         62,
			PromptMs:           459.502,
			PredictedMs:        1444.654,
			PromptPerSecond:    39.17,
			PredictedPerSecond: 42.91,
		},
	}

	if msg.Timings == nil {
		t.Fatal("expected Timings to be set")
	}
	if msg.Timings.PromptN != 18 {
		t.Errorf("expected PromptN=18, got %d", msg.Timings.PromptN)
	}
	if msg.Timings.PredictedPerSecond != 42.91 {
		t.Errorf("expected PredictedPerSecond=42.91, got %f", msg.Timings.PredictedPerSecond)
	}
}

func TestMessage_PromptProgress(t *testing.T) {
	msg := Message{
		Type: Content,
		Role: Assistant,
		PromptProgress: &PromptProgress{
			Total:     24,
			Cache:     6,
			Processed: 24,
			TimeMs:    399,
		},
	}

	if msg.PromptProgress == nil {
		t.Fatal("expected PromptProgress to be set")
	}
	if msg.PromptProgress.Total != 24 {
		t.Errorf("expected Total=24, got %d", msg.PromptProgress.Total)
	}
	if msg.PromptProgress.Processed != 24 {
		t.Errorf("expected Processed=24, got %d", msg.PromptProgress.Processed)
	}
}

func TestMessage_StatsOnlyMessage(t *testing.T) {
	// Message can carry only stats without content/thinking
	msg := Message{
		Timings: &TokenTimings{
			PromptN:    10,
			PredictedN: 5,
		},
	}

	if msg.Timings.PromptN != 10 {
		t.Errorf("expected PromptN=10, got %d", msg.Timings.PromptN)
	}
	if msg.Content != "" {
		t.Error("expected empty content for stats-only message")
	}
}
