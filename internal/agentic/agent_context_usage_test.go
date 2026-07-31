// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Regression tests for the deepseek-v4/opencode-go overflow incident: the
// chars-based estimator under-read the provider's token count by ~20% (84%
// estimated vs 100% actual), so every compression gate and the status bar
// were blind while the provider rejected the request. The provider-reported
// gross usage must floor the context estimate.

func recordTestUsage(a *Agent, u *provider.Usage) {
	a.mu.Lock()
	a.recordContextUsageLocked(u)
	a.mu.Unlock()
}

func TestComputeContextStats_FloorsAtProviderUsage(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: "hi"},
		{Type: Content, Role: Assistant, Content: "hello"},
	})

	baseline := a.ContextStats().EstimatedTokens
	if baseline > 1000 {
		t.Fatalf("test setup: chars-based estimate should be tiny, got %d", baseline)
	}

	// The provider reports a nearly-full window: 200 uncached + 899800 cached
	// input tokens (gross = 900000). The estimate must follow the provider,
	// not the character heuristic.
	recordTestUsage(a, &provider.Usage{InputTokens: 200, CacheReadTokens: 899800, OutputTokens: 100})

	stats := a.ContextStats()
	if stats.EstimatedTokens != 900000 {
		t.Errorf("EstimatedTokens = %d, want 900000 (provider gross input)", stats.EstimatedTokens)
	}
	if stats.UsagePercent != 90 {
		t.Errorf("UsagePercent = %d, want 90", stats.UsagePercent)
	}
}

func TestEstimateContextTokens_AddsMessagesAppendedSinceUsage(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: "hi"},
	})
	recordTestUsage(a, &provider.Usage{InputTokens: 500000})

	// A tool result arrives AFTER the usage line: its estimated cost must be
	// added on top of the recorded prompt size, or the next request's size is
	// again under-read.
	big := strings.Repeat("x", 3300)
	a.mu.Lock()
	a.history = append(a.history, Message{Type: Content, Role: ToolRole, Content: big, ToolCallID: "c1"})
	a.mu.Unlock()

	stats := a.ContextStats()
	want := 500000 + messageTokenCount(&Message{Type: Content, Role: ToolRole, Content: big, ToolCallID: "c1"})
	if stats.EstimatedTokens != want {
		t.Errorf("EstimatedTokens = %d, want %d (recorded prompt + appended message estimate)", stats.EstimatedTokens, want)
	}
}

func TestEstimateContextUsage_InvalidatedBySelectiveCompression(t *testing.T) {
	a := NewAgent(Config{
		Model:              provider.Model{ContextWindow: 1000000},
		ContextCompression: ContextCompressionConfig{PreserveRecentTurns: 1},
	})
	history := []Message{{Type: Content, Role: System, Content: "sys"}}
	for i := 0; i < 6; i++ {
		history = append(history,
			Message{Type: Content, Role: User, Content: "question"},
			Message{Type: Content, Role: Assistant, Content: "answer"})
	}
	a.SetHistory(history)
	recordTestUsage(a, &provider.Usage{InputTokens: 900000})

	if got := a.ContextStats().EstimatedTokens; got != 900000 {
		t.Fatalf("pre-condition: EstimatedTokens = %d, want 900000", got)
	}

	a.mu.Lock()
	a.compressSelective()
	a.mu.Unlock()

	a.mu.Lock()
	gross := a.lastGrossInputTokens
	a.mu.Unlock()
	if gross != 0 {
		t.Errorf("lastGrossInputTokens = %d, want 0 after history-shrinking compression", gross)
	}
	if got := a.ContextStats().EstimatedTokens; got >= 100000 {
		t.Errorf("EstimatedTokens = %d after selective, want the small chars-based value (stale provider floor must be dropped)", got)
	}
}

func TestEstimateContextUsage_InvalidatedByClearAndSetHistory(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "hi"}})
	recordTestUsage(a, &provider.Usage{InputTokens: 900000})

	a.Clear()
	a.mu.Lock()
	gross := a.lastGrossInputTokens
	a.mu.Unlock()
	if gross != 0 {
		t.Errorf("lastGrossInputTokens = %d after Clear, want 0", gross)
	}

	recordTestUsage(a, &provider.Usage{InputTokens: 900000})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "new conversation"}})
	a.mu.Lock()
	gross = a.lastGrossInputTokens
	a.mu.Unlock()
	if gross != 0 {
		t.Errorf("lastGrossInputTokens = %d after SetHistory, want 0", gross)
	}
}

func TestRecordContextUsage_IgnoresEmptyUsage(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "hi"}})

	recordTestUsage(a, &provider.Usage{})
	a.mu.Lock()
	gross := a.lastGrossInputTokens
	a.mu.Unlock()
	if gross != 0 {
		t.Errorf("lastGrossInputTokens = %d after zero usage, want 0 (no recording)", gross)
	}
}

// --- F5: estimator structural honesty ---

func TestEstimateTokensFromHistory_IncludesStructuralOverhead(t *testing.T) {
	// Two plain messages: content tokens + per-message framing overhead.
	msgs := []Message{
		{Type: Content, Role: User, Content: "hello"},
		{Type: Content, Role: Assistant, Content: "hello"},
	}
	want := 2 * (messageOverheadTokens + estimateTokens("hello"))
	if got := estimateTokensFromHistory(msgs); got != want {
		t.Errorf("estimateTokensFromHistory = %d, want %d (content + %d framing tokens/message)",
			got, want, messageOverheadTokens)
	}
}

func TestMessageTokenCount_CountsToolCallIDsAndNames(t *testing.T) {
	bare := &Message{Type: Content, Role: Assistant, Content: ""}
	withCall := &Message{Type: Content, Role: Assistant, ToolCalls: []ToolCallInfo{
		{ID: "call_9f8e7d6c5b", Name: "bash", Arguments: `{"cmd":"ls"}`},
	}}
	diff := messageTokenCount(withCall) - messageTokenCount(bare)
	want := toolCallOverheadTokens + estimateTokens("call_9f8e7d6c5b") + estimateTokens("bash") + estimateTokens(`{"cmd":"ls"}`)
	if diff != want {
		t.Errorf("tool call adds %d tokens, want %d (wrapper + id + name + args)", diff, want)
	}

	withResultID := &Message{Type: Content, Role: ToolRole, ToolCallID: "call_9f8e7d6c5b"}
	if got, wantID := messageTokenCount(withResultID)-messageTokenCount(bare), estimateTokens("call_9f8e7d6c5b"); got != wantID {
		t.Errorf("tool result id adds %d tokens, want %d", got, wantID)
	}
}
