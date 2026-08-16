// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/ansi"
)

// cacheTurns builds a chronological turn series from (prompt, read, write)
// triples, numbered sequentially from 1.
func cacheTurns(triples ...[3]int) []core.TurnRecord {
	out := make([]core.TurnRecord, len(triples))
	for i, tr := range triples {
		out[i] = core.TurnRecord{
			Number: i + 1,
			TokenUsage: core.TurnTokenUsage{
				PromptN:    tr[0],
				CacheRead:  tr[1],
				CacheWrite: tr[2],
			},
		}
	}
	return out
}

// TestLatestCacheRates covers the per-completion rate extraction that feeds the
// horizontal chart: cache-active turns only, chronological order, capped at the
// latest maxBars, rightmost = newest.
func TestLatestCacheRates(t *testing.T) {
	t.Run("raw per-completion rates, oldest to newest", func(t *testing.T) {
		// Anthropic-style turns: 300/400=75% and 100/100=50%.
		turns := cacheTurns([3]int{0, 300, 100}, [3]int{0, 100, 100})
		rates, colors := latestCacheRates(cacheTurnsFromHistory(turns, nil), 20)
		if len(rates) != 2 || rates[0] != 75 || rates[1] != 50 {
			t.Errorf("rates = %v, want [75 50] oldest→newest", rates)
		}
		if len(colors) != 2 {
			t.Errorf("colors = %v, want one per bar", colors)
		}
	})
	t.Run("capped at latest maxBars, rightmost is newest", testLatestCacheRatesCap)
	t.Run("never-caching turns excluded", testLatestCacheRatesSkipsCold)
	t.Run("empty when no cache activity", testLatestCacheRatesEmpty)
}

// testLatestCacheRatesCap pins the ≤20 cap and the oldest→newest ordering.
func testLatestCacheRatesCap(t *testing.T) {
	var triples [][3]int
	for i := 0; i < 30; i++ {
		// ramping rate so the last completion is distinguishable
		triples = append(triples, [3]int{100, i, 0}) // read=i
	}
	rates, _ := latestCacheRates(cacheTurnsFromHistory(cacheTurns(triples...), nil), 20)
	if len(rates) != 20 {
		t.Fatalf("len(rates) = %d, want 20", len(rates))
	}
	// rightmost (index 19) is the newest completion (read=29)
	last := cacheTurnRate(cacheTurn{CacheRead: 29, CacheWrite: 0, PromptN: 100})
	if rates[19] != last {
		t.Errorf("rates[19] = %v, want newest %v (rightmost = most recent)", rates[19], last)
	}
	// leftmost kept is the 11th newest (read=10)
	first := cacheTurnRate(cacheTurn{CacheRead: 10, CacheWrite: 0, PromptN: 100})
	if rates[0] != first {
		t.Errorf("rates[0] = %v, want %v (oldest of the last 20)", rates[0], first)
	}
}

// testLatestCacheRatesSkipsCold pins that turns with no cache tokens drop out.
func testLatestCacheRatesSkipsCold(t *testing.T) {
	turns := cacheTurns([3]int{500, 0, 0}, [3]int{600, 0, 0}, [3]int{0, 100, 100})
	rates, _ := latestCacheRates(cacheTurnsFromHistory(turns, nil), 20)
	if len(rates) != 1 {
		t.Errorf("rates = %v, want only the cache-active turn", rates)
	}
}

// testLatestCacheRatesEmpty pins the no-cache-activity → nil contract.
func testLatestCacheRatesEmpty(t *testing.T) {
	rates, colors := latestCacheRates(cacheTurnsFromHistory(cacheTurns([3]int{100, 0, 0}), nil), 20)
	if rates != nil || colors != nil {
		t.Errorf("rates/colors = %v/%v, want nil", rates, colors)
	}
}

// TestWriteCacheChart covers the horizontal chart geometry: cacheChartRows
// bands tall, one 1-col bar per completion, rightmost = newest, with a
// percentage gutter and baseline.
func TestWriteCacheChart(t *testing.T) {
	// Three completions at 100%, 50%, 0% — oldest→newest. Use the real color
	// sequence so bars are wrapped in color + reset like production.
	rates := []float64{100, 50, 0}
	_, colors := latestCacheRates(cacheTurnsFromHistory(cacheTurns(
		[3]int{0, 100, 0}, // 100%
		[3]int{0, 50, 50}, // 50%
		[3]int{100, 0, 0}, // 0%
	), nil), 20)
	var b strings.Builder
	writeCacheChart(&b, rates, colors)

	// Strip ANSI color codes to assert on the plain-text geometry.
	plain := ansi.Strip(b.String())
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")

	// cacheChartRows bar rows + 1 baseline + 1 label row.
	if len(lines) != cacheChartRows+2 {
		t.Fatalf("chart has %d lines, want %d (rows+baseline+labels):\n%s",
			len(lines), cacheChartRows+2, plain)
	}
	assertChartTopRow(t, lines[0], plain)
	assertChartBand50(t, lines, plain)
	assertChartBaselineAndLabels(t, lines, plain)
}

// assertChartTopRow pins the 100% gutter and that the 100% bar fills cell 0.
func assertChartTopRow(t *testing.T, top, plain string) {
	t.Helper()
	if !strings.Contains(top, "100%") {
		t.Errorf("top row missing 100%% gutter:\n%s", plain)
	}
	if !strings.HasPrefix(top, "100% █") {
		t.Errorf("top row first bar should be filled (100%% completion):\n%q", top)
	}
}

// assertChartBand50 pins that the 50% band fills bars 1&2 and empties bar 3.
func assertChartBand50(t *testing.T, lines []string, plain string) {
	t.Helper()
	var band50 string
	for _, l := range lines {
		if strings.HasPrefix(l, " 50%") {
			band50 = l
		}
	}
	if band50 == "" {
		t.Fatalf("no 50%% gutter row:\n%s", plain)
	}
	// cells: gutter(5) then bar,space,bar,space,bar → cols 5,7,9
	cells := []rune(band50)
	if cells[5] != '█' || cells[7] != '█' || cells[9] != ' ' {
		t.Errorf("50%% row should fill bars 1&2, empty bar 3: %q", band50)
	}
}

// assertChartBaselineAndLabels pins the ─ baseline and the per-bar label row.
func assertChartBaselineAndLabels(t *testing.T, lines []string, plain string) {
	t.Helper()
	if !strings.Contains(lines[cacheChartRows], "─") {
		t.Errorf("baseline row missing:\n%s", plain)
	}
	label := lines[cacheChartRows+1]
	if !strings.Contains(label, "100") || !strings.Contains(label, "50") || !strings.Contains(label, "0") {
		t.Errorf("label row missing per-bar values:\n%q", label)
	}
}

// TestWriteCacheChartEmpty covers the degenerate no-rates case.
func TestWriteCacheChartEmpty(t *testing.T) {
	var b strings.Builder
	writeCacheChart(&b, nil, nil)
	if b.Len() != 0 {
		t.Errorf("empty chart wrote %q, want nothing", b.String())
	}
}

func TestDetectCacheDropsSession(t *testing.T) {
	t.Run("drop to zero after warm cache detected", func(t *testing.T) {
		// 300/(300+100)=75% then 0% → TTL-expiry bust signature.
		turns := cacheTurns([3]int{0, 300, 100}, [3]int{0, 400, 100}, [3]int{500, 0, 0})
		drops := detectCacheDrops(cacheTurnsFromHistory(turns, nil), 5)
		if len(drops) != 1 {
			t.Fatalf("drops = %+v, want 1", drops)
		}
		d := drops[0]
		if d.Before != 80 || d.After != 0 || d.Turn != 3 {
			t.Errorf("drop = %+v, want Before=80 After=0 Turn=3", d)
		}
	})

	t.Run("small wobble under threshold ignored", func(t *testing.T) {
		turns := cacheTurns([3]int{0, 300, 100}, [3]int{0, 295, 105}) // 75% → 73.75%
		if drops := detectCacheDrops(cacheTurnsFromHistory(turns, nil), 5); len(drops) != 0 {
			t.Errorf("drops = %+v, want none (<5pts)", drops)
		}
	})

	t.Run("cold first completion is not a drop", func(t *testing.T) {
		turns := cacheTurns([3]int{0, 300, 100}, [3]int{500, 0, 0})
		if drops := detectCacheDrops(cacheTurnsFromHistory(turns, nil), 5); len(drops) != 1 {
			t.Errorf("drops = %+v, want one drop (300→500 with 0 cache)", drops)
		}
	})

	t.Run("cold turn between warm ones is a drop", func(t *testing.T) {
		// A turn that calls the LLM (prompt>0) but gets zero cache after a
		// warm turn IS a drop — the cache was established but not used.
		turns := cacheTurns(
			[3]int{0, 300, 100}, // 75% — turn 1
			[3]int{500, 0, 0},   // 0% — turn 2 (called LLM, no cache)
			[3]int{0, 400, 100}, // 80% — turn 3
		)
		drops := detectCacheDrops(cacheTurnsFromHistory(turns, nil), 5)
		if len(drops) != 1 || drops[0].Turn != 2 {
			t.Errorf("drops = %+v, want one drop at turn 2 (75%%→0%%)", drops)
		}
	})
}

// TestStatsCommand_CacheView covers the routed /stats:cache output: the
// horizontal chart header, the per-bar label values, and the drop table.
func TestStatsCommand_CacheView(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: cacheTurns(
			[3]int{0, 300, 100}, // 75%
			[3]int{0, 400, 100}, // 80%
			[3]int{500, 0, 0},   // 0% — bust
			[3]int{0, 200, 400}, // 33.3% — recovery
		),
	}

	w := newWriter()
	if err := showCacheStats(w, rec); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := w.Text()

	for _, want := range []string{
		"Cache hit rate — latest completions (rightmost = newest)",
		"Cache drops",
		"BEFORE", "AFTER",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The drop row: 80.0% → 0.0%.
	if !strings.Contains(out, "80.0%") || !strings.Contains(out, "  0.0%") {
		t.Errorf("drop table lacks before/after rates:\n%s", out)
	}
	// The chart renders block bars.
	if !strings.Contains(out, "█") {
		t.Errorf("chart lacks block bars:\n%s", out)
	}
}

// TestStatsCommand_CacheViewEmpty covers the no-activity case: no turns with
// cache tokens must not render an empty chart or crash.
func TestStatsCommand_CacheViewEmpty(t *testing.T) {
	rec := &fakeSessionRecorder{}
	w := newWriter()
	if err := showCacheStats(w, rec); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(w.Text(), "No turn history available") {
		t.Errorf("expected no-history message, got:\n%s", w.Text())
	}
}

// TestStatsCommand_CacheCompletion locks the /stats:cache arg completion.
func TestStatsCommand_CacheCompletion(t *testing.T) {
	var ac core.ArgCompleter = &StatsCommand{}
	for _, c := range ac.CompleteArgs(core.Context{}, "ca") {
		if c.Value == "cache" {
			return
		}
	}
	t.Fatal("cache completion missing")
}
