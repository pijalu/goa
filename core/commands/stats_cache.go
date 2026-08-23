// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/metrics"
)

// This file implements the /stats:cache view: an evolution bar chart of the
// per-completion cache hit rate plus a table of cache drops (before/after
// rates), derived from the current session's turn history (all agent calls
// in this session) — not from the persistent cross-session usage store.
//
// Rates use metrics.CacheHitPct — the same formula as the footer CH% stat —
// so the chart, the drop table, and the live footer always agree.

// cacheDropThresholdPts is the minimum rate fall (percentage points) between
// two consecutive completions of the same model that counts as a cache drop.
// It matches the footer's red "drop" threshold (cacheHitDropDelta in
// internal/app/stats.go).
const cacheDropThresholdPts = 5.0

// cacheTurn is one completion's cache-token counts: the session-scoped
// counterpart of a usage.Record row for cache-rate purposes, tagged with the
// producing agent and active goal so the view can section per agent/goal.
type cacheTurn struct {
	Num        int // turn number (1-based)
	CacheRead  int
	CacheWrite int
	PromptN    int
	AgentRole  string // ""/"main" for the primary agent, else the multiagent role
	GoalID     string // active goal at turn time ("" = none)
}

const cacheMissDropTolerance = 1024

type cacheMissTurn struct {
	num, full, partial int
	missed             int
	prev               int // cache-read of the previous turn (the prefix the miss is measured against)
}

// cacheDrop is one detected cache-rate fall between consecutive completions.
type cacheDrop struct {
	Turn   int // turn number where the drop occurred
	Before float64
	After  float64
}

// cacheTurnsFromHistory flattens the session turn history (plus the optional
// in-progress turn) into a chronological cacheTurn series for the cache
// evolution chart and drop detection.
func cacheTurnsFromHistory(history []core.TurnRecord, current *core.TurnRecord) []cacheTurn {
	turns := history
	if current != nil {
		turns = append(append([]core.TurnRecord{}, history...), *current)
	}
	out := make([]cacheTurn, 0, len(turns))
	for _, t := range turns {
		out = append(out, cacheTurn{
			Num:        t.Number,
			CacheRead:  t.TokenUsage.CacheRead,
			CacheWrite: t.TokenUsage.CacheWrite,
			PromptN:    t.TokenUsage.PromptN,
			AgentRole:  t.AgentRole,
			GoalID:     t.GoalID,
		})
	}
	return out
}

// cacheTurnRate computes the per-completion cache hit rate for one turn
// using the shared formula (Anthropic- and OpenAI-style branches).
func cacheTurnRate(t cacheTurn) float64 {
	return metrics.CacheHitPct(t.CacheRead, t.CacheWrite, t.PromptN)
}

// detectCacheDrops finds falls of >= thresholdPts percentage points between
// consecutive cache-active turns. A cache-active turn is one with any prompt
// tokens (it called the LLM); turns with zero prompt tokens are skipped
// (no LLM call). A drop to 0% (TTL expiry, prefix invalidation) is the
// classic bust signature and is caught here.
func detectCacheDrops(turns []cacheTurn, thresholdPts float64) []cacheDrop {
	var active []cacheTurn
	for _, t := range turns {
		if t.PromptN > 0 || t.CacheRead > 0 || t.CacheWrite > 0 {
			active = append(active, t)
		}
	}
	var drops []cacheDrop
	for i := 1; i < len(active); i++ {
		rate := cacheTurnRate(active[i])
		prev := cacheTurnRate(active[i-1])
		if prev-rate >= thresholdPts {
			drops = append(drops, cacheDrop{Turn: active[i].Num, Before: prev, After: rate})
		}
	}
	return drops
}

// cacheChartBars caps the last-completions table at the latest N entries.
const cacheChartBars = 10

// cacheLevelColor maps a cache-hit percentage onto the band colors:
// red <90%, orange <95%, green ≥95%. Used by the weighted session total
// line so all sections agree.
func cacheLevelColor(pct float64) string {
	const (
		red    = "#f85149"
		orange = "#d29922"
		green  = "#3fb950"
	)
	switch {
	case pct >= 95:
		return ansi.Fg(green)
	case pct >= 90:
		return ansi.Fg(orange)
	default:
		return ansi.Fg(red)
	}
}

// cacheGroup is one agent/goal section of the cache view.
type cacheGroup struct {
	key   string // display label ("main", "companion", …)
	turns []cacheTurn
}

// groupCacheTurns partitions the turn series by (AgentRole, GoalID) in
// first-appearance order. Solo sessions collapse to a single unlabeled group
// so the output keeps today's header-less look.
func groupCacheTurns(turns []cacheTurn) []cacheGroup {
	var groups []cacheGroup
	index := map[string]int{}
	for _, t := range turns {
		key := t.AgentRole
		if key == "" {
			key = "main"
		}
		if t.GoalID != "" {
			key += " · goal:" + t.GoalID
		}
		i, ok := index[key]
		if !ok {
			groups = append(groups, cacheGroup{key: key})
			i = len(groups) - 1
			index[key] = i
		}
		groups[i].turns = append(groups[i].turns, t)
	}
	return groups
}

// writeCacheView renders the /stats:cache output: per agent/goal group, the
// last-10 cache-hit chart, the per-turn average bars, the weighted session
// total, and the cache-miss list. A single group (the common solo session)
// renders without a section header.
func writeCacheView(b *strings.Builder, turns []cacheTurn) {
	groups := groupCacheTurns(turns)
	multi := len(groups) > 1
	for _, g := range groups {
		if multi {
			fmt.Fprintf(b, "## %s\n", g.key)
		}
		writeCacheGroupSections(b, g.turns)
	}
}

// writeCacheGroupSections renders the four required sections for one group.
func writeCacheGroupSections(b *strings.Builder, turns []cacheTurn) {
	writeCacheHitLast10(b, turns)
	writeCacheAvgPerTurn(b, turns)
	writeCacheSessionTotal(b, turns)
	writeCacheMissList(b, turns)
	writeCacheDrops(b, detectCacheDrops(turns, cacheDropThresholdPts))
}

func cacheMisses(turns []cacheTurn) []cacheMissTurn {
	out := make([]cacheMissTurn, 0, len(turns))
	prev, established := 0, false
	for _, t := range turns {
		m := cacheMissTurn{num: t.Num, prev: prev}
		if t.CacheRead > 0 {
			established = true
		}
		switch {
		case established && t.CacheRead == 0 && prev > 0:
			m.full, m.missed = 1, prev
		case prev > 0 && t.CacheRead+cacheMissDropTolerance < prev:
			m.partial, m.missed = 1, prev-t.CacheRead
		}
		out = append(out, m)
		prev = t.CacheRead
	}
	return out
}

// writeCacheHitLast10 renders section 1: an MD table of the last ≤10
// cache-active completions (oldest first, newest last), one row per turn
// with its cache-hit rate. Rendered on screen through the MD pipeline.
func writeCacheHitLast10(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("# Cache hit — last completions\n")
	active := cacheActiveTurns(turns)
	if len(active) == 0 {
		b.WriteString("No cache activity recorded yet.\n")
		return
	}
	if len(active) > cacheChartBars {
		active = active[len(active)-cacheChartBars:]
	}
	b.WriteString("| Turn | CH % |\n|---|---|\n")
	for _, t := range active {
		fmt.Fprintf(b, "| T%d | %.1f%% |\n", t.Num, cacheTurnRate(t))
	}
}

// cacheActiveTurns filters to turns with any cache activity (read or write).
func cacheActiveTurns(turns []cacheTurn) []cacheTurn {
	out := make([]cacheTurn, 0, len(turns))
	for _, t := range turns {
		if t.CacheRead > 0 || t.CacheWrite > 0 {
			out = append(out, t)
		}
	}
	return out
}

// cacheTurnsOnActivity filters to turns that actually called the LLM
// (any prompt-side volume). Unlike cacheActiveTurns this KEEPS bust rounds
// (zero reads after establishment): their 0% line and bumped CM counters are
// exactly what the per-turn view must surface.
func cacheTurnsOnActivity(turns []cacheTurn) []cacheTurn {
	out := make([]cacheTurn, 0, len(turns))
	for _, t := range turns {
		if t.PromptN > 0 || t.CacheRead > 0 || t.CacheWrite > 0 {
			out = append(out, t)
		}
	}
	return out
}

// writeCacheAvgPerTurn renders section 2: an MD table with one row per
// cache-active turn — turn number, full prompt-side token volume (kT),
// cumulative cache-miss counters, per-turn hit rate.
func writeCacheAvgPerTurn(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("# Cache usage per turn\n")
	active := cacheTurnsOnActivity(turns)
	if len(active) == 0 {
		return
	}
	missByTurn := cacheMissCounters(turns)
	b.WriteString("| Turn | Tokens kT | CM | CH % |\n|---|---|---|---|\n")
	for _, t := range active {
		cm := missByTurn[t.Num]
		fmt.Fprintf(b, "| T%d | %08.1f | %d-%d | %.1f%% |\n",
			t.Num, cacheTurnTokensK(t), cm[0], cm[1], cacheTurnRate(t))
	}
}

// cacheTurnTokensK returns the turn's full prompt-side volume (uncached input
// + cache reads + cache writes) in kilo-tokens.
func cacheTurnTokensK(t cacheTurn) float64 {
	return float64(t.PromptN+t.CacheRead+t.CacheWrite) / 1000.0
}

// cacheMissCounters folds the per-turn miss detection into CUMULATIVE
// full/partial counters keyed by turn number — the same semantics as the
// footer's CM:<full>-<partial> display, so the two surfaces agree.
func cacheMissCounters(turns []cacheTurn) map[int][2]int {
	out := make(map[int][2]int, len(turns))
	var cumFull, cumPartial int
	for _, m := range cacheMisses(turns) {
		cumFull += m.full
		cumPartial += m.partial
		out[m.num] = [2]int{cumFull, cumPartial}
	}
	return out
}

// writeCacheSessionTotal renders section 3: the token-weighted cache-hit
// percentage across the group's turns.
func writeCacheSessionTotal(b *strings.Builder, turns []cacheTurn) {
	active := cacheActiveTurns(turns)
	if len(active) == 0 {
		return
	}
	var read, write, prompt int
	for _, t := range active {
		read += t.CacheRead
		write += t.CacheWrite
		prompt += t.PromptN
	}
	total := metrics.CacheHitPct(read, write, prompt)
	fmt.Fprintf(b, "# Session total: %s%.2f%%%s cache hit (token-weighted over %d turns)\n",
		cacheLevelColor(total), total, ansi.Reset, len(active))
}

// writeCacheMissList renders section 4: an MD table with one row per cache
// miss — turn number, kind (full/partial), share of the previously-cached
// prefix, and the recomputed token volume (full miss = 100% of the prefix).
func writeCacheMissList(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("# Cache misses\n")
	misses := cacheMisses(turns)
	var any bool
	for _, m := range misses {
		if m.full == 0 && m.partial == 0 {
			continue
		}
		if !any {
			b.WriteString("| Turn | Kind | % of prefix | Tokens recomputed |\n|---|---|---|---|\n")
			any = true
		}
		kind := "partial"
		if m.full != 0 {
			kind = "full"
		}
		pct := 100.0
		if m.partial != 0 && m.prev > 0 {
			pct = float64(m.missed) / float64(m.prev) * 100
		}
		fmt.Fprintf(b, "| T%d | %s | %.1f%% | %s |\n",
			m.num, kind, pct, groupThousands(int64(m.missed)))
	}
	if !any {
		b.WriteString("No cache misses detected.\n")
	}
}

// groupThousands renders n with comma thousands separators (8192 → "8,192")
// for the miss-list token figure.
func groupThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var b strings.Builder
	first := len(digits) % 3
	if first > 0 {
		b.WriteString(digits[:first])
	}
	for i := first; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// writeCacheDrops renders the cache drop table as MD: turn number,
// before/after rates, and the fall in points.
func writeCacheDrops(b *strings.Builder, drops []cacheDrop) {
	if len(drops) == 0 {
		b.WriteString("No cache drops detected.\n")
		return
	}
	b.WriteString("# Cache drops\n")
	b.WriteString("| Turn | Before | After | Δ |\n|---|---|---|---|\n")
	for _, d := range drops {
		fmt.Fprintf(b, "| T%d | %.1f%% | %.1f%% | %.1f |\n",
			d.Turn, d.Before, d.After, d.Before-d.After)
	}
}

// runCacheStats backs /stats:cache: read the current session's turn history
// and render the cache hit-rate evolution view.
func (c *StatsCommand) runCacheStats(ctx core.Context, _ []string) error {
	return showCacheStats(ctx, ctx)
}

// showCacheStats renders the session cache view from any SessionRecorder
// source (the core.Context in production, a fake in tests).
func showCacheStats(w core.OutputWriter, rec core.SessionRecorder) error {
	history := rec.TurnHistory()
	current := rec.CurrentTurn()
	if len(history) == 0 && current == nil {
		writeStr(w, "No turn history available. Send a message first.\n")
		return nil
	}
	turns := cacheTurnsFromHistory(history, current)
	var b strings.Builder
	writeCacheView(&b, turns)
	writeFmt(w, "%s", b.String())
	return nil
}
