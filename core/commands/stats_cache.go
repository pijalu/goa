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

// cacheChartBars caps the last-10 vertical chart at the latest N completions.
const cacheChartBars = 10

// cacheChartRows is the vertical resolution (block rows) of the last-10
// chart — each bar is scaled into this many row bands.
const cacheChartRows = 8

// cacheLevelColor maps a cache-hit percentage onto the required band colors:
// red <90%, orange <95%, green ≥95%. Shared by the last-10 chart, the
// per-turn average bars, and the weighted session total so all sections
// agree.
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

// writeCacheHitLast10 renders section 1: a vertical bar chart of the last
// ≤10 cache-active completions, exact percentage centered under each bar,
// colored by band (red <90, orange <95, green ≥95).
func writeCacheHitLast10(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("# Cache hit — last completions (rightmost = newest)\n")
	active := cacheActiveTurns(turns)
	if len(active) == 0 {
		b.WriteString("No cache activity recorded yet.\n")
		return
	}
	if len(active) > cacheChartBars {
		active = active[len(active)-cacheChartBars:]
	}
	rates := make([]float64, len(active))
	for i, t := range active {
		rates[i] = cacheTurnRate(t)
	}
	writeCacheChart(b, rates, cacheLevelColors(rates))
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

// cacheLevelColors maps each rate to its band color.
func cacheLevelColors(rates []float64) []string {
	colors := make([]string, len(rates))
	for i, r := range rates {
		colors[i] = cacheLevelColor(r)
	}
	return colors
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

// writeCacheAvgPerTurn renders section 2: one line per cache-active turn in
// the required format — turn number, full prompt-side token volume (padded
// kT), cumulative cache-miss counters, per-turn hit rate, and a band-colored
// 20-column bar.
func writeCacheAvgPerTurn(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("# Cache usage per turn\n")
	active := cacheTurnsOnActivity(turns)
	if len(active) == 0 {
		return
	}
	missByTurn := cacheMissCounters(turns)
	const barWidth = 20 // block columns at 100%
	for _, t := range active {
		r := cacheTurnRate(t)
		filled := int(r*barWidth/100 + 0.5)
		if filled > barWidth {
			filled = barWidth
		}
		cm := missByTurn[t.Num]
		fmt.Fprintf(b, "T%d : %08.1fkT - CM: %d-%d - CH: %.1f%% %s%s%s\n",
			t.Num, cacheTurnTokensK(t), cm[0], cm[1],
			r, cacheLevelColor(r), strings.Repeat("█", filled), ansi.Reset)
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

// writeCacheMissList renders section 4: one line per cache miss with the
// miss size in tokens and as a percentage of the previously-cached prefix
// (full miss = 100% of the prefix recomputed).
func writeCacheMissList(b *strings.Builder, turns []cacheTurn) {
	misses := cacheMisses(turns)
	var any bool
	for _, m := range misses {
		if m.full == 0 && m.partial == 0 {
			continue
		}
		if !any {
			b.WriteString("# Cache misses\n")
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
		fmt.Fprintf(b, "T%-4d %s miss — %.1f%% of prefix · %s tokens recomputed\n",
			m.num, kind, pct, groupThousands(int64(m.missed)))
	}
	if !any {
		b.WriteString("# Cache misses\nNo cache misses detected.\n")
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

// cacheChartCellW is the per-bar column width of the vertical chart. Bars
// are drawn 4 columns wide so each bar's actual percentage ("93%", "100%")
// fits under it on the label row.
const cacheChartCellW = 4

// writeCacheChart renders a horizontal bar chart: one 4-column-wide bar per
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
// steps) then one cell per bar — a colored block run where the bar reaches
// this band, blank spacing otherwise (cells stay column-aligned).
func writeCacheChartRow(b *strings.Builder, row int, height []int, colors []string) {
	b.WriteString(cacheRowGutter(row))
	writeCacheRowCells(b, func(i int) string {
		if height[i] >= row {
			return colors[i] + strings.Repeat("█", cacheChartCellW) + ansi.Reset
		}
		return strings.Repeat(" ", cacheChartCellW)
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
	writeCacheRowCells(b, func(int) string { return ansi.RepeatHorizontal(cacheChartCellW) }, n)
	b.WriteString("\n")
}

// writeCacheChartLabels lists each bar's actual percentage under it,
// right-aligned to the 4-column cells ("100%", " 93%", "  0%"), newest
// rightmost.
func writeCacheChartLabels(b *strings.Builder, rates []float64) {
	b.WriteString(cacheChartGutter)
	writeCacheRowCells(b, func(i int) string { return fmt.Sprintf("%3.0f%%", rates[i]) }, len(rates))
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
