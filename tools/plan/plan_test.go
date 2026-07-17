// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal"
)

func TestPlanToolSchema(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)
	schema := tool.Schema()

	if schema.Name != "plan" {
		t.Errorf("Name = %q, want 'plan'", schema.Name)
	}
	if schema.Description == "" {
		t.Error("Description should not be empty")
	}

	actions, ok := schema.Schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	if !ok {
		t.Fatal("expected enum array in schema")
	}

	expectedActions := []string{
		"add_item", "update_item", "remove_item", "reorder", "get",
		"submit_review", "resolve_comment",
		"start_item", "complete_item", "block_item", "skip_item",
	}

	if len(actions) != len(expectedActions) {
		t.Fatalf("expected %d actions, got %d: %v", len(expectedActions), len(actions), actions)
	}

	for _, expected := range expectedActions {
		found := false
		for _, a := range actions {
			if a == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("action %q not found in schema", expected)
		}
	}
}

func TestPlanToolUnknownAction(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)
	result, err := tool.ExecuteWithResult(`{"action": "nonexistent"}`)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}

	if _, ok := err.(*internal.ToolError); !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}

	if !strings.Contains(err.Error(), "invalid_action") {
		t.Errorf("error should mention invalid_action, got: %v", err)
	}

	if result.StopTurn {
		t.Error("StopTurn should be false for unknown action")
	}
}

func TestPlanToolBadJSON(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)
	_, err = tool.ExecuteWithResult(`not json`)
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}

	if _, ok := err.(*internal.ToolError); !ok {
		t.Fatalf("expected ToolError, got %T", err)
	}

	if !strings.Contains(err.Error(), "invalid_input") {
		t.Errorf("error should mention invalid_input, got: %v", err)
	}
}

func TestPlanToolDocs(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)

	short := tool.ShortDoc()
	if short == "" {
		t.Error("ShortDoc should not be empty")
	}

	long := tool.LongDoc()
	if long == "" {
		t.Error("LongDoc should not be empty")
	}

	examples := tool.Examples()
	if len(examples) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestPlanToolGet(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test get")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)

	// get should return rendered markdown.
	result, err := tool.Execute(`{"action": "get"}`)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(result, "test get") {
		t.Errorf("result should contain objective, got: %s", result)
	}
}

func TestPlanToolAddItem(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test add")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)

	// Successful add.
	result, err := tool.Execute(`{"action": "add_item", "title": "My Task", "description": "Do it"}`)
	if err != nil {
		t.Fatalf("add_item: %v", err)
	}
	if !strings.Contains(result, "My Task") {
		t.Errorf("result should mention title, got: %s", result)
	}

	// Missing title.
	_, err = tool.Execute(`{"action": "add_item"}`)
	if err == nil {
		t.Error("expected error for missing title")
	}

	// Unknown action.
	_, err = tool.Execute(`{"action": "nonexistent"}`)
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestPlanToolSubmitReview(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test submit")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)

	result, err := tool.ExecuteWithResult(`{"action": "submit_review"}`)
	if err != nil {
		t.Fatalf("submit_review: %v", err)
	}
	if !result.StopTurn {
		t.Error("submit_review should set StopTurn")
	}
}

func TestPlanToolPhaseEnforcement(t *testing.T) {
	s, err := plan.Create(t.TempDir(), "test phase")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	tool := NewPlanTool(s)

	// start_item should fail during draft phase.
	_, err = tool.Execute(`{"action": "start_item", "id": "item-1"}`)
	if err == nil {
		t.Error("start_item should fail during draft phase")
	}
	if !strings.Contains(err.Error(), "wrong_phase") {
		t.Errorf("error should mention wrong_phase, got: %v", err)
	}

	// Execution actions should fail during draft phase.
	for _, action := range []string{"complete_item", "block_item", "skip_item"} {
		_, err := tool.Execute(`{"action": "` + action + `", "id": "item-1"}`)
		if err == nil {
			t.Errorf("%s should fail during draft phase", action)
		}
	}

	// Structural actions should work during draft phase.
	_, err = tool.Execute(`{"action": "add_item", "title": "Test"}`)
	if err != nil {
		t.Errorf("add_item should work during draft: %v", err)
	}
}
