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
// counterpart of a usage.Record row for cache-rate purposes. A session runs
// one model at a time, so model-switch tracking is not needed here.
type cacheTurn struct {
	Num        int // turn number (1-based)
	CacheRead  int
	CacheWrite int
	PromptN    int
}

const cacheMissDropTolerance = 1024

type cacheMissTurn struct {
	num, full, partial int
	missed             int
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

// cacheChartBars caps the horizontal chart at the latest N completions.
const cacheChartBars = 20

// cacheChartRows is the vertical resolution (block rows) of the horizontal
// chart — each bar is scaled into this many row bands.
const cacheChartRows = 8

// writeCacheView renders the /stats:cache output: a horizontal bar chart of
// the latest per-completion cache-hit rates plus the cache drop table.
func writeCacheView(b *strings.Builder, turns []cacheTurn) {
	writeCacheMisses(b, turns)
	b.WriteString("Cache hit rate — latest completions (rightmost = newest)\n")
	b.WriteString("# Cache usage per turn\n")
	rates, colors := latestCacheRates(turns, cacheChartBars)
	if len(rates) == 0 {
		b.WriteString("No cache activity recorded yet.\n")
		return
	}
	writeHorizontalCacheChart(b, turns, rates, colors)
	writeCacheDrops(b, detectCacheDrops(turns, cacheDropThresholdPts))
}

func cacheMisses(turns []cacheTurn) []cacheMissTurn {
	out := make([]cacheMissTurn, 0, len(turns))
	prev, established := 0, false
	for _, t := range turns {
		m := cacheMissTurn{num: t.Num}
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

func writeCacheMisses(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("# Cache misses\n")
	for _, m := range cacheMisses(turns) {
		fullTokens, partialTokens := 0, 0
		if m.full != 0 {
			fullTokens = m.missed
		}
		if m.partial != 0 {
			partialTokens = m.missed
		}
		b.WriteString(fmt.Sprintf("T%d - CM: Full %d (%dt) / Partial %d (%dt)\n", m.num, m.full, fullTokens, m.partial, partialTokens))
	}
}

func writeHorizontalCacheChart(b *strings.Builder, turns []cacheTurn, rates []float64, colors []string) {
	active := make([]cacheTurn, 0, len(turns))
	for _, t := range turns {
		if t.CacheRead > 0 || t.CacheWrite > 0 {
			active = append(active, t)
		}
	}
	if len(active) > len(rates) {
		active = active[len(active)-len(rates):]
	}
	for i, r := range rates {
		width := int(r * 24 / 100)
		b.WriteString(fmt.Sprintf("T%d - CH: %.2f%% |", active[i].Num, r))
		b.WriteString(colors[i])
		b.WriteString(strings.Repeat("█", width))
		b.WriteString(ansi.Reset + "\n")
	}
}

// latestCacheRates extracts the per-completion cache-hit rate of the latest
// maxBars cache-active completions, oldest→newest (so the rightmost bar is the
// most recent), plus the per-bar color from the CH delta scheme. Completions
// with no cache activity (read==0 && write==0) are skipped: their permanent 0%
// would read as drops.
func latestCacheRates(turns []cacheTurn, maxBars int) ([]float64, []string) {
	var rates []float64
	for _, t := range turns {
		if t.CacheRead > 0 || t.CacheWrite > 0 {
			rates = append(rates, cacheTurnRate(t))
		}
	}
	if len(rates) == 0 {
		return nil, nil
	}
	if len(rates) > maxBars {
		rates = rates[len(rates)-maxBars:]
	}
	colors := make([]string, len(rates))
	prev := -1.0
	for i, r := range rates {
		colors[i] = cacheBarColor(r, prev)
		prev = r
	}
	return rates, colors
}

// cacheChartGutter is the fixed left-gutter width holding the band percentage
// labels (e.g. "100% ").
const cacheChartGutter = "     "

// writeCacheChart renders a horizontal bar chart: one 1-column-wide bar per
// completion, separated by a single space, rightmost = newest. Each bar's
// height encodes the completion's cache-hit rate (scaled to cacheChartRows
// block bands); the percentage axis is on the left, per-bar labels under the
// baseline. Color per bar follows the footer CH thresholds.
func writeCacheChart(b *strings.Builder, rates []float64, colors []string) {
	if len(rates) == 0 {
		return
	}
	height := cacheBarHeights(rates)
	for row := cacheChartRows; row >= 1; row-- {
		writeCacheChartRow(b, row, height, colors)
	}
	writeCacheChartBaseline(b, len(rates))
	writeCacheChartLabels(b, rates)
}

// cacheBarHeights scales each rate into a bar height in row bands
// (0..cacheChartRows).
func cacheBarHeights(rates []float64) []int {
	height := make([]int, len(rates))
	for i, r := range rates {
		h := int(r*cacheChartRows/100 + 0.5)
		if h > cacheChartRows {
			h = cacheChartRows
		}
		height[i] = h
	}
	return height
}

// writeCacheChartRow draws one horizontal band: the gutter label (at 25%
// steps) then one cell per bar — a colored block where the bar reaches this
// band, a space otherwise.
func writeCacheChartRow(b *strings.Builder, row int, height []int, colors []string) {
	b.WriteString(cacheRowGutter(row))
	writeCacheRowCells(b, func(i int) string {
		if height[i] >= row {
			return colors[i] + "█" + ansi.Reset
		}
		return " "
	}, len(height))
	b.WriteString("\n")
}

// cacheRowGutter returns the left gutter for a band: the band's percentage at
// 25% steps, blank otherwise.
func cacheRowGutter(row int) string {
	pct := row * 100 / cacheChartRows
	if pct%25 == 0 {
		return fmt.Sprintf("%3d%% ", pct)
	}
	return cacheChartGutter
}

// writeCacheRowCells writes n single-column cells separated by a space, each
// cell's content from cell(i).
func writeCacheRowCells(b *strings.Builder, cell func(i int) string, n int) {
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(cell(i))
	}
}

// writeCacheChartBaseline draws the ─ axis under the bars.
func writeCacheChartBaseline(b *strings.Builder, n int) {
	b.WriteString(cacheChartGutter)
	writeCacheRowCells(b, func(int) string { return "─" }, n)
	b.WriteString("\n")
}

// writeCacheChartLabels lists each bar's percentage under the chart, newest
// rightmost. With ≤ 20 bars the per-bar column is too narrow for the value, so
// labels are listed as a compact oldest→newest row of percentages instead.
func writeCacheChartLabels(b *strings.Builder, rates []float64) {
	b.WriteString(cacheChartGutter)
	writeCacheRowCells(b, func(i int) string { return fmt.Sprintf("%.0f", rates[i]) }, len(rates))
	b.WriteString("\n")
}

// cacheBarColor maps a bucket's delta vs the previous bucket onto the CH
// color scheme (see cacheHitColorFor in internal/app/stats.go). The first
// bucket is stable green. Only significant changes (>=5pt drop) shift the
// color — minor fluctuations stay green.
func cacheBarColor(pct, prev float64) string {
	const (
		green  = "#3fb950"
		yellow = "#d29922"
		orange = "#db6d28"
		red    = "#f85149"
		gray   = "#8b949e"
	)
	switch {
	case pct >= 95:
		return ansi.Fg(green)
	case pct >= 80:
		return ansi.Fg(yellow)
	case pct >= 70:
		return ansi.Fg(orange)
	case pct >= 60:
		return ansi.Fg(red)
	default:
		return ansi.Fg(gray)
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
