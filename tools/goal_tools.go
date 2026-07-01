// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
	goaltools "github.com/pijalu/goa/tools/goal"
)

// NewGoalTools creates the agent-facing goal tools bound to the given GoalMode.
// The reminder callback is used by UpdateGoal to inject completion/block summaries.
func NewGoalTools(mode *goal.GoalMode, reminderFn func(string)) []agentic.Tool {
	return []agentic.Tool{
		&goaltools.CreateGoalTool{Mode: mode},
		&goaltools.UpdateGoalTool{Mode: mode, ReminderFn: reminderFn},
		&goaltools.GetGoalTool{Mode: mode},
		&goaltools.SetGoalBudgetTool{Mode: mode},
	}
}
