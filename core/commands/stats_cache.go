// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/metrics"
	"github.com/pijalu/goa/internal/usage"
)

// This file implements the /stats:cache view: an evolution bar chart of the
// per-completion cache hit rate plus a table of cache drops (before/after
// rates), both derived from the raw per-completion rows of the usage store.
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

// cacheRowLimit bounds how many recent events feed the view.
const cacheRowLimit = 2000

// cacheBucket is one bar of the evolution chart: a token-weighted aggregate
// of consecutive completions.
type cacheBucket struct {
	At    time.Time // first completion in the bucket
	Rows  int
	Pct   float64 // token-weighted cache hit rate
	Model string  // dominant model (first row's)
}

// cacheDrop is one detected cache-rate fall between consecutive completions
// of the same model.
type cacheDrop struct {
	At     time.Time
	Model  string
	Before float64
	After  float64
}

// cacheRowRate computes the per-completion cache hit rate for one usage row
// using the shared formula (Anthropic- and OpenAI-style branches).
func cacheRowRate(r usage.Record) float64 {
	return metrics.CacheHitPct(r.CacheRead, r.CacheWrite, r.PromptN)
}

// cacheCapableModels returns the set of models that ever reported cache
// activity (read or write). Rows from never-caching models are excluded from
// the chart and drop detection: their permanent 0% would read as drops.
func cacheCapableModels(rows []usage.Record) map[string]bool {
	capable := map[string]bool{}
	for _, r := range rows {
		if r.CacheRead > 0 || r.CacheWrite > 0 {
			capable[r.Model] = true
		}
	}
	return capable
}

// bucketCacheRows aggregates consecutive cache-active rows into at most
// maxBuckets token-weighted buckets, preserving chronology. Rows from
// never-caching models are skipped. A bucket never mixes models: a model
// switch starts a fresh bucket (cache keys are per-model).
func bucketCacheRows(rows []usage.Record, maxBuckets int) []cacheBucket {
	capable := cacheCapableModels(rows)
	var active []usage.Record
	for _, r := range rows {
		if capable[r.Model] {
			active = append(active, r)
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
		// Never mix models in one bucket: cut at the next model change.
		for k := i + 1; k < j; k++ {
			if active[k].Model != active[i].Model {
				j = k
				break
			}
		}
		out = append(out, aggregateCacheBucket(active[i:j]))
		i = j
	}
	return out
}

// aggregateCacheBucket folds one run of same-model rows into a bucket with a
// token-weighted rate (total reads / total cache ops), not a mean of rates —
// a 100-token completion must not weigh as much as a 100k-token one.
func aggregateCacheBucket(rows []usage.Record) cacheBucket {
	b := cacheBucket{At: rows[0].At, Rows: len(rows), Model: rows[0].Model}
	var read, write, prompt int
	for _, r := range rows {
		read += r.CacheRead
		write += r.CacheWrite
		prompt += r.PromptN
	}
	b.Pct = metrics.CacheHitPct(read, write, prompt)
	return b
}

// detectCacheDrops finds falls of >= thresholdPts percentage points between
// consecutive completions of the SAME model (a model switch restarts the
// baseline — a cold first completion on a new model is not a drop). Rows
// from never-caching models are ignored. A drop to 0% (TTL expiry, prefix
// invalidation) is the classic bust signature and is caught here.
func detectCacheDrops(rows []usage.Record, thresholdPts float64) []cacheDrop {
	capable := cacheCapableModels(rows)
	lastRate := map[string]float64{}
	seen := map[string]bool{}
	var drops []cacheDrop
	for _, r := range rows {
		if !capable[r.Model] {
			continue
		}
		rate := cacheRowRate(r)
		if seen[r.Model] && lastRate[r.Model]-rate >= thresholdPts {
			drops = append(drops, cacheDrop{At: r.At, Model: r.Model, Before: lastRate[r.Model], After: rate})
		}
		lastRate[r.Model], seen[r.Model] = rate, true
	}
	return drops
}

// writeCacheView renders the /stats:cache output: evolution bar chart +
// cache drop table.
func writeCacheView(b *strings.Builder, rows []usage.Record, scope string) {
	b.WriteString("Cache hit rate evolution")
	if scope != "" {
		b.WriteString(" — " + scope)
	}
	b.WriteString("\n" + strings.Repeat("-", 60) + "\n")

	buckets := bucketCacheRows(rows, cacheChartMaxBuckets)
	if len(buckets) == 0 {
		b.WriteString("No cache activity recorded yet.\n")
		return
	}
	writeCacheChart(b, buckets)
	writeCacheDrops(b, detectCacheDrops(rows, cacheDropThresholdPts))
}

// writeCacheChart renders one bar per bucket: timestamp, a bar whose length
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
		fmt.Fprintf(b, "%s %s%s%s%s %5.1f%% (%d)\n",
			bk.At.Format("01-02 15:04"),
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

// writeCacheDrops renders the cache drop table: time, model, before/after
// rates, and the fall in points.
func writeCacheDrops(b *strings.Builder, drops []cacheDrop) {
	if len(drops) == 0 {
		b.WriteString("No cache drops detected.\n")
		return
	}
	b.WriteString("Cache drops (fall ≥ 5pts between consecutive completions):\n")
	fmt.Fprintf(b, "%-14s %-24s %7s %7s %6s\n", "TIME", "MODEL", "BEFORE", "AFTER", "Δ")
	for _, d := range drops {
		fmt.Fprintf(b, "%-14s %-24s %6.1f%% %6.1f%% %5.1f\n",
			d.At.Format("01-02 15:04"), d.Model, d.Before, d.After, d.Before-d.After)
	}
}

// runCacheStats backs /stats:cache: load recent per-completion rows from the
// usage store (scoped to the current project — cache behavior is a per-project
// conversation property) and render the evolution view.
func (c *StatsCommand) runCacheStats(ctx core.Context, args []string) error {
	open := c.OpenStore
	if open == nil {
		open = defaultStoreOpener
	}
	st, err := open()
	if err != nil {
		ctx.Writef("stats: cannot open store: %v\n", err)
		return nil
	}
	defer st.Close()

	project := c.ProjectDir
	if project == "" {
		project = ctx.ProjectDir
	}
	// /stats:cache all lifts the project scope (cross-project evolution).
	scope := "project " + project
	if len(args) > 0 && args[0] == "all" {
		project = ""
		scope = "all projects"
	}
	rows, err := st.Rows(project, time.Time{}, cacheRowLimit)
	if err != nil {
		ctx.Writef("stats: cache query failed: %v\n", err)
		return nil
	}
	var b strings.Builder
	writeCacheView(&b, rows, scope)
	ctx.Writef("%s", b.String())
	return nil
}
