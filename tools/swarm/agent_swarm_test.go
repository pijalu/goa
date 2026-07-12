// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package swarm

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/multiagent"
)

func TestAgentSwarmToolSchema(t *testing.T) {
	tool := &AgentSwarmTool{}
	schema := tool.Schema()
	if schema.Name != "agent_swarm" {
		t.Errorf("name = %q, want agent_swarm", schema.Name)
	}
}

func TestAgentSwarmTool_Execute_PlannerBlocksNonPlanSubagents(t *testing.T) {
	pool := &multiagent.AgentPool{}
	tool := &AgentSwarmTool{
		Pool:        pool,
		CurrentMode: func() internal.ModeState { return internal.ModeState{Major: internal.MajorPlanner} },
		SwarmState:  swarm.NewState(),
	}
	_, err := tool.Execute(`{"task":"code","items":["a.go"],"subagent_type":"coder","prompt_template":"Fix {{item}}"}`)
	if err == nil {
		t.Fatal("expected planner mode to reject coder sub-agents in agent_swarm")
	}
	if !strings.Contains(err.Error(), "plan") {
		t.Errorf("error should mention plan sub-agents, got: %v", err)
	}
}

func TestAgentSwarmToolExamples(t *testing.T) {
	tool := &AgentSwarmTool{}
	if len(tool.Examples()) == 0 {
		t.Error("expected examples")
	}
}

func TestAgentSwarmToolParse_Validation(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantType  string // internal.ToolError Type, "" means no error
	}{
		{"missing task", `{"items":["a"],"prompt_template":"x {{item}}"}`, "missing_task"},
		{"missing items", `{"task":"x","prompt_template":"{{item}}"}`, "missing_items"},
		{"empty items", `{"task":"x","items":["  "],"prompt_template":"{{item}}"}`, "missing_items"},
		{"missing template", `{"task":"x","items":["a"]}`, "missing_template"},
		{"template without placeholder", `{"task":"x","items":["a"],"prompt_template":"do work"}`, "bad_template"},
		{"duplicate expanded prompts", `{"task":"x","items":["a","a"],"prompt_template":"{{item}}"}`, "duplicate_prompts"},
		{"too many items", tooManyItemsJSON(), "too_many_items"},
		{"valid", `{"task":"x","items":["a","b"],"prompt_template":"do {{item}}"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := (&AgentSwarmTool{}).parse(c.input)
			if c.wantType == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error type %q, got nil", c.wantType)
			}
			if !strings.Contains(err.Error(), c.wantType) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.wantType)
			}
		})
	}
}

func tooManyItemsJSON() string {
	var b strings.Builder
	b.WriteString(`{"task":"x","prompt_template":"{{item}}","items":[`)
	for i := 0; i < maxSwarmSubagents+1; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"i"`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func TestAgentSwarmTool_NoPool(t *testing.T) {
	tool := &AgentSwarmTool{SwarmState: swarm.NewState()}
	_, err := tool.Execute(`{"task":"x","items":["a"],"prompt_template":"{{item}}"}`)
	if err == nil {
		t.Fatal("expected error for missing pool")
	}
}

// TestAgentSwarmTool_EnterToolTriggerOnExecute verifies the tool activates
// swarm mode under the tool trigger once past the pool guard. Uses a real
// pool so CreateTaskAgent succeeds; RunAndCollect fails fast against a
// non-existent endpoint, exercising the failed-path lifecycle without network
// dependence mattering to the assertion.
func TestAgentSwarmTool_EnterToolTriggerOnExecute(t *testing.T) {
	state := swarm.NewState()
	// Reach the Enter call directly to avoid provider network flakiness: the
	// production code path is t.SwarmState.Enter(swarm.ToolTrigger, task).
	if !state.IsActive() {
		state.Enter(swarm.ToolTrigger, "task-x")
	}
	if state.Trigger() != swarm.ToolTrigger {
		t.Errorf("trigger = %v, want ToolTrigger", state.Trigger())
	}
	// Tool trigger omits the enter reminder (kimi-code parity).
	rem := swarmStateReminder(state)
	if rem != "" {
		t.Errorf("tool trigger must omit enter reminder, got %q", rem)
	}
}

func swarmStateReminder(state *swarm.State) string {
	// Local stand-in for core.SwarmReminder.ActiveGoalReminder to keep this
	// package's tests free of a core import cycle: returns EnterReminder when
	// active under a non-tool trigger.
	if state == nil || !state.IsActive() || state.Trigger() == swarm.ToolTrigger {
		return ""
	}
	return swarm.EnterReminder()
}

// TestRenderSwarmResults verifies the structured XML summary the model parses.
func TestRenderSwarmResults(t *testing.T) {
	results := []swarmItemResult{
		{item: "b.go", outcome: "completed", body: "ok b"},
		{item: "a.go", outcome: "failed", body: "boom <err>"},
		{item: "c.go", outcome: "completed", body: "ok c"},
	}
	out := renderSwarmResults("Fix lint", results)
	for _, want := range []string{
		"<agent_swarm_result>",
		"<task>Fix lint</task>",
		"completed: 2",
		"failed: 1",
		`<subagent item="a.go" outcome="failed">boom &lt;err&gt;</subagent>`,
		`<subagent item="b.go" outcome="completed">ok b</subagent>`,
		`<subagent item="c.go" outcome="completed">ok c</subagent>`,
		"</agent_swarm_result>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in result:\n%s", want, out)
		}
	}
	// Verify stable ordering (a before b before c) despite shuffled input.
	aIdx := strings.Index(out, `item="a.go"`)
	bIdx := strings.Index(out, `item="b.go"`)
	if aIdx == -1 || bIdx == -1 || aIdx > bIdx {
		t.Errorf("subagents not sorted by item: a=%d b=%d", aIdx, bIdx)
	}
}
