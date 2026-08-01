// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

// TestGoalDescriptionGuard is a build-time context guard: the goal tool
// description ships with every LLM request when the goal tool is enabled.
// It must stay dense and must never carry the SPDX license header (stripped
// at load — see GoalDescription).
func TestGoalDescriptionGuard(t *testing.T) {
	d := GoalDescription()
	const ceiling = 3600
	if len(d) > ceiling {
		t.Errorf("goal description = %d chars, ceiling %d — keep it dense; it ships with every request", len(d), ceiling)
	}
	if len(d) < 1000 {
		t.Errorf("goal description = %d chars — suspiciously small; did the rules survive?", len(d))
	}
	for _, banned := range []string{"SPDX-License-Identifier", "Copyright (C)", "<!--"} {
		if strings.Contains(d, banned) {
			t.Errorf("goal description contains %q — comments must be stripped", banned)
		}
	}
	// Load-bearing contracts that must survive any future compression.
	for _, required := range []string{"create", "update", "complete", "blocked", "expectation", "verifyCommand", "add_todo", "update_todo", "postpone", "promote"} {
		if !strings.Contains(d, required) {
			t.Errorf("goal description lost required contract keyword %q", required)
		}
	}
}
