// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "context"

func (a *Agent) runElision(force bool) (ContextStats, compactionResult) {
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.compressToolElision(force)
	a.mu.Unlock()
	return before, res
}

// runSelective executes compressSelective under a.mu and returns the
// pre-pass stats plus the work done.
func (a *Agent) runSelective() (ContextStats, compactionResult) {
	a.mu.Lock()
	before := a.computeContextStats()
	res := a.compressSelective()
	a.mu.Unlock()
	return before, res
}

// compressHybrid applies tool_elision then selective if still over threshold.
// The in-memory steps run under a.mu; Compact (network) runs off-lock.
// The "still too full" gate uses the escalation level (effectiveHard−5), not
// the proactive trigger — so the last-resort summarize only fires when the
// window is genuinely near full, independent of any opt-in trigger level.
//
// Emission: exactly one EventCompact per invocation. When the cheap steps
// free enough, a single "hybrid" event fires; when they escalate to Compact,
// Compact emits its own "summarize" event and no "hybrid" event fires (the
// elision/selective work is subsumed by the summarize).
func (a *Agent) compressHybrid(ctx context.Context) error {
	threshold := a.cfg.ContextCompression.resolveThresholds().effectiveHard()

	a.mu.Lock()
	before := a.computeContextStats()
	res := a.compressToolElision(true)
	stats := a.computeContextStats()
	needMore := stats.UsagePercent >= threshold
	if needMore {
		sel := a.compressSelective()
		res.removed += sel.removed
		res.changed += sel.changed
		res.freedTokens += sel.freedTokens
		stats = a.computeContextStats()
		needMore = stats.UsagePercent >= threshold
	}
	a.mu.Unlock()
	if !needMore {
		a.emitCompactionResult(string(CompressionHybrid), before, res, "")
		return nil
	}
	return a.Compact(ctx)
}

// compressToolElision replaces old tool arguments and results with placeholders.
// When force is true (manual /compress invocation), the recent-turn preserve
// window is reduced so that small histories still have messages to elide.
// The proactive path (force=false) picks its boundary via
// proactiveElisionBoundary: eager with a cold cache, token-budgeted with
// hysteresis when the pass must bust a hot provider prefix cache.
//
// It returns the work done (messages mutated in place + estimated tokens
// freed, plus any selective escalation) so the caller can emit one EventCompact
// after unlock. The caller must hold a.mu.
func (a *Agent) compressToolElision(force bool) compactionResult {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}
	boundary := computeElisionBoundary(len(a.history), preserve)
	escalate := false
	if force {
		// Forced compression must always do visible work. If the standard
		// boundary leaves nothing to elide, keep only the two most recent
		// messages and process everything before them.
		boundary = forcedElisionBoundary(boundary, len(a.history))
	} else {
		boundary, escalate = a.proactiveElisionBoundary(boundary)
	}
	changed, freed := a.elideToolMessages(boundary)
	if boundary > 1 {
		// Tool payloads were replaced in place: the recorded provider prompt
		// no longer matches the conversation.
		a.invalidateContextUsageLocked()
	}
	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied tool_elision to messages before index %d", boundary)
	}
	res := compactionResult{changed: changed, freedTokens: freed}
	if escalate {
		// The elidable payload could not meet the hysteresis budget, so this
		// hot-cache bust would repeat next round. Drop old turns instead so
		// the bust buys real headroom (prefix-cache bust loop).
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "tool_elision budget unmet: escalating to selective compression")
		}
		sel := a.compressSelective()
		res.removed += sel.removed
		res.changed += sel.changed
		res.freedTokens += sel.freedTokens
		res.escalated = true
	}
	return res
}

// forcedElisionBoundary applies the forced-pass floor: keep only the two most
// recent messages when the count-based boundary leaves nothing to elide.
func forcedElisionBoundary(boundary, histLen int) int {
	if boundary > 1 {
		return boundary
	}
	boundary = histLen - 2
	if boundary < 1 {
		boundary = 1
	}
	return boundary
}

// proactiveElisionBoundary returns the elision boundary for a threshold-
// triggered pass, and whether the caller must escalate to selective
// compression afterwards. With a cold (or cache-gate-disabled) cache the
// legacy count-based boundary is used: mutation is free, so elide eagerly.
//
// With a HOT cache (reachable only at/above the deferral ceiling, where the
// gate overrides cache protection) the pass must buy hysteresis: elide
// oldest-first by token budget until the estimated usage drops to the
// elision target (hard−20), so one cache bust buys many rounds of headroom
// instead of re-busting every round as the count boundary advances with
// history growth (prefix-cache bust loop: the count boundary frees
// only the ~2 messages that crossed it per round while usage stays at the
// ceiling, so the bust repeats every round and never converges). The budget
// walk may extend past the count boundary into the preserve window but never
// touches the in-flight tail (the last two messages). When the elidable
// payload cannot meet the budget, escalate is set: nibbling again next round
// is strictly worse than one headroom-buying selective pass.
//
// The caller must hold a.mu (cache gate and history reads).
func (a *Agent) proactiveElisionBoundary(countBoundary int) (boundary int, escalate bool) {
	if a.cfg.ContextCompression.DisableCacheGate || a.cacheAssumedColdForProactive() {
		return countBoundary, false
	}
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return countBoundary, false
	}
	rt := a.cfg.ContextCompression.resolveThresholds()
	// Elision maintains the SOFT level: bring usage down to the soft ceiling (the
	// level soft maintenance is configured for), or the hard ceiling when no soft
	// level is set. No derived hard−N target.
	targetPct := rt.soft
	if targetPct <= 0 {
		targetPct = rt.effectiveHard()
	}
	need := a.computeContextStatsForMax(maxTokens).EstimatedTokens - maxTokens*targetPct/100
	if need <= 0 {
		return countBoundary, false
	}
	freed := 0
	boundary = 1
	tailCap := len(a.history) - 2
	for i := 1; i < tailCap && freed < need; i++ {
		freed += elisionReclaim(&a.history[i])
		boundary = i + 1
	}
	return boundary, freed < need
}

// elisionReclaim estimates the tokens elideToolMessages would free on msg:
// tool-call arguments collapse to the elision marker and tool results to the
// result placeholder. Messages elideToolMessages would not touch (and already
// elided ones) reclaim zero or less; the caller sums the raw deltas.
func elisionReclaim(msg *Message) int {
	reclaim := 0
	switch msg.Role {
	case Assistant:
		for j := range msg.ToolCalls {
			reclaim += estimateTokens(msg.ToolCalls[j].Arguments) - estimateTokens(elidedToolCallArguments)
		}
	case ToolRole:
		reclaim += estimateTokens(msg.Content) - estimateTokens(elidedToolResultContent)
	}
	return reclaim
}

func computeElisionBoundary(histLen, preserve int) int {
	boundary := histLen - preserve*3
	if boundary < 1 {
		boundary = 1
	}
	return boundary
}

// elideToolMessages replaces tool payloads before boundary with placeholders.
// It returns the number of messages mutated and the estimated tokens freed
// (sum of the per-message reclaim). The caller must hold a.mu.
func (a *Agent) elideToolMessages(boundary int) (changed, freed int) {
	for i := 1; i < boundary && i < len(a.history); i++ {
		msg := &a.history[i]
		reclaim := 0
		switch msg.Role {
		case Assistant:
			if len(msg.ToolCalls) > 0 {
				for j := range msg.ToolCalls {
					reclaim += estimateTokens(msg.ToolCalls[j].Arguments) - estimateTokens(elidedToolCallArguments)
					msg.ToolCalls[j].Arguments = elidedToolCallArguments
				}
			}
		case ToolRole:
			// Always replace the tool result body with a compact placeholder,
			// regardless of size, so tool_elision consistently frees tokens.
			reclaim += estimateTokens(msg.Content) - estimateTokens(elidedToolResultContent)
			msg.Content = elidedToolResultContent
		}
		if reclaim > 0 {
			changed++
			freed += reclaim
		}
	}
	return changed, freed
}

// compressSelective drops oldest messages, keeping system + recent turns.
// It returns the number of messages removed so the caller can emit one
// EventCompact after unlock. The caller must hold a.mu.
func (a *Agent) compressSelective() compactionResult {
	preserve := a.cfg.ContextCompression.PreserveRecentTurns
	if preserve == 0 {
		preserve = 2
	}

	var newHistory []Message
	if len(a.history) > 0 && a.history[0].Role == System {
		newHistory = append(newHistory, a.history[0])
	}

	boundary := findCompressionBoundary(a.history, preserve)
	newHistory = append(newHistory, a.history[boundary:]...)

	removed := len(a.history) - len(newHistory)
	a.history = newHistory
	if removed > 0 {
		a.cacheGeneration++
		// Messages were dropped: the recorded provider prompt is stale.
		a.invalidateContextUsageLocked()
	}

	if a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Applied selective compression: removed %d messages", removed)
	}
	return compactionResult{removed: removed}
}

// findCompressionBoundary finds the oldest message index to keep, ensuring
// tool call chains are never split.
func findCompressionBoundary(history []Message, preserve int) int {
	turnsKept := 0
	boundary := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == User {
			turnsKept++
			if turnsKept >= preserve {
				boundary = i
				break
			}
		}
	}

	// Ensure we don't split tool call chains.
	boundary = widenBoundaryForChains(history, boundary)
	return boundary
}

func widenBoundaryForChains(history []Message, boundary int) int {
	for boundary > 1 {
		prevIdx := boundary - 1
		prevRole := history[prevIdx].Role

		if prevRole == ToolRole {
			boundary--
			for boundary > 1 && history[boundary-1].Role == ToolRole {
				boundary--
			}
			if boundary > 1 && history[boundary-1].Role == Assistant {
				boundary--
			}
			continue
		}

		if prevRole == Assistant && len(history[prevIdx].ToolCalls) > 0 {
			boundary--
			continue
		}

		break
	}
	return boundary
}

// checkSilentOverflow detects providers that silently accept an oversized
// prompt and return a successful response instead of an error (e.g. z.ai,
// Xiaomi MiMo-style truncation).  When the estimated context usage exceeds
// the hard ceiling, it schedules compression for the next turn.
