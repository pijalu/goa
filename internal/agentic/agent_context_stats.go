// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "github.com/pijalu/goa/internal/agentic/provider"

func (a *Agent) ContextStats() ContextStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.computeContextStats()
}

func (a *Agent) computeContextStats() ContextStats {
	var chars int
	for _, m := range a.history {
		chars += len(m.Content)
		chars += len(m.Thinking)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Arguments)
		}
	}

	estimated := a.estimateContextTokensLocked()

	// The UI should always reflect the model's actual capacity. Prefer the
	// runtime-refreshed context window, then the configured model window, and
	// fall back to the explicit compression limit only when no model window is
	// known. This prevents a stale auto-derived compression limit from hiding
	// a smaller loaded context window (e.g., local models reporting 32k after
	// the default registry advertised 131k).
	maxTokens := int(a.contextWindow.Load())
	autoMax := maxTokens > 0
	if maxTokens == 0 {
		maxTokens = a.cfg.Model.ContextWindow
		autoMax = maxTokens > 0
	}
	if maxTokens == 0 {
		maxTokens = a.cfg.ContextCompression.MaxTokens
		autoMax = false
	}

	usagePercent := 0
	if maxTokens > 0 {
		usagePercent = estimated * 100 / maxTokens
	}

	return ContextStats{
		Messages:        len(a.history),
		Characters:      chars,
		EstimatedTokens: estimated,
		MaxTokens:       maxTokens,
		UsagePercent:    usagePercent,
		AutoMax:         autoMax,
	}
}

// Token-estimation constants. The estimator is the FALLBACK for providers
// that do not report usage; it must err on the high side. Undercounting once
// deferred every compression gate past the provider's real window (opencode-go
// deepseek-v4 session: 84% estimated while the provider counted 100% and
// rejected the request with context_length_exceeded).
const (
	// asciiCharsPerTokenX10 is the ASCII chars-per-token rate ×10 (i.e. 3.3
	// chars/token). Empirically, tool-heavy coding sessions (Go/SQL/JSON/hex
	// tool output) tokenize at ≈3.3 chars/token for BPE tokenizers
	// (deepseek/openai families); the historical /4 under-read them by ~20%.
	asciiCharsPerTokenX10 = 33
	// messageOverheadTokens covers the per-message wire scaffolding the raw
	// text does not show: role tokens and chat-template framing (~3-4 tokens
	// per message on OpenAI-style APIs). Counted by the provider, invisible
	// to a chars-only heuristic.
	messageOverheadTokens = 4
	// toolCallOverheadTokens covers the function-call wrapper (type plus the
	// id/name/arguments JSON keys) around each tool call, beyond the id, name
	// and arguments payloads themselves.
	toolCallOverheadTokens = 4
)

// estimateContextTokensLocked returns the best available context occupancy in
// tokens: the chars-based estimate of history plus the per-turn fixed cost,
// floored at the provider-reported gross prompt size of the last completed
// request plus the estimated cost of messages appended since that request.
//
// The provider's usage line is the ground truth — it is the exact number of
// tokens charged against the window for the exact request sent. The chars/3.3
// heuristic is only a fallback for providers that report no usage; when both
// exist, the larger (safer) value wins. A stale floor is never used: every
// history-shrinking operation (compaction, ceiling enforcement, Clear,
// SetHistory) invalidates the recording via invalidateContextUsageLocked.
//
// The caller must hold a.mu.
func (a *Agent) estimateContextTokensLocked() int {
	estimated := estimateTokensFromHistory(a.history) + a.fixedCostTokens()
	if a.lastGrossInputTokens > 0 {
		floor := a.lastGrossInputTokens
		if a.lastUsageHistoryLen < len(a.history) {
			floor += estimateTokensFromHistory(a.history[a.lastUsageHistoryLen:])
		}
		if floor > estimated {
			estimated = floor
		}
	}
	return estimated
}

// recordContextUsageLocked stores the provider-reported occupancy of the
// request that just completed, along with the history length it covered, so
// estimateContextTokensLocked can floor later estimates at the real value.
// The caller must hold a.mu.
func (a *Agent) recordContextUsageLocked(u *provider.Usage) {
	gross := u.TotalInputTokens()
	if gross <= 0 {
		return
	}
	a.lastGrossInputTokens = gross
	a.lastUsageOutputTokens = u.OutputTokens
	a.lastUsageHistoryLen = len(a.history)
}

// invalidateContextUsageLocked drops the recorded provider occupancy: after
// history is shrunk, replaced, or mutated in place, the recorded prompt no
// longer corresponds to the conversation and flooring at it would overstate
// usage (potentially above the hard ceiling, blocking new turns forever).
// The cache-warmth evidence (cacheWarmObserved) is dropped for the same
// reason: the mutation that invalidated the prompt also busted the provider
// prefix cache, so the gate must re-learn warmth from the next cache read.
// The caller must hold a.mu.
func (a *Agent) invalidateContextUsageLocked() {
	a.lastGrossInputTokens = 0
	a.lastUsageHistoryLen = 0
	a.lastUsageOutputTokens = 0
	a.cacheWarmObserved = false
}

// estimateTokensFromHistory returns a token estimate for a message slice:
// per-message content cost plus structural overhead (messageTokenCount).
func estimateTokensFromHistory(msgs []Message) int {
	var total int
	for i := range msgs {
		total += messageTokenCount(&msgs[i])
	}
	return total
}

func estimateTokens(text string) int {
	cjkCount := 0
	asciiCount := 0
	for _, r := range text {
		switch {
		case r >= '\u4e00' && r <= '\u9fff',
			r >= '\u3040' && r <= '\u309f',
			r >= '\u30a0' && r <= '\u30ff',
			r >= '\uac00' && r <= '\ud7af':
			cjkCount++
		case r < 128:
			asciiCount++
		}
	}
	others := len([]rune(text)) - cjkCount - asciiCount
	return cjkCount + asciiCount*10/asciiCharsPerTokenX10 + others/2
}

// MaybeCompress manually triggers context compression regardless of thresholds.
// Returns the compression result. No-op if the context is empty.