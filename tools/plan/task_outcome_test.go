// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestTaskOutcomeSchema(t *testing.T) {
	tool := &TaskOutcomeTool{}
	schema := tool.Schema()

	if schema.Name != "task_outcome" {
		t.Errorf("Name = %q", schema.Name)
	}

	statusEnum := schema.Schema["properties"].(map[string]any)["status"].(map[string]any)["enum"].([]string)
	if len(statusEnum) != 3 {
		t.Errorf("expected 3 status values, got %d", len(statusEnum))
	}
}

func TestTaskOutcomeDone(t *testing.T) {
	tool := &TaskOutcomeTool{}
	result, err := tool.ExecuteWithResult(`{"status": "done", "summary": "Implemented the feature"}`)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !result.StopTurn {
		t.Error("StopTurn should be true")
	}

	var out map[string]string
	json.Unmarshal([]byte(result.Output), &out)
	if out["status"] != "done" {
		t.Errorf("status = %q", out["status"])
	}
	if out["summary"] != "Implemented the feature" {
		t.Errorf("summary = %q", out["summary"])
	}
}

func TestTaskOutcomeNeedsClarification(t *testing.T) {
	tool := &TaskOutcomeTool{}
	result, err := tool.ExecuteWithResult(`{"status": "needs_clarification", "summary": "Unclear", "question": "Which port?"}`)
	if err != nil {
		t.Fatalf("needs_clarification: %v", err)
	}
	if !result.StopTurn {
		t.Error("StopTurn should be true")
	}

	var out map[string]string
	json.Unmarshal([]byte(result.Output), &out)
	if out["status"] != "needs_clarification" {
		t.Errorf("status = %q", out["status"])
	}
	if out["question"] != "Which port?" {
		t.Errorf("question = %q", out["question"])
	}
}

func TestTaskOutcomeBlocked(t *testing.T) {
	tool := &TaskOutcomeTool{}
	result, err := tool.ExecuteWithResult(`{"status": "blocked", "summary": "Missing credentials"}`)
	if err != nil {
		t.Fatalf("blocked: %v", err)
	}
	if !result.StopTurn {
		t.Error("StopTurn should be true")
	}

	var out map[string]string
	json.Unmarshal([]byte(result.Output), &out)
	if out["status"] != "blocked" {
		t.Errorf("status = %q", out["status"])
	}
}

func TestTaskOutcomeMissingFields(t *testing.T) {
	tool := &TaskOutcomeTool{}

	tests := []struct {
		name  string
		input string
	}{
		{"done missing summary", `{"status": "done"}`},
		{"clarification missing question", `{"status": "needs_clarification", "summary": "?"}`},
		{"blocked missing summary", `{"status": "blocked"}`},
		{"unknown status", `{"status": "invalid", "summary": "x"}`},
		{"bad JSON", `not json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.ExecuteWithResult(tt.input)
			if err == nil {
				t.Error("expected error")
			}
			if _, ok := err.(*internal.ToolError); !ok {
				t.Errorf("expected ToolError, got %T", err)
			}
		})
	}
}

func TestTaskOutcomeTruncation(t *testing.T) {
	tool := &TaskOutcomeTool{}
	longSummary := make([]byte, 5000)
	for i := range longSummary {
		longSummary[i] = 'a'
	}
	input := `{"status": "done", "summary": "` + string(longSummary) + `"}`

	result, err := tool.ExecuteWithResult(input)
	if err != nil {
		t.Fatalf("truncation: %v", err)
	}

	var out map[string]string
	json.Unmarshal([]byte(result.Output), &out)
	if len(out["summary"]) > 4015 { // 4000 + " [truncated]"
		t.Errorf("summary too long: %d chars", len(out["summary"]))
	}
	if len(out["summary"]) < 4000 {
		t.Errorf("summary should be ~4000 chars, got %d", len(out["summary"]))
	}
}

func TestTaskOutcomeDocs(t *testing.T) {
	tool := &TaskOutcomeTool{}

	if tool.ShortDoc() == "" {
		t.Error("ShortDoc should not be empty")
	}
	if tool.LongDoc() == "" {
		t.Error("LongDoc should not be empty")
	}
	if len(tool.Examples()) == 0 {
		t.Error("Examples should not be empty")
	}
}

func TestTaskOutcomeDefaultSummary(t *testing.T) {
	tool := &TaskOutcomeTool{}
	result, err := tool.ExecuteWithResult(`{"status": "needs_clarification", "question": "Which approach?"}`)
	if err != nil {
		t.Fatalf("needs_clarification no summary: %v", err)
	}
	var out map[string]string
	json.Unmarshal([]byte(result.Output), &out)
	if out["summary"] != "Clarification needed" {
		t.Errorf("summary = %q, want 'Clarification needed'", out["summary"])
	}
}
