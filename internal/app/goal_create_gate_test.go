// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/tools"
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
// contract on the REAL tool instance the agent holds: the tool is registered
// once (registerGoalTools, enabled=false), handed to the agent through the
// production StartSession path, and must accept `create` after the in-session
// config flip the /config → Tools → goal row and /config:set
// tools.enabled.goal true both perform — without rebuilding or re-registering
// the tool.
func TestGoalToolEnabledLive_ConfigMenuPath(t *testing.T) {
	cfg := &config.Config{} // goal OFF in this session's live config
	gm := core.NewGoalManager(t.TempDir())
	reg := tools.NewToolRegistry()
	registerGoalTools(reg, gm, goalCreateGate(cfg, RuntimeOptions{}), nil, nil, nil)

	am := core.NewAgentManager(cfg, nil, nil, nil, nil, "")
	if _, err := am.StartSession(agenticprovider.Model{},
		agenticprovider.StreamOptions{SessionID: "sess-live-goal"}, "sys", reg.All(), cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	agent := am.CurrentAgent()
	if agent == nil {
		t.Fatal("no active agent after StartSession")
	}

	// The instance the agent holds must be the REGISTERED one, not a copy.
	registered, ok := reg.Get("goal")
	if !ok {
		t.Fatal("goal tool is not registered")
	}
	var held agentic.Tool
	for _, tl := range agent.Tools() {
		if tl.Schema().Name == "goal" {
			held = tl
			break
		}
	}
	if held == nil {
		t.Fatal("the agent does not hold the goal tool")
	}
	if held != registered {
		t.Fatal("agent holds a different goal tool instance than the registry (rebuilt copy)")
	}

	if _, err := held.Execute(`{"action":"create","objective":"blocked"}`); err == nil {
		t.Fatal("create must be blocked while tools.enabled.goal is false")
	}

	// /config → Tools → goal: setToolEnabled flips the flag on the LIVE config
	// and the menu pushes the registry's tools to the agent (applyToolToggle →
	// AgentManager.SetTools). /config:set tools.enabled.goal true writes the same
	// field through the CLI setter.
	cfg.Tools.Enabled.SetEnabled("goal", true)
	_ = am.SetTools(reg.All())

	// The SAME instance, in the SAME session, now accepts create.
	out, err := held.Execute(`{"action":"create","objective":"live enabled goal"}`)
	if err != nil {
		t.Fatalf("create must succeed in-session after the config flip (no restart): %v", err)
	}
	if !strings.Contains(out, "live enabled goal") {
		t.Errorf("create output missing the objective: %q", out)
	}
	if gm.Mode.GetActiveGoal() == nil {
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
