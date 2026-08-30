// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"time"

	"github.com/pijalu/goa/internal/metrics"
	"github.com/pijalu/goa/tui"
)

// streamState tracks the current streaming context for LLM output.
// Decoupled from content type so thinking segments break correctly on
// any non-thinking event (tool call, tool result, content, idle, end).
type streamState struct {
	kind     tui.ConsoleItemType
	text     strings.Builder
	isActive bool
}

func (s *streamState) begin(kind tui.ConsoleItemType) {
	s.kind = kind
	s.text.Reset()
	s.isActive = true
}

func (s *streamState) end() {
	s.isActive = false
	s.text.Reset()
}

func (s *streamState) is(kind tui.ConsoleItemType) bool {
	return s.isActive && s.kind == kind
}

func (s *streamState) active() bool {
	return s.isActive
}

// ToolCallLevel indicates the severity of tool call loop detection for
// color-coding the TC:N display in the footer.
type ToolCallLevel int

const (
	ToolCallNormal  ToolCallLevel = 0 // green — all good
	ToolCallWarning ToolCallLevel = 1 // orange — duplicate/repeat detected
	ToolCallStopped ToolCallLevel = 2 // red — budget exceeded, force-stopped
)

// CacheHitTrend bundles the cache-hit rates rendered by the footer's CH
// segment: the most recent completion's rate plus the token-weighted
// session-wide level. Seen gates display (no cache activity yet); HasPrev
// gates delta coloring (first observation has no baseline and renders as
// stable). The same gating applies independently to the global pair
// (GlobalHasPrev).
type CacheHitTrend struct {
	Pct     float64 // last completion's cache-hit rate
	PrevPct float64 // previous value (for delta coloring)
	Seen    bool    // at least one cache-active round observed
	HasPrev bool    // at least two observations (delta coloring armed)
	// Token-weighted session-wide cache-hit level (the report's running
	// formula: newLevel = (level·W + rate·w)/(W+w), W += w with w = the
	// round's cached token volume). This is the 1st value of the CH segment;
	// Pct stays as the 2nd (most recent round).
	GlobalPct     float64
	GlobalPrevPct float64
	GlobalHasPrev bool
}

// observe folds one new per-completion cache-hit rate into the trend: the
// current value becomes the previous baseline and pct becomes current.
func (t *CacheHitTrend) observe(pct float64) {
	t.PrevPct, t.HasPrev = t.Pct, t.Seen
	t.Pct, t.Seen = pct, true
}

// foldGlobal records one folded cache-active round into the token-weighted
// session level. rate is the new level; prevLevel/prevHad carry the previous
// baseline so the first observation renders as stable green. The weighted
// arithmetic itself lives in App.applyTokenTimingsLocked (it owns the running
// weight total).
func (t *CacheHitTrend) foldGlobal(rate, prevLevel float64, prevHad bool) {
	t.GlobalPct = rate
	t.GlobalPrevPct = prevLevel
	t.GlobalHasPrev = prevHad
}

// cacheHitTrendFromTotals builds a display-only trend from aggregate token
// counters, for construction sites that have no evolving baseline
// (orchestrator role rows, headless stats): Seen gates display, HasPrev
// stays false so the value renders as stable green.
func cacheHitTrendFromTotals(read, write, prompt int) CacheHitTrend {
	if read == 0 && write == 0 {
		return CacheHitTrend{}
	}
	pct := metrics.CacheHitPct(read, write, prompt)
	// Aggregated totals are already token-weighted, so the same figure feeds
	// both the global slot and the last-completion slot; there is no evolving
	// baseline (HasPrev stays false → stable rendering).
	return CacheHitTrend{Pct: pct, Seen: true, GlobalPct: pct}
}

// sessionStats holds cumulative + last-turn statistics for footer display.
type sessionStats struct {
	PromptN         int
	PredictedN      int
	CacheReadTotal  int
	CacheWriteTotal int
	// Cache-miss counters, split by failure mode (CM footer part and the
	// persisted session summary): unexpected = zero cache-read while the
	// prefix was still valid (entire prefix recomputed — TTL expiry,
	// provider eviction, micro-compaction busts), partial = cache-read
	// drop beyond tolerance (a suffix recomputed). Intentional resets
	// (fresh-context goal, a summarize pass) never count — every other
	// compaction is a cost whose misses still count — bugs.md 2026-08-30
	// renamed the old "full" category to "unexpected".
	// CacheMissedTokens is the exact token damage summed over the counted
	// misses (missed = prevCacheRead for unexpected,
	// prevCacheRead-cacheRead for partial).
	CacheMissesUnexpected int     `json:"cm_unexpected,omitempty"`
	CacheMissesPartial    int     `json:"cm_partial,omitempty"`
	CacheMissedTokens     int64   `json:"cm_tokens,omitempty"`
	SpeedTokPerSec        float64 // last turn output tok/s
	ContextEstimate       int
	ContextProjected      int
	ContextMax            int
	CostUSD               float64
	ShowCost              bool
	ToolCalls             int
	ToolCallLevel         ToolCallLevel // 0=normal, 1=warning, 2=stopped
	MicroCompacts         int
	Compacts              int
	// LastCacheHit is the most recent completion's cache-hit trend —
	// rendered as CH:<avg>%▸<last>% where <avg> is the rolling average
	// of the last 10 observations and <last> is the most recent rate.
	// Each element is colored independently by its own evolution.
	LastCacheHit CacheHitTrend
	// Compactions documents each completed compression round (strategy,
	// before/after %, freed tokens, removed messages, time). The aggregate
	// MicroCompacts/Compacts counters above feed the footer; this per-round
	// record makes the session stats self-documenting ("context
	// compressions are invisible").
	Compactions []CompactionRound
}

// CompactionRound documents one completed compression pass in the session.
type CompactionRound struct {
	Strategy    string    `json:"strategy"` // elision|selective|micro|summarize|hybrid|ceiling|overflow|truncation
	BeforePct   int       `json:"before_pct"`
	AfterPct    int       `json:"after_pct"`
	FreedTokens int       `json:"freed_tokens,omitempty"`
	Removed     int       `json:"removed,omitempty"`
	At          time.Time `json:"at"` // when the round completed
}
