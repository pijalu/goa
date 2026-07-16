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

// The built-in model registry carries per-token cost data (including cache
// read/write rates), but applyPricing only read the user's YAML PricingConfig
// — so cache-aware cost was invisible unless the user hand-configured rates.
// The bridge must fall back to the registry (converted per-token → per-1M)
// when no YAML pricing is set, while always honoring an explicit override.
func TestResolvePricing_BuiltinRegistryFallback(t *testing.T) {
	t.Run("falls back to built-in registry for a known model", func(t *testing.T) {
		cfg := &config.Config{
			Models: []config.ModelConfig{
				{ID: "sonnet", Model: "claude-sonnet-4-20250514"}, // no YAML pricing
			},
		}
		p := resolvePricing(cfg, "sonnet")
		require.NotNil(t, p, "known built-in model must resolve pricing from the registry")
		// claude-sonnet-4: input $3/1M, output $15/1M, cache_read $0.30/1M, cache_write $3.75/1M.
		assert.InDelta(t, 3.0, p.InputPer1M, 1e-6)
		assert.InDelta(t, 15.0, p.OutputPer1M, 1e-6)
		assert.InDelta(t, 0.30, p.CacheReadPer1M, 1e-6, "cache read rate must come from the registry")
		assert.InDelta(t, 3.75, p.CacheWritePer1M, 1e-6, "cache write rate must come from the registry")
	})

	t.Run("honors explicit YAML pricing over the registry", func(t *testing.T) {
		cfg := &config.Config{
			Models: []config.ModelConfig{
				{
					ID:    "sonnet",
					Model: "claude-sonnet-4-20250514",
					Pricing: &config.PricingConfig{
						InputPer1M: 99.0, OutputPer1M: 88.0, CacheReadPer1M: 7.0,
					},
				},
			},
		}
		p := resolvePricing(cfg, "sonnet")
		require.NotNil(t, p)
		assert.InDelta(t, 99.0, p.InputPer1M, 1e-6, "user override must win over the registry")
		assert.InDelta(t, 7.0, p.CacheReadPer1M, 1e-6)
	})

	t.Run("returns nil for an unknown model", func(t *testing.T) {
		cfg := &config.Config{
			Models: []config.ModelConfig{{ID: "custom", Model: "my-local-finetune-9000"}},
		}
		assert.Nil(t, resolvePricing(cfg, "custom"))
	})

	t.Run("matches by config ID when Model name is unset", func(t *testing.T) {
		cfg := &config.Config{
			Models: []config.ModelConfig{{ID: "claude-sonnet-4-20250514"}}, // Model unset
		}
		p := resolvePricing(cfg, "claude-sonnet-4-20250514")
		require.NotNil(t, p)
		assert.InDelta(t, 3.0, p.InputPer1M, 1e-6)
	})
}

// End-to-end: a cache-hit turn on a known Anthropic model must now produce a
// non-zero, cache-discounted cost WITHOUT any YAML pricing configured.
func TestApplyPricing_BuiltinCacheCostVisible(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelConfig{{ID: "sonnet", Model: "claude-sonnet-4-20250514"}},
	}
	st := &sessionStats{
		PromptN:         2_000,   // small fresh input
		PredictedN:      500,     // output
		CacheReadTotal:  100_000, // big cache hit
		CacheWriteTotal: 0,
	}
	applyPricing(st, cfg, "sonnet")
	assert.True(t, st.ShowCost, "cost must be shown for a known model without YAML pricing")
	// 2000/1e6*3 + 500/1e6*15 + 100000/1e6*0.30 = 0.006 + 0.0075 + 0.03 = 0.0435
	assert.InDelta(t, 0.0435, st.CostUSD, 1e-9, "cache-aware cost from the registry")
}
