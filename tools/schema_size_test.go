// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/ask"
	"github.com/pijalu/goa/tools/goal"
	"github.com/pijalu/goa/tools/plan"
	"github.com/pijalu/goa/tools/swarm"
	"github.com/pijalu/goa/tools/todo"
)

// Build-time context guard (recontext, specs/recontext-plan.md): tool schema
// text is sent to the LLM with EVERY request, so its size is capped here.
// This is a CI check only — Goa never refuses to run at runtime because
// context grew; user-added tools/skills may legitimately add context.
//
// If this test fails because you intentionally grew a description, shrink it
// first; raise a ceiling only with a recontext-plan update and a measured
// justification.

const (
	// maxToolDescriptionChars caps each tool's Schema().Description. The goal
	// tool is the sole exception (its description is the goal-system spec).
	maxToolDescriptionChars = 500
	// maxGoalDescriptionChars caps the goal tool description (spec-grade).
	maxGoalDescriptionChars = 3600
	// maxTotalSchemaJSONBytes caps the combined JSON payload of the default
	// tool set (descriptions + parameter schemas).
	maxTotalSchemaJSONBytes = 22000
)

func defaultToolSet() []agentic.Tool {
	return []agentic.Tool{
		&tools.ReadFileTool{},
		&tools.WriteFileTool{},
		&tools.EditFileTool{},
		&tools.SearchTool{},
		&tools.LSPTool{},
		&tools.BashTool{},
		&tools.TerminalTool{},
		&tools.PythonTool{},
		&tools.WebFetchTool{},
		&tools.BGExecTool{},
		&tools.MementoTool{},
		&tools.PTYExecTool{},
		&tools.ReadMediaFileTool{},
		&tools.SmartSearchTool{},
		&tools.SSHBashTool{},
		&tools.VerifyTool{},
		&ask.AskUserQuestionTool{},
		&goal.GoalTool{},
		&plan.PlanModeTool{},
		&plan.PlanTool{},
		&plan.TaskOutcomeTool{},
		&swarm.AgentSwarmTool{},
		&todo.TodoListTool{},
	}
}

func TestToolDescriptionSizeCeilings(t *testing.T) {
	for _, tl := range defaultToolSet() {
		s := tl.Schema()
		ceiling := maxToolDescriptionChars
		if s.Name == "goal" {
			ceiling = maxGoalDescriptionChars
		}
		if len(s.Description) > ceiling {
			t.Errorf("tool %q description = %d chars, ceiling %d — tool descriptions are sent with every LLM request; keep them dense",
				s.Name, len(s.Description), ceiling)
		}
		if len(s.Description) == 0 {
			t.Errorf("tool %q has an empty description", s.Name)
		}
	}
}

func TestTotalToolSchemaJSONCeiling(t *testing.T) {
	total := 0
	for _, tl := range defaultToolSet() {
		s := tl.Schema()
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %s: %v", s.Name, err)
		}
		total += len(b)
	}
	if total > maxTotalSchemaJSONBytes {
		t.Errorf("default tool schemas = %d JSON bytes total, ceiling %d — this payload ships with every LLM request",
			total, maxTotalSchemaJSONBytes)
	}
	t.Logf("default tool schemas: %d JSON bytes (~%d tokens)", total, total/4)
}
