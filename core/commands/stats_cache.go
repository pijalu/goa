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

// cacheChartMaxBuckets caps the bar chart width in rows; longer histories are
// bucketed (token-weighted) so the chart stays readable.
const cacheChartMaxBuckets = 40

// cacheDropThresholdPts is the minimum rate fall (percentage points) between
// two consecutive completions of the same model that counts as a cache drop.
// It matches the footer's red "drop" threshold (cacheHitDropDelta in
// internal/app/stats.go).
const cacheDropThresholdPts = 5.0

// cacheTurn is one completion's cache-token counts: the session-scoped
// counterpart of a usage.Record row for cache-rate purposes. A session runs
// one model at a time, so model-switch tracking is not needed here.
type cacheTurn struct {
	Num        int // turn number (1-based)
	CacheRead  int
	CacheWrite int
	PromptN    int
}

// cacheBucket is one bar of the evolution chart: a token-weighted aggregate
// of consecutive completions.
type cacheBucket struct {
	FirstTurn int // first turn number in the bucket
	Rows      int
	Pct       float64 // token-weighted cache hit rate
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
		})
	}
	return out
}

// cacheTurnRate computes the per-completion cache hit rate for one turn
// using the shared formula (Anthropic- and OpenAI-style branches).
func cacheTurnRate(t cacheTurn) float64 {
	return metrics.CacheHitPct(t.CacheRead, t.CacheWrite, t.PromptN)
}

// bucketCacheTurns aggregates consecutive cache-active turns into at most
// maxBuckets token-weighted buckets, preserving chronology. Turns with no
// cache activity (read==0 && write==0) are skipped: their permanent 0%
// would read as drops.
func bucketCacheTurns(turns []cacheTurn, maxBuckets int) []cacheBucket {
	var active []cacheTurn
	for _, t := range turns {
		if t.CacheRead > 0 || t.CacheWrite > 0 {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if maxBuckets <= 0 {
		maxBuckets = cacheChartMaxBuckets
	}
	per := (len(active) + maxBuckets - 1) / maxBuckets
	var out []cacheBucket
	for i := 0; i < len(active); {
		j := i + per
		if j > len(active) {
			j = len(active)
		}
		out = append(out, aggregateCacheBucket(active[i:j]))
		i = j
	}
	return out
}

// aggregateCacheBucket folds one run of turns into a bucket with a
// token-weighted rate (total reads / total cache ops), not a mean of rates —
// a 100-token completion must not weigh as much as a 100k-token one.
func aggregateCacheBucket(turns []cacheTurn) cacheBucket {
	b := cacheBucket{FirstTurn: turns[0].Num, Rows: len(turns)}
	var read, write, prompt int
	for _, t := range turns {
		read += t.CacheRead
		write += t.CacheWrite
		prompt += t.PromptN
	}
	b.Pct = metrics.CacheHitPct(read, write, prompt)
	return b
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

// writeCacheView renders the /stats:cache output: evolution bar chart +
// cache drop table.
func writeCacheView(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("Cache hit rate evolution — this session\n")
	b.WriteString(strings.Repeat("-", 60) + "\n")

	buckets := bucketCacheTurns(turns, cacheChartMaxBuckets)
	if len(buckets) == 0 {
		b.WriteString("No cache activity recorded yet.\n")
		return
	}
	writeCacheChart(b, buckets)
	writeCacheDrops(b, detectCacheDrops(turns, cacheDropThresholdPts))
}

// writeCacheChart renders one bar per bucket: turn range, a bar whose length
// is the hit rate, the numeric rate, and the completion count. Bar color
// follows the footer's CH coloring: bold green growing, green stable/minor
// change (< 5pts drop), red significant drop (>= 5pts) — delta vs the
// previous bucket.
func writeCacheChart(b *strings.Builder, buckets []cacheBucket) {
	const barWidth = 24
	prev := -1.0
	for _, bk := range buckets {
		filled := int(bk.Pct*barWidth/100 + 0.5)
		if filled > barWidth {
			filled = barWidth
		}
		color := cacheBarColor(bk.Pct, prev)
		prev = bk.Pct
		fmt.Fprintf(b, "T%-4d %s%s%s%s %5.1f%% (%d)\n",
			bk.FirstTurn,
			color,
			strings.Repeat("█", filled),
			strings.Repeat("░", barWidth-filled),
			ansi.Reset,
			bk.Pct, bk.Rows)
	}
	b.WriteString("\n")
}

// cacheBarColor maps a bucket's delta vs the previous bucket onto the CH
// color scheme (see cacheHitColorFor in internal/app/stats.go). The first
// bucket is stable green. Only significant changes (>=5pt drop) shift the
// color — minor fluctuations stay green.
func cacheBarColor(pct, prev float64) string {
	const (
		green = "#3fb950"
		red   = "#f85149"
	)
	if prev < 0 {
		return ansi.Fg(green)
	}
	switch delta := pct - prev; {
	case delta >= 1.0:
		return ansi.Bold + ansi.Fg(green)
	case delta > -cacheDropThresholdPts:
		return ansi.Fg(green)
	default:
		return ansi.Fg(red)
	}
}

// writeCacheDrops renders the cache drop table: turn number, before/after
// rates, and the fall in points.
func writeCacheDrops(b *strings.Builder, drops []cacheDrop) {
	if len(drops) == 0 {
		b.WriteString("No cache drops detected.\n")
		return
	}
	b.WriteString("Cache drops (fall ≥ 5pts between consecutive completions):\n")
	fmt.Fprintf(b, "%-6s %7s %7s %6s\n", "TURN", "BEFORE", "AFTER", "Δ")
	for _, d := range drops {
		fmt.Fprintf(b, "T%-5d %6.1f%% %6.1f%% %5.1f\n",
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