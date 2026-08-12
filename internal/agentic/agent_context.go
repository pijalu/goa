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
//
// The mutation runs under a.mu (enforceContextCeilingLocked); the structured
// EventCompact is emitted after unlock so every visible surface — the
// conversation bubble, the footer counter, and the session JSONL — records
// the otherwise-silent reactive cut ("context compressions are
// invisible").
func (a *Agent) enforceContextCeiling() {
	before, res, ok := a.enforceContextCeilingLocked()
	if !ok {
		return
	}
	a.emitCompactionResult("ceiling", before, res, "")
}

// enforceContextCeilingLocked performs the reactive cut under a.mu and
// returns the pre-cut stats plus the work done (messages dropped + tokens
// freed). ok is false when nothing was cut (under ceiling, empty history, or
// no configured window) so the caller emits no phantom event.
func (a *Agent) enforceContextCeilingLocked() (before ContextStats, res compactionResult, ok bool) {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens == 0 {
		return before, res, false
	}

	// Use effectiveHard: the ceiling enforcer is a REACTIVE safety net that
	// stays on even when proactive threshold compression is disabled (hard=0).
	hardCeilingPercent := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	hardCeiling := maxTokens * hardCeilingPercent / 100
	// The fixed per-turn cost (system prompt + tool schemas) is always present;
	// history must fit in the remainder or the outgoing request still overflows.
	historyCeiling := hardCeiling - a.fixedCostTokens()
	// Reactive target (CM:13 design rule 4): a destructive front-cut
	// busts the provider prefix cache, so cut once to free ≥50% of the window
	// rather than nibbling just under the ceiling (which re-busts every round).
	// historyTarget is the token budget for retained history after the cut; the
	// enforcer finds the smallest cut whose retained tail fits this lower target
	// so one cache miss buys many rounds of headroom.
	targetPercent := a.cfg.ContextCompression.resolveThresholds().reactiveTargetPercent()
	historyTarget := maxTokens*targetPercent/100 - a.fixedCostTokens()
	if historyTarget < 0 {
		historyTarget = 0
	}

	// History is mutated here; hold the agent mutex for the whole transaction.
	// The rest of the agent uniformly guards a.history with a.mu, and this
	// last-resort safety net must too (it runs on the turn goroutine, but an
	// off-turn history reader would otherwise race it under -race).
	a.mu.Lock()
	defer a.mu.Unlock()

	hist := a.history
	if len(hist) <= 1 {
		return before, res, false
	}
	before = a.computeContextStats()

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
	// Floor history occupancy at the provider-reported value: when the
	// provider counts more than the chars-based estimate, the per-message
	// costs are scaled proportionally so the cut below retains a tail that
	// also fits the REAL window, not just the estimated one.
	total = a.floorTokensAtProviderUsage(tok, total)
	if total <= historyCeiling {
		return before, res, false
	}

	// Keep the system prompt (index 0) plus the most-recent contiguous tail
	// whose tokens fit the reactive TARGET (≥50% savings), not just the ceiling.
	// Find the smallest cut k in [1, n] such that tok[0] + sum(tok[k:]) <=
	// historyTarget. Cutting to the lower target frees ≥ReactiveSavingsPercent
	// of the window in one pass (design rule 4) so the next tool result does not
	// immediately re-cross the ceiling and bust the cache again. If the target
	// is unachievable (every cut still exceeds it), fall back to the hard
	// ceiling as the safety bound so the request at least fits.
	system := tok[0]
	nonSystem := total - system // sum(tok[1:])
	cut := len(hist)            // fall-back: keep only the system prompt
	droppedTokens := 0
	fittedCeiling := false
	for k := 1; k < len(hist); k++ {
		keptHere := system + (nonSystem - droppedTokens) // tok[0] + sum(tok[k:])
		if !fittedCeiling && keptHere <= historyCeiling {
			fittedCeiling = true
		}
		if keptHere <= historyTarget {
			cut = k
			break
		}
		droppedTokens += tok[k]
	}
	// The target was unreachable even after dropping everything non-system:
	// fall back to the smallest cut that fits the hard ceiling so the outgoing
	// request still fits (the absolute safety guarantee of this enforcer).
	if cut == len(hist) && fittedCeiling {
		droppedTokens = 0
		for k := 1; k < len(hist); k++ {
			keptHere := system + (nonSystem - droppedTokens)
			if keptHere <= historyCeiling {
				cut = k
				break
			}
			droppedTokens += tok[k]
		}
	}

	// Advance past any tool results whose owning assistant(tool_calls) message
	// was just dropped by the cut. A leading tool result with no preceding
	// tool_calls is rejected by strict providers (OpenAI/DeepSeek HTTP 400:
	// "Messages with role 'tool' must be a response to a preceding message
	// with 'tool_calls'"). Dropping these orphans is the correct move for a
	// last-resort safety net: they reference a call the model no longer sees,
	// so they carry no useful information. This never widens backward (which
	// could re-exceed the ceiling and cascade); it only drops more from the
	// front, so the token budget invariant is preserved.
	for cut < len(hist) && hist[cut].Role == ToolRole {
		droppedTokens += tok[cut]
		cut++
	}

	for _, m := range hist[1:cut] {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Warn, "Context ceiling enforced: dropped %s message (len=%d)", m.Role, len(m.Content))
		}
	}
	if a.cfg.Logger != nil && droppedTokens > 0 {
		a.cfg.Logger.Log(Warn, "Context ceiling enforced: reactive cut freed ~%d tokens (target %d%% of window, ≥%d%% savings per CM:13)",
			droppedTokens, targetPercent, ReactiveSavingsPercent)
	}

	kept := append(hist[:1:1], hist[cut:]...)
	a.history = kept
	// History shrank: the recorded provider prompt no longer corresponds.
	a.invalidateContextUsageLocked()

	if messageTokenCount(&hist[0])+(total-system-droppedTokens) > historyCeiling {
		a.cfg.Logger.Log(Error, "Context ceiling cannot be enforced: even minimal history + fixed cost exceeds %d tokens", hardCeiling)
	}

	// The cut dropped every non-system message before index cut. Report the
	// round so the ceiling pass surfaces like every other compression.
	res.removed = cut - 1
	res.freedTokens = droppedTokens
	return before, res, true
}

// floorTokensAtProviderUsage scales per-message token estimates up so their
// sum matches the provider-reported occupancy when that is larger (see
// estimateContextTokensLocked), and returns the corrected total. When the
// estimate already meets or exceeds the provider figure the slice is left
// untouched. The caller must hold a.mu.
func (a *Agent) floorTokensAtProviderUsage(tok []int, total int) int {
	floored := a.estimateContextTokensLocked() - a.fixedCostTokens()
	if floored <= total || total == 0 {
		return total
	}
	for i := range tok {
		tok[i] = tok[i] * floored / total
	}
	return floored
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

	estimated := a.estimateContextTokensLocked()
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
	hardCeilingPercent := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	hardCeiling := maxTokens * hardCeilingPercent / 100
	a.mu.Lock()
	estimated := a.estimateContextTokensLocked()
	a.mu.Unlock()
	if estimated > hardCeiling {
		return fmt.Errorf("context window full: estimated tokens exceed %d (%d%% of %d); compress or reset the conversation", hardCeiling, hardCeilingPercent, maxTokens)
	}
	return nil
}
