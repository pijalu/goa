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

// TestGoalToolEnabledLive_ConfigMenuPath is the same-session, no-restart
// contract driven through the REAL /config → Tools → goal row: with
// tools.enabled.goal false the goal tool is not registered at session start, so
// the menu must construct it through the host factory, register it, push it to
// the running agent — and `create` must then be accepted by the SAME instance,
// because the create gate reads the live config. No restart, no manual
// SetTools: this is the end-to-end contract of bugs.md "Enabling a tool during a
// session does not enable it".
func TestGoalToolEnabledLive_ConfigMenuPath(t *testing.T) {
	cfg := factoryConfig()
	cfg.Tools.Enabled.SetEnabled("goal", false) // goal OFF in this session's live config
	h := newConfigMenuHarness(t, cfg, &probeLiveTool{name: "read"})

	if _, ok := h.subs.toolRegistry.Get("goal"); ok {
		t.Fatal("precondition: the goal tool must not be registered while tools.enabled.goal is false")
	}
	if toolNamesContain(h.agentToolNames(), "goal") {
		t.Fatal("precondition: the agent must not hold the goal tool")
	}

	// /config → Tools → Enabled/disabled tools → goal (the real menu).
	h.pick("goal")
	h.runConfig(t)

	if !cfg.Tools.Enabled.Goal {
		t.Fatal("/config → Tools → goal did not flip tools.enabled.goal")
	}
	registered := mustTool(t, h.subs.toolRegistry, "goal")
	if _, ok := h.subs.toolRegistry.Get("goal"); !ok {
		t.Fatal("/config → Tools → goal did not register the goal tool")
	}
	held := h.findAgentTool("goal")
	if held == nil {
		t.Fatalf("/config → Tools → goal did not push the goal tool to the running agent: %v", h.agentToolNames())
	}
	if held != registered {
		t.Error("the agent holds a different goal tool instance than the registry (rebuilt copy)")
	}

	// The SAME instance, in the SAME session, now accepts create.
	out, err := held.Execute(`{"action":"create","objective":"enabled live from /config"}`)
	if err != nil {
		t.Fatalf("create must succeed in-session after the /config enable (no restart): %v", err)
	}
	if !strings.Contains(out, "enabled live from /config") {
		t.Errorf("create output missing the objective: %q", out)
	}
	if h.subs.goalManager.Mode.GetActiveGoal() == nil {
		t.Error("goal must be active after the in-session create")
	}
}

// TestGoalTool_CreateGateFollowsLiveConfigOff is the OFF direction of the live
// gate: with the flag on and no goal left open, turning tools.enabled.goal off
// in-session must block autonomous create again — with no re-registration.
func TestGoalTool_CreateGateFollowsLiveConfigOff(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("goal", true) // ON
	gm := core.NewGoalManager(t.TempDir())
	tool := newGoalTool(gm, goalCreateGate(cfg, RuntimeOptions{}), nil, nil, nil)

	if _, err := tool.Execute(`{"action":"create","objective":"created while on"}`); err != nil {
		t.Fatalf("create must be allowed while the flag is on: %v", err)
	}
	// Close the goal: otherwise the "a goal exists" clause (not the flag) would
	// answer for the create below.
	if _, err := tool.Execute(`{"action":"update","status":"complete","reason":"done for the test"}`); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if gm.Mode.GetGoal().Goal != nil {
		t.Fatal("precondition: no goal must exist before flipping the flag off")
	}

	// In-session flip OFF: /config → Tools → goal, /tools:goal:off,
	// /config:set tools.enabled.goal false.
	cfg.Tools.Enabled.SetEnabled("goal", false)

	if _, err := tool.Execute(`{"action":"create","objective":"blocked again"}`); err == nil {
		t.Fatal("create must be blocked again after the flag is turned off in-session")
	}
	if gm.Mode.GetGoal().Goal != nil {
		t.Error("a blocked create must not start a goal")
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
