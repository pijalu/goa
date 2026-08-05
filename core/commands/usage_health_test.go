// SPDX-License-Identifier: GPL-3.0-or-later

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/usage"
)

// TestUsageCommand_FreshCachedHealth covers the E3 enhancement (ENHANCE.md):
// the global /usage block must surface the fresh-vs-cached spend ratio so a
// user can see the expensive (fresh) share at a glance. Fresh input tokens are
// the costly currency; cache-read tokens are cheap — the ratio is the health
// metric. Pure function of existing aggregates (no schema change).
func TestUsageCommand_FreshCachedHealth(t *testing.T) {
	t.Run("shows fresh share and per-turn ratio", func(t *testing.T) {
		var buf strings.Builder
		// 4 turns: 6,000 fresh input total, 400,000 cache-read -> fresh share of
		// (fresh+cached) is 6000/406000 ~= 1.5%, per-turn 1.5K fresh : 100K cached.
		store := &fakeUsageStore{
			sum: usage.Stat{Turns: 4, PromptN: 6000, PredictedN: 800, CacheRead: 400000, CacheWrite: 0},
			stats: map[string][]usage.Stat{
				dimKey(usage.ByModel, ""): {{Key: "glm-5.2", Turns: 4, PromptN: 6000, PredictedN: 800, CacheRead: 400000}},
			},
		}
		cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
		if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "Fresh:") {
			t.Errorf("global block must show a Fresh spend line:\n%s", out)
		}
		if !strings.Contains(out, "1.5%") {
			t.Errorf("global block must show the fresh share percent (1.5%%):\n%s", out)
		}
	})

	t.Run("no cache reported shows full-fresh", func(t *testing.T) {
		var buf strings.Builder
		store := &fakeUsageStore{
			sum: usage.Stat{Turns: 2, PromptN: 2000, PredictedN: 500, CacheRead: 0, CacheWrite: 0},
			stats: map[string][]usage.Stat{
				dimKey(usage.ByModel, ""): {{Key: "local", Turns: 2, PromptN: 2000, PredictedN: 500}},
			},
		}
		cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
		if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
		out := buf.String()
		// No cache-read: fresh share of input is 100% — still meaningful.
		if !strings.Contains(out, "Fresh:") {
			t.Errorf("cache-less provider must still show the Fresh line:\n%s", out)
		}
	})
}

// TestUsageCommand_BustCount covers the E2 enhancement (ENHANCE.md): the
// global /usage block must surface the provider cache-bust count for the
// range. A bust is a turn whose cache_read collapsed to ~0 after the cache
// was established (provider TTL expiry / prefix invalidation) — it converts
// cheap cached re-reads into expensive fresh input. Derived from the existing
// cache_read column (no schema change).
func TestUsageCommand_BustCount(t *testing.T) {
	var buf strings.Builder
	store := &fakeUsageStore{
		sum: usage.Stat{Turns: 10, PromptN: 20000, PredictedN: 3000, CacheRead: 900000},
		stats: map[string][]usage.Stat{
			dimKey(usage.ByModel, ""): {{Key: "glm-5.2", Turns: 10, PromptN: 20000, PredictedN: 3000, CacheRead: 900000}},
		},
		busts: 2,
	}
	cmd := &UsageCommand{OpenStore: func() (usageStore, error) { return store, nil }}
	if err := cmd.Run(newUsageCtx(&buf, "/a"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Busts: 2") {
		t.Errorf("global block must show the cache-bust count (Busts: 2):\n%s", out)
	}
}
