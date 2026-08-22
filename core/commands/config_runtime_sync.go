// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// Runtime-sync helpers shared by /config:set (applyConfigSet -> syncRuntimeConfig)
// and the interactive /config menu. Each pushes a persisted config change into
// the matching live subsystem so the change takes effect without a restart.

// syncStreamLoopThresholds pushes the stream-loop numeric knobs to the live
// detector (nil-safe; invalid values restore detector defaults).
func syncStreamLoopThresholds(ld *core.LoopDetector, exec config.ExecutionConfig) {
	if ld == nil {
		return
	}
	ld.SetStreamMaxRepeats(exec.StreamLoopMaxRepeats)
	ld.SetStreamMinPeriod(exec.StreamLoopMinPeriod)
}

// syncGoalLimits pushes the two numeric goal limits (goals.default_turn_budget,
// goals.stall_turns) into the live goal subsystem after a config change. It is
// nil-safe on every dependency so headless/test contexts degrade to a no-op.
// The stall watchdog can be tuned or disabled live; enabling it live requires
// the probe wired at startup (initGoalSystem), so a positive value with no
// probe keeps the watchdog off until the next session.
func syncGoalLimits(ctx core.Context) {
	if ctx.GoalManager != nil {
		ctx.GoalManager.Mode.SetDefaultTurnBudget(max(ctx.Config.Goals.DefaultTurnBudget, 0))
	}
	d := ctx.GoalDriver
	if d == nil {
		return
	}
	if s := ctx.Config.Goals.StallTurns; s > 0 && d.Probe != nil && d.Remind != nil {
		d.StallTurns = s
	} else {
		d.StallTurns = 0
	}
}
