// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

// ForModel returns a copy of the snapshot with GoalID removed.
// The model never needs the goal ID — there's only ever one goal,
// and no tool accepts a goal ID as input.
func ForModel(snapshot GoalSnapshot) GoalSnapshot {
	snapshot.GoalID = ""
	return snapshot
}

// ResultForModel returns a GoalToolResult with GoalID stripped.
func ResultForModel(result GoalToolResult) GoalToolResult {
	if result.Goal == nil {
		return result
	}
	stripped := ForModel(*result.Goal)
	return GoalToolResult{Goal: &stripped}
}
