// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package metrics holds pure, dependency-free derived metrics shared between
// the app layer (footer stats) and the TUI (orchestrator stats table). Keeping
// them here avoids an import cycle between internal/app and tui/orchestrator
// and gives a single source of truth for token/cache accounting formulas.
package metrics

// CacheHitPct calculates the cache hit percentage from the three token
// counters a provider reports.
//
// When cacheWrite > 0 (Anthropic-style cache_creation_input_tokens), the rate
// is reads / (reads + writes), measuring the fraction of cache operations
// that were hits.
//
// When cacheWrite == 0 (OpenAI-style, where cached tokens are a subset of the
// prompt), the denominator is reads + net prompt tokens (the non-cached
// portion), yielding a meaningful rate instead of always 100%.
//
// Returns 0 when there is no cache activity (cacheRead == 0 && cacheWrite == 0).
func CacheHitPct(cacheRead, cacheWrite, promptN int) float64 {
	if cacheWrite > 0 {
		denom := cacheRead + cacheWrite
		if denom == 0 {
			denom = 1
		}
		return float64(cacheRead) / float64(denom) * 100
	}
	denom := cacheRead + promptN
	if denom == 0 {
		denom = 1
	}
	return float64(cacheRead) / float64(denom) * 100
}
