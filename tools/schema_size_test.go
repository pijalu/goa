// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/ask"
	"github.com/pijalu/goa/tools/goal"
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
//
// defaultToolSet mirrors the tools registered at runtime (internal/app
// bootstrap.go + subsystems.go) plus every config-toggleable tool that can be
// enabled without code changes, so the guard is an upper bound on the default
// per-request payload. Tools registered unconditionally (agent, goa, goal,
// todo_list) are included; tools that are never registered as agent tools
// (plan, plan_mode, task_outcome — worker/plan-mode only) are excluded.
// Dynamic tool namespaces (MCP servers, user skills via skill_runner) are
// intentionally unguarded — they vary per project.

const (
	// maxToolDescriptionChars caps each tool's Schema().Description. The goal
	// tool is the sole exception (its description is the goal-system spec).
	maxToolDescriptionChars = 500
	// maxGoalDescriptionChars caps the goal tool description (spec-grade).
	maxGoalDescriptionChars = 3600
	// maxToolSearchDescriptionChars caps the tool_search loader description:
	// its base text plus the compact deferred-tool catalog (P1). The catalog
	// itself is bounded by catalogBudget in tools/tool_search.go.
	maxToolSearchDescriptionChars = 1100
	// maxToolSchemaJSONBytes caps the total JSON payload (description +
	// parameter schema) of each non-goal tool, so parameter-schema bloat is
	// bounded too — the per-description cap alone does not catch it.
	maxToolSchemaJSONBytes = 2500
	// maxGoalSchemaJSONBytes caps the goal tool's total JSON payload; its
	// parameter surface (11 actions) is inherently larger than other tools.
	maxGoalSchemaJSONBytes = 5000
	// maxToolSearchSchemaJSONBytes caps the loader's total JSON payload
	// (description incl. catalog + one-field parameter schema).
	maxToolSearchSchemaJSONBytes = 1400
	// maxTotalSchemaJSONBytes caps the combined JSON payload of the default
	// tool set (descriptions + parameter schemas).
	maxTotalSchemaJSONBytes = 22000
)

func defaultToolSet() []agentic.Tool {
	list := []agentic.Tool{
		&tools.ReadFileTool{},
		&tools.WriteFileTool{},
		&tools.EditFileTool{},
		&tools.SearchTool{},
		&tools.LSPTool{},
		&tools.BashTool{},
		&tools.TerminalsTool{},
		&tools.PythonTool{},
		&tools.WebFetchTool{},
		&tools.BGExecTool{},
		&tools.MementoTool{},
		&tools.ReadMediaFileTool{},
		&tools.SmartSearchTool{},
		&tools.SSHBashTool{},
		&tools.VerifyTool{},
		&ask.AskUserQuestionTool{},
		&goal.GoalTool{},
		&swarm.AgentSwarmTool{},
		&todo.TodoListTool{},
		// Registered unconditionally at runtime (subsystems.go / bootstrap.go).
		&multiagent.AgentTool{},
		&core.GoaCommandTool{},
		// Session query tools are always registered (registerSessionQueryTools).
		&tools.SessionSearchTool{},
		&tools.SessionEventReadTool{},
		// Agent-driven tools are config-toggleable without code changes
		// (registerAgentDrivenTools).
		&multiagent.RequestReviewTool{},
		&multiagent.DelegateTool{},
	}
	// The P1 loader (tool_search) is registered last at runtime so its
	// catalog sees the full deferred set; mirror that here by building a
	// registry with all tools and then appending the loader.
	reg := tools.NewToolRegistry()
	for _, t := range list {
		reg.Register(t)
	}
	return append(list, tools.NewToolSearchTool(reg))
}

func TestToolDescriptionSizeCeilings(t *testing.T) {
	for _, tl := range defaultToolSet() {
		s := tl.Schema()
		ceiling := maxToolDescriptionChars
		switch s.Name {
		case "goal":
			ceiling = maxGoalDescriptionChars
		case "tool_search":
			// The loader's description legitimately embeds the compact
			// deferred-tool catalog (P1); its size is bounded separately by
			// catalogBudget in tools/tool_search.go.
			ceiling = maxToolSearchDescriptionChars
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

func TestPerToolSchemaJSONCeiling(t *testing.T) {
	for _, tl := range defaultToolSet() {
		s := tl.Schema()
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %s: %v", s.Name, err)
		}
		ceiling := maxToolSchemaJSONBytes
		switch s.Name {
		case "goal":
			ceiling = maxGoalSchemaJSONBytes
		case "tool_search":
			// The loader schema legitimately carries the compact deferred-tool
			// catalog (P1); its total is bounded by the catalog budget.
			ceiling = maxToolSearchSchemaJSONBytes
		}
		if len(b) > ceiling {
			t.Errorf("tool %q schema = %d JSON bytes total, ceiling %d — description AND parameter schema ship with every LLM request; keep both dense",
				s.Name, len(b), ceiling)
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

// TestDeferredToolSchemaCeiling asserts that with P1 deferred loading active
// (≥ deferralThreshold deferred-eligible tools + a tool_search loader), the
// per-request schema payload is partitioned: the stable eager block +
// tool_search catalog, with deferred tools withheld until loaded. The
// partitioned payload must be measurably smaller than the full set — the
// point of P1.
func TestDeferredToolSchemaCeiling(t *testing.T) {
	full := 0
	for _, tl := range defaultToolSet() {
		s := tl.Schema()
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %s: %v", s.Name, err)
		}
		full += len(b)
	}

	// Build the agentic registry over the same set; the loader is present and
	// the deferred count is ≥ threshold, so deferral must be active.
	reg := agentic.NewToolRegistry(defaultToolSet())

	// Verify deferral actually activated: at least one deferred tool is
	// withheld from the eager view.
	first := reg.Schemas()
	if len(first) >= len(defaultToolSet()) {
		t.Fatalf("deferral did not activate: partitioned view has %d schemas, full set %d (loader missing or deferred count < threshold)",
			len(first), len(defaultToolSet()))
	}

	partitioned := 0
	for _, s := range first {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %s: %v", s.Name, err)
		}
		partitioned += len(b)
	}

	// The partitioned view must be measurably smaller (P1 goal: drop the
	// ~17KB full payload toward core ≈ 8–10KB + catalog ≈ 0.5KB). Use a
	// clear relative margin so the guard is about the win, not a fixed number
	// that drifts as descriptions evolve.
	const minRelativeDrop = 0.15
	if partitioned > int(float64(full)*(1-minRelativeDrop)) {
		t.Errorf("deferred partition = %d JSON bytes vs full %d (drop %.1f%%) — P1 requires a measurable drop (≥%.0f%%)",
			partitioned, full, 100*float64(full-partitioned)/float64(full), 100*minRelativeDrop)
	}
	t.Logf("full tool schemas: %d JSON bytes; partitioned (eager+catalog): %d bytes (drop %.1f%%)",
		full, partitioned, 100*float64(full-partitioned)/float64(full))
}
