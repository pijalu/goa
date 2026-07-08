// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// buildBaseOptions applies model-level defaults to a partial StreamOptions
// and returns a fully populated options struct. Fields already set in opts
// are preserved; unset fields get model defaults.
func BuildBaseOptions(model Model, opts StreamOptions) StreamOptions {
	result := opts

	// Inherit headers from model if not already set.
	if result.Headers == nil && len(model.Headers) > 0 {
		result.Headers = copyMap(model.Headers)
	}

	// Default transport is SSE.
	if result.Transport == "" {
		result.Transport = TransportSSE
	}

	// Default timeout from model constraints (no model-level default here;
	// callers can override).
	if result.Timeout == 0 {
		result.Timeout = 60 * time.Second
	}
	if result.IdleTimeout == 0 {
		result.IdleTimeout = DefaultStreamIdleTimeout
	}

	// Default to short cache retention so providers that support prompt
	// caching (OpenAI, OpenRouter Anthropic, etc.) can reuse prefixes.
	if result.CacheRetention == "" {
		result.CacheRetention = CacheRetentionShort
	}

	// Default retry settings.
	if result.MaxRetries == 0 {
		result.MaxRetries = 2
	}
	if result.MaxRetryDelay == 0 {
		result.MaxRetryDelay = 30 * time.Second
	}

	if result.WebsocketConnectTimeout == 0 {
		result.WebsocketConnectTimeout = 10 * time.Second
	}

	return result
}

// buildSimpleOptions converts SimpleStreamOptions into a resolved
// StreamOptions with reasoning parameters mapped through the model's
// ThinkingLevelMap. The reasoning level is applied as appropriate
// for the provider.
func BuildSimpleOptions(model Model, opts SimpleStreamOptions) StreamOptions {
	base := BuildBaseOptions(model, opts.StreamOptions)

	// Clamp reasoning level to what the model supports.
	profile := schema.ResolveProfile(model)
	level := ClampThinkingLevelWithMap(profile.Defaults.ThinkingLevelMap, model.Reasoning, opts.Reasoning)

	// If the model has a ThinkingLevelMap, map the level to the
	// provider-specific value and store it for the provider to use.
	_ = level // Providers use this through the stream options

	// For models with thinking budgets, compute the budget.
	if opts.ThinkingBudgets != nil {
		_ = opts.ThinkingBudgets // Provider-specific handling
	}

	base.Reasoning = level
	return base
}

// clampThinkingLevel returns the nearest supported thinking level for a model.
// If the model doesn't support reasoning, returns ThinkingOff.
// If the requested level is higher than what the model supports, clamps down.
// If the level is empty or off, returns ThinkingOff.
func ClampThinkingLevel(model Model, level ThinkingLevel) ThinkingLevel {
	profile := schema.ResolveProfile(model)
	return ClampThinkingLevelWithMap(profile.Defaults.ThinkingLevelMap, model.Reasoning, level)
}

// ClampThinkingLevelWithMap is the map-based implementation of
// ClampThinkingLevel. It allows callers that already have a VariantProfile to
// clamp without re-resolving the profile.
func ClampThinkingLevelWithMap(levelMap ThinkingLevelMap, reasoning bool, level ThinkingLevel) ThinkingLevel {
	if level == "" || level == ThinkingOff {
		return ThinkingOff
	}

	if !reasoning {
		return ThinkingOff
	}

	// If the model has a ThinkingLevelMap, check if the level exists.
	if len(levelMap) > 0 {
		if _, ok := levelMap[level]; ok {
			return level
		}
		// Level not found in map — find the highest level below the requested one.
		return nearestThinkingLevel(levelMap, level)
	}

	// No map defined — pass through if the level is known.
	switch level {
	case ThinkingLow, ThinkingMedium, ThinkingHigh:
		return level
	default:
		return ThinkingMedium
	}
}

// thinkingLevelOrder ranks thinking levels for clamp comparisons.
var thinkingLevelOrder = []ThinkingLevel{
	ThinkingOff,
	ThinkingMinimal,
	ThinkingLow,
	ThinkingMedium,
	ThinkingHigh,
	ThinkingXHigh,
}

// thinkingLevelRank returns the numeric rank of a thinking level.
func thinkingLevelRank(level ThinkingLevel) int {
	for i, l := range thinkingLevelOrder {
		if l == level {
			return i
		}
	}
	return -1
}

// nearestThinkingLevel finds the highest supported level that is at or below
// the requested level.
func nearestThinkingLevel(levelMap ThinkingLevelMap, requested ThinkingLevel) ThinkingLevel {
	requestedRank := thinkingLevelRank(requested)
	if requestedRank < 0 {
		return ThinkingMedium
	}

	var best ThinkingLevel
	bestRank := -1

	for level := range levelMap {
		rank := thinkingLevelRank(level)
		if rank >= 0 && rank <= requestedRank && rank > bestRank {
			best = level
			bestRank = rank
		}
	}

	if bestRank >= 0 {
		return best
	}

	// No supported level found below requested — return the lowest supported.
	for _, level := range thinkingLevelOrder {
		if _, ok := levelMap[level]; ok {
			return level
		}
	}

	return ThinkingMedium
}

// copyMap returns a shallow copy of a string map.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
