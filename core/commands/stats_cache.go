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

// This file implements the /stats:cache view: per agent/goal sections (the
// section labels use the goal's user-friendly alias such as "cheery.swan"
// when the session's goal log can resolve it — opaque IDs are only shown
// otherwise), each with the required report layout:
//
//	1. Last 10 exchanges   — one MD-table row per LLM API call with its own
//	                          cache-hit rate plus the tokens Lost vs the
//	                          previous interaction's cached prefix.
//	2. Global statistics   — token-weighted session hit rate and the
//	                          headline "missed cache tokens" totals measured
//	                          against previous content: a perfect chain
//	                          loses 0 tokens, a full cache miss costs the
//	                          complete size of the previously-cached prefix.
//	+ supporting detail tables (per-turn usage, misses, drops).
//
// Heading levels: the agent/goal group header is "#" and every section
// inside it is "##" — children never outrank their parent.
//
// ALL cache surfaces of a group (rates, CM counters, misses, drops, totals)
// scan ONE authoritative series — cacheGroupSeries: the per-API-call
// completion log when it exists, else the turn series (legacy sessions).
// Turn records flatten a multi-call turn to its LAST call, so scanning them
// for misses/drops hid intra-turn busts while the completion-based headline
// reported them (bugs.md 2026-08-30).
//
// Derived from the current session's turn history (all agent calls in this
// session) — not from the persistent cross-session usage store.
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
	Turn       int // turn number where the drop occurred
	Before     float64
	After      float64
	LostTokens int // cached-but-lost tokens: prev cache-read − current cache-read (0 when the read grew)
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

// cacheCompletionsFromHistory maps the per-API-call completion log onto the
// cacheTurn shape so it flows through the same rate/grouping helpers. Unlike
// the turn series, one multi-call turn yields one entry per LLM completion.
func cacheCompletionsFromHistory(comps []core.CompletionRecord) []cacheTurn {
	out := make([]cacheTurn, 0, len(comps))
	for _, c := range comps {
		out = append(out, cacheTurn{
			Num:        c.TurnNumber,
			CacheRead:  c.CacheRead,
			CacheWrite: c.CacheWrite,
			PromptN:    c.PromptN,
			AgentRole:  c.AgentRole,
			GoalID:     c.GoalID,
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
// classic bust signature and is caught here. Each drop also carries the
// cached-but-lost token delta: how many of the previous prefix's cache-read
// tokens stopped being served (a full bust loses the entire previous prefix;
// a rate fall driven by writes with an intact read loses nothing).
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
			drops = append(drops, cacheDrop{
				Turn:       active[i].Num,
				Before:     prev,
				After:      rate,
				LostTokens: lostCachedTokens(active[i-1], active[i]),
			})
		}
	}
	return drops
}

// lostCachedTokens returns how many previously-cached tokens stopped being
// served across one drop: max(0, prev read − cur read). A full bust (read 0)
// loses the entire previous prefix — the same figure the misses table's full
// miss shows — while a write-driven rate fall with an intact or grown read
// loses nothing.
func lostCachedTokens(prev, cur cacheTurn) int {
	if lost := prev.CacheRead - cur.CacheRead; lost > 0 {
		return lost
	}
	return 0
}

// cacheChartBars caps the last-completions table at the latest N entries.
const cacheChartBars = 10

// cacheLevelColor maps a cache-hit percentage onto the band colors:
// red <90%, orange <95%, green ≥95%. Used by the weighted session total
// line so all sections agree.
func cacheLevelColor(pct float64) string {
	switch {
	case pct >= 95:
		return ansi.Fg(cacheColorGreen)
	case pct >= 90:
		return ansi.Fg(cacheColorOrange)
	default:
		return ansi.Fg(cacheColorRed)
	}
}

// Severity band colors shared by every /stats:cache surface.
const (
	cacheColorRed    = "#f85149"
	cacheColorOrange = "#d29922"
	cacheColorGreen  = "#3fb950"
)

// cacheGroup is one agent/goal section of the cache view.
type cacheGroup struct {
	key         string // display label ("main", "companion", …)
	turns       []cacheTurn
	completions []cacheTurn // per-API-call series (superset granularity of turns)
}

// cacheGroupKey derives the section key of a turn/completion from its
// identity, labeling the active goal with its user-friendly alias when the
// name source knows it ("main · cheery.swan"); unknown or absent aliases
// keep today's opaque form ("main · goal:<id>"). Solo sessions collapse to
// a single unlabeled group so the output keeps today's header-less look.
func cacheGroupKey(t cacheTurn, names map[string]string) string {
	key := t.AgentRole
	if key == "" {
		key = "main"
	}
	if t.GoalID != "" {
		label := "goal:" + t.GoalID
		if alias := names[t.GoalID]; alias != "" {
			label = alias
		}
		key += " · " + label
	}
	return key
}

// groupCacheTurns partitions the turn series by (AgentRole, GoalID) in
// first-appearance order, then assigns each completion of the per-call log to
// its matching section (creating a section when the completion log covers a
// group the turn series does not, e.g. after a history reset).
func groupCacheTurns(turns []cacheTurn, completions []cacheTurn, names map[string]string) []cacheGroup {
	var groups []cacheGroup
	index := map[string]int{}
	for _, t := range turns {
		key := cacheGroupKey(t, names)
		i, ok := index[key]
		if !ok {
			groups = append(groups, cacheGroup{key: key})
			i = len(groups) - 1
			index[key] = i
		}
		groups[i].turns = append(groups[i].turns, t)
	}
	for _, c := range completions {
		key := cacheGroupKey(c, names)
		i, ok := index[key]
		if !ok {
			groups = append(groups, cacheGroup{key: key})
			i = len(groups) - 1
			index[key] = i
		}
		groups[i].completions = append(groups[i].completions, c)
	}
	return groups
}

// cacheGroupSeries returns the group's authoritative chronological series for
// every cache surface: the per-API-call completion log when it exists (it
// keeps every LLM call — turn records flatten multi-call turns to their last
// call), falling back to the turn series for legacy sessions without a
// completion log.
func cacheGroupSeries(g cacheGroup) []cacheTurn {
	if len(g.completions) > 0 {
		return g.completions
	}
	return g.turns
}

// writeCacheView renders the /stats:cache output: per agent/goal group
// (friendly goal aliases in the headers), the last-10 exchanges table, the
// global statistics block (weighted total + missed-cache-token headline),
// then the per-turn average bars, miss list, and drops. A single group (the
// common solo session) renders without a section header.
func writeCacheView(b *strings.Builder, turns, completions []cacheTurn, names map[string]string) {
	groups := groupCacheTurns(turns, completions, names)
	multi := len(groups) > 1
	for _, g := range groups {
		if multi {
			fmt.Fprintf(b, "# %s\n", g.key)
		}
		writeCacheGroupSections(b, g)
	}
}

// writeCacheGroupSections renders the report layout for one group: the two
// user-facing sections first, then the supporting detail tables. Every miss/
// drop/rate surface reads the same authoritative series.
func writeCacheGroupSections(b *strings.Builder, g cacheGroup) {
	series := cacheGroupSeries(g)
	writeCacheHitLast10(b, series)
	writeCacheGlobalStatistics(b, g)
	writeCacheAvgPerTurn(b, g.turns, g.completions)
	writeCacheMissList(b, series)
	writeCacheDrops(b, detectCacheDrops(series, cacheDropThresholdPts))
}

// missScan is one index-aligned step of the shared cache-miss scan: Prev is
// the previous entry's cached-prefix size, Missed the tokens lost here.
type missScan struct {
	Num    int
	Prev   int
	Missed int
}

// Full reports a full cache miss: every previously-cached token stopped
// being served (the whole prefix must be recomputed from scratch).
func (s missScan) Full() bool { return s.Missed > 0 && s.Missed == s.Prev }

// Partial reports a partial miss: part of the previous prefix vanished.
func (s missScan) Partial() bool { return s.Missed > 0 && s.Missed != s.Prev }

// scanMisses walks ANY chronological series (per-turn or per-API-call) and
// classifies each entry against the previous interaction's cached content —
// the single place encoding the rules so footer CM counters, miss tables,
// Lost columns and global totals can never disagree:
//
//   - no loss until a prefix is established;
//   - read falling to zero after establishment = FULL miss worth the entire
//     previous prefix ("complete size from the previous interaction");
//   - reads shrinking by more than the tolerance = PARTIAL miss worth the
//     vanished remainder;
//   - growth and sub-tolerance wobble cost nothing → perfect caches total 0.
func scanMisses(series []cacheTurn) []missScan {
	out := make([]missScan, len(series))
	prev, established := 0, false
	for i, t := range series {
		if t.CacheRead > 0 {
			established = true
		}
		s := missScan{Num: t.Num, Prev: prev}
		switch {
		case established && t.CacheRead == 0 && prev > 0:
			s.Missed = prev
		case prev > 0 && t.CacheRead+cacheMissDropTolerance < prev:
			s.Missed = prev - t.CacheRead
		}
		out[i] = s
		prev = t.CacheRead
	}
	return out
}

// cacheMisses folds the shared scan into the per-turn miss rows consumed by
// the CM counters and the misses table (shape kept for compatibility).
func cacheMisses(turns []cacheTurn) []cacheMissTurn {
	scans := scanMisses(turns)
	out := make([]cacheMissTurn, len(turns))
	for i, sc := range scans {
		m := cacheMissTurn{num: sc.Num, missed: sc.Missed, prev: sc.Prev}
		if sc.Full() {
			m.full = 1
		} else if sc.Partial() {
			m.partial = 1
		}
		out[i] = m
	}
	return out
}

// writeCacheHitLast10 renders report section 1 "Last 10 exchanges": an MD
// table of the last ≤10 exchanges (oldest first, newest last), one row per
// LLM API call with its own cache-hit rate plus the Lost column — tokens
// measured against the PREVIOUS interaction's cached prefix, so a healthy
// exchange shows 0 and a full bust shows the complete previous size. Unlike
// the old turn-keyed view this keeps cache-bust calls (0%) — hiding the very
// call that dropped the rate would defeat the per-call window. Rendered on
// screen through the MD pipeline.
func writeCacheHitLast10(b *strings.Builder, completions []cacheTurn) {
	b.WriteString("## Last 10 exchanges\n")
	scans := scanMisses(completions) // index-aligned with completions
	type exchangeRow struct {
		turn cacheTurn
		lost int
	}
	rows := make([]exchangeRow, 0, len(completions))
	for i, c := range completions { // keep only LLM-active calls (see below)
		if !cacheCallActive(c) {
			continue
		}
		rows = append(rows, exchangeRow{turn: c, lost: scans[i].Missed})
	}
	if len(rows) == 0 {
		b.WriteString("No cache activity recorded yet.\n")
		return
	}
	if len(rows) > cacheChartBars {
		rows = rows[len(rows)-cacheChartBars:]
	}
	b.WriteString("| Turn | Call | CH % | Lost |\n|---|---|---|---|\n")
	// Ordinal of each call within its turn: consecutive completions sharing a
	// turn number are calls 1, 2, … of that turn.
	callNo, lastTurn := 0, 0
	for _, r := range rows {
		if r.turn.Num != lastTurn {
			callNo, lastTurn = 1, r.turn.Num
		} else {
			callNo++
		}
		fmt.Fprintf(b, "| T%d | #%d | %.1f%% | %s |\n", r.turn.Num, callNo, cacheTurnRate(r.turn), groupThousands(int64(r.lost)))
	}
}

// cacheCallActive mirrors cacheTurnsOnActivity for one call: it actually
// called the LLM, so bust rounds stay visible in the exchanges window.
func cacheCallActive(t cacheTurn) bool {
	return t.PromptN > 0 || t.CacheRead > 0 || t.CacheWrite > 0
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

// writeCacheAvgPerTurn renders the per-turn MD table: one row per cache-active
// turn — aggregated prompt-side token volume (kT), cumulative cache-miss
// counters, per-turn hit rate. When the per-call log exists it aggregates
// each turn's OWN calls (a turn record flattens multi-call turns to their
// last call, which mislabeled whole turns with one call's rate); the turn
// series is the legacy fallback. The CM counters always scan the same
// unified series as the misses table and the global headline.
func writeCacheAvgPerTurn(b *strings.Builder, turns, completions []cacheTurn) {
	b.WriteString("## Cache usage per turn\n")
	var active []cacheTurn
	if len(completions) > 0 {
		active = aggregateTurnsFromCalls(completions)
	} else {
		active = cacheTurnsOnActivity(turns)
	}
	if len(active) == 0 {
		return
	}
	missByTurn := cacheMissCounters(cacheGroupSeries(cacheGroup{turns: turns, completions: completions}))
	b.WriteString("| Turn | Tokens kT | CM | CH % |\n|---|---|---|---|\n")
	for _, t := range active {
		cm := missByTurn[t.Num]
		fmt.Fprintf(b, "| T%d | %08.1f | %d-%d | %.1f%% |\n",
			t.Num, cacheTurnTokensK(t), cm[0], cm[1], cacheTurnRate(t))
	}
}

// aggregateTurnsFromCalls folds the per-call log into one cacheTurn per turn
// number for the per-turn table: prompt-side counters are summed across the
// turn's calls, so kT is the volume the turn actually processed and CH% the
// token-weighted rate of all its calls (first-appearance order, chronological
// in the log).
func aggregateTurnsFromCalls(calls []cacheTurn) []cacheTurn {
	index := make(map[int]int, len(calls))
	out := make([]cacheTurn, 0, len(calls))
	for _, c := range calls {
		if !cacheCallActive(c) {
			continue
		}
		if i, ok := index[c.Num]; ok {
			out[i].PromptN += c.PromptN
			out[i].CacheRead += c.CacheRead
			out[i].CacheWrite += c.CacheWrite
			continue
		}
		index[c.Num] = len(out)
		out = append(out, cacheTurn{
			Num: c.Num, PromptN: c.PromptN, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite,
			AgentRole: c.AgentRole, GoalID: c.GoalID,
		})
	}
	return out
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

// writeSessionTotalLine emits the weighted-percentage headline over an
// already-filtered LLM-active series. unit names the series granularity
// ("LLM calls" for the per-call log, "turns" for the legacy fallback).
func writeSessionTotalLine(b *strings.Builder, active []cacheTurn, unit string) {
	var read, write, prompt int
	for _, t := range active {
		read += t.CacheRead
		write += t.CacheWrite
		prompt += t.PromptN
	}
	total := metrics.CacheHitPct(read, write, prompt)
	fmt.Fprintf(b, "Session total: %s%.2f%%%s cache hit (token-weighted over %d %s)\n",
		cacheLevelColor(total), total, ansi.Reset, len(active), unit)
}

// missTotals aggregates one chain's missed-cache-token accounting for the
// Global statistics headline.
type missTotals struct {
	Tokens                int // total tokens recomputed against previous content
	Events, Full, Partial int // miss counts by kind
}

// totalMissedTokens folds a chain's per-call (falling back to per-turn when
// no call log exists) miss detection into headline figures. Semantics fixed
// by requirement: a perfect chain totals ZERO tokens; a full bust totals the
// complete size of the previous interaction's prefix; partials only the
// vanished portion beyond the shared tolerance.
func totalMissedTokens(series []cacheTurn) missTotals {
	var tot missTotals
	for _, sc := range scanMisses(series) {
		if sc.Missed == 0 {
			continue
		}
		tot.Tokens += sc.Missed
		tot.Events++
		if sc.Full() {
			tot.Full++
		} else {
			tot.Partial++
		}
	}
	return tot
}

// missedTokensLine renders the headline figures color-banded: green zero
// (perfect), red when any full bust occurred, orange otherwise.
func missedTokensLine(tot missTotals) string {
	label := fmt.Sprintf("Missed cache tokens: %s", groupThousands(int64(tot.Tokens)))
	color := cacheColorOrange
	if tot.Tokens == 0 {
		color = cacheColorGreen
	} else if tot.Full > 0 {
		color = cacheColorRed
	}
	if tot.Tokens == 0 {
		return fmt.Sprintf("%s%s — perfect cache%s\n", ansi.Fg(color), label, ansi.Reset)
	}
	return fmt.Sprintf("%s%s across %d exchange(s) (%d full, %d partial)%s\n",
		ansi.Fg(color), label, tot.Events, tot.Full, tot.Partial, ansi.Reset)
}

// writeCacheGlobalStatistics renders the "Global statistics" section: the
// token-weighted session hit rate plus the missed-cache-token totals,
// computed from the group's authoritative series (the per-call log — turn
// snapshots only mirror its last call). LLM-active calls feed the weighted
// rate, so bust rounds dilute it exactly like the footer's global fold. A
// group whose provider never reported cache tokens says so explicitly
// instead of a silent (or meaningless 0.00%) section.
func writeCacheGlobalStatistics(b *strings.Builder, g cacheGroup) {
	series := cacheGroupSeries(g)
	if len(series) == 0 {
		return
	}
	b.WriteString("## Global statistics\n")
	if !seriesHasCacheTokens(series) {
		fmt.Fprintf(b, "No prompt-cache activity: the provider reported 0 cached tokens across %d LLM call(s).\n",
			len(cacheTurnsOnActivity(series)))
		return
	}
	writeSessionTotalLine(b, cacheTurnsOnActivity(series), "LLM calls")
	b.WriteString(missedTokensLine(totalMissedTokens(series)))
}

// seriesHasCacheTokens reports whether any entry of the series carried cache
// reads or writes — the provider engaged prompt caching at least once.
func seriesHasCacheTokens(series []cacheTurn) bool {
	for _, t := range series {
		if t.CacheRead > 0 || t.CacheWrite > 0 {
			return true
		}
	}
	return false
}

// writeCacheMissList renders section 4: an MD table with one row per cache
// miss — turn number, kind (full/partial), share of the previously-cached
// prefix, and the recomputed token volume (full miss = 100% of the prefix).
func writeCacheMissList(b *strings.Builder, turns []cacheTurn) {
	b.WriteString("## Cache misses\n")
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
// before/after rates, the fall in points, and the cached-but-lost token
// delta (grouped like the misses table's token figure).
func writeCacheDrops(b *strings.Builder, drops []cacheDrop) {
	b.WriteString("## Cache drops\n")
	if len(drops) == 0 {
		b.WriteString("No cache drops detected.\n")
		return
	}
	b.WriteString("| Turn | Before | After | Δ | Lost tokens |\n|---|---|---|---|---|\n")
	for _, d := range drops {
		fmt.Fprintf(b, "| T%d | %.1f%% | %.1f%% | %.1f | %s |\n",
			d.Turn, d.Before, d.After, d.Before-d.After, groupThousands(int64(d.LostTokens)))
	}
}

// runCacheStats backs /stats:cache: read the current session's turn history
// and render the cache hit-rate evolution view, labeling goal sections with
// their friendly aliases when a goal name source is wired.
func (c *StatsCommand) runCacheStats(ctx core.Context, _ []string) error {
	return showCacheStats(ctx, ctx, c.goalAliases())
}

// showCacheStats renders the session cache view from any SessionRecorder
// source (the core.Context in production, a fake in tests). names maps goal
// IDs to friendly aliases; nil keeps the opaque-ID labels of old sessions.
func showCacheStats(w core.OutputWriter, rec core.SessionRecorder, names map[string]string) error {
	history := rec.TurnHistory()
	current := rec.CurrentTurn()
	completions := rec.CompletionHistory()
	if len(history) == 0 && current == nil && len(completions) == 0 {
		writeStr(w, "No turn history available. Send a message first.\n")
		return nil
	}
	turns := cacheTurnsFromHistory(history, current)
	var b strings.Builder
	writeCacheView(&b, turns, cacheCompletionsFromHistory(completions), names)
	writeFmt(w, "%s", b.String())
	return nil
}
