// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import promptgoal "github.com/pijalu/goa/prompts/goal"

// GoalDescription returns the LLM-facing description for the unified goal tool.
func GoalDescription() string { return promptgoal.GoalDescription() }
