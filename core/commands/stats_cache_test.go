// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	tui "github.com/pijalu/goa/tui"
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

// cacheCalls builds a chronological completion log from (turn, prompt, read,
// write) tuples — one entry per LLM API call. Several entries sharing a turn
// number model a multi-round (multi-call) turn.
func cacheCalls(tuples ...[4]int) []core.CompletionRecord {
	out := make([]core.CompletionRecord, len(tuples))
	for i, tu := range tuples {
		out[i] = core.CompletionRecord{
			TurnNumber: tu[0],
			PromptN:    tu[1],
			CacheRead:  tu[2],
			CacheWrite: tu[3],
		}
	}
	return out
}

// TestDetectCacheDropsSession covers drop detection feeding the drops table.
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
		// Cached-but-lost tokens: the entire previous read prefix (400).
		if d.LostTokens != 400 {
			t.Errorf("LostTokens = %d, want 400 (full bust loses the prev prefix)", d.LostTokens)
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

// TestDetectCacheDrops_LostTokens pins the cached-but-lost token delta of
// each drop (bugs.md §5): full bust loses the entire previous prefix, partial
// shed loses the difference, and a write-driven rate fall with an intact read
// loses nothing.
func TestDetectCacheDrops_LostTokens(t *testing.T) {
	for _, tt := range []struct {
		name  string
		turns [][3]int
		want  int
	}{{
		name:  "partial shed loses only the shed suffix",
		turns: [][3]int{{0, 400, 100}, {0, 250, 300}}, // 80% → 45.5%
		want:  150,
	}, {
		name:  "full bust loses the entire previous prefix",
		turns: [][3]int{{0, 400, 100}, {500, 0, 0}}, // 80% → 0%
		want:  400,
	}, {
		name:  "write-driven fall with intact read loses nothing",
		turns: [][3]int{{0, 400, 100}, {0, 400, 3400}}, // 80% → ~10.5%
		want:  0,
	}} {
		t.Run(tt.name, func(t *testing.T) {
			drops := detectCacheDrops(cacheTurnsFromHistory(cacheTurns(tt.turns...), nil), 5)
			if len(drops) != 1 {
				t.Fatalf("drops = %+v, want 1", drops)
			}
			if drops[0].LostTokens != tt.want {
				t.Errorf("LostTokens = %d, want %d", drops[0].LostTokens, tt.want)
			}
		})
	}
}

// TestStatsCommand_CacheView covers the routed /stats:cache output rendered
// as Markdown tables: every section emits an MD header + separator row and
// no block-bar characters remain anywhere in the output.
func TestStatsCommand_CacheView(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: cacheTurns(
			[3]int{0, 300, 100}, // 75%
			[3]int{0, 400, 100}, // 80%
			[3]int{500, 0, 0},   // 0% — bust
			[3]int{0, 200, 400}, // 33.3% — recovery
		),
		// Completion log mirrors the turn series (one call per turn here).
		completions: cacheCalls(
			[4]int{1, 0, 300, 100},
			[4]int{2, 0, 400, 100},
			[4]int{3, 500, 0, 0},
			[4]int{4, 0, 200, 400},
		),
	}

	w := newWriter()
	if err := showCacheStats(w, rec); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertCacheViewSkeleton(t, w.Text())
}

// assertCacheViewSections checks the five section headings are present.
func assertCacheViewSections(t *testing.T, plain string) {
	t.Helper()
	for _, want := range []string{
		"Cache hit — last completions",
		"Cache usage per turn",
		"Session total",
		"Cache misses",
		"Cache drops",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("output missing %q:\n%s", want, plain)
		}
	}
}

// assertCacheViewSkeleton checks headings, table separators, absence of
// bar blocks, and the exact data rows of each MD table.
func assertCacheViewSkeleton(t *testing.T, plain string) {
	t.Helper()
	assertCacheViewSections(t, plain)
	// No barchart blocks may remain.
	if strings.Contains(plain, "█") {
		t.Errorf("barchart block chars must be gone:\n%s", plain)
	}
	// Every tabular section carries a valid MD separator row.
	if got := strings.Count(plain, "|---|"); got < 4 {
		t.Errorf("want ≥4 MD tables (last10/per-turn/misses/drops), found %d separators:\n%s", got, plain)
	}
	for _, want := range []string{
		"| T1 | #1 | 75.0% |",                // last-completions row (75%)
		"| T4 | #1 | 33.3% |",                // last-completions row (recovery)
		"| T1 | 000000.4 | 0-0 | 75.0% |",    // per-turn row
		"| T3 | 000000.5 | 1-0 | 0.0% |",     // per-turn row after bust
		"| T3 | 80.0% | 0.0% | 80.0 | 400 |", // drops row before/after/Δ/lost
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("table missing expected row %q:\n%s", want, plain)
		}
	}
	// Misses table carries the full-miss token figure.
	if !strings.Contains(plain, "full") || !strings.Contains(plain, "400") {
		t.Errorf("misses table lacks the full-miss token figure:\n%s", plain)
	}
}

// TestStatsCommand_CacheViewEmpty covers the no-activity case.
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

// TestCacheLevelColor pins the required band thresholds: red <90,
// orange <95, green ≥95 (used by the session-total line coloring).
func TestCacheLevelColor(t *testing.T) {
	red, orange, green := "\x1b[38;2;248;81;73m", "\x1b[38;2;210;153;34m", "\x1b[38;2;63;185;80m"
	for pct, want := range map[float64]string{
		0: red, 89.9: red, 90: orange, 94.9: orange, 95: green, 100: green,
	} {
		if got := cacheLevelColor(pct); got != want {
			t.Errorf("cacheLevelColor(%v) = %q, want %q", pct, got, want)
		}
	}
}

// TestWriteCacheHitLast10 verifies the last-completions MD table: capped at
// 10 rows, oldest first, one row per cache-active API call with its rate.
func TestWriteCacheHitLast10(t *testing.T) {
	// 12 cache-active completions at 95% — only the last 10 may appear.
	var tuples [][4]int
	for i := 0; i < 12; i++ {
		tuples = append(tuples, [4]int{i + 1, 0, 95, 5}) // 95%
	}
	var b strings.Builder
	writeCacheHitLast10(&b, cacheCompletionsFromHistory(cacheCalls(tuples...)))
	out := b.String()
	rows := strings.Count(out, "| T") - strings.Count(out, "| Turn |")
	if rows != 10 {
		t.Errorf("table must cap at 10 data rows, got %d:\n%s", rows, out)
	}
	if !strings.HasPrefix(out, "# Cache hit — last completions\n| Turn | Call | CH % |\n|---|---|---|\n") {
		t.Errorf("missing heading or table skeleton:\n%s", out)
	}
	// Oldest first, newest last: first data row is T3, last is T12.
	if !strings.Contains(out, "| T3 | #1 | 95.0% |") || !strings.Contains(out, "| T12 | #1 | 95.0% |") {
		t.Errorf("rows must run oldest→newest (T3…T12):\n%s", out)
	}
	if strings.Contains(out, "| T2 |") {
		t.Errorf("dropped completion T2 must not render:\n%s", out)
	}
}

// TestWriteCacheHitLast10_PerCallRows pins the bug-1 fix: a multi-call turn
// contributes one row per LLM API call — not one flattened turn row — each
// with its own cache-hit rate and a per-turn call ordinal, newest last.
func TestWriteCacheHitLast10_PerCallRows(t *testing.T) {
	// Turn 2 made 3 API calls: 75%, 0% (bust), 33.3% (recovery). Turn 3 is a
	// single-call turn. The table must show 4 rows total.
	comps := cacheCalls(
		[4]int{2, 0, 300, 100}, // 75.0%
		[4]int{2, 500, 0, 0},   // 0.0% — the per-call rate the old turn row lost
		[4]int{2, 0, 200, 400}, // 33.3%
		[4]int{3, 0, 900, 100}, // 90.0%
	)
	var b strings.Builder
	writeCacheHitLast10(&b, cacheCompletionsFromHistory(comps))
	out := b.String()
	want := []string{
		"| T2 | #1 | 75.0% |",
		"| T2 | #2 | 0.0% |",
		"| T2 | #3 | 33.3% |",
		"| T3 | #1 | 90.0% |",
	}
	last := -1
	for _, w := range want {
		idx := strings.Index(out, w)
		if idx < 0 {
			t.Fatalf("missing per-call row %q:\n%s", w, out)
		}
		if idx < last {
			t.Errorf("rows must be ordered oldest→newest: %q after later row:\n%s", w, out)
		}
		last = idx
	}
	if got := strings.Count(out, "| T") - 1; got != len(want) { // minus header row
		t.Errorf("data row count = %d, want %d:\n%s", got, len(want), out)
	}
}

// TestWriteCacheHitLast10_GroupsPerAgentGoal verifies per-call rows respect
// the agent/goal sections: main-agent completions never leak into a
// sub-agent section and vice versa.
func TestWriteCacheHitLast10_GroupsPerAgentGoal(t *testing.T) {
	turns := []core.TurnRecord{
		{Number: 1, AgentRole: "main", TokenUsage: core.TurnTokenUsage{CacheRead: 300, CacheWrite: 100}},
		{Number: 2, AgentRole: "companion", TokenUsage: core.TurnTokenUsage{CacheRead: 800, CacheWrite: 200}},
	}
	comps := []core.CompletionRecord{
		{TurnNumber: 1, AgentRole: "main", CacheRead: 300, CacheWrite: 100},
		{TurnNumber: 1, AgentRole: "main", CacheRead: 900, CacheWrite: 100},
		{TurnNumber: 2, AgentRole: "companion", CacheRead: 800, CacheWrite: 200},
	}
	var b strings.Builder
	writeCacheView(&b, cacheTurnsFromHistory(turns, nil), cacheCompletionsFromHistory(comps))
	out := b.String()
	// Two sections, each carrying only its own completions: main keeps its
	// two per-call rows, companion its one.
	for _, want := range []string{
		"## main\n",
		"## companion\n",
		"| T1 | #1 | 75.0% |",
		"| T1 | #2 | 90.0% |",
		"| T2 | #1 | 80.0% |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sectioned output missing %q:\n%s", want, out)
		}
	}
}

// TestWriteCacheHitLast10_Empty pins the no-activity placeholder.
func TestWriteCacheHitLast10_Empty(t *testing.T) {
	var b strings.Builder
	writeCacheHitLast10(&b, nil)
	if !strings.Contains(b.String(), "No cache activity recorded yet.") {
		t.Errorf("empty view must say so:\n%s", b.String())
	}
}

// TestWriteCacheAvgPerTurn verifies the per-turn MD table shape: turn,
// padded kT volume, CM counters, hit rate.
func TestWriteCacheAvgPerTurn(t *testing.T) {
	var b strings.Builder
	writeCacheAvgPerTurn(&b, cacheTurnsFromHistory(cacheTurns(
		[3]int{0, 90, 10},  // 90%, 100 tokens total → 000000.1kT
		[3]int{0, 960, 40}, // 96%, 1000 tokens → 000001.0kT
	), nil))
	out := b.String()
	for _, want := range []string{
		"| Turn | Tokens kT | CM | CH % |",
		"| T1 | 000000.1 | 0-0 | 90.0% |",
		"| T2 | 000001.0 | 0-0 | 96.0% |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("per-turn table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "█") {
		t.Errorf("no bars allowed:\n%s", out)
	}
}

// TestWriteCacheAvgPerTurn_MissCounters pins that a turn which busts the
// established cache shows up in the cumulative CM counters of its own and
// all later rows.
func TestWriteCacheAvgPerTurn_MissCounters(t *testing.T) {
	var b strings.Builder
	writeCacheAvgPerTurn(&b, cacheTurnsFromHistory(cacheTurns(
		[3]int{0, 800, 200}, // T1: warm, 80%
		[3]int{500, 0, 0},   // T2: full miss (500 tokens recomputed)
	), nil))
	out := b.String()
	if !strings.Contains(out, "| T1 | 000001.0 | 0-0 |") {
		t.Errorf("T1 must show clean counters:\n%s", out)
	}
	if !strings.Contains(out, "| T2 | 000000.5 | 1-0 |") {
		t.Errorf("T2 full miss must bump the cumulative full counter:\n%s", out)
	}
}

// TestWriteCacheSessionTotal verifies the token-weighted session percentage.
func TestWriteCacheSessionTotal(t *testing.T) {
	var b strings.Builder
	writeCacheSessionTotal(&b, cacheTurnsFromHistory(cacheTurns(
		[3]int{100, 50, 50},
		[3]int{100, 810, 90},
	), nil))
	out := b.String()
	if !strings.Contains(out, "Session total:") || !strings.Contains(out, "weighted over 2 turns") {
		t.Errorf("weighted total line missing:\n%s", out)
	}
}

// TestWriteCacheMissList verifies the misses MD table carries kind,
// percent-of-prefix, and the grouped token figure.
func TestWriteCacheMissList(t *testing.T) {
	var b strings.Builder
	writeCacheMissList(&b, cacheTurnsFromHistory(cacheTurns(
		[3]int{0, 8000, 2000},
		[3]int{2000, 0, 0}, // full miss: 8000 tokens recomputed
	), nil))
	out := b.String()
	for _, want := range []string{
		"| Turn | Kind | % of prefix | Tokens recomputed |",
		"| T2 | full | 100.0% | 8,000 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("misses table missing %q:\n%s", want, out)
		}
	}
}

// TestWriteCacheDrops verifies the drops MD table rows (including the
// cached-but-lost token delta) and the empty case.
func TestWriteCacheDrops(t *testing.T) {
	drops := []cacheDrop{{Turn: 3, Before: 80, After: 0, LostTokens: 8000}}
	var b strings.Builder
	writeCacheDrops(&b, drops)
	out := b.String()
	if !strings.Contains(out, "| Turn | Before | After | Δ | Lost tokens |") ||
		!strings.Contains(out, "| T3 | 80.0% | 0.0% | 80.0 | 8,000 |") {
		t.Errorf("drops table wrong:\n%s", out)
	}

	b.Reset()
	writeCacheDrops(&b, nil)
	if !strings.Contains(b.String(), "No cache drops detected.") {
		t.Errorf("empty drops must say so:\n%s", b.String())
	}
}

// TestWriteCacheMDOutput_RendersThroughMarkdownPipeline feeds the full
// /stats:cache output through the app's markdown renderer and asserts it
// comes out as box-drawn TABLE rows (not fallback paragraphs/preformatted).
func TestWriteCacheMDOutput_RendersThroughMarkdownPipeline(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: cacheTurns(
			[3]int{0, 300, 100},
			[3]int{0, 400, 100},
			[3]int{500, 0, 0},
		),
		completions: cacheCalls(
			[4]int{1, 0, 300, 100},
			[4]int{2, 0, 400, 100},
			[4]int{3, 500, 0, 0},
		),
	}
	w := newWriter()
	if err := showCacheStats(w, rec); err != nil {
		t.Fatalf("run: %v", err)
	}
	r := tui.NewMDStreamRenderer(80, tui.DarkTheme())
	rendered := strings.Join(r.Render(w.Text()), "\n")
	if !strings.Contains(rendered, "│") {
		t.Fatalf("markdown pipeline did not render tables (no box-drawing glyphs):\n%s", rendered)
	}
	if strings.Contains(rendered, "█") {
		t.Errorf("rendered output still contains bar blocks:\n%s", rendered)
	}
}
