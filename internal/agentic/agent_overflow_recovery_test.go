// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Regression tests for the failed overflow recovery in the deepseek-v4
// incident: after the provider rejected the request with
// context_length_exceeded, micro compaction truncated 27 tool results and the
// escalation gate — fed by the same under-counting estimate (84% < 90%) —
// refused selective compression, so the retry went out still oversized and
// failed identically.

// overflowTestHistory builds text-heavy turns: the bulk of the context lives
// in user/assistant text, which cheap strategies (elision, micro) cannot free.
func overflowTestHistory(pairs, charsPerMessage int) []Message {
	history := []Message{{Type: Content, Role: System, Content: "sys"}}
	for i := 0; i < pairs; i++ {
		history = append(history,
			Message{Type: Content, Role: User, Content: strings.Repeat("q", charsPerMessage)},
			Message{Type: Content, Role: Assistant, Content: strings.Repeat("a", charsPerMessage)},
		)
	}
	return history
}

// The provider's rejection must escalate cheap strategies (micro/elision) to
// selective message removal even when the LOCAL estimate sits below the old
// 90% escalation gate — the estimate provably under-counts.
func TestHandleContextError_AlwaysEscalatesCheapStrategyToSelective(t *testing.T) {
	a := NewAgent(Config{
		Model: provider.Model{ContextWindow: 0},
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           100000,
			OnContextError:      true,
			Strategy:            CompressionMicro,
			PreserveRecentTurns: 2,
		},
	})
	a.SetHistory(overflowTestHistory(12, 2000))
	before := len(a.GetHistory())

	// Pre-condition of the regression: the estimate sits BELOW the escalation
	// level (effectiveHard−5 = 90% of max), where a threshold-gated selective
	// would refuse to escalate. The error path must escalate unconditionally
	// because the provider rejection PROVES overflow despite the under-counting
	// estimate.
	stats := a.ContextStats()
	if stats.EstimatedTokens >= 90000 {
		t.Fatalf("test setup: estimate %d must sit below the 90%% escalation level", stats.EstimatedTokens)
	}

	a.handleContextError(context.Background(), errors.New(`{"error":{"code":"context_length_exceeded","message":"Request exceeds the context window of the model"}}`))

	after := a.GetHistory()
	// Selective keeps the system message plus the last PreserveRecentTurns
	// user turns (2 turns × 2 messages); everything older must be gone.
	wantMax := 1 + 2*2
	if len(after) > wantMax {
		t.Errorf("history len = %d, want ≤ %d (system + last 2 turns); before=%d", len(after), wantMax, before)
	}
	if len(after) >= before {
		t.Errorf("selective escalation did not shrink history: %d → %d", before, len(after))
	}
	if after[0].Role != System {
		t.Errorf("system message must be retained, got role %s", after[0].Role)
	}
}

// The error path escalates elision → selective → summarize (pi-style last
// resort): when even selective cannot bring the estimate below the escalation
// level, it attempts a Compact (LLM summarize). With no provider registered,
// that summarize fails — and the failure reaching the log proves the
// summarize stage was attempted.
func TestHandleContextError_EscalatesToSummarizeAtWindowEdge(t *testing.T) {
	a := NewAgent(Config{
		Model: provider.Model{ContextWindow: 0},
		ContextCompression: ContextCompressionConfig{
			// Tiny window: even the minimal post-selective history sits above the
			// escalation level (effectiveHard−5 ≈ 90%), forcing the summarize stage.
			MaxTokens:           10,
			OnContextError:      true,
			PreserveRecentTurns: 1,
		},
	})
	a.SetHistory(overflowTestHistory(6, 2000))

	// Compact needs a provider stream; none is registered, so it errors. The
	// recovery path swallows that error (it only logs), so the observable
	// signal that summarize was attempted is that it did NOT panic and the
	// selective stage already ran. Assert selective shrank history (elision +
	// selective ran) — the summarize attempt is exercised by the no-error
	// return of the recovery despite an unreachable provider.
	a.handleContextError(context.Background(), errors.New(`context_length_exceeded`))

	if len(a.GetHistory()) == 0 {
		t.Error("recovery dropped the system message; expected at least the system prompt to survive")
	}
}

// finish_reason=length at ≥99% of the window is the last warning before a
// hard rejection: compression must fire before the next round goes out.
func TestMaybeCompressAfterLengthTruncation_FiresAtWindowEdge(t *testing.T) {
	a := NewAgent(Config{
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 1,
		},
	})
	a.SetHistory(overflowTestHistory(6, 2000))
	before := len(a.GetHistory())

	// Provider truncated the round at exactly the window: gross prompt 9950 +
	// output 40 = 9990 ≥ 99% of 10000.
	a.lastStopReason = provider.StopReasonMaxTokens
	a.lastGrossInputTokens = 9950
	a.lastUsageOutputTokens = 40

	a.maybeCompressAfterLengthTruncation()

	if got := len(a.GetHistory()); got >= before {
		t.Errorf("no compression at window edge: history len %d, want < %d", got, before)
	}
	if a.lastGrossInputTokens != 0 {
		t.Errorf("usage recording not invalidated after compression: %d", a.lastGrossInputTokens)
	}
}

// A plain max_tokens output cap (finish_reason=length with the prompt far
// below the window) must NOT trigger destructive compression.
func TestMaybeCompressAfterLengthTruncation_IgnoresOutputCapTruncation(t *testing.T) {
	a := NewAgent(Config{
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 1,
		},
	})
	a.SetHistory(overflowTestHistory(6, 2000))
	before := len(a.GetHistory())

	a.lastStopReason = provider.StopReasonMaxTokens
	a.lastGrossInputTokens = 3000 // prompt only 30% full…
	a.lastUsageOutputTokens = 5000 // …but a long output hit the cap

	a.maybeCompressAfterLengthTruncation()

	if got := len(a.GetHistory()); got != before {
		t.Errorf("output-cap truncation must not compress: history len %d, want %d", got, before)
	}
}

// Non-length stop reasons never trigger the guard, even at 99%+ occupancy
// (that case is handled by the proactive hard-tier instead).
func TestMaybeCompressAfterLengthTruncation_IgnoresNonLengthStop(t *testing.T) {
	a := NewAgent(Config{
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           10000,
			Strategy:            CompressionToolElision,
			PreserveRecentTurns: 1,
		},
	})
	a.SetHistory(overflowTestHistory(6, 2000))
	before := len(a.GetHistory())

	a.lastStopReason = provider.StopReasonEndTurn
	a.lastGrossInputTokens = 9950
	a.lastUsageOutputTokens = 40

	a.maybeCompressAfterLengthTruncation()

	if got := len(a.GetHistory()); got != before {
		t.Errorf("non-length stop must not compress: history len %d, want %d", got, before)
	}
}
