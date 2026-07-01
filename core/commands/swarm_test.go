// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/swarm"
)

func runSwarm(t *testing.T, cmd *SwarmCommand, buf *strings.Builder, args ...string) string {
	t.Helper()
	ctx := core.Context{OutputBuffer: buf}
	if err := cmd.Run(ctx, args); err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	out := buf.String()
	buf.Reset()
	return out
}

func TestSwarmCommand_OnSetsManualTrigger(t *testing.T) {
	state := swarm.NewState()
	cmd := &SwarmCommand{State: state}
	buf := &strings.Builder{}

	out := runSwarm(t, cmd, buf, "on")
	if !state.IsActive() {
		t.Error("expected swarm active after on")
	}
	if state.Trigger() != swarm.ManualTrigger {
		t.Errorf("trigger = %v, want ManualTrigger", state.Trigger())
	}
	if !strings.Contains(out, "manual") {
		t.Errorf("on output should mention manual trigger, got: %q", out)
	}

	// Manual toggle persists (no auto-exit).
	if state.ShouldAutoExit() {
		t.Error("manual trigger must not auto-exit")
	}

	// Idempotent: second 'on' reports already on and does not change state.
	out = runSwarm(t, cmd, buf, "on")
	if !strings.Contains(out, "already on") {
		t.Errorf("expected 'already on' message, got: %q", out)
	}
}

func TestSwarmCommand_Off(t *testing.T) {
	state := swarm.NewState()
	cmd := &SwarmCommand{State: state}
	buf := &strings.Builder{}

	runSwarm(t, cmd, buf, "on")
	out := runSwarm(t, cmd, buf, "off")
	if state.IsActive() {
		t.Error("expected swarm inactive after off")
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("off output should mention disabled, got: %q", out)
	}

	// Idempotent.
	out = runSwarm(t, cmd, buf, "off")
	if !strings.Contains(out, "already off") {
		t.Errorf("expected 'already off' message, got: %q", out)
	}
}

func TestSwarmCommand_TaskSetsTaskTriggerAndAutoExits(t *testing.T) {
	state := swarm.NewState()
	cmd := &SwarmCommand{State: state}
	// No AgentManager on ctx: the command still sets the trigger; the
	// background RunUserInput is skipped via the nil guard.
	buf := &strings.Builder{}

	out := runSwarm(t, cmd, buf, "fix", "lints", "everywhere")
	if !state.IsActive() {
		t.Error("expected swarm active for task")
	}
	if state.Trigger() != swarm.TaskTrigger {
		t.Errorf("trigger = %v, want TaskTrigger", state.Trigger())
	}
	if state.Task() != "fix lints everywhere" {
		t.Errorf("task = %q, want 'fix lints everywhere'", state.Task())
	}
	if !state.ShouldAutoExit() {
		t.Error("task trigger must auto-exit at end of turn")
	}
	if !strings.Contains(out, "Swarm task started") {
		t.Errorf("task output should mention start, got: %q", out)
	}
}

func TestSwarmCommand_StatusReflectsTrigger(t *testing.T) {
	state := swarm.NewState()
	cmd := &SwarmCommand{State: state}
	buf := &strings.Builder{}

	out := runSwarm(t, cmd, buf)
	if !strings.Contains(out, "OFF") {
		t.Errorf("inactive status should say OFF, got: %q", out)
	}

	runSwarm(t, cmd, buf, "on")
	out = runSwarm(t, cmd, buf)
	if !strings.Contains(out, "ON") || !strings.Contains(out, "manual") {
		t.Errorf("active status should show ON + manual, got: %q", out)
	}
}
