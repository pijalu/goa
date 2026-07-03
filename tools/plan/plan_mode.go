// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// PlanModeTool toggles plan mode.
type PlanModeTool struct {
	agentic.BaseTool
	State *plan.State
}

type planModeInput struct {
	Action string `json:"action"`
}

// Schema returns the tool schema.
func (t *PlanModeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "plan_mode",
		Description: "Toggle plan mode.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "enter or exit",
					"enum":        []string{"enter", "exit"},
				},
			},
			"required": []string{"action"},
		},
	}
}

// Execute toggles plan mode.
func (t *PlanModeTool) Execute(input string) (string, error) {
	if t.State == nil {
		return "", &internal.ToolError{Tool: "plan_mode", Type: "not_configured", Detail: "plan state not configured", HintText: "Plan mode is not available."}
	}
	var p planModeInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{Tool: "plan_mode", Type: "invalid_input", Detail: fmt.Sprintf("parse error: %v", err), HintText: "Provide action: enter or exit."}
	}
	switch p.Action {
	case "enter":
		t.State.Enable()
		return "[plan_mode] Entered plan mode. Write/edit now restricted to PLAN.md.", nil
	case "exit":
		t.State.Disable()
		return "[plan_mode] Exited plan mode.", nil
	default:
		return "", &internal.ToolError{Tool: "plan_mode", Type: "invalid_action", Detail: fmt.Sprintf("unknown action %q", p.Action), HintText: "Use enter or exit."}
	}
}

// IsRetryable returns false.
func (t *PlanModeTool) IsRetryable(err error) bool { return false }

// ShortDoc returns a short doc string.
//
//go:embed plan_mode.short.md plan_mode.long.md
var plan_modeDocs embed.FS

func (t *PlanModeTool) ShortDoc() string { return common.ReadDoc(plan_modeDocs, "plan_mode.short.md") }
func (t *PlanModeTool) LongDoc() string  { return common.ReadDoc(plan_modeDocs, "plan_mode.long.md") }
func (t *PlanModeTool) Examples() []string {
	return []string{`{"action": "enter"}`, `{"action": "exit"}`}
}
