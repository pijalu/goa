// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

func (a *App) handleAgentOutputEvent(ev *agentic.OutputEvent) {
	if a.streamCapture != nil {
		a.streamCapture.record(ev)
	}
	switch ev.Type {
	case agentic.EventContent:
		a.handleStreamContent(ev)
	case agentic.EventToolResult:
		a.handleToolResult(ev)
	case agentic.EventEnd:
		a.handleSessionEnd(ev)
	case agentic.EventStateChange:
		a.handleStateChange(ev)
	case agentic.EventToolCall:
		a.handleToolCall(ev)
	case agentic.EventToolStart:
		a.handleToolStart(ev)
	case agentic.EventToolProgress:
		a.handleToolProgress(ev)
	case agentic.EventProgress:
		a.handleProgressEvent(ev)
	default:
		a.handleAgentStatsEvent(ev)
	}
}

// handleAgentStatsEvent routes the stats/lifecycle branch of
// handleAgentOutputEvent: token/context stats, compaction bookkeeping, and
// the clear/reset signals that re-arm or wipe session counters. Extracted so
// the event switch stays within the gocyclo budget as cases grow.
func (a *App) handleAgentStatsEvent(ev *agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventClear:
		a.clearStats()
		a.handleTokenStats(ev)
	case agentic.EventContextReset:
		a.resetCacheBustBaseline()
	case agentic.EventCompact:
		a.recordCompact(ev)
		a.showCompactionBubble(ev)
	default:
		a.handleTokenStats(ev)
	}
}

// compactionStrategy extracts the strategy label from an EventCompact: the
// structured Compaction payload wins, falling back to the free-text Text
// label for events emitted by paths that predate the payload.
func compactionStrategy(ev *agentic.OutputEvent) string {
	if ev.Compaction != nil && ev.Compaction.Strategy != "" {
		return ev.Compaction.Strategy
	}
	return ev.Text
}

// isMicroCompaction reports whether a compression strategy label counts
// toward the footer's micro bucket (the m in c:Xm-Y) rather than the
// full-compact bucket.
func isMicroCompaction(strategy string) bool {
	return strategy == string(agentic.CompressionMicro)
}

// recordCompact counts one completed compression pass and appends its
// per-round record to the session stats.
func (a *App) recordCompact(ev *agentic.OutputEvent) {
	strategy := compactionStrategy(ev)
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if isMicroCompaction(strategy) {
		a.microCompacts++
	} else {
		a.compacts++
	}
	a.compactions = append(a.compactions, compactionRoundFromEvent(ev, strategy))
}

// compactionRoundFromEvent builds the per-round session-stats record from an
// EventCompact. Structured fields come from the Compaction payload; the time
// is stamped now (the event carries no timestamp).
func compactionRoundFromEvent(ev *agentic.OutputEvent, strategy string) CompactionRound {
	r := CompactionRound{Strategy: strategy, At: time.Now()}
	if ev.Compaction != nil {
		r.BeforePct = ev.Compaction.BeforePct
		r.AfterPct = ev.Compaction.AfterPct
		r.FreedTokens = ev.Compaction.FreedTokens
		r.Removed = ev.Compaction.Removed
	}
	return r
}

// showCompactionBubble renders a dedicated conversation element for a
// completed compression pass so the user sees the drop instead of an
// unexplained context reset (context compressions are invisible).
// AddFlashMessage dedups a repeated same-strategy pass (a reactive ceiling
// enforcer firing several turns in a row) by updating the last bubble in
// place instead of stacking. It runs on the commandLoop via apply (the chat
// single-owner invariant), guarded for headless/tests.
func (a *App) showCompactionBubble(ev *agentic.OutputEvent) {
	if a.subs == nil || a.subs.chat == nil {
		return
	}
	a.subs.chat.AddFlashMessage(formatCompactionBubble(ev))
}

// formatCompactionBubble renders the one-line compaction bubble text. The ⚡
// prefix + "Context compacted (<strategy>):" shape is the flash-dedup key
// (flashKind), so repeated passes of the same strategy update in place.
func formatCompactionBubble(ev *agentic.OutputEvent) string {
	strategy := ev.Text
	var ci *agentic.CompactionInfo
	if ev.Compaction != nil {
		ci = ev.Compaction
		if ci.Strategy != "" {
			strategy = ci.Strategy
		}
	}
	if strategy == "" {
		strategy = "unknown"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⚡ Context compacted (%s)", strategy)
	if ci != nil {
		fmt.Fprintf(&b, ": %d%% → %d%%", ci.BeforePct, ci.AfterPct)
		if ci.Removed > 0 {
			fmt.Fprintf(&b, " · %d messages dropped", ci.Removed)
		}
		if ci.FreedTokens > 0 {
			fmt.Fprintf(&b, " · ~%d tokens freed", ci.FreedTokens)
		}
		if ci.Detail != "" {
			fmt.Fprintf(&b, "\n%s", ci.Detail)
		}
	}
	return b.String()
}

func (a *App) clearStats() {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.tokenPromptTotal = 0
	a.tokenPredictedTotal = 0
	a.tokenCacheReadTotal = 0
	a.tokenCacheWriteTotal = 0
	a.tokenCacheFullMisses = 0
	a.tokenCachePartialMisses = 0
	a.tokenCacheMissedTokens = 0
	a.cacheReadEstablished = false
	a.lastTurnPromptN = 0
	a.lastTurnPredictedN = 0
	a.lastTurnCacheRead = 0
	a.lastTurnCacheWrite = 0
	a.tokenSessionMax = 0
	a.tokenSessionEstimate = 0
	a.tokenSessionProjected = 0
	a.lastTurnSpeed = 0
	a.turnCount = 0
	a.turnStatsSeen = false
	a.microCompacts = 0
	a.compacts = 0
	a.compactions = nil
	a.toolCallsTotal = 0
	a.toolCallWarningLevel = ToolCallNormal
	a.lastCacheHit = CacheHitTrend{}
	a.cacheHitGlobalLevel = 0
	a.cacheHitGlobalWeight = 0
}

// resetCacheBustBaseline re-arms the cache-bust detector after an in-place
// context reset (EventContextReset — a fresh-context goal begin): subsequent
// token stats belong to a NEW conversation on a fresh provider cache key,
// whose cold start (zero or tiny cache reads) is not a bust. Unlike
// clearStats (user /clear), session totals (CH/CW) and the CM counter itself
// survive — only the per-conversation detector baseline resets.
