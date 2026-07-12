// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
)

// effectiveMaxTokens returns the context window limit the agent should use for
// compression and ceiling decisions. When no compression limit is configured it
// falls back to the model's advertised context window (which can be refreshed at
// runtime by SetContextWindow). When a compression limit is configured, it is
// bounded by the actual model window: the model cannot hold more than its
// advertised capacity, so the effective limit is the smaller of the two.
func (a *Agent) effectiveMaxTokens() int {
	maxTokens := a.cfg.ContextCompression.MaxTokens
	if maxTokens == 0 {
		if cw := a.contextWindow.Load(); cw > 0 {
			return int(cw)
		}
		return a.cfg.Model.ContextWindow
	}
	// Compression is configured; respect it, but cap it at the actual model
	// window so we never defer compression past the model's real limit.
	if cw := a.contextWindow.Load(); cw > 0 {
		if int(cw) < maxTokens {
			return int(cw)
		}
		return maxTokens
	}
	if a.cfg.Model.ContextWindow > 0 && a.cfg.Model.ContextWindow < maxTokens {
		return a.cfg.Model.ContextWindow
	}
	return maxTokens
}

// enforceContextCeiling is a last-resort safety net. After proactive compression
// has run, if the estimated context still exceeds the configured maximum it
// drops the oldest non-system messages until usage is back under the ceiling.
// This prevents runaway conversations from growing unbounded when compression
// is disabled, misconfigured, or unable to keep up.
func (a *Agent) enforceContextCeiling() {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return
	}

	const hardCeilingPercent = 95
	hardCeiling := maxTokens * hardCeilingPercent / 100
	// The fixed per-turn cost (system prompt + tool schemas) is always present;
	// history must fit in the remainder or the outgoing request still overflows.
	historyCeiling := hardCeiling - a.fixedCostTokens()

	// History is mutated here; hold the agent mutex for the whole transaction.
	// The rest of the agent uniformly guards a.history with a.mu, and this
	// last-resort safety net must too (it runs on the turn goroutine, but an
	// off-turn history reader would otherwise race it under -race).
	a.mu.Lock()
	defer a.mu.Unlock()

	hist := a.history
	if len(hist) <= 1 {
		return
	}

	// Compute each message's token cost once. The previous implementation
	// removed the oldest non-system message one at a time, re-estimating the
	// whole history (O(n)) and shifting the slice (O(n)) per iteration, making
	// the last-resort safety net O(n^2) on long sessions exactly when it runs.
	tok := make([]int, len(hist))
	total := 0
	for i := range hist {
		tok[i] = messageTokenCount(&hist[i])
		total += tok[i]
	}
	if total <= historyCeiling {
		return
	}

	// Keep the system prompt (index 0) plus the most-recent contiguous tail
	// whose tokens fit under the ceiling. Find the smallest cut k in [1, n]
	// such that tok[0] + sum(tok[k:]) <= historyCeiling. This produces the same
	// retained set as dropping oldest messages one at a time, but in one pass.
	system := tok[0]
	nonSystem := total - system // sum(tok[1:])
	cut := len(hist)            // fall-back: keep only the system prompt
	droppedTokens := 0
	for k := 1; k < len(hist); k++ {
		keptHere := system + (nonSystem - droppedTokens) // tok[0] + sum(tok[k:])
		if keptHere <= historyCeiling {
			cut = k
			break
		}
		droppedTokens += tok[k]
	}

	for _, m := range hist[1:cut] {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Warn, "Context ceiling enforced: dropped %s message (len=%d)", m.Role, len(m.Content))
		}
	}

	kept := append(hist[:1:1], hist[cut:]...)
	a.history = kept

	if messageTokenCount(&hist[0])+(total-system-droppedTokens) > historyCeiling {
		a.cfg.Logger.Log(Error, "Context ceiling cannot be enforced: even minimal history + fixed cost exceeds %d tokens", hardCeiling)
	}
}

// computeContextStatsForMax computes context stats using the supplied max
// instead of the config value. Used by the fallback compression path.
func (a *Agent) computeContextStatsForMax(maxTokens int) ContextStats {
	var chars int
	for _, m := range a.history {
		chars += len(m.Content)
		chars += len(m.Thinking)
		for _, tc := range m.ToolCalls {
			chars += len(tc.Arguments)
		}
	}

	estimated := estimateTokensFromHistory(a.history) + a.fixedCostTokens()
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
		AutoMax:         a.cfg.ContextCompression.MaxTokens == 0 && a.cfg.Model.ContextWindow > 0,
	}
}

// checkContextLimit returns an error when the current context already exceeds
// the hard ceiling before a new turn starts. Callers should refuse to add more
// user input until the conversation is compressed or reset.
func (a *Agent) checkContextLimit() error {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return nil
	}
	const hardCeilingPercent = 95
	hardCeiling := maxTokens * hardCeilingPercent / 100
	a.mu.Lock()
	estimated := estimateTokensFromHistory(a.history) + a.fixedCostTokens()
	a.mu.Unlock()
	if estimated > hardCeiling {
		return fmt.Errorf("context window full: estimated tokens exceed %d (%d%% of %d); compress or reset the conversation", hardCeiling, hardCeilingPercent, maxTokens)
	}
	return nil
}
