// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestProcessTurn_EmitsEstimatedTokenStats verifies estimated token stats at turn end.
func TestProcessTurn_EmitsEstimatedTokenStats(t *testing.T) {
	p := registerTestProvider("est-stats", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Hello world"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	go func() {
		for range agent.Output {
		}
	}()
	go agent.Run(context.Background(), "Hi there")
	time.Sleep(200 * time.Millisecond)
	agent.Stop()

	foundStats := false
	for _, e := range obs.Events() {
		if e.Type == EventTokenStats {
			foundStats = true
			if e.Timings == nil {
				t.Fatal("expected Timings in estimated token_stats event")
			}
			if e.Timings.PromptN <= 0 {
				t.Errorf("expected PromptN > 0, got %d", e.Timings.PromptN)
			}
			if e.Timings.PredictedN <= 0 {
				t.Errorf("expected PredictedN > 0, got %d", e.Timings.PredictedN)
			}
		}
	}
	if !foundStats {
		t.Error("expected EventTokenStats to be emitted with estimated values")
	}
}

// TestProcessTurn_EmitsContextStats verifies context stats at turn end.
func TestProcessTurn_EmitsContextStats(t *testing.T) {
	p := registerTestProvider("ctx-stats", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Hello"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	go func() {
		for range agent.Output {
		}
	}()
	go agent.Run(context.Background(), "Hi")
	time.Sleep(200 * time.Millisecond)
	agent.Stop()

	foundContextStats := false
	for _, e := range obs.Events() {
		if e.Type == EventContextStats {
			foundContextStats = true
			if e.ContextStats == nil {
				t.Fatal("expected ContextStats in context_stats event")
			}
			if e.ContextStats.Messages == 0 {
				t.Error("expected Messages > 0 in context stats")
			}
			if e.ContextStats.EstimatedTokens == 0 {
				t.Error("expected EstimatedTokens > 0 in context stats")
			}
		}
	}
	if !foundContextStats {
		t.Error("expected EventContextStats to be emitted at turn end")
	}
}

// TestProcessTurn_ContextStatsWithMaxTokens verifies MaxTokens configuration.
func TestProcessTurn_ContextStatsWithMaxTokens(t *testing.T) {
	assistantContent := "This is a reasonably long assistant response to exercise the context stats computation, and every part of it is intentionally unique so that loop detection does not fire. "
	p := registerTestProvider("mx-tokens", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: assistantContent},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 4096,
		},
	})

	obs := runAgentObservingStats(t, agent, "Hi there, how are you doing today")
	assertContextStats(t, obs.Events(), 4096)
}

func runAgentObservingStats(t *testing.T, agent *Agent, prompt string) *mockEventObserver {
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()
	go agent.Run(context.Background(), prompt)
	time.Sleep(200 * time.Millisecond)
	agent.Stop()
	return obs
}

func assertContextStats(t *testing.T, events []OutputEvent, wantMaxTokens int) {
	for _, e := range events {
		if e.Type != EventContextStats {
			continue
		}
		if e.ContextStats == nil {
			t.Fatal("expected ContextStats")
		}
		if e.ContextStats.MaxTokens != wantMaxTokens {
			t.Errorf("expected MaxTokens=%d, got %d", wantMaxTokens, e.ContextStats.MaxTokens)
		}
		if e.ContextStats.UsagePercent == 0 {
			t.Error("expected UsagePercent > 0 when MaxTokens is set")
		}
		if e.ContextStats.EstimatedTokens == 0 {
			t.Error("expected EstimatedTokens > 0")
		}
		return
	}
	t.Error("expected EventContextStats in events")
}
