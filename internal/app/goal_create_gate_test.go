// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// TestGoalTool_CreateGateFollowsLiveConfig pins the requirement that the goal
// tool's `create` gate is LIVE, not captured at construction: a tool built
// while tools.enabled.goal is false must accept `create` as soon as the flag is
// flipped in-session (/config → Tools, /tools:goal:on, /config:set
// tools.enabled.goal true) — with no re-registration and no restart. Before the
// fix newGoalTool took a plain bool, so the registered tool kept the stale
// `false` and `create` kept failing.
func TestGoalTool_CreateGateFollowsLiveConfig(t *testing.T) {
	cfg := &config.Config{} // zero value: tools.enabled.goal == false
	gm := core.NewGoalManager(t.TempDir())
	tool := newGoalTool(gm, goalCreateGate(cfg, RuntimeOptions{}), nil, nil, nil)

	if _, err := tool.Execute(`{"action":"create","objective":"must be blocked"}`); err == nil {
		t.Fatal("create must be rejected while tools.enabled.goal is false")
	}
	if gm.Mode.GetActiveGoal() != nil {
		t.Fatal("a rejected create must not have started a goal")
	}

	// The live flip: exactly what the /config menu, /tools:goal:on and
	// /config:set tools.enabled.goal true do to the shared config.
	cfg.Tools.Enabled.SetEnabled("goal", true)

	out, err := tool.Execute(`{"action":"create","objective":"allowed after flip"}`)
	if err != nil {
		t.Fatalf("create must succeed after the live flag flip without re-registration: %v", err)
	}
	if !strings.Contains(out, "allowed after flip") {
		t.Errorf("create output missing the objective: %q", out)
	}
	if gm.Mode.GetActiveGoal() == nil {
		t.Error("create after the live flip must start a goal")
	}
}

// TestMakeToolFactory_GoalCreateGateFollowsLiveConfig is the same contract on
// the runtime factory path: the tool the /tools:goal:on factory builds reads
// the live config, so it honours a flip made after it was constructed.
func TestMakeToolFactory_GoalCreateGateFollowsLiveConfig(t *testing.T) {
	cfg := &config.Config{} // goal OFF
	gm := core.NewGoalManager(t.TempDir())
	subs := &subsystems{cfg: cfg, goalManager: gm}

	tool, ok := makeToolFactory(subs)("goal")
	if !ok || tool == nil {
		t.Fatalf("factory did not build the goal tool: ok=%v tool=%v", ok, tool)
	}
	if _, err := tool.Execute(`{"action":"create","objective":"must be blocked"}`); err == nil {
		t.Fatal("runtime goal tool must reject create while the flag is off")
	}

	cfg.Tools.Enabled.SetEnabled("goal", true)
	if _, err := tool.Execute(`{"action":"create","objective":"allowed"}`); err != nil {
		t.Fatalf("runtime goal tool must follow the live flag flip: %v", err)
	}
}

// TestGoalCreateGate_ForceOnByFlag pins the --goal headless force-enable on the
// SAME live gate the registered tool uses (previously the startup path ORed
// opts.Goal into a captured bool; the runtime factory path had no force-on).
func TestGoalCreateGate_ForceOnByFlag(t *testing.T) {
	cfg := &config.Config{}
	gate := goalCreateGate(cfg, RuntimeOptions{Goal: true})
	if !gate() {
		t.Fatal("--goal must force-open the create gate")
	}
	// Flipping the config flag off must not close a force-enabled gate.
	cfg.Tools.Enabled.SetEnabled("goal", false)
	if !gate() {
		t.Error("--goal force-enable must survive a config flip back to false")
	}
}
