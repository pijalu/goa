// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
	goaltools "github.com/pijalu/goa/tools/goal"
)

// NewGoalTools creates the single agent-facing goal tool bound to the given
// GoalMode. createAllowed gates autonomous goal creation at execution time
// (bugs.md S2): it is consulted only for the `create` action and callers
// typically allow create when the feature flag is on OR a goal is active.
// Terminal transitions are self-describing: the tool enforces the
// terminal-answer contract (reason/expectation) and the done-gate challenge
// inline in its results, so no post-hoc reminder callback is needed.
func NewGoalTools(mode *goal.GoalMode, createAllowed func() bool) []agentic.Tool {
	return []agentic.Tool{
		&goaltools.GoalTool{Mode: mode, CreateAllowed: createAllowed},
	}
}
