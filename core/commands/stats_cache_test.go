// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
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

func TestBucketCacheTurns(t *testing.T) {
	t.Run("turns under the cap stay unmerged (raw per-completion rates)", func(t *testing.T) {
		// Anthropic-style turns: 300/400=75% and 100/100=50% — one bar each.
		turns := cacheTurns([3]int{0, 300, 100}, [3]int{0, 100, 100})
		b := bucketCacheTurns(cacheTurnsFromHistory(turns, nil), 40)
		if len(b) != 2 || b[0].Pct != 75 || b[1].Pct != 50 {
			t.Errorf("buckets = %+v, want two raw-rate bars 75/50", b)
		}
	})

	t.Run("merged buckets are token-weighted", func(t *testing.T) {
		// Force a merge: 2 turns into 1 bucket. Token-weighted rate is
		// 400/(400+200) = 66.7%, not the mean of rates (62.5%).
		turns := cacheTurns([3]int{0, 300, 100}, [3]int{0, 100, 100})
		b := bucketCacheTurns(cacheTurnsFromHistory(turns, nil), 1)
		want := float64(400) / float64(600) * 100
		if len(b) != 1 || b[0].Pct < want-0.01 || b[0].Pct > want+0.01 || b[0].Rows != 2 {
			t.Errorf("buckets = %+v, want one bucket Pct≈%.1f Rows=2", b, want)
		}
	})

	t.Run("buckets cap at maxBuckets", func(t *testing.T) {
		var triples [][3]int
		for i := 0; i < 100; i++ {
			triples = append(triples, [3]int{100, 50, 0}) // OpenAI-style 50/150 = 33.3%
		}
		b := bucketCacheTurns(cacheTurnsFromHistory(cacheTurns(triples...), nil), 10)
		if len(b) > 10 {
			t.Errorf("len(buckets) = %d, want <= 10", len(b))
		}
		total := 0
		for _, bk := range b {
			total += bk.Rows
		}
		if total != 100 {
			t.Errorf("bucket rows total = %d, want 100 (no rows lost)", total)
		}
	})

	t.Run("never-caching turns excluded", func(t *testing.T) {
		turns := cacheTurns([3]int{500, 0, 0}, [3]int{600, 0, 0}, [3]int{0, 100, 100})
		b := bucketCacheTurns(cacheTurnsFromHistory(turns, nil), 40)
		if len(b) != 1 || b[0].FirstTurn != 3 {
			t.Errorf("buckets = %+v, want only the cache-active turn 3", b)
		}
	})

	t.Run("empty when no cache activity", func(t *testing.T) {
		if b := bucketCacheTurns(cacheTurnsFromHistory(cacheTurns([3]int{100, 0, 0}), nil), 40); b != nil {
			t.Errorf("buckets = %+v, want nil", b)
		}
	})
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

// TestStatsCommand_CacheView covers the routed /stats:cache output: chart
// bars with rates and the drop table with before/after columns.
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
		"Cache hit rate evolution — this session",
		"75.0%", "80.0%",
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
