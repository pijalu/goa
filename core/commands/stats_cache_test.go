// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/usage"
)

// cacheRows builds a chronological row series from (prompt, read, write)
// triples, one minute apart, all on the same model.
func cacheRows(model string, base time.Time, triples ...[3]int) []usage.Record {
	out := make([]usage.Record, len(triples))
	for i, tr := range triples {
		out[i] = usage.Record{
			Project:    "/p",
			Provider:   "prov",
			Model:      model,
			PromptN:    tr[0],
			CacheRead:  tr[1],
			CacheWrite: tr[2],
			At:         base.Add(time.Duration(i) * time.Minute),
		}
	}
	return out
}

func TestBucketCacheRows(t *testing.T) {
	base := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)

	t.Run("rows under the cap stay unmerged (raw per-completion rates)", func(t *testing.T) {
		// Anthropic-style rows: 300/400=75% and 100/100=50% — one bar each.
		rows := cacheRows("m", base, [3]int{0, 300, 100}, [3]int{0, 100, 100})
		b := bucketCacheRows(rows, 40)
		if len(b) != 2 || b[0].Pct != 75 || b[1].Pct != 50 {
			t.Errorf("buckets = %+v, want two raw-rate bars 75/50", b)
		}
	})

	t.Run("merged buckets are token-weighted", func(t *testing.T) {
		// Force a merge: 2 rows into 1 bucket. Token-weighted rate is
		// 400/(400+200) = 66.7%, not the mean of rates (62.5%).
		rows := cacheRows("m", base, [3]int{0, 300, 100}, [3]int{0, 100, 100})
		b := bucketCacheRows(rows, 1)
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
		b := bucketCacheRows(cacheRows("m", base, triples...), 10)
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

	t.Run("model switch starts a new bucket", func(t *testing.T) {
		rows := append(cacheRows("a", base, [3]int{0, 100, 100}),
			cacheRows("b", base.Add(time.Minute), [3]int{0, 50, 50})...)
		b := bucketCacheRows(rows, 40)
		if len(b) != 2 || b[0].Model != "a" || b[1].Model != "b" {
			t.Errorf("buckets = %+v, want split by model", b)
		}
	})

	t.Run("never-caching models excluded", func(t *testing.T) {
		rows := append(cacheRows("cold-model", base, [3]int{500, 0, 0}, [3]int{600, 0, 0}),
			cacheRows("warm-model", base.Add(2*time.Minute), [3]int{0, 100, 100})...)
		b := bucketCacheRows(rows, 40)
		if len(b) != 1 || b[0].Model != "warm-model" {
			t.Errorf("buckets = %+v, want only the warm model", b)
		}
	})

	t.Run("empty when no cache activity", func(t *testing.T) {
		if b := bucketCacheRows(cacheRows("m", base, [3]int{100, 0, 0}), 40); b != nil {
			t.Errorf("buckets = %+v, want nil", b)
		}
	})
}

func TestDetectCacheDrops(t *testing.T) {
	base := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)

	t.Run("drop to zero after warm cache detected", func(t *testing.T) {
		// 300/(300+100)=75% then 0% → TTL-expiry bust signature.
		rows := cacheRows("m", base, [3]int{0, 300, 100}, [3]int{0, 400, 100}, [3]int{500, 0, 0})
		drops := detectCacheDrops(rows, 5)
		if len(drops) != 1 {
			t.Fatalf("drops = %+v, want 1", drops)
		}
		d := drops[0]
		if d.Before != 80 || d.After != 0 || d.Model != "m" {
			t.Errorf("drop = %+v, want Before=80 After=0 Model=m", d)
		}
		if !d.At.Equal(base.Add(2 * time.Minute)) {
			t.Errorf("drop.At = %v, want %v", d.At, base.Add(2*time.Minute))
		}
	})

	t.Run("small wobble under threshold ignored", func(t *testing.T) {
		rows := cacheRows("m", base, [3]int{0, 300, 100}, [3]int{0, 295, 105}) // 75% → 73.75%
		if drops := detectCacheDrops(rows, 5); len(drops) != 0 {
			t.Errorf("drops = %+v, want none (<5pts)", drops)
		}
	})

	t.Run("cold first completion of a new model is not a drop", func(t *testing.T) {
		rows := append(cacheRows("a", base, [3]int{0, 300, 100}),
			cacheRows("b", base.Add(time.Minute), [3]int{500, 0, 0})...)
		// "b" never caches → excluded entirely; "a" has one row → no pair.
		if drops := detectCacheDrops(rows, 5); len(drops) != 0 {
			t.Errorf("drops = %+v, want none", drops)
		}
	})

	t.Run("per-model baselines are independent", func(t *testing.T) {
		// Interleaved models: a stays hot, b drops.
		rows := []usage.Record{
			{Model: "a", CacheRead: 300, CacheWrite: 100, At: base},
			{Model: "b", CacheRead: 300, CacheWrite: 100, At: base.Add(time.Minute)},
			{Model: "a", CacheRead: 400, CacheWrite: 100, At: base.Add(2 * time.Minute)},
			{Model: "b", PromptN: 500, At: base.Add(3 * time.Minute)},
		}
		drops := detectCacheDrops(rows, 5)
		if len(drops) != 1 || drops[0].Model != "b" {
			t.Errorf("drops = %+v, want one drop on model b", drops)
		}
	})
}

// TestStatsCommand_CacheView covers the routed /stats:cache output: chart
// bars with rates and the drop table with before/after columns.
func TestStatsCommand_CacheView(t *testing.T) {
	base := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	rows := cacheRows("claude", base,
		[3]int{0, 300, 100}, // 75%
		[3]int{0, 400, 100}, // 80%
		[3]int{500, 0, 0},   // 0% — bust
		[3]int{0, 200, 400}, // 33.3% — recovery
	)
	store := &fakeUsageStore{rows: rows}

	var buf strings.Builder
	cmd := &StatsCommand{OpenStore: func() (usageStore, error) { return store, nil }, ProjectDir: "/p"}
	if err := cmd.Run(newUsageCtx(&buf, "/p"), []string{"cache"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Cache hit rate evolution",
		"75.0%", "80.0%",
		"Cache drops",
		"BEFORE", "AFTER",
		"claude",
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

// TestStatsCommand_CacheViewEmpty covers the no-activity case: no rows with
// cache tokens must not render an empty chart or crash.
func TestStatsCommand_CacheViewEmpty(t *testing.T) {
	store := &fakeUsageStore{}
	var buf strings.Builder
	cmd := &StatsCommand{OpenStore: func() (usageStore, error) { return store, nil }, ProjectDir: "/p"}
	if err := cmd.Run(newUsageCtx(&buf, "/p"), []string{"cache"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(buf.String(), "No cache activity") {
		t.Errorf("expected no-activity message, got:\n%s", buf.String())
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
