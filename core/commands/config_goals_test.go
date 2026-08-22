// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
)

// goalsMenuTestContext extends newMenuTestContext with a live goal subsystem:
// a GoalManager bound to a temp dir plus a stall-capable driver, mirroring the
// production wiring (coreContextForCommand + configureGoalMode, which applies
// the configured limits to the mode at startup). mutate customizes the config
// before that initial sync.
func goalsMenuTestContext(t *testing.T, mutate func(*config.Config)) (*core.Context, *selectRecorder, *inputRecorder, *core.GoalManager, *core.GoalDriver) {
	t.Helper()
	ctx, sr, ir, _ := newMenuTestContext(t, nil)
	if mutate != nil {
		mutate(ctx.Config)
	}
	mgr := core.NewGoalManager(filepath.Join(t.TempDir(), ".goa"))
	driver := &core.GoalDriver{
		Mode:       mgr.Mode,
		Probe:      func() string { return "fp" },
		Remind:     func(string) {},
		StallTurns: 5,
	}
	ctx.GoalManager = mgr
	ctx.GoalDriver = driver
	syncGoalLimits(*ctx)
	return ctx, sr, ir, mgr, driver
}

func TestGoalLimitLabels(t *testing.T) {
	tests := []struct {
		name          string
		budget        int
		stall         int
		wantBudget    string
		wantStallDesc string
	}{
		{"defaults", 50, 5, "50 turns", "5 turns"},
		{"unlimited budget", -1, 0, "unlimited", "disabled"},
		{"unset budget", 0, -1, "unset", "disabled"},
		{"custom", 120, 9, "120 turns", "9 turns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := goalTurnBudgetLabel(tt.budget); got != tt.wantBudget {
				t.Errorf("goalTurnBudgetLabel(%d) = %q, want %q", tt.budget, got, tt.wantBudget)
			}
			if got := goalStallTurnsLabel(tt.stall); got != tt.wantStallDesc {
				t.Errorf("goalStallTurnsLabel(%d) = %q, want %q", tt.stall, got, tt.wantStallDesc)
			}
		})
	}
}

// TestConfigMenu_GoalsMenuShowsLimitRows verifies /config -> Goals exposes the
// two numeric goal limits alongside the existing settings.
func TestConfigMenu_GoalsMenuShowsLimitRows(t *testing.T) {
	ctx, sr, _, _, _ := goalsMenuTestContext(t, func(cfg *config.Config) {
		cfg.Goals.DefaultTurnBudget = 50
		cfg.Goals.StallTurns = 5
	})
	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("goals", true)

	if sr.title != "Goals:" {
		t.Fatalf("title = %q, want Goals:", sr.title)
	}
	want := []string{"enabled", "days", "auto_unblock", "fresh_context", "turn_budget", "stall_turns"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d goals items, got %d: %+v", len(want), len(sr.options), sr.options)
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("item[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
	if desc := sr.options[4].Description; desc != "50 turns" {
		t.Errorf("turn_budget description = %q, want %q", desc, "50 turns")
	}
	if desc := sr.options[5].Description; desc != "5 turns" {
		t.Errorf("stall_turns description = %q, want %q", desc, "5 turns")
	}
}

// TestConfigMenu_TurnBudgetEditAppliesAndPersists drives the full edit flow:
// prompt pre-filled with the current value, config updated on submit, runtime
// default applied to the goal mode (a fresh goal inherits it), and the value
// persisted into the home config.
func TestConfigMenu_TurnBudgetEditAppliesAndPersists(t *testing.T) {
	ctx, sr, ir, mgr, _ := goalsMenuTestContext(t, func(cfg *config.Config) {
		cfg.Goals.DefaultTurnBudget = 50
	})
	menu := newConfigMenu(*ctx)

	_ = menu.showRoot()
	sr.onSel("goals", true)
	sr.onSel("turn_budget", true)

	if !strings.Contains(ir.prompt, "turn budget") {
		t.Errorf("prompt = %q, want mention of turn budget", ir.prompt)
	}
	if ir.current != "50" {
		t.Errorf("prompt current = %q, want 50", ir.current)
	}

	ir.onSub("20", true)

	if ctx.Config.Goals.DefaultTurnBudget != 20 {
		t.Errorf("config DefaultTurnBudget = %d, want 20", ctx.Config.Goals.DefaultTurnBudget)
	}
	// Runtime effect: a newly created goal carries a 20-turn ceiling.
	snap, err := mgr.Mode.CreateGoal(goal.CreateGoalInput{Objective: "probe"}, goal.GoalActorUser)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if snap.Budget.TurnBudget == nil || *snap.Budget.TurnBudget != 20 {
		t.Errorf("new goal turn budget = %v, want 20", snap.Budget.TurnBudget)
	}
	// Persisted to the home config layer.
	home := os.Getenv("HOME")
	data, err := os.ReadFile(filepath.Join(home, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	if !strings.Contains(string(data), "default_turn_budget: 20") {
		t.Errorf("home config does not contain default_turn_budget: 20:\n%s", data)
	}
}

// TestConfigMenu_TurnBudgetBelowContractRejected and
// TestConfigMenu_TurnBudgetNonNumericRejected verify out-of-contract values
// (< -1, non-numeric) are refused without changing state.
func TestConfigMenu_TurnBudgetBelowContractRejected(t *testing.T) {
	rejectTurnBudgetEdit(t, "-2")
}

func TestConfigMenu_TurnBudgetNonNumericRejected(t *testing.T) {
	rejectTurnBudgetEdit(t, "abc")
}

// rejectTurnBudgetEdit submits an invalid turn-budget value through the Goals
// menu flow and asserts config, menu description, and live goal mode are all
// unchanged.
func rejectTurnBudgetEdit(t *testing.T, input string) {
	t.Helper()
	ctx, sr, ir, mgr, _ := goalsMenuTestContext(t, func(cfg *config.Config) {
		cfg.Goals.DefaultTurnBudget = 50
	})
	menu := newConfigMenu(*ctx)

	_ = menu.showRoot()
	sr.onSel("goals", true)
	sr.onSel("turn_budget", true)
	ir.onSub(input, true)

	if ctx.Config.Goals.DefaultTurnBudget != 50 {
		t.Errorf("config DefaultTurnBudget = %d, want unchanged 50", ctx.Config.Goals.DefaultTurnBudget)
	}
	// The menu re-opens on the Goals page with the old description.
	if sr.title != "Goals:" {
		t.Fatalf("title = %q, want Goals: (menu re-opened)", sr.title)
	}
	if desc := sr.options[4].Description; desc != "50 turns" {
		t.Errorf("turn_budget description = %q, want 50 turns", desc)
	}
	snap, err := mgr.Mode.CreateGoal(goal.CreateGoalInput{Objective: "probe"}, goal.GoalActorUser)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if snap.Budget.TurnBudget == nil || *snap.Budget.TurnBudget != 50 {
		t.Errorf("new goal turn budget = %v, want 50 (unchanged)", snap.Budget.TurnBudget)
	}
}

// TestConfigMenu_StallTurnsEditSyncsDriver verifies editing the stall watchdog
// updates the live driver, including disabling it, and that enabling has no
// effect when the probe was never wired (watchdog disabled at startup).
func TestConfigMenu_StallTurnsEditSyncsDriver(t *testing.T) {
	ctx, sr, ir, _, driver := goalsMenuTestContext(t, nil)
	menu := newConfigMenu(*ctx)

	_ = menu.showRoot()
	sr.onSel("goals", true)
	sr.onSel("stall_turns", true)
	ir.onSub("3", true)

	if ctx.Config.Goals.StallTurns != 3 {
		t.Errorf("config StallTurns = %d, want 3", ctx.Config.Goals.StallTurns)
	}
	if driver.StallTurns != 3 {
		t.Errorf("driver StallTurns = %d, want 3 (live sync)", driver.StallTurns)
	}

	// Disable live.
	sr.onSel("stall_turns", true)
	ir.onSub("0", true)
	if driver.StallTurns != 0 {
		t.Errorf("driver StallTurns = %d, want 0 after disable", driver.StallTurns)
	}

	// Re-enable with no probe wired stays off (cannot construct the probe at
	// runtime outside app startup).
	driver.Probe = nil
	sr.onSel("stall_turns", true)
	ir.onSub("7", true)
	if driver.StallTurns != 0 {
		t.Errorf("driver StallTurns = %d, want 0 when probe is unwired", driver.StallTurns)
	}
}

// TestSyncRuntimeConfig_GoalLimitsCLI covers the /config:set path end-to-end:
// known keys apply to the live goal subsystem and persist; values violating
// validation are rejected wholesale.
func TestSyncRuntimeConfig_GoalLimitsCLI(t *testing.T) {
	ctx, _, _, mgr, driver := goalsMenuTestContext(t, nil)
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(*ctx, "goals.default_turn_budget", "25"); err != nil {
		t.Fatalf("applyConfigSet turn budget: %v", err)
	}
	if ctx.Config.Goals.DefaultTurnBudget != 25 {
		t.Errorf("DefaultTurnBudget = %d, want 25", ctx.Config.Goals.DefaultTurnBudget)
	}
	snap, err := mgr.Mode.CreateGoal(goal.CreateGoalInput{Objective: "probe"}, goal.GoalActorUser)
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if snap.Budget.TurnBudget == nil || *snap.Budget.TurnBudget != 25 {
		t.Errorf("new goal turn budget = %v, want 25", snap.Budget.TurnBudget)
	}

	if err := applyConfigSet(*ctx, "goals.stall_turns", "7"); err != nil {
		t.Fatalf("applyConfigSet stall turns: %v", err)
	}
	if driver.StallTurns != 7 {
		t.Errorf("driver StallTurns = %d, want 7", driver.StallTurns)
	}

	// Validation rejects values below -1 before commit.
	if err := applyConfigSet(*ctx, "goals.default_turn_budget", "-2"); err != nil {
		t.Fatalf("applyConfigSet invalid: %v", err)
	}
	if ctx.Config.Goals.DefaultTurnBudget != 25 {
		t.Errorf("DefaultTurnBudget = %d, want 25 after rejected set", ctx.Config.Goals.DefaultTurnBudget)
	}
}

// TestSetConfigField_ExecutionLimits verifies the previously missing
// execution-limit keys are customizable via /config:set like their YAML
// counterparts.
func TestSetConfigField_ExecutionLimits(t *testing.T) {
	cfg := &config.Config{}
	tests := []struct {
		key, value string
	}{
		{"execution.max_tool_error_streak", "6"},
		{"execution.tool_call_limit_reset_window", "12"},
		{"execution.max_stream_rounds", "40"},
	}
	for _, tc := range tests {
		if err := setConfigField(cfg, strings.Split(tc.key, "."), tc.value); err != nil {
			t.Fatalf("setConfigField(%s): %v", tc.key, err)
		}
	}
	if cfg.Execution.MaxToolErrorStreak != 6 {
		t.Errorf("MaxToolErrorStreak = %d, want 6", cfg.Execution.MaxToolErrorStreak)
	}
	if cfg.Execution.ToolCallLimitResetWindow != 12 {
		t.Errorf("ToolCallLimitResetWindow = %d, want 12", cfg.Execution.ToolCallLimitResetWindow)
	}
	if cfg.Execution.MaxStreamRounds != 40 {
		t.Errorf("MaxStreamRounds = %d, want 40", cfg.Execution.MaxStreamRounds)
	}
}
