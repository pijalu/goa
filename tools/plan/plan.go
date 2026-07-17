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

// PlanTool provides the structured plan management tool for planner and
// orchestrator agents. It wraps a plan.Store and dispatches actions through
// a handler map (spec §15: no fat switch).
type PlanTool struct {
	agentic.BaseTool
	store    *plan.Store
	handlers map[string]func(json.RawMessage) (agentic.ToolResult, error)
}

//go:embed plan.short.md plan.long.md
var planDocs embed.FS

// ShortDoc returns a short doc string.
func (t *PlanTool) ShortDoc() string { return common.ReadDoc(planDocs, "plan.short.md") }

// LongDoc returns a long doc string.
func (t *PlanTool) LongDoc() string { return common.ReadDoc(planDocs, "plan.long.md") }

// Examples returns usage examples.
func (t *PlanTool) Examples() []string {
	return []string{
		`{"action": "add_item", "title": "Setup DB", "description": "Create schema"}`,
		`{"action": "submit_review"}`,
	}
}

// actionNames lists all valid actions for the schema enum.
var actionNames = []string{
	"add_item", "update_item", "remove_item", "reorder", "get",
	"submit_review", "resolve_comment",
	"start_item", "complete_item", "block_item", "skip_item",
}

// NewPlanTool creates a PlanTool bound to the given store.
func NewPlanTool(store *plan.Store) *PlanTool {
	t := &PlanTool{
		store:    store,
		handlers: make(map[string]func(json.RawMessage) (agentic.ToolResult, error)),
	}
	t.registerHandlers()
	return t
}

// registerHandlers builds the dispatch map.
func (t *PlanTool) registerHandlers() {
	// Structural actions (planning phase)
	t.handlers["add_item"] = t.handleAddItem
	t.handlers["update_item"] = t.handleUpdateItem
	t.handlers["remove_item"] = t.handleRemoveItem
	t.handlers["reorder"] = t.handleReorder
	t.handlers["get"] = t.handleGet

	// Review actions
	t.handlers["submit_review"] = t.handleSubmitReview
	t.handlers["resolve_comment"] = t.handleResolveComment

	// Execution actions
	t.handlers["start_item"] = t.handleStartItem
	t.handlers["complete_item"] = t.handleCompleteItem
	t.handlers["block_item"] = t.handleBlockItem
	t.handlers["skip_item"] = t.handleSkipItem
}

// Schema returns the tool's JSON schema.
func (t *PlanTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "plan",
		Description: "Manage a structured work plan: add/update/reorder items, submit for review, and execute items.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "The action to perform",
					"enum":        actionNames,
				},
			},
			"required": []string{"action"},
		},
	}
}

// Execute implements agentic.Tool.
func (t *PlanTool) Execute(input string) (string, error) {
	result, err := t.ExecuteWithResult(input)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// ExecuteWithResult implements agentic.ResultTool.
func (t *PlanTool) ExecuteWithResult(input string) (agentic.ToolResult, error) {
	// First, extract the action from the input.
	var actionOnly struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(input), &actionOnly); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{
			Tool:     "plan",
			Type:     "invalid_input",
			Detail:   fmt.Sprintf("invalid JSON input: %v", err),
			HintText: "Provide a valid JSON object with an 'action' field.",
		}
	}

	handler, ok := t.handlers[actionOnly.Action]
	if !ok {
		return agentic.ToolResult{}, &internal.ToolError{
			Tool:   "plan",
			Type:   "invalid_action",
			Detail: fmt.Sprintf("unknown action %q", actionOnly.Action),
			HintText: fmt.Sprintf("Valid actions: %v", actionNames),
		}
	}

	return handler([]byte(input))
}

// --- Handler implementations ---

// requirePlanningPhase returns an error if the plan is not in a planning-allowed
// status (draft or in_review).
func (t *PlanTool) requirePlanningPhase() error {
	p := t.store.Plan()
	if p.Status != plan.PlanDraft && p.Status != plan.PlanInReview {
		return &internal.ToolError{
			Tool:     "plan",
			Type:     "wrong_phase",
			Detail:   fmt.Sprintf("plan is %q; structural changes are only allowed during planning", p.Status),
			HintText: "Use /plan:replan to re-enter planning or wait for the current execution to finish.",
		}
	}
	return nil
}

// requireExecutionPhase returns an error if the plan is not in executing status.
func (t *PlanTool) requireExecutionPhase() error {
	p := t.store.Plan()
	if p.Status != plan.PlanExecuting {
		return &internal.ToolError{
			Tool:     "plan",
			Type:     "wrong_phase",
			Detail:   fmt.Sprintf("plan is %q; execution actions are only allowed during execution", p.Status),
			HintText: "Use /plan:approve to start execution.",
		}
	}
	return nil
}

// input types
type addItemInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Role        string   `json:"role,omitempty"`
	After       string   `json:"after,omitempty"`
}

type updateItemInput struct {
	ID          string   `json:"id"`
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	DependsOn   *[]string `json:"depends_on,omitempty"`
	Role        *string  `json:"role,omitempty"`
}

type removeItemInput struct {
	ID string `json:"id"`
}

type reorderInput struct {
	IDs []string `json:"ids"`
}

type resolveCommentInput struct {
	ID   string `json:"id"`
	Note string `json:"note,omitempty"`
}

type startItemInput struct {
	ID      string `json:"id"`
	Role    string `json:"role,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

type completeItemInput struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

type blockItemInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type skipItemInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

func (t *PlanTool) handleAddItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requirePlanningPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in addItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("add_item: %v", err)}
	}
	if in.Title == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "add_item: title is required"}
	}
	id, err := t.store.AddItem(in.Title, in.Description, in.After, in.DependsOn, in.Role)
	if err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("add_item: %v", err)}
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Added item %q (%s)", in.Title, id)}, nil
}

func (t *PlanTool) handleUpdateItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requirePlanningPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in updateItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("update_item: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "update_item: id is required"}
	}
	patch := plan.PlanItemPatch{
		Title:       in.Title,
		Description: in.Description,
		DependsOn:   in.DependsOn,
		Role:        in.Role,
	}
	if err := t.store.UpdateItem(in.ID, patch); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("update_item: %v", err)}
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Updated item %q", in.ID)}, nil
}

func (t *PlanTool) handleRemoveItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requirePlanningPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in removeItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("remove_item: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "remove_item: id is required"}
	}
	if err := t.store.RemoveItem(in.ID); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("remove_item: %v", err)}
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Removed item %q", in.ID)}, nil
}

func (t *PlanTool) handleReorder(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requirePlanningPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in reorderInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("reorder: %v", err)}
	}
	if len(in.IDs) == 0 {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "reorder: ids array is required"}
	}
	if err := t.store.Reorder(in.IDs); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("reorder: %v", err)}
	}
	return agentic.ToolResult{Output: "Items reordered"}, nil
}

func (t *PlanTool) handleGet(raw json.RawMessage) (agentic.ToolResult, error) {
	// get is allowed in any phase.
	p := t.store.Plan()
	md, _ := plan.Render(p)
	return agentic.ToolResult{Output: md}, nil
}

func (t *PlanTool) handleSubmitReview(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requirePlanningPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	if err := t.store.SubmitRevision(); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("submit_review: %v", err)}
	}
	return agentic.ToolResult{
		Output:   "Plan submitted for review. The pager will open for annotations.",
		StopTurn: true,
	}, nil
}

func (t *PlanTool) handleResolveComment(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requirePlanningPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in resolveCommentInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("resolve_comment: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "resolve_comment: id is required"}
	}
	if err := t.store.ResolveComment(in.ID, in.Note); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("resolve_comment: %v", err)}
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Comment %q resolved", in.ID)}, nil
}

func (t *PlanTool) handleStartItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requireExecutionPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in startItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("start_item: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "start_item: id is required"}
	}
	if in.Role == "" {
		in.Role = "coder"
	}
	if in.AgentID == "" {
		in.AgentID = "worker"
	}
	if err := t.store.StartItem(in.ID, in.Role, in.AgentID); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("start_item: %v", err), HintText: "Check dependencies and ensure no other item is in progress."}
	}
	// Build worker brief: title, description, dependency results, and instruction.
	p := t.store.Plan()
	item := p.Item(in.ID)
	if item == nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "internal_error", Detail: "item disappeared after start"}
	}
	brief := fmt.Sprintf("## %s\n\n%s\n\n", item.Title, item.Description)
	if len(item.DependsOn) > 0 {
		brief += "### Dependency results\n"
		for _, depID := range item.DependsOn {
			dep := p.Item(depID)
			if dep != nil {
				brief += fmt.Sprintf("- %s: %s\n", dep.ID, dep.Result)
			}
		}
		brief += "\n"
	}
	brief += "Finish by calling task_outcome."
	return agentic.ToolResult{Output: brief}, nil
}

func (t *PlanTool) handleCompleteItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requireExecutionPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in completeItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("complete_item: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "complete_item: id is required"}
	}
	if in.Result == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "complete_item: result must not be empty"}
	}
	if err := t.store.CompleteItem(in.ID, in.Result); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("complete_item: %v", err)}
	}
	// Check if all items are now terminal.
	p := t.store.Plan()
	if p.AllTerminal() {
		// Don't auto-finish; the orchestrator should call Finish explicitly.
		return agentic.ToolResult{Output: fmt.Sprintf("Item %q completed. All items are terminal — call finish to complete the plan.", in.ID)}, nil
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Item %q completed. Result: %s", in.ID, in.Result)}, nil
}

func (t *PlanTool) handleBlockItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requireExecutionPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in blockItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("block_item: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "block_item: id is required"}
	}
	if in.Reason == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "block_item: reason is required"}
	}
	if err := t.store.BlockItem(in.ID, in.Reason); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("block_item: %v", err)}
	}
	// List dependents that are now unstartable.
	p := t.store.Plan()
	deps := p.Dependents(in.ID)
	msg := fmt.Sprintf("Item %q blocked: %s", in.ID, in.Reason)
	if len(deps) > 0 {
		msg += fmt.Sprintf("\nDependents now unstartable: %v", deps)
	}
	return agentic.ToolResult{Output: msg}, nil
}

func (t *PlanTool) handleSkipItem(raw json.RawMessage) (agentic.ToolResult, error) {
	if err := t.requireExecutionPhase(); err != nil {
		return agentic.ToolResult{}, err
	}
	var in skipItemInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: fmt.Sprintf("skip_item: %v", err)}
	}
	if in.ID == "" {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "invalid_input", Detail: "skip_item: id is required"}
	}
	if err := t.store.SkipItem(in.ID, in.Reason); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{Tool: "plan", Type: "operation_failed", Detail: fmt.Sprintf("skip_item: %v", err)}
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Item %q skipped", in.ID)}, nil
}
