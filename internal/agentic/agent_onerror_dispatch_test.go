// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"
)

// Dispatch-table tests for the configurable on-error recovery strategy
// (ContextCompressionConfig.OnErrorStrategy; empty = hybrid). Each entry pins
// which recovery actions run for a PROVEN context overflow:
//
//   - summarize  → straight to Compact (LLM), no in-memory elision first
//   - tool_elision → elision only
//   - selective  → selective message removal only
//   - micro      → forced micro truncation only
//   - hybrid     → elision + selective, then Compact only when the estimate
//     still sits at/above the escalation level

func TestOnErrorStrategyResolution(t *testing.T) {
	tests := []struct {
		name string
		cfg  ContextCompressionConfig
		want CompressionStrategy
	}{
		{"empty defaults to hybrid", ContextCompressionConfig{}, CompressionHybrid},
		{"explicit summarize", ContextCompressionConfig{OnErrorStrategy: CompressionSummarize}, CompressionSummarize},
		{"explicit micro", ContextCompressionConfig{OnErrorStrategy: CompressionMicro}, CompressionMicro},
		{"explicit tool_elision", ContextCompressionConfig{OnErrorStrategy: CompressionToolElision}, CompressionToolElision},
		{"explicit selective", ContextCompressionConfig{OnErrorStrategy: CompressionSelective}, CompressionSelective},
		{"explicit hybrid", ContextCompressionConfig{OnErrorStrategy: CompressionHybrid}, CompressionHybrid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAgent(Config{ContextCompression: tt.cfg})
			if got := a.onErrorStrategy(); got != tt.want {
				t.Errorf("onErrorStrategy() = %q, want %q", got, tt.want)
			}
		})
	}
}

// dispatchAgent builds an agent over a micro-tool history with a huge window
// so the post-recovery estimate sits far below the escalation level (no
// escalate-to-Compact tail); the tail is pinned separately.
func dispatchAgent(t *testing.T, onError CompressionStrategy) *Agent {
	t.Helper()
	p := textEventProvider("Summary: dispatch table.")
	a := NewAgent(Config{
		Model: testModel(p.API()),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           1000000, // post-pass estimate ≪ escalation level
			OnErrorStrategy:     onError,
			PreserveRecentTurns: 2,
		},
	})
	a.history = microToolHistory(2000, 6)
	return a
}

func compactStrategies(obs *mockEventObserver) []string {
	var out []string
	for _, ev := range compactEvents(obs) {
		if ev.Compaction != nil {
			out = append(out, ev.Compaction.Strategy)
		}
	}
	return out
}

func hasStrategy(obs *mockEventObserver, strategy string) bool {
	for _, s := range compactStrategies(obs) {
		if s == strategy {
			return true
		}
	}
	return false
}

// toolContents returns the concatenated tool-result contents (post-recovery
// in-place state) for assertions on elision/micro truncation.
func toolContents(a *Agent) string {
	var b strings.Builder
	for _, m := range a.history {
		if m.Role == ToolRole {
			b.WriteString(m.Content)
		}
	}
	return b.String()
}

func TestOverflowRecovery_ToolElisionOnly(t *testing.T) {
	a := dispatchAgent(t, CompressionToolElision)
	before := len(a.history)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.compressOverflowRecovery(context.Background())

	if n := len(a.history); n != before {
		t.Errorf("tool_elision must not drop messages: history %d → %d", before, n)
	}
	total := 6 * 2000
	if c := len(toolContents(a)); c == 0 || c >= total {
		t.Errorf("tool_elision must elide old tool results while preserving the recent window, got %d of %d chars", c, total)
	} else if c > 2*2000+2*len("[elided]")+100 {
		t.Errorf("tool_elision kept more than the preserve window: %d residual chars", c)
	}
	if hasStrategy(obs, "summarize") {
		t.Error("tool_elision must not run the LLM Compact stage")
	}
	if !hasStrategy(obs, "overflow") {
		t.Error("tool_elision recovery must emit an overflow compaction event")
	}
}

func TestOverflowRecovery_SelectiveOnly(t *testing.T) {
	a := dispatchAgent(t, CompressionSelective)
	before := len(a.history)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.compressOverflowRecovery(context.Background())

	if n := len(a.history); n >= before {
		t.Errorf("selective must drop old messages: history %d → %d", before, n)
	}
	if n := len(a.history); n > 1+2*3 {
		t.Errorf("selective keeps system + last 2 turns, got %d messages", n)
	}
	if hasStrategy(obs, "summarize") {
		t.Error("selective must not run the LLM Compact stage")
	}
	if !hasStrategy(obs, "overflow") {
		t.Error("selective recovery must emit an overflow compaction event")
	}
}

func TestOverflowRecovery_MicroForced(t *testing.T) {
	a := dispatchAgent(t, CompressionMicro)
	before := len(a.history)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.compressOverflowRecovery(context.Background())

	if n := len(a.history); n != before {
		t.Errorf("micro must truncate in place, not drop messages: history %d → %d", before, n)
	}
	full := strings.Count(toolContents(a), strings.Repeat("r", 100))
	if full > 0 {
		t.Errorf("micro left %d untruncated tool payloads", full)
	}
	if hasStrategy(obs, "summarize") {
		t.Error("micro must not run the LLM Compact stage")
	}
}

func TestOverflowRecovery_HybridBelowEscalation(t *testing.T) {
	a := dispatchAgent(t, "")
	before := len(a.history)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.compressOverflowRecovery(context.Background())

	// Hybrid runs elision + selective unconditionally: old messages dropped.
	if n := len(a.history); n >= before {
		t.Errorf("hybrid must drop old messages: history %d → %d", before, n)
	}
	// Estimate far below the escalation level → no Compact, one overflow event.
	if hasStrategy(obs, "summarize") {
		t.Error("hybrid must not escalate to Compact below the escalation level")
	}
	if !hasStrategy(obs, "overflow") {
		t.Error("hybrid recovery must emit an overflow compaction event")
	}
}

func TestOverflowRecovery_SummarizeSkipsInMemory(t *testing.T) {
	a := dispatchAgent(t, CompressionSummarize)
	before := len(a.history)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.compressOverflowRecovery(context.Background())

	// Summarize goes straight to Compact: the LLM was used and the history
	// becomes the [summary-request, summary] pair.
	if !hasStrategy(obs, "summarize") {
		t.Error("summarize on-error must run the LLM Compact stage")
	}
	if n := len(a.history); n != 2 {
		t.Errorf("after Compact the history is the [summary-request, summary] pair, got %d (before=%d)", n, before)
	}
}

func TestOverflowRecovery_HybridEscalatesAtWindowEdge(t *testing.T) {
	p := textEventProvider("Summary: hybrid escalation.")
	a := NewAgent(Config{
		Model: testModel(p.API()),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10, // even the minimal history sits ≥ escalation
			OnErrorStrategy:     "",
			PreserveRecentTurns: 1,
		},
	})
	a.history = overflowTestHistory(6, 2000)
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	a.compressOverflowRecovery(context.Background())

	if !hasStrategy(obs, "summarize") {
		t.Error("hybrid must escalate to Compact when the estimate stays at/above the escalation level")
	}
}
