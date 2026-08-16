// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
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

// enforceContextCeiling is the bounded last-resort fallback for the hard layer.
// After the hard-layer strategy (summarize by default) has run, if the estimated
// context still exceeds the hard ceiling it drops the oldest non-system messages
// until usage is back under the HARD ceiling (no derived magic target). This
// prevents an over-window request from going out when summarize failed or could
// not free enough. It is emitted with the explicit "hard fallback" label (NOT
// the confusing "ceiling") so the surface shows this is a fallback for a failed
// summarize, not a normal compression.
//
// The mutation runs under a.mu (enforceContextCeilingLocked); the structured
// EventCompact is emitted after unlock so every visible surface — the
// conversation bubble, the footer counter, and the session JSONL — records the
// cut.
func (a *Agent) enforceContextCeiling() {
	before, res, ok := a.enforceContextCeilingLocked()
	if !ok {
		return
	}
	a.emitCompactionResult("hard fallback", before, res, "summarize did not fit the window; dropped oldest messages to the hard ceiling")
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
	// whose tokens fit the HARD ceiling. Find the smallest cut k in [1, n] such
	// that tok[0] + sum(tok[k:]) <= historyCeiling. There is no lower magic
	// target: the fallback cuts to the configured hard ceiling (the cap the user
	// set), dropping the fewest oldest messages needed to fit.
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
		a.cfg.Logger.Log(Warn, "Hard-ceiling fallback: dropped oldest messages, freed ~%d tokens to fit under %d%% of window",
			droppedTokens, hardCeilingPercent)
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
