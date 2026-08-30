// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/ansi"
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
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertCacheViewSkeleton(t, w.Text())
}

// assertCacheViewSections checks the required section headings are present:
// the two user-facing report sections (last 10 exchanges, global statistics)
// plus the supporting detail tables.
func assertCacheViewSections(t *testing.T, plain string) {
	t.Helper()
	for _, want := range []string{
		"Last 10 exchanges",
		"Global statistics",
		"Session total",
		"Missed cache tokens:",
		"Cache usage per turn",
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
		"| T1 | #1 | 75.0% | 0 |",            // last-exchanges row (75%, nothing lost)
		"| T4 | #1 | 33.3% | 0 |",            // recovery exchange, no loss vs 200
		"| T3 | #1 | 0.0% | 400 |",           // bust lost the whole 400-token prefix
		"| T1 | 000000.4 | 0-0 | 75.0% |",    // per-turn row
		"| T3 | 000000.5 | 1-0 | 0.0% |",     // per-turn row after bust
		"| T3 | 80.0% | 0.0% | 80.0 | 400 |", // drops row before/after/Δ/lost
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("table missing expected row %q:\n%s", want, plain)
		}
	}
	// Misses table carries the unexpected-miss token figure.
	if !strings.Contains(plain, "unexpected") || !strings.Contains(plain, "400") {
		t.Errorf("misses table lacks the unexpected-miss token figure:\n%s", plain)
	}
}

// TestStatsCommand_CacheViewEmpty covers the no-activity case.
func TestStatsCommand_CacheViewEmpty(t *testing.T) {
	rec := &fakeSessionRecorder{}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
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
	if !strings.HasPrefix(out, "## Last 10 exchanges\n| Turn | Call | CH % | Lost |\n|---|---|---|---|\n") {
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
		"| T2 | #1 | 75.0% | 0 |",  // nothing cached before this chain
		"| T2 | #2 | 0.0% | 300 |", // bust: whole previous prefix lost
		"| T2 | #3 | 33.3% | 0 |",  // prev read was already 0
		"| T3 | #1 | 90.0% | 0 |",  // grew from 200 → 900
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
	writeCacheView(&b, cacheTurnsFromHistory(turns, nil), cacheCompletionsFromHistory(comps), nil)
	out := b.String()
	// Two sections, each carrying only its own completions: main keeps its
	// two per-call rows, companion its one.
	for _, want := range []string{
		"# main\n",
		"# companion\n",
		"| T1 | #1 | 75.0% | 0 |",
		"| T1 | #2 | 90.0% | 0 |",
		"| T2 | #1 | 80.0% | 0 |",
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

// TestWriteCacheAvgPerTurn verifies the per-turn MD table shape (legacy
// turns-only series): turn, padded kT volume, CM counters, hit rate.
func TestWriteCacheAvgPerTurn(t *testing.T) {
	var b strings.Builder
	writeCacheAvgPerTurn(&b, cacheTurnsFromHistory(cacheTurns(
		[3]int{0, 90, 10},  // 90%, 100 tokens total → 000000.1kT
		[3]int{0, 960, 40}, // 96%, 1000 tokens → 000001.0kT
	), nil), nil)
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
		[3]int{500, 0, 0},   // T2: unexpected miss (500 tokens recomputed)
	), nil), nil)
	out := b.String()
	if !strings.Contains(out, "| T1 | 000001.0 | 0-0 |") {
		t.Errorf("T1 must show clean counters:\n%s", out)
	}
	if !strings.Contains(out, "| T2 | 000000.5 | 1-0 |") {
		t.Errorf("T2 unexpected miss must bump the cumulative unexpected counter:\n%s", out)
	}
}

// TestWriteCacheSessionTotal verifies the token-weighted session percentage
// over the legacy turns-only series (the per-call path is pinned by
// TestWriteCacheGlobalStatistics_WeightedOverCalls).
func TestWriteCacheSessionTotal(t *testing.T) {
	var b strings.Builder
	writeSessionTotalLine(&b, cacheTurnsOnActivity(cacheTurnsFromHistory(cacheTurns(
		[3]int{100, 50, 50},
		[3]int{100, 810, 90},
	), nil)), "turns")
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
		[3]int{2000, 0, 0}, // unexpected miss: 8000 tokens recomputed
	), nil))
	out := b.String()
	for _, want := range []string{
		"| Turn | Kind | % of prefix | Tokens recomputed |",
		"| T2 | unexpected | 100.0% | 8,000 |",
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
	if err := showCacheStats(w, rec, nil); err != nil {
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

// --- Friendly goal names + missed-token totals (bugs.md 2026-08-26) -------

// fakeGoalNamer is a stub GoalNameSource with a fixed ID→name map.
type fakeGoalNamer struct{ names map[string]string }

func (f fakeGoalNamer) GoalFriendlyNames() map[string]string { return f.names }

// TestCacheGroupKeyFriendlyNames pins the section-key contract: a goal with
// a known friendly alias renders the alias; an unknown/empty mapping falls
// back to the opaque ID form; goals and roles absent stay unlabeled.
func TestCacheGroupKeyFriendlyNames(t *testing.T) {
	turn := func(role, goalID string) cacheTurn {
		return cacheTurn{Num: 1, PromptN: 1, AgentRole: role, GoalID: goalID}
	}
	names := map[string]string{"goal-3f9c1a2b": "cheery.swan"}
	for tc, want := range map[string]string{
		cacheGroupKey(turn("main", "goal-3f9c1a2b"), names):      "main · cheery.swan",
		cacheGroupKey(turn("main", "goal-ffffffff"), names):      "main · goal:goal-ffffffff",
		cacheGroupKey(turn("main", "goal-ffffffff"), nil):        "main · goal:goal-ffffffff",
		cacheGroupKey(turn("companion", "goal-3f9c1a2b"), names): "companion · cheery.swan",
	} {
		if tc != want {
			t.Errorf("cacheGroupKey = %q, want %q", tc, want)
		}
	}
	// Solo no-goal sessions stay unlabeled — today's header-less look.
	if got := cacheGroupKey(cacheTurn{Num: 1, PromptN: 1}, names); got != "main" {
		t.Errorf("solo key = %q, want \"main\"", got)
	}
}

// TestWriteCacheView_FriendlyGoalHeaders verifies the rendered section
// headers carry the friendly name end-to-end through writeCacheView.
func TestWriteCacheView_FriendlyGoalHeaders(t *testing.T) {
	turns := []core.TurnRecord{
		{Number: 1, AgentRole: "main", GoalID: "g1", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 300, CacheWrite: 100}},
	}
	comps := []core.CompletionRecord{
		{TurnNumber: 1, AgentRole: "main", GoalID: "g1", PromptN: 10, CacheRead: 300, CacheWrite: 100},
	}
	var b strings.Builder
	writeCacheView(&b,
		cacheTurnsFromHistory(turns, nil),
		cacheCompletionsFromHistory(comps),
		map[string]string{"g1": "tidy.falcon"},
	)
	// The single-group case renders without a section header (as before);
	// force the multi-group path to observe the label.
	turns2 := append(append([]core.TurnRecord{}, turns...), core.TurnRecord{
		Number: 2, AgentRole: "companion", TokenUsage: core.TurnTokenUsage{PromptN: 5},
	})
	comps2 := append(comps, core.CompletionRecord{TurnNumber: 2, AgentRole: "companion", PromptN: 5})
	b.Reset()
	writeCacheView(&b, cacheTurnsFromHistory(turns2, nil), cacheCompletionsFromHistory(comps2),
		map[string]string{"g1": "tidy.falcon"})
	out := b.String()
	if !strings.Contains(out, "# main · tidy.falcon\n") || !strings.Contains(out, "# companion\n") {
		t.Errorf("friendly main header and unlabeled unnamed-goal fallback missing:\n%s", out)
	}
}

// TestScanMissesSemantics locks the shared per-call miss classification used
// by the Lost column and the global totals: perfect caching contributes zero;
// a bust on a still-valid prefix (unexpected) loses the entire previous
// prefix; narrow falls under the tolerance are not misses; growth never
// loses.
func TestScanMissesSemantics(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 0},    // cold: nothing established
		{Num: 2, PromptN: 10, CacheRead: 8000}, // writes establish prefix
		{Num: 3, PromptN: 10, CacheRead: 8010}, // grew → no loss
		{Num: 4, PromptN: 10, CacheRead: 7900}, // wobble <1024 → tolerated
		{Num: 5, PromptN: 10, CacheRead: 6200}, // partial −1700
		{Num: 6, PromptN: 10, CacheRead: 0},    // unexpected bust → whole prev prefix
	}
	scans := scanMisses(series)
	if len(scans) != len(series) {
		t.Fatalf("scan length %d, want %d", len(scans), len(series))
	}
	type expect struct {
		missed     int
		unexpected bool
	}
	want := []expect{{0, false}, {0, false}, {0, false}, {0, false}, {1700, false}, {6200, true}}
	for i, e := range want {
		if scans[i].Missed != e.missed {
			t.Errorf("step %d missed = %d, want %d", i+1, scans[i].Missed, e.missed)
		}
		if scans[i].Unexpected() != e.unexpected {
			t.Errorf("step %d unexpected = %v (scan %+v)", i+1, scans[i].Unexpected(), scans[i])
		}
	}
	// Perfect chain: identical warm reads every call.
	for _, s := range scanMisses([]cacheTurn{
		{Num: 1, CacheRead: 5000}, {Num: 2, CacheRead: 5000}, {Num: 3, CacheRead: 5200},
	}) {
		if s.Missed != 0 {
			t.Errorf("perfect-cache scan shows loss: %+v", s)
		}
	}
}

// TestTotalMissedTokens checks the Global statistics aggregation over both
// granularity series, including the mixed multi-call-turn sum.
func TestTotalMissedTokens(t *testing.T) {
	cases := []struct {
		name   string
		series []cacheTurn
		tot    missTotals
	}{
		{"perfect cache is zero loss", []cacheTurn{
			{Num: 1, CacheRead: 4000}, {Num: 2, CacheRead: 4400},
		}, missTotals{}},
		{"unexpected bust costs the previous interaction's size", []cacheTurn{
			{Num: 1, CacheRead: 9000}, {Num: 2, PromptN: 1, CacheRead: 0},
		}, missTotals{Tokens: 9000, Events: 1, Unexpected: 1}},
		{"partial narrows by the vanished portion", []cacheTurn{
			{Num: 1, CacheRead: 7000}, {Num: 2, CacheRead: 6500},
		}, missTotals{}}, // 500 < 1024 tolerance → not a miss event
		{"multi-call turn accumulates calls", []cacheTurn{
			{Num: 1, CacheRead: 8000},
			{Num: 1, CacheRead: 0},    // unexpected: 8,000
			{Num: 1, CacheRead: 2000}, // recovery vs 0
			{Num: 1, CacheRead: 900},  // partial: −1100
		}, missTotals{Tokens: 9100, Events: 2, Unexpected: 1, Partial: 1}},
	}
	for _, tc := range cases {
		if got := totalMissedTokens(tc.series); got != tc.tot {
			t.Errorf("%s: totals = %+v, want %+v", tc.name, got, tc.tot)
		}
	}
}

// TestScanMissesResetBoundary is the regression test for the cache-miss
// classification rework (bugs.md 2026-08-30): a Reset-marked entry (an
// intentional context reset — fresh-context goal begin, summarize pass)
// starts a new conversation for the scan, so the boundary round itself and
// the collapse from the pre-reset prefix classify as NOTHING; the new
// conversation's later busts classify normally. Non-summarize compaction
// busts carry no marker and keep counting — they are costs.
func TestScanMissesResetBoundary(t *testing.T) {
	t.Run("summarize boundary is not a miss and restarts the baseline", func(t *testing.T) {
		series := []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 150000},         // hot prefix
			{Num: 2, PromptN: 10, CacheRead: 0, Reset: true}, // summarize: cold by design
			{Num: 3, PromptN: 10, CacheRead: 0},              // still warming — no prev, no miss
			{Num: 4, PromptN: 10, CacheRead: 12000},          // new prefix established
			{Num: 5, PromptN: 10, CacheRead: 0},              // TTL bust inside the new conversation
		}
		scans := scanMisses(series)
		for i, want := range []int{0, 0, 0, 0, 12000} {
			if scans[i].Missed != want {
				t.Errorf("step %d missed = %d, want %d", i+1, scans[i].Missed, want)
			}
		}
		if !scans[4].Unexpected() {
			t.Errorf("step 5 must classify unexpected (real bust inside the new conversation): %+v", scans[4])
		}
		if tot := totalMissedTokens(series); tot != (missTotals{Tokens: 12000, Events: 1, Unexpected: 1}) {
			t.Errorf("totals = %+v, want one 12000-token unexpected miss", tot)
		}
	})

	t.Run("collapse across a summarize boundary is not a partial miss", func(t *testing.T) {
		series := []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 150000},
			{Num: 2, PromptN: 10, CacheRead: 8192, Reset: true}, // reads only the shared head
		}
		if tot := totalMissedTokens(series); tot.Tokens != 0 {
			t.Errorf("totals = %+v, want zero (intentional boundary, not a cost)", tot)
		}
	})

	t.Run("non-summarize compaction bust has no marker and counts", func(t *testing.T) {
		series := []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 150000},
			{Num: 2, PromptN: 10, CacheRead: 0}, // micro/elision/truncation bust — a cost
		}
		if tot := totalMissedTokens(series); tot != (missTotals{Tokens: 150000, Events: 1, Unexpected: 1}) {
			t.Errorf("totals = %+v, want one 150000-token unexpected miss", tot)
		}
	})
}

// TestWriteCacheMissListUnexpectedKind verifies the misses table renders the
// reclassified kind ("unexpected") end-to-end, and that a reset-marked
// boundary produces no miss row at all.
func TestWriteCacheMissListUnexpectedKind(t *testing.T) {
	var b strings.Builder
	writeCacheMissList(&b, []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 9000},
		{Num: 2, PromptN: 10, CacheRead: 0}, // unexpected bust
	})
	out := b.String()
	if !strings.Contains(out, "| T2 | unexpected | 100.0% | 9,000 |") {
		t.Errorf("miss list missing the unexpected kind row:\n%s", out)
	}
	if strings.Contains(out, "| full |") || strings.Contains(out, " full ") {
		t.Errorf("miss list must not render the retired 'full' kind:\n%s", out)
	}

	b.Reset()
	writeCacheMissList(&b, []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 150000},
		{Num: 2, PromptN: 10, CacheRead: 0, Reset: true}, // summarize boundary
	})
	if !strings.Contains(b.String(), "No cache misses detected.") {
		t.Errorf("reset boundary must render no miss row:\n%s", b.String())
	}
}

// TestWriteCacheGlobalStatistics verifies the section heading, the weighted
// session-total line, and the headline missed-cache-tokens line with counts;
// plus that a perfect session advertises zero loss.
func TestWriteCacheGlobalStatistics(t *testing.T) {
	g := cacheGroup{
		key: "main",
		turns: cacheTurnsFromHistory(cacheTurns(
			[3]int{100, 50, 50},
			[3]int{100, 810, 90},
		), nil),
		completions: []cacheTurn{
			{Num: 1, CacheRead: 60}, {Num: 2, CacheRead: 900},
		},
	}
	var b strings.Builder
	writeCacheGlobalStatistics(&b, g)
	out := b.String()
	if !strings.Contains(out, "## Global statistics\n") ||
		!strings.Contains(out, "Session total:") || !strings.Contains(out, "weighted over 2 LLM calls") {
		t.Fatalf("global statistics skeleton missing:\n%s", out)
	}
	if !strings.Contains(out, "Missed cache tokens: 0") || !strings.Contains(out, "perfect cache") {
		t.Errorf("perfect session must report zero loss:\n%s", out)
	}
	// Bust chain: unexpected-miss totals in red with counts.
	g.completions = []cacheTurn{{Num: 1, CacheRead: 8000}, {Num: 2, PromptN: 1, CacheRead: 0}}
	b.Reset()
	writeCacheGlobalStatistics(&b, g)
	out = b.String()
	if !strings.Contains(out, "Missed cache tokens: 8,000 across 1 exchange(s) (1 unexpected, 0 partial)") ||
		!strings.Contains(out, "\x1b[38;2;248;81;73m") {
		t.Errorf("bust totals line wrong:\n%q", out)
	}
	// No activity → silent, as the old session-total renderer behaved.
	b.Reset()
	writeCacheGlobalStatistics(&b, cacheGroup{})
	if b.Len() != 0 {
		t.Errorf("no-activity group must render nothing, got:\n%s", b.String())
	}
}

// TestShowCacheStats_UsesFriendlyNames drives the command end-to-end with an
// injected name source and expects the friendly aliases on multi-goal output.
func TestShowCacheStats_UsesFriendlyNames(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: []core.TurnRecord{
			{Number: 1, AgentRole: "main", GoalID: "goal-1", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 300, CacheWrite: 100}},
			{Number: 2, AgentRole: "companion", TokenUsage: core.TurnTokenUsage{PromptN: 20}},
		},
		completions: []core.CompletionRecord{
			{TurnNumber: 1, AgentRole: "main", PromptN: 10, CacheRead: 300, CacheWrite: 100},
			{TurnNumber: 2, AgentRole: "companion", PromptN: 20},
		},
	}
	w := newWriter()
	if err := showCacheStats(w, rec, map[string]string{"goal-1": "tidy.falcon"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := w.Text()
	if !strings.Contains(out, "# main · tidy.falcon\n") || !strings.Contains(out, "# companion\n") {
		t.Errorf("expected friendly main header and unnamed companion fallback:\n%s", out)
	}
}

// TestStatsCommand_GoalNameSourceInjection guards the lazy nil-safe accessor.
func TestStatsCommand_GoalNameSourceInjection(t *testing.T) {
	c := &StatsCommand{}
	if names := c.goalAliases(); names != nil {
		t.Errorf("nil source must yield nil names, got %v", names)
	}
	c.GoalNames = fakeGoalNamer{names: map[string]string{"g": "happy.fox"}}
	if names := c.goalAliases(); len(names) != 1 || names["g"] != "happy.fox" {
		t.Errorf("injected source bypassed: %v", names)
	}
}

// --- /stats:cache report accuracy (bugs.md 2026-08-30) ---------------------

// TestStatsCommand_CacheView_MultiCallBustConsistency reproduces the reported
// contradiction: a goal turn whose per-call log holds a mid-turn cache bust
// must report it on EVERY surface — the global headline, the misses table,
// the CM counters, the drops table and the per-turn row — because all of them
// scan the same per-call series. The turn record only snapshots the LAST call
// (RecordTokenStats' last-call-wins), so turn-based scans hid the bust while
// the completion-based headline reported it ("3,713 missed" vs "No cache
// misses detected.").
func TestStatsCommand_CacheView_MultiCallBustConsistency(t *testing.T) {
	// One goal-tagged turn (T2) whose TurnRecord snapshot holds only the last
	// (recovered) call — exactly what the last-call-wins accumulation leaves.
	history := []core.TurnRecord{{
		Number: 2, AgentRole: "main", GoalID: "g1",
		TokenUsage: core.TurnTokenUsage{CacheRead: 8000},
	}}
	comps := []core.CompletionRecord{
		{TurnNumber: 2, AgentRole: "main", GoalID: "g1", CacheRead: 8000},                 // warm: 100%
		{TurnNumber: 2, AgentRole: "main", GoalID: "g1", PromptN: 232163, CacheRead: 100}, // bust: −7,900
		{TurnNumber: 2, AgentRole: "main", GoalID: "g1", CacheRead: 8000},                 // recovery
	}
	rec := &fakeSessionRecorder{history: history, completions: comps}
	w := newWriter()
	if err := showCacheStats(w, rec, map[string]string{"g1": "sunny.toad"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := w.Text()
	// The headline (already completion-based) reports the partial miss…
	if !strings.Contains(out, "Missed cache tokens: 7,900 across 1 exchange(s) (0 unexpected, 1 partial)") {
		t.Errorf("headline must report the intra-turn partial miss:\n%s", out)
	}
	// …and every other surface must agree with it.
	if !strings.Contains(out, "| T2 | partial | 98.8% | 7,900 |") {
		t.Errorf("misses table must list the intra-turn partial miss:\n%s", out)
	}
	if !strings.Contains(out, "0-1 |") {
		t.Errorf("CM counters must show the cumulative partial miss:\n%s", out)
	}
	if strings.Contains(out, "No cache misses detected.") || strings.Contains(out, "No cache drops detected.") {
		t.Errorf("no-miss/drop lines contradict the reported miss:\n%s", out)
	}
	// The 100% → 0% rate collapse lands in the drops table.
	if !strings.Contains(out, "| T2 | 100.0% | 0.0% | 100.0 | 7,900 |") {
		t.Errorf("drops table must show the mid-turn collapse:\n%s", out)
	}
	// The per-turn row aggregates the turn's OWN calls (248.3kT over 3 calls,
	// weighted 6.5%), not the last-call snapshot (8kT at 100%).
	if !strings.Contains(out, "| T2 | 000248.3 | 0-1 | 6.5% |") {
		t.Errorf("per-turn row must aggregate the turn's own calls:\n%s", out)
	}
	// The session total is token-weighted over the group's LLM calls — bust
	// rounds dilute it (16,100 reads / 248,263 prompt-side tokens = 6.49%) —
	// never a lone last-call snapshot presented as the whole session.
	if !strings.Contains(out, "6.49%") || !strings.Contains(out, "(token-weighted over 3 LLM calls)") {
		t.Errorf("session total must be weighted over all LLM calls:\n%s", out)
	}
}

// TestStatsCommand_CacheView_HeadingHierarchy pins the corrected heading
// levels: the agent/goal group header outranks its sections (# group, ##
// sections) and the drops section is always headed like the misses one.
func TestStatsCommand_CacheView_HeadingHierarchy(t *testing.T) {
	turns := []core.TurnRecord{
		{Number: 1, AgentRole: "main", GoalID: "g1", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 300, CacheWrite: 100}},
		{Number: 2, AgentRole: "companion", TokenUsage: core.TurnTokenUsage{PromptN: 5}},
	}
	comps := []core.CompletionRecord{
		{TurnNumber: 1, AgentRole: "main", GoalID: "g1", PromptN: 10, CacheRead: 300, CacheWrite: 100},
		{TurnNumber: 2, AgentRole: "companion", PromptN: 5},
	}
	var b strings.Builder
	writeCacheView(&b, cacheTurnsFromHistory(turns, nil), cacheCompletionsFromHistory(comps),
		map[string]string{"g1": "tidy.falcon"})
	out := b.String()
	for _, want := range []string{
		"# main · tidy.falcon\n",
		"# companion\n",
		"## Last 10 exchanges\n",
		"## Global statistics\n",
		"## Cache usage per turn\n",
		"## Cache misses\n",
		"## Cache drops\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("heading hierarchy missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## main ·") || strings.Contains(out, "## companion") {
		t.Errorf("group headers must not render below their sections:\n%s", out)
	}
}

// TestWriteCacheGlobalStatistics_WeightedOverCalls pins the session-total
// contract: token-weighted over the group's LLM calls — bust rounds dilute
// the rate exactly like the footer's global fold — never over last-call-wins
// turn snapshots, and never silent for a group the completion log knows.
func TestWriteCacheGlobalStatistics_WeightedOverCalls(t *testing.T) {
	g := cacheGroup{
		key: "main",
		turns: cacheTurnsFromHistory(cacheTurns(
			[3]int{10000, 0, 0}, // last-call snapshot: the bust at 0%
		), nil),
		completions: []cacheTurn{
			{Num: 1, CacheRead: 9000},              // warm call: 100%
			{Num: 2, PromptN: 10000, CacheRead: 0}, // bust: 0% — 9,000-token prefix lost
		},
	}
	var b strings.Builder
	writeCacheGlobalStatistics(&b, g)
	out := b.String()
	if !strings.Contains(out, "47.37%") {
		t.Errorf("weighted total must include the bust round (9000/19000 = 47.37%%):\n%s", out)
	}
	if !strings.Contains(out, "token-weighted over 2 LLM calls") {
		t.Errorf("total must be labeled over LLM calls:\n%s", out)
	}
	if !strings.Contains(out, "Missed cache tokens: 9,000 across 1 exchange(s) (1 unexpected, 0 partial)") {
		t.Errorf("headline wrong:\n%s", out)
	}
}

// TestStatsCommand_CacheView_CompletionsOnlyGroup: a group known only through
// the per-call log (the turn series is empty, e.g. after a history reset)
// must still render its Global statistics instead of falling silent.
func TestStatsCommand_CacheView_CompletionsOnlyGroup(t *testing.T) {
	rec := &fakeSessionRecorder{
		completions: cacheCalls(
			[4]int{1, 0, 8000, 0},
			[4]int{2, 500, 0, 0},
		),
	}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := w.Text()
	if !strings.Contains(out, "Session total:") || !strings.Contains(out, "Missed cache tokens: 8,000") {
		t.Errorf("completion-only group lost its Global statistics:\n%s", out)
	}
}

// TestStatsCommand_CacheView_NoCacheTraffic: LLM traffic with zero cache
// tokens (provider without prompt caching) must say so explicitly instead of
// an empty Global statistics section that reads like a healthy cache.
func TestStatsCommand_CacheView_NoCacheTraffic(t *testing.T) {
	rec := &fakeSessionRecorder{
		completions: cacheCalls(
			[4]int{1, 12000, 0, 0},
			[4]int{2, 15500, 0, 0},
		),
	}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := w.Text()
	if !strings.Contains(out, "No prompt-cache activity") {
		t.Errorf("cache-less traffic must be called out explicitly:\n%s", out)
	}
	if strings.Contains(out, "Session total:") {
		t.Errorf("a 0.00%% rate over cache-less calls is noise; the explicit line replaces it:\n%s", out)
	}
}

// --- Section separators: --- rules between goal groups + blank lines -------
// between report sections (bugs.md 2026-08-30) -------------------------------

// assertBlankLineBeforeSections verifies every ## section header that follows
// a previous section is preceded by a blank line, so sections never abut.
// (The first section sits under the group header or opens the report.)
func assertBlankLineBeforeSections(t *testing.T, out string) {
	t.Helper()
	for _, h := range []string{
		"## Global statistics",
		"## Cache usage per turn",
		"## Cache misses",
		"## Cache drops",
	} {
		if !strings.Contains(out, "\n\n"+h+"\n") {
			t.Errorf("%q not preceded by a blank line:\n%s", h, out)
		}
	}
}

// TestWriteCacheView_MultiGroupSeparators pins the multi-group layout: a
// `---` horizontal rule exactly once between the two goal groups (never
// leading, never trailing) plus a blank line before every ## section header.
func TestWriteCacheView_MultiGroupSeparators(t *testing.T) {
	turns := cacheTurns([3]int{10, 300, 100})
	turns[0].AgentRole, turns[0].GoalID = "main", "g1"
	comps := cacheCalls([4]int{1, 10, 300, 100})
	comps[0].AgentRole, comps[0].GoalID = "main", "g1"
	turns2 := append(turns, core.TurnRecord{
		Number: 2, AgentRole: "companion",
		TokenUsage: core.TurnTokenUsage{PromptN: 5, CacheRead: 40, CacheWrite: 10},
	})
	comps2 := append(comps, core.CompletionRecord{
		TurnNumber: 2, AgentRole: "companion", PromptN: 5, CacheRead: 40, CacheWrite: 10,
	})
	var b strings.Builder
	writeCacheView(&b, cacheTurnsFromHistory(turns2, nil), cacheCompletionsFromHistory(comps2),
		map[string]string{"g1": "tidy.falcon"})
	out := b.String()

	if got := strings.Count(out, "\n---\n"); got != 1 {
		t.Fatalf("want exactly one --- rule between groups, found %d:\n%s", got, out)
	}
	first := strings.Index(out, "# main · tidy.falcon\n")
	rule := strings.Index(out, "\n---\n")
	second := strings.Index(out, "# companion\n")
	if rule <= first || rule >= second {
		t.Fatalf("rule at %d must sit strictly between the group headers (%d < rule < %d):\n%s",
			rule, first, second, out)
	}
	assertBlankLineBeforeSections(t, out)
}

// TestWriteCacheView_SingleGroupSeparators: the header-less solo report gains
// the inter-section blank lines but never a rule.
func TestWriteCacheView_SingleGroupSeparators(t *testing.T) {
	var b strings.Builder
	writeCacheView(&b,
		cacheTurnsFromHistory(cacheTurns([3]int{10, 300, 100}), nil),
		cacheCompletionsFromHistory(cacheCalls([4]int{1, 10, 300, 100})),
		nil,
	)
	out := b.String()
	if strings.Contains(out, "\n---\n") {
		t.Fatalf("single-group report must not contain a --- rule:\n%s", out)
	}
	assertBlankLineBeforeSections(t, out)
}

// TestWriteCacheView_RuleRendersThroughMarkdownPipeline feeds a multi-group
// report through the real MD renderer: the --- rule must surface as the
// full-width faint rule line (────) between the goal sections, not as raw
// dashes or a mis-parsed heading.
func TestWriteCacheView_RuleRendersThroughMarkdownPipeline(t *testing.T) {
	turns := cacheTurns([3]int{10, 300, 100})
	turns[0].AgentRole, turns[0].GoalID = "main", "g1"
	comps := cacheCalls([4]int{1, 10, 300, 100})
	comps[0].AgentRole, comps[0].GoalID = "main", "g1"
	turns2 := append(turns, core.TurnRecord{
		Number: 2, AgentRole: "companion",
		TokenUsage: core.TurnTokenUsage{PromptN: 5, CacheRead: 40, CacheWrite: 10},
	})
	comps2 := append(comps, core.CompletionRecord{
		TurnNumber: 2, AgentRole: "companion", PromptN: 5, CacheRead: 40, CacheWrite: 10,
	})
	var b strings.Builder
	writeCacheView(&b, cacheTurnsFromHistory(turns2, nil), cacheCompletionsFromHistory(comps2),
		map[string]string{"g1": "tidy.falcon"})
	r := tui.NewMDStreamRenderer(80, tui.DarkTheme())
	rendered := ansi.Strip(strings.Join(r.Render(b.String()), "\n"))
	if !strings.Contains(rendered, "────") {
		t.Fatalf("rendered output lacks the horizontal rule line:\n%s", rendered)
	}
}

// --- Cache-miss shape classification (bugs.md 2026-08-30, plan F3/F4):
// the P7 no-tools collapse bust is a distinct report kind -----------------

// TestScanMissesNoToolsStepClassification verifies the third miss kind: a
// bust on a text-only-collapse call (request carried no tools + tool_choice
// "none" — the turn's final summary round) keeps its recomputed tokens but
// classifies as a NO-TOOLS STEP — never unexpected — mirroring the RCA
// export shape (read 80,768 → 0 with an intact message prefix).
func TestScanMissesNoToolsStepClassification(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},      // prefix established
		{Num: 1, PromptN: 1, TextOnlyCollapse: true}, // collapse round: read 0 BY DESIGN
	}
	scans := scanMisses(series)
	if scans[1].Missed != 80768 {
		t.Errorf("step 2 missed = %d, want 80768 (the recomputed prefix stays visible)", scans[1].Missed)
	}
	if scans[1].Unexpected() {
		t.Error("collapse bust must NOT classify unexpected (intentional request-shape change)")
	}
	if scans[1].Partial() {
		t.Error("collapse bust must NOT classify partial")
	}
	if !scans[1].NoToolsStep() {
		t.Error("collapse bust must classify as a no-tools step")
	}
	if scans[0].NoToolsStep() || scans[0].Unexpected() {
		t.Errorf("step 1 (prefix establishment) must not classify at all: %+v", scans[0])
	}
}

// TestScanMissesNoToolsStepPartialShape verifies the kind also covers the
// partial shape: a no-tools call whose read shrank without vanishing (tool
// schemas gone from the prefix, message head still served) classifies as a
// no-tools step, not partial.
func TestScanMissesNoToolsStepPartialShape(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},
		{Num: 1, PromptN: 1, CacheRead: 60000, TextOnlyCollapse: true},
	}
	scans := scanMisses(series)
	if scans[1].Missed != 20768 {
		t.Errorf("step 2 missed = %d, want 20768", scans[1].Missed)
	}
	if !scans[1].NoToolsStep() || scans[1].Partial() || scans[1].Unexpected() {
		t.Errorf("shrunken no-tools call must classify no-tools-step only: %+v", scans[1])
	}
}

// TestScanMissesNoToolsStepNormalBustUnchanged pins the F4 non-regression:
// a bust on a NORMAL call (no collapse flag) still classifies unexpected.
func TestScanMissesNoToolsStepNormalBustUnchanged(t *testing.T) {
	series := []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},
		{Num: 1, PromptN: 1, CacheRead: 0}, // plain bust: no flag
	}
	scans := scanMisses(series)
	if !scans[1].Unexpected() {
		t.Errorf("normal bust must still classify unexpected: %+v", scans[1])
	}
	if scans[1].NoToolsStep() {
		t.Error("normal bust must not classify as a no-tools step")
	}
}

// TestTotalMissedTokensNoToolsKind verifies the Global statistics fold:
// no-tools events keep their tokens in the total but count under their own
// NoTools counter — never under Unexpected/Partial (same rationale as
// intentional resets, minus the baseline restart: the loss is real, just
// intentional).
func TestTotalMissedTokensNoToolsKind(t *testing.T) {
	cases := []struct {
		name   string
		series []cacheTurn
		tot    missTotals
	}{
		{"collapse bust is its own kind in the headline", []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 80768},
			{Num: 1, PromptN: 1, CacheRead: 0, TextOnlyCollapse: true},
		}, missTotals{Tokens: 80768, Events: 1, NoTools: 1}},
		{"mixed: unexpected and no-tools stay separate counters", []cacheTurn{
			{Num: 1, PromptN: 10, CacheRead: 5000},
			{Num: 2, PromptN: 1, CacheRead: 0},                         // real unexpected bust
			{Num: 2, PromptN: 10, CacheRead: 3000},                     // re-warm
			{Num: 2, PromptN: 1, CacheRead: 0, TextOnlyCollapse: true}, // collapse bust
		}, missTotals{Tokens: 8000, Events: 2, Unexpected: 1, NoTools: 1}},
	}
	for _, tc := range cases {
		if got := totalMissedTokens(tc.series); got != tc.tot {
			t.Errorf("%s: totals = %+v, want %+v", tc.name, got, tc.tot)
		}
	}
}

// TestWriteCacheMissListNoToolsKind verifies the misses table renders the
// distinct kind label end-to-end.
func TestWriteCacheMissListNoToolsKind(t *testing.T) {
	var b strings.Builder
	writeCacheMissList(&b, []cacheTurn{
		{Num: 1, PromptN: 10, CacheRead: 80768},
		{Num: 1, PromptN: 1, CacheRead: 0, TextOnlyCollapse: true},
	})
	out := b.String()
	if !strings.Contains(out, "| T1 | no-tools step | 100.0% | 80,768 |") {
		t.Errorf("miss list must render the no-tools step kind:\n%s", out)
	}
	if strings.Contains(out, "unexpected") {
		t.Errorf("collapse bust must not render as unexpected:\n%s", out)
	}
}

// TestMissedTokensLineNoToolsSegment verifies the headline names no-tools
// events in their own segment and that sessions without them keep the exact
// established format.
func TestMissedTokensLineNoToolsSegment(t *testing.T) {
	line := missedTokensLine(missTotals{Tokens: 80768, Events: 1, NoTools: 1})
	if !strings.Contains(line, "80,768 across 1 exchange(s) (0 unexpected, 0 partial, 1 no-tools)") {
		t.Errorf("headline must name the no-tools event: %q", line)
	}
	line = missedTokensLine(missTotals{Tokens: 8000, Events: 1, Unexpected: 1})
	if !strings.Contains(line, "(1 unexpected, 0 partial)") || strings.Contains(line, "no-tools") {
		t.Errorf("established headline format changed: %q", line)
	}
}

// TestCacheSeriesMappingTextOnlyCollapse verifies the flag plumbing into the
// view: the completion log carries TextOnlyCollapse into the cache series,
// while the legacy turn series (no per-call provenance) never invents it.
func TestCacheSeriesMappingTextOnlyCollapse(t *testing.T) {
	comps := []core.CompletionRecord{
		{TurnNumber: 1, PromptN: 10, CacheRead: 80768},
		{TurnNumber: 1, PromptN: 1, TextOnlyCollapse: true},
	}
	series := cacheCompletionsFromHistory(comps)
	if series[0].TextOnlyCollapse || !series[1].TextOnlyCollapse {
		t.Errorf("completion→cacheTurn mapping lost the collapse flag: %+v", series)
	}
	turns := cacheTurnsFromHistory(cacheTurns([3]int{10, 80768, 0}), nil)
	if turns[0].TextOnlyCollapse {
		t.Error("turn series must not invent a collapse flag")
	}
}

// TestShowCacheStatsNoToolsScenario replays the RCA export scenario
// end-to-end through the rendered view: established prefix (read 80,768),
// then the P7 collapse bust (read → 0, flag set), then the next turn
// re-warming. The misses table must answer "real miss or switch of context"
// at a glance: the bust shows as "no-tools step", the unexpected counters
// stay at zero, and the headline keeps the recomputed tokens visible.
func TestShowCacheStatsNoToolsScenario(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: []core.TurnRecord{
			{Number: 1, AgentRole: "main", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 80768}},
			{Number: 2, AgentRole: "main", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 60000}},
		},
		completions: []core.CompletionRecord{
			{TurnNumber: 1, AgentRole: "main", PromptN: 10, CacheRead: 80768},
			{TurnNumber: 1, AgentRole: "main", PromptN: 1, TextOnlyCollapse: true},
			{TurnNumber: 2, AgentRole: "main", PromptN: 10, CacheRead: 60000},
		},
	}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("showCacheStats: %v", err)
	}
	report := w.Text()
	if !strings.Contains(report, "| T1 | no-tools step | 100.0% | 80,768 |") {
		t.Errorf("misses table lacks the no-tools step row:\n%s", report)
	}
	if !strings.Contains(report, "(0 unexpected, 0 partial, 1 no-tools)") {
		t.Errorf("global headline must exclude the no-tools event from unexpected/partial:\n%s", report)
	}
	if strings.Contains(report, "| T1 | unexpected |") {
		t.Errorf("collapse bust must not file under unexpected:\n%s", report)
	}
}

// TestShowCacheStatsNoToolsScenarioMDPipeline runs the same RCA scenario
// through the MD pipeline (the way /stats:cache actually renders on screen)
// and verifies the distinct kind label survives rendering in the misses
// table (plan validation step 4).
func TestShowCacheStatsNoToolsScenarioMDPipeline(t *testing.T) {
	rec := &fakeSessionRecorder{
		history: []core.TurnRecord{
			{Number: 1, AgentRole: "main", TokenUsage: core.TurnTokenUsage{PromptN: 10, CacheRead: 80768}},
		},
		completions: []core.CompletionRecord{
			{TurnNumber: 1, AgentRole: "main", PromptN: 10, CacheRead: 80768},
			{TurnNumber: 1, AgentRole: "main", PromptN: 1, TextOnlyCollapse: true},
		},
	}
	w := newWriter()
	if err := showCacheStats(w, rec, nil); err != nil {
		t.Fatalf("showCacheStats: %v", err)
	}
	r := tui.NewMDStreamRenderer(80, tui.DarkTheme())
	rendered := strings.Join(r.Render(w.Text()), "\n")
	if !strings.Contains(rendered, "no-tools step") {
		t.Errorf("rendered misses table lacks the no-tools step kind:\n%s", rendered)
	}
	// The ONLY legitimate "unexpected" mention is the headline's zero count
	// — no miss row may classify the collapse bust unexpected.
	if n := strings.Count(rendered, "unexpected"); n != 1 || !strings.Contains(rendered, "(0 unexpected") {
		t.Errorf("rendered report must classify the collapse bust only as no-tools step:\n%s", rendered)
	}
	if !strings.Contains(rendered, "80,768") {
		t.Errorf("rendered report must keep the recomputed tokens visible:\n%s", rendered)
	}
}
