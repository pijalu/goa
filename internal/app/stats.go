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

// cacheHitWindowSize is the number of recent cache-hit rates kept for the
// rolling average shown in the footer CH:<avg>% segment.
const cacheHitWindowSize = 10

// CacheHitTrend bundles a cache-hit rate with its previous value so the
// footer can color it by evolution: bold green when growing, green when
// stable/slightly growing, orange on a slight drop (< 5 points), red on a
// drop (>= 5 points). Seen gates display (no cache activity yet); HasPrev
// gates delta coloring (first observation has no baseline and renders as
// stable).
//
// The trend also maintains a rolling window of the last cacheHitWindowSize
// rates for the average (CH:<avg>%) — the avg and last values are colored
// independently, each only shifting to orange/red on a >= 5-point change.
type CacheHitTrend struct {
	Pct     float64   // last completion's cache-hit rate
	PrevPct float64   // previous value (for delta coloring)
	Seen    bool      // at least one cache-active round observed
	HasPrev bool      // at least two observations (delta coloring armed)
	window  []float64 // rolling window of recent rates (max cacheHitWindowSize)
}

// observe folds one new cache-hit rate into the trend: the current value
// becomes the previous baseline and pct becomes current. The rate is also
// appended to the rolling window (capped at cacheHitWindowSize).
func (t *CacheHitTrend) observe(pct float64) {
	t.PrevPct, t.HasPrev = t.Pct, t.Seen
	t.Pct, t.Seen = pct, true
	t.window = append(t.window, pct)
	if len(t.window) > cacheHitWindowSize {
		t.window = t.window[len(t.window)-cacheHitWindowSize:]
	}
}

// AvgPct returns the rolling average of the last cacheHitWindowSize
// cache-hit rates. Returns 0 when no observations exist.
func (t *CacheHitTrend) AvgPct() float64 {
	if len(t.window) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range t.window {
		sum += v
	}
	return sum / float64(len(t.window))
}

// AvgPrevPct returns the average of the window *before* the most recent
// observation — the baseline for delta coloring the average. Returns 0 when
// fewer than 2 observations exist.
func (t *CacheHitTrend) AvgPrevPct() float64 {
	if len(t.window) < 2 {
		return 0
	}
	// Compute avg of window[0:len-1] (exclude the latest).
	prev := t.window[:len(t.window)-1]
	sum := 0.0
	for _, v := range prev {
		sum += v
	}
	return sum / float64(len(prev))
}

// cacheHitTrendFromTotals builds a display-only trend from aggregate token
// counters, for construction sites that have no evolving baseline
// (orchestrator role rows, headless stats): Seen gates display, HasPrev
// stays false so the value renders as stable green.
func cacheHitTrendFromTotals(read, write, prompt int) CacheHitTrend {
	if read == 0 && write == 0 {
		return CacheHitTrend{}
	}
	return CacheHitTrend{Pct: metrics.CacheHitPct(read, write, prompt), Seen: true}
}

// sessionStats holds cumulative + last-turn statistics for footer display.
type sessionStats struct {
	PromptN         int
	PredictedN      int
	CacheReadTotal  int
	CacheWriteTotal int
	// Cache-miss counters, split by failure mode (CM footer part and the
	// persisted session summary): full = zero cache-read after establishment
	// (entire prefix recomputed), partial = cache-read drop beyond tolerance
	// (a suffix recomputed). CacheMissedTokens is the exact token damage
	// summed over the counted misses (missed = prevCacheRead for full,
	// prevCacheRead-cacheRead for partial).
	CacheMissesFull    int     `json:"cm_full,omitempty"`
	CacheMissesPartial int     `json:"cm_partial,omitempty"`
	CacheMissedTokens  int64   `json:"cm_tokens,omitempty"`
	SpeedTokPerSec     float64 // last turn output tok/s
	ContextEstimate    int
	ContextProjected   int
	ContextMax         int
	CostUSD            float64
	ShowCost           bool
	ToolCalls          int
	ToolCallLevel      ToolCallLevel // 0=normal, 1=warning, 2=stopped
	MicroCompacts      int
	Compacts           int
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
