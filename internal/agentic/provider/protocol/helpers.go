// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import "github.com/pijalu/goa/internal/agentic/provider/schema"

func resolveThinkingBudget(profile schema.VariantProfile) int {
	if profile.Defaults.ThinkingBudgets != nil {
		if b, ok := profile.Defaults.ThinkingBudgets[schema.ThinkingMedium]; ok {
			return b
		}
	}
	return 0
}
