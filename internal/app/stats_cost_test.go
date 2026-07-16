// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// computeCost must charge each token bucket at its own rate. Cache reads are
// dramatically cheaper than fresh input (Anthropic cache_read ≈ 1/10 input);
// cache writes carry a premium. Charging everything at the plain input rate
// both overstates cache-hit turns (no visible savings) and understates the
// cache-write premium. Regression test for the bug where cache buckets were
// ignored entirely in cost computation.
func TestComputeCost_CacheBuckets(t *testing.T) {
	pricing := &config.PricingConfig{
		InputPer1M:      3.0,   // $3 / 1M fresh input
		OutputPer1M:     15.0,  // $15 / 1M output
		CacheReadPer1M:  0.30,  // $0.30 / 1M cache reads (1/10 input)
		CacheWritePer1M: 3.75,  // $3.75 / 1M cache writes (1.25x input)
	}

	t.Run("no cache: input+output only", func(t *testing.T) {
		// 1M fresh in @3 + 1M out @15 = $18.
		got := computeCost(1_000_000, 1_000_000, 0, 0, pricing)
		assert.InDelta(t, 18.0, got, 1e-9)
	})

	t.Run("cache reads charged at cache-read rate, not input rate", func(t *testing.T) {
		// 1M cache reads should cost $0.30, NOT $3.00 (the input rate).
		got := computeCost(0, 0, 1_000_000, 0, pricing)
		assert.InDelta(t, 0.30, got, 1e-9, "cache reads must use the cheap cache-read rate")
	})

	t.Run("cache writes charged at cache-write premium", func(t *testing.T) {
		// 1M cache writes should cost $3.75 (the write premium over input).
		got := computeCost(0, 0, 0, 1_000_000, pricing)
		assert.InDelta(t, 3.75, got, 1e-9, "cache writes must use the write premium")
	})

	t.Run("mixed turn sums all buckets", func(t *testing.T) {
		// 1M fresh in @3 + 1M out @15 + 1M read @0.30 + 1M write @3.75 = $22.05.
		got := computeCost(1_000_000, 1_000_000, 1_000_000, 1_000_000, pricing)
		assert.InDelta(t, 22.05, got, 1e-9)
	})

	t.Run("nil pricing is free", func(t *testing.T) {
		assert.Zero(t, computeCost(1_000_000, 1_000_000, 1_000_000, 1_000_000, nil))
	})
}

// A cache-hit turn must cost strictly less than the equivalent uncached turn —
// the savings are the whole point of prompt caching and must be visible.
func TestComputeCost_CacheHitCheaperThanUncached(t *testing.T) {
	pricing := &config.PricingConfig{
		InputPer1M:     3.0,
		OutputPer1M:    15.0,
		CacheReadPer1M: 0.30,
	}
	// Same 10k prompt served fresh vs. served from cache (plus a small output).
	uncached := computeCost(10_000, 500, 0, 0, pricing)
	cached := computeCost(0, 500, 10_000, 0, pricing)
	require.Less(t, cached, uncached, "a cache-hit turn must cost less than the uncached turn")
}
