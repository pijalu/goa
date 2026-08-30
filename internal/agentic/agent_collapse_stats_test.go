// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// usageRecordingProvider records every provider context and ends each round's
// stream with a predetermined Usage so emitTurnStats emits one EventTokenStats
// per round through the provider-usage path (the OpenAI include_usage shape).
// Round 0 replays the given events; every later round streams a plain text
// answer (no more tool calls).
type usageRecordingProvider struct {
	api    provider.Api
	events []provider.AssistantMessageEvent
	usages []*provider.Usage // per-round usage; rounds past the slice end get none
	ctxs   []provider.Context
	round  int
}

func (p *usageRecordingProvider) API() provider.Api { return p.api }

func (p *usageRecordingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.ctxs = append(p.ctxs, ctx)
	// Claim the round synchronously so later Stream calls never observe a
	// stale round counter from an in-flight goroutine.
	round := p.round
	p.round++
	var usage *provider.Usage
	if round < len(p.usages) {
		usage = p.usages[round]
	}
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if round > 0 {
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "Summary."})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Summary."}},
				StopReason: provider.StopReasonEndTurn,
				Usage:      usage,
			})
			return
		}
		for _, e := range p.events {
			result.Push(e)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "mock"}},
			StopReason: provider.StopReasonEndTurn,
			Usage:      usage,
		})
	}()
	return result, nil
}

func (p *usageRecordingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// newUsageRecordingProvider registers a usageRecordingProvider under a unique
// API id (safe across repeated test runs) and returns it.
func newUsageRecordingProvider(name string, events []provider.AssistantMessageEvent, usages ...*provider.Usage) *usageRecordingProvider {
	p := &usageRecordingProvider{
		api:    provider.Api(fmt.Sprintf("usage-rec-%s-%d", name, testProviderCounter.Add(1))),
		events: events,
		usages: usages,
	}
	provider.RegisterApiProvider(p)
	return p
}

// tokenStatsEvents filters the observed events down to the EventTokenStats
// series, in emission order.
func tokenStatsEvents(obs *mockEventObserver) []OutputEvent {
	var stats []OutputEvent
	for _, e := range obs.Events() {
		if e.Type == EventTokenStats {
			stats = append(stats, e)
		}
	}
	return stats
}

// TestStopTurnCollapse_TokenStatsCarryFlag verifies F1 of the cache-miss
// shape-classification plan: the turn's final text-only round (P7 collapse —
// no tools, tool_choice "none") marks ITS OWN token-stats event with
// TextOnlyCollapse so /stats:cache can classify the round's by-design
// provider-prefix bust as an intentional request-shape change instead of an
// unexpected miss (bugs.md 2026-08-30). The tool round's stats must NOT
// carry the flag.
func TestStopTurnCollapse_TokenStatsCarryFlag(t *testing.T) {
	events := []provider.AssistantMessageEvent{toolCallEvent("call_1", "stop_it", `{}`)}
	p := newUsageRecordingProvider("collapse-stats", events,
		&provider.Usage{InputTokens: 80768, OutputTokens: 5, CacheReadTokens: 80768}, // round 0: prefix established
		&provider.Usage{InputTokens: 80773, OutputTokens: 5},                         // round 1 (collapse): read 0 — the by-design bust shape
	)

	agent := NewAgent(Config{
		Model:        testModel(p.api),
		SystemPrompt: "test",
		Tools:        []Tool{stopTurnTool{}},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	if _, err := agent.RunAndCollect(context.Background(), "complete it"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(p.ctxs) != 2 {
		t.Fatalf("expected 2 stream rounds (tool batch + text-only summary), got %d", len(p.ctxs))
	}
	if !p.ctxs[1].NoTools {
		t.Fatal("round 1 must be the collapsed no-tools round for this test to be meaningful")
	}

	stats := tokenStatsEvents(obs)
	if len(stats) != 2 {
		t.Fatalf("expected one EventTokenStats per round (2), got %d", len(stats))
	}
	if stats[0].TextOnlyCollapse {
		t.Error("round 0 (tools present) token stats must NOT carry the collapse flag")
	}
	if !stats[1].TextOnlyCollapse {
		t.Error("round 1 (P7 no-tools collapse) token stats must carry the collapse flag")
	}
}

// TestNormalRounds_TokenStatsNoCollapseFlag verifies the flag is specific to
// no-tools rounds: a normal tool round followed by a plain re-stream round
// (tools still present) emits token stats WITHOUT the collapse flag on both.
func TestNormalRounds_TokenStatsNoCollapseFlag(t *testing.T) {
	events := []provider.AssistantMessageEvent{toolCallEvent("call_1", "mock_tool", `{}`)}
	p := newUsageRecordingProvider("normal-stats", events,
		&provider.Usage{InputTokens: 100, CacheReadTokens: 90},
		&provider.Usage{InputTokens: 120, CacheReadTokens: 110},
	)

	agent := NewAgent(Config{
		Model:        testModel(p.api),
		SystemPrompt: "test",
		Tools:        []Tool{mockTool{name: "mock_tool", schema: ToolSchema{Name: "mock_tool", Description: "test"}}},
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	if _, err := agent.RunAndCollect(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(p.ctxs) != 2 {
		t.Fatalf("expected 2 stream rounds (tool batch + text answer), got %d", len(p.ctxs))
	}
	if p.ctxs[1].NoTools {
		t.Fatal("round 1 of a normal turn must keep tools (no collapse) for this test to be meaningful")
	}

	stats := tokenStatsEvents(obs)
	if len(stats) != 2 {
		t.Fatalf("expected one EventTokenStats per round (2), got %d", len(stats))
	}
	for i, s := range stats {
		if s.TextOnlyCollapse {
			t.Errorf("round %d is a normal tools-present round; its token stats must NOT carry the collapse flag", i)
		}
	}
}

// TestRecoveryCollapse_TokenStatsCarryFlag verifies the recovery path is
// classified like the stop-turn path: recovery rounds are the turn's last
// step and run text-only (P7), so their token stats carry the flag too.
func TestRecoveryCollapse_TokenStatsCarryFlag(t *testing.T) {
	events := []provider.AssistantMessageEvent{toolCallEvent("call_1", "stop_it", `{}`)}
	p := newUsageRecordingProvider("recovery-stats", events,
		&provider.Usage{InputTokens: 500, CacheReadTokens: 400},
		&provider.Usage{InputTokens: 505}, // recovery round: read 0 bust by design
	)

	agent := NewAgent(Config{
		Model:                    testModel(p.api),
		SystemPrompt:             "test",
		Tools:                    []Tool{stopTurnTool{}},
		MaxConsecutiveToolRounds: 1,
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	if _, err := agent.RunAndCollect(context.Background(), "keep working"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(p.ctxs) != 2 {
		t.Fatalf("expected tool round + recovery round, got %d", len(p.ctxs))
	}
	if !p.ctxs[1].NoTools {
		t.Fatal("recovery round must be the collapsed no-tools round for this test to be meaningful")
	}

	stats := tokenStatsEvents(obs)
	if len(stats) != 2 {
		t.Fatalf("expected one EventTokenStats per round (2), got %d", len(stats))
	}
	if stats[0].TextOnlyCollapse {
		t.Error("round 0 (tools present) token stats must NOT carry the collapse flag")
	}
	if !stats[1].TextOnlyCollapse {
		t.Error("recovery round (P7 no-tools collapse) token stats must carry the collapse flag")
	}
}
