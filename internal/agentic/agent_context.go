// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"time"
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

// compressionCause describes why the hard-layer compression left the context
// over the ceiling, so the last-resort fallback can surface the TRUE reason
// instead of the one-size "summarize did not fit the window" label (which was
// also emitted when summarize had been canceled or had failed for any other
// reason).
type compressionCause string

const (
	// causeSummarizeRan: the hard-layer compression ran to completion (nil
	// error) yet usage is still over the ceiling — the landed summary
	// overshot the window.
	causeSummarizeRan compressionCause = ""
	// causeSummarizeOverflow: summarize failed with a context-length error —
	// its request did not fit the window even after the micro shrink retry.
	causeSummarizeOverflow compressionCause = "overflow"
	// causeSummarizeFailed: summarize failed for a non-fit reason (provider
	// error, cancellation propagated by a caller that ignored the dead-turn
	// guard, ...).
	causeSummarizeFailed compressionCause = "failed"
)

// compressionCauseFromErr maps a maybeCompress error to the fallback cause.
func compressionCauseFromErr(err error) compressionCause {
	if err == nil {
		return causeSummarizeRan
	}
	if isContextLengthError(err) {
		return causeSummarizeOverflow
	}
	return causeSummarizeFailed
}

// enforceContextCeiling is the bounded last-resort safety net for the hard
// layer (zero-cause convenience wrapper for tests and direct callers).
//
// Per the bug-2 contract this should essentially NEVER fire: the hard-layer
// summarize is engineered to always fit the window (it shrinks its own input
// until the request fits, and lands a summary sized to the ceiling). The
// enforcer remains only as a defensive net for the pathological case where
// the fixed cost (system prompt + tool schemas) alone approaches the window
// — and in that case it explains WHY the hard ceiling could not be met
// instead of silently dropping messages behind a "did not fit" label.
func (a *Agent) enforceContextCeiling() {
	a.enforceContextCeilingWithCause(causeSummarizeOverflow)
}

// enforceContextCeilingUnlessCanceled skips the destructive fallback entirely
// when the turn context is already done: the cut exists to let the NEXT
// request go out, and a canceled/deadline turn sends none — dropping messages
// there is silent data loss.
func (a *Agent) enforceContextCeilingUnlessCanceled(ctx context.Context, cause compressionCause) {
	if ctx != nil && ctx.Err() != nil {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Context ceiling fallback skipped: turn context is done (%v)", ctx.Err())
		}
		return
	}
	a.enforceContextCeilingWithCause(cause)
}

// enforceContextCeilingWithCause runs the hard-ceiling safety net and emits
// the structured EventCompact with a detail naming the real cause and, when
// the configured hard ceiling is unreachable (fixed cost alone exceeds it),
// an explicit explanation rather than a bare drop. Per the escalation
// contract the model-free passes run FIRST (tool-result pruning + micro
// truncation): only when the window still does not fit are the oldest
// messages dropped to the hard ceiling.
func (a *Agent) enforceContextCeilingWithCause(cause compressionCause) {
	before, res, ok := a.enforceContextCeilingLocked()
	if !ok {
		return
	}
	a.emitCompactionResult("hard fallback", before, res, a.ceilingFallbackDetail(cause, res))
}

// ceilingFallbackDetail builds the user-visible detail for the hard-fallback
// event from the failure cause and the work record (so a model-free-only pass
// is not described as a message drop). When the hard ceiling is fundamentally
// unreachable — the fixed per-turn cost (system prompt + tool schemas) alone
// meets or exceeds it — the message says so explicitly and advises lowering
// the ceiling or the fixed footprint, which is the "explained/avoided"
// requirement for a 95% target that cannot be achieved.
func (a *Agent) ceilingFallbackDetail(cause compressionCause, res compactionResult) string {
	action := "shrank tool payloads to the hard ceiling"
	if res.removed > 0 {
		action = "dropped oldest messages to the hard ceiling"
	}
	var reason string
	switch cause {
	case causeSummarizeRan:
		reason = "summary still exceeds the window"
	case causeSummarizeFailed:
		reason = "summarize could not complete"
	default:
		reason = "summarize did not fit the window"
	}
	detail := reason + "; " + action
	if a.fixedCostExceedsCeiling() {
		detail += fmt.Sprintf(
			" (warning: system prompt + tool schemas alone use ~%d tokens, meeting/exceeding the hard ceiling — lower the ceiling or reduce the fixed footprint)",
			a.fixedCostTokens())
	}
	return detail
}

// fixedCostExceedsCeiling reports whether the fixed per-turn cost (system
// prompt + tool schemas) alone meets or exceeds the hard ceiling — the one
// condition under which NO history cut can bring usage under it, which must
// be surfaced as an explanation rather than a silent drop loop.
func (a *Agent) fixedCostExceedsCeiling() bool {
	maxTokens := a.effectiveMaxTokens()
	if maxTokens <= 0 {
		return false
	}
	hard := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	return a.fixedCostTokens() >= maxTokens*hard/100
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

	// Use effectiveHard: the ceiling enforcer is the reactive fallback that
	// stays on even when the proactive hard layer is disabled (hard=0).
	hardCeilingPercent := a.cfg.ContextCompression.resolveThresholds().effectiveHard()
	hardCeiling := maxTokens * hardCeilingPercent / 100
	// The fixed per-turn cost (system prompt + tool schemas) is always present;
	// history must fit in the remainder or the outgoing request still overflows.
	// The cut target IS the hard ceiling — no derived hard−N magic level: the
	// user configured hard as the cap, so the fallback brings usage under it.
	historyCeiling := hardCeiling - a.fixedCostTokens()
	if historyCeiling < 0 {
		historyCeiling = 0
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

	// Core bug-2 fix: the fallback must not fire while TOTAL usage is under
	// the configured hard ceiling. The history-only cut target
	// (historyCeiling = hard − fixedCost) is stricter than the hard-tier
	// trigger (total usage ≥ hard), so without this gate the fallback dropped
	// messages at ~94% — BEFORE summarize ever ran (summarize fires at ≥95%)
	// — and falsely reported "summarize did not fit the window". When total
	// usage is under the hard ceiling, compression belongs to the proactive
	// summarize, not to this destructive net; skip and let the next turn's
	// hard tier summarize.
	totalUsage := a.estimateContextTokensLocked()
	if totalUsage <= hardCeiling {
		if a.cfg.Logger != nil {
			a.cfg.Logger.Log(Info, "Hard-ceiling fallback suppressed: total usage %d ≤ hard ceiling %d (history-only occupancy is over only because of fixed cost); summarize owns this range", totalUsage, hardCeiling)
		}
		return before, res, false
	}

	// Escalation order (per the bug-2 contract): the destructive message drop
	// is the TRUE last resort. First run the model-free shrink passes —
	// stale/over-budget tool-result pruning and micro truncation — which
	// reclaim the bulk of a dump-heavy session WITHOUT dropping any message.
	// Only when those leave the window still over the ceiling do we drop.
	// (A summarize that could not fit used to jump straight to the drop even
	// when elidable tool payload would have made the window fit.)
	if shrinkRes, fits := a.shrinkToolPayloadsToFitLocked(historyCeiling); fits {
		return before, shrinkRes, shrinkRes.didWork()
	} else {
		res.changed = shrinkRes.changed
	}

	// Compute each message's token cost once. The previous implementation
	// removed the oldest non-system message one at a time, re-estimating the
	// whole history (O(n)) and shifting the slice (O(n)) per iteration, making
	// the last-resort safety net O(n^2) on long sessions exactly when it runs.
	hist = a.history
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
		// The shrink above (or a concurrent reader's view) already fits.
		return before, res, res.didWork()
	}

	cut, droppedTokens := a.dropOldestToCeilingLocked(hist, tok, total, historyCeiling, hardCeiling)
	res.removed = cut - 1
	res.freedTokens = droppedTokens
	return before, res, true
}

// shrinkToolPayloadsToFitLocked runs the model-free shrink passes (tool-result
// pruning + micro truncation) and reports whether history now fits the ceiling
// without dropping any message. The caller must hold a.mu. When it returns
// fits=false the caller proceeds to the destructive drop; the shrink work is
// still reported via the returned compactionResult.
func (a *Agent) shrinkToolPayloadsToFitLocked(historyCeiling int) (res compactionResult, fits bool) {
	changed := a.pruneToolResultsLocked(a.cfg.ContextCompression.ToolResultPruning.resolve())
	microRes := a.applyMicroLocked(microFallbackConfig(a.cfg.ContextCompression.MicroCompaction))
	changed += microRes.changed
	res.changed = changed
	if changed == 0 {
		return res, a.historyTokensLocked() <= historyCeiling
	}
	fits = a.historyTokensLocked() <= historyCeiling
	if fits && a.cfg.Logger != nil {
		a.cfg.Logger.Log(Info, "Hard-ceiling fallback resolved by tool-payload shrink (%d message(s)); no messages dropped", changed)
	}
	return res, fits
}

// historyTokensLocked sums the estimated tokens of the current history.
// The caller must hold a.mu.
func (a *Agent) historyTokensLocked() int {
	total := 0
	for i := range a.history {
		total += messageTokenCount(&a.history[i])
	}
	return total
}

// dropOldestToCeilingLocked drops the oldest non-system messages until the
// kept tail fits historyCeiling, advancing past orphaned tool results. It
// returns the cut index and freed tokens, mutating a.history. The caller must
// hold a.mu. This is the destructive last resort, reached only when the
// model-free shrink passes could not fit the window.
func (a *Agent) dropOldestToCeilingLocked(hist []Message, tok []int, total, historyCeiling, hardCeiling int) (cut, droppedTokens int) {
	// Keep the system prompt (index 0) plus the most-recent contiguous tail
	// whose tokens fit the HARD ceiling. Find the smallest cut k in [1, n] such
	// that tok[0] + sum(tok[k:]) <= historyCeiling. There is no lower magic
	// target: the fallback cuts to the configured hard ceiling (the cap the user
	// set), dropping the fewest oldest messages needed to fit.
	system := tok[0]
	nonSystem := total - system // sum(tok[1:])
	cut = len(hist)             // fall-back: keep only the system prompt
	for k := 1; k < len(hist); k++ {
		keptHere := system + (nonSystem - droppedTokens) // tok[0] + sum(tok[k:])
		if keptHere <= historyCeiling {
			cut = k
			break
		}
		droppedTokens += tok[k]
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

	if a.cfg.Logger != nil {
		for _, m := range hist[1:cut] {
			a.cfg.Logger.Log(Warn, "Context ceiling enforced: dropped %s message (len=%d)", m.Role, len(m.Content))
		}
		if droppedTokens > 0 {
			a.cfg.Logger.Log(Warn, "Hard-ceiling fallback: dropped oldest messages, freed ~%d tokens to fit under ceiling", droppedTokens)
		}
	}

	kept := append(hist[:1:1], hist[cut:]...)
	a.history = kept
	// History shrank: the recorded provider prompt no longer corresponds.
	a.invalidateContextUsageLocked()
	if cut > 1 {
		// Oldest messages were dropped — the prefix a provider may have cached
		// under this conversation's key no longer matches (Hard Rule 7: only a
		// byte-exact append may keep the cache identity). Rotate like every other
		// drop-oldest path (compressSelective, wholesale replacement).
		a.cacheGeneration++
	}

	if messageTokenCount(&hist[0])+(total-system-droppedTokens) > historyCeiling {
		a.cfg.Logger.Log(Error, "Context ceiling cannot be enforced: even minimal history + fixed cost exceeds %d tokens", hardCeiling)
	}
	return cut, droppedTokens
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
	projected := a.projectedContextTokensLocked()
	usagePercent := 0
	if maxTokens > 0 {
		usagePercent = projected * 100 / maxTokens
	}

	return ContextStats{
		Messages:        len(a.history),
		Characters:      chars,
		EstimatedTokens: estimated,
		ProjectedTokens: projected,
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

// timeContextMetaKey marks an injected time-context reading in Message
// metadata (value: the sampled RFC3339Nano wall time). The marker is never
// sent to the LLM; it exists so the latest-injection scan can find readings
// in the durable conversation history.
const timeContextMetaKey = "goa.time-context"

// timeContextZone returns the display zone for temporal-context readings:
// the configured IANA zone when set, otherwise the local zone. An
// unsupported configured zone (already rejected by config validation) falls
// back to local.
func (a *Agent) timeContextZone() *time.Location {
	if a.cfg.TimeContext.TimeZone != "" {
		if loc, err := time.LoadLocation(a.cfg.TimeContext.TimeZone); err == nil {
			return loc
		}
	}
	return time.Local
}

// formatTimeContextDuration renders a non-negative elapsed duration in
// compact whole-second units (dsh time-context parity): 0s, 5s, 1m 5s,
// 1h 2m 3s, 1d 2h 3m 4s. Negative durations (backward wall-clock movement)
// clamp to 0s.
func formatTimeContextDuration(elapsed time.Duration) string {
	seconds := int64(elapsed / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	seconds %= 60
	var b strings.Builder
	if days > 0 {
		fmt.Fprintf(&b, "%dd ", days)
	}
	if hours > 0 {
		fmt.Fprintf(&b, "%dh ", hours)
	}
	if minutes > 0 {
		fmt.Fprintf(&b, "%dm ", minutes)
	}
	fmt.Fprintf(&b, "%ds", seconds)
	return b.String()
}

// renderTimeContextMessage builds the durable temporal-context text for one
// step entry, adapted from the dsh time-context README shape (three lines:
// sampled timestamp with numeric offset and IANA zone, the display zone, and
// the elapsed baseline). Goa has no browser provenance, so the zone is the
// configured or local display zone rather than a request-scoped browser zone.
func renderTimeContextMessage(now time.Time, zone *time.Location, turn, step int, elapsed *time.Duration) string {
	elapsedText := "unavailable"
	if elapsed != nil {
		elapsedText = formatTimeContextDuration(*elapsed)
	}
	baseline := "model-visible message"
	if step > 1 {
		baseline = "step context"
	}
	ts := now.In(zone).Format("2006-01-02T15:04:05-07:00") + "[" + zoneName(zone) + "]"
	return fmt.Sprintf(
		"Time sampled while preparing turn %d, step %d: %s\n"+
			"Time zone for this request: %s. Interpret otherwise-unqualified dates and times in this zone.\n"+
			"Elapsed since the preceding %s: %s.",
		turn, step, ts, zoneName(zone), baseline, elapsedText,
	)
}

// zoneName returns the IANA-style name of a location for the temporal-context
// message. LoadLocation results carry their canonical name; the local zone
// falls back to its OS name or "Local".
func zoneName(loc *time.Location) string {
	if loc == nil {
		return "Local"
	}
	return loc.String()
}

// latestTimeContextReadingLocked scans the durable conversation history in
// reverse for the most recent injected time-context reading and returns its
// sampled wall time. It scans history (never a process-local cache) so the
// interval-suppression state re-derives from whatever history currently
// holds, which is what makes it correct across history rewrites such as
// compaction: a shadowed reading is no longer findable, so the next eligible
// step injects a fresh one. The caller must hold a.mu.
func (a *Agent) latestTimeContextReadingLocked() (time.Time, bool) {
	for i := len(a.history) - 1; i >= 0; i-- {
		if v, ok := a.history[i].Metadata[timeContextMetaKey]; ok {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// latestStepContextReadingLocked scans the durable conversation history in
// reverse for the most recent time-context reading within the current turn
// (messages at or after the turn's first appended message). It stops at the
// turn boundary so a later step measures elapsed from the preceding step
// context in the SAME turn, not from an earlier turn's reading. The caller
// must hold a.mu.
func (a *Agent) latestStepContextReadingLocked() (time.Time, bool) {
	start := a.turnStartHistoryLen
	if start < 0 {
		start = 0
	}
	for i := len(a.history) - 1; i >= start; i-- {
		if v, ok := a.history[i].Metadata[timeContextMetaKey]; ok {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// injectTimeContextIfDue runs at every model step entry (round 0 via
// prepareTurn, later rounds, recovery rounds) and, when the temporal-context
// feature is enabled, appends one durable time-context reading to history
// when due per the refresh interval: no earlier reading, wall clock moved
// backward, or at least RefreshInterval elapsed since the latest reading.
// Zero/negative interval injects at every eligible entry. It returns true
// when a reading was injected. The reading is a User-role message (the model
// sees it as ordinary context) carrying the injection marker in metadata; it
// stays in history until compaction shadows it.
func (a *Agent) injectTimeContextIfDue(now time.Time) bool {
	cfg := a.cfg.TimeContext
	if !cfg.Enabled {
		return false
	}
	a.turnStep++
	step := a.turnStep

	zone := a.timeContextZone()

	a.mu.Lock()
	last, ok := a.latestTimeContextReadingLocked()
	due := cfg.RefreshInterval <= 0 || !ok || now.Before(last) || now.Sub(last) >= cfg.RefreshInterval
	if !due {
		a.mu.Unlock()
		return false
	}

	var elapsed *time.Duration
	if step == 1 {
		if t, found := a.latestTimeContextReadingLocked(); found {
			d := now.Sub(t)
			elapsed = &d
		}
	} else {
		if t, found := a.latestStepContextReadingLocked(); found {
			d := now.Sub(t)
			elapsed = &d
		}
	}

	msg := Message{
		Type:     Content,
		Role:     User,
		Content:  renderTimeContextMessage(now, zone, a.turnCounter, step, elapsed),
		Metadata: map[string]string{timeContextMetaKey: now.Format(time.RFC3339Nano)},
	}
	a.history = append(a.history, msg)
	a.mu.Unlock()

	// Emit outside a.mu (persistGoalReminder parity): the reading lands in
	// the observer pipeline (session JSONL, TUI) like every other message.
	a.emitMessage(msg)
	return true
}
