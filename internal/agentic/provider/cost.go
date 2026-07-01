// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

// CalculateCost computes the cost of a usage record based on the model's
// pricing and an optional service-tier multiplier. Returns 0 if the model has
// no pricing data.
func CalculateCost(model Model, usage Usage, serviceTier string) float64 {
	pricing := model.Cost
	if pricing.Input == 0 && pricing.Output == 0 {
		return 0
	}

	multiplier := serviceTierMultiplier(serviceTier)
	return costForUsage(pricing, usage) * multiplier
}

func costForUsage(pricing ModelPricing, usage Usage) float64 {
	var cost float64
	cost += tokenCost(usage.InputTokens, pricing.Input)
	cost += tokenCost(usage.OutputTokens, pricing.Output)
	cost += tokenCost(usage.CacheReadTokens, pricing.CacheRead)
	cost += tokenCost(usage.CacheCreationTokens, pricing.CacheWrite)
	cost += tokenCost(usage.CacheWrite1hTokens, pricing.CacheWrite)
	cost += tokenCost(usage.ReasoningTokens, pricing.Output)
	return cost
}

func tokenCost(tokens int, rate float64) float64 {
	if tokens > 0 && rate > 0 {
		return float64(tokens) * rate
	}
	return 0
}

func serviceTierMultiplier(tier string) float64 {
	switch tier {
	case "high":
		return 2.0
	case "low":
		return 0.5
	default:
		return 1.0
	}
}
