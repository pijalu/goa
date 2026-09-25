// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/plan"
	tuirender "github.com/pijalu/goa/internal/tuirender"
)

// --- plan tool renderer ---

// TestPlanToolRenderer_RenderCall pins the action labels the user sees, the
// add-item title truncation, and the fallbacks for unknown/absent actions.
func TestPlanToolRenderer_RenderCall(t *testing.T) {
	r := &PlanToolRenderer{}
	ctx := tuirender.RenderContext{}

	cases := []struct {
		name   string
		args   map[string]any
		want   []string
		reject []string
	}{
		{name: "add with title", args: map[string]any{"action": "add_item", "title": "Setup DB"}, want: []string{"📋 add", "Setup DB"}},
		{name: "reorder", args: map[string]any{"action": "reorder"}, want: []string{"🔀 reorder"}},
		{name: "get", args: map[string]any{"action": "get"}, want: []string{"📄 get"}},
		{name: "submit review", args: map[string]any{"action": "submit_review"}, want: []string{"📬 submit review"}},
		{name: "start with id", args: map[string]any{"action": "start_item", "id": "plan-1-item-2"}, want: []string{"▶️ start", "plan-1-item-2"}},
		{name: "unknown action", args: map[string]any{"action": "mystery"}, want: []string{"📋 plan", "mystery"}},
		{name: "no action", args: map[string]any{}, want: []string{"📋 plan"}, reject: []string{"mystery"}},
	}
	for _, tc := range cases {
		got := r.RenderCall(tc.args, ctx)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: RenderCall = %q, missing %q", tc.name, got, want)
			}
		}
		for _, reject := range tc.reject {
			if strings.Contains(got, reject) {
				t.Errorf("%s: RenderCall = %q, should not contain %q", tc.name, got, reject)
			}
		}
	}

	longTitle := strings.Repeat("x", 60)
	got := r.RenderCall(map[string]any{"action": "add_item", "title": longTitle}, ctx)
	if !strings.Contains(got, "…") {
		t.Errorf("long title not truncated: %q", got)
	}
	if strings.Contains(got, longTitle) {
		t.Errorf("untruncated title leaked into the header: %q", got)
	}
}

// TestPlanToolRenderer_RenderResult pins the markdown excerpt behaviour and the
// pass-through cases.
func TestPlanToolRenderer_RenderResult(t *testing.T) {
	r := &PlanToolRenderer{}

	if got := r.RenderResult("boom", tuirender.RenderContext{IsError: true}); got != "boom" {
		t.Errorf("error result must pass through unchanged, got %q", got)
	}
	if got := r.RenderResult("", tuirender.RenderContext{}); got != "" {
		t.Errorf("empty result must stay empty, got %q", got)
	}
	if got := r.RenderResult("plain text", tuirender.RenderContext{}); got != "plain text" {
		t.Errorf("non-plan output must pass through, got %q", got)
	}

	short := "# Plan: demo\n## items\n- one\n- two"
	if got := r.RenderResult(short, tuirender.RenderContext{}); strings.Contains(got, "…") {
		t.Errorf("short plan output must not be elided: %q", got)
	}

	long := "# Plan: demo\nline2\nline3\nline4\nline5\nline6"
	got := r.RenderResult(long, tuirender.RenderContext{})
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long plan output must be elided, got %q", got)
	}
	if strings.Contains(got, "line6") {
		t.Errorf("elided excerpt leaked later lines: %q", got)
	}

	if r.PreviewLines() <= 0 {
		t.Errorf("PreviewLines = %d, want > 0", r.PreviewLines())
	}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed = true, want false (the result is the payload)")
	}
}

// TestTruncateForRenderAndMinInt pins the rune-safe helpers.
func TestTruncateForRenderAndMinInt(t *testing.T) {
	if got := truncateForRender("short", 40); got != "short" {
		t.Errorf("truncateForRender(short) = %q", got)
	}
	got := truncateForRender(strings.Repeat("é", 50), 10)
	if runes := []rune(got); len(runes) != 10 || runes[len(runes)-1] != '…' {
		t.Errorf("truncateForRender multibyte = %q (%d runes), want 10 runes ending in …", got, len([]rune(got)))
	}
	if minInt(1, 2) != 1 || minInt(2, 1) != 1 || minInt(3, 3) != 3 {
		t.Error("minInt returned the wrong minimum")
	}
}

// --- task_outcome renderer ---

// TestTaskOutcomeRenderer pins the status headers and pass-through behaviour.
func TestTaskOutcomeRenderer(t *testing.T) {
	r := &TaskOutcomeRenderer{}
	ctx := tuirender.RenderContext{}
	for status, want := range map[string]string{
		"done":                "✅ task done",
		"needs_clarification": "❓ needs clarification",
		"blocked":             "🚫 task blocked",
		"other":               "📋 task outcome",
	} {
		if got := r.RenderCall(map[string]any{"status": status}, ctx); got != want {
			t.Errorf("RenderCall(%q) = %q, want %q", status, got, want)
		}
	}
	if got := r.RenderResult("body", ctx); got != "body" {
		t.Errorf("RenderResult = %q, want body", got)
	}
	if r.PreviewLines() <= 0 {
		t.Errorf("PreviewLines = %d, want > 0", r.PreviewLines())
	}
	if r.HideResultWhenCollapsed() {
		t.Error("HideResultWhenCollapsed = true, want false")
	}
}

// --- plan_mode tool surface ---

// TestPlanModeTool_Schema pins the schema contract.
func TestPlanModeTool_Schema(t *testing.T) {
	schema := (&PlanModeTool{}).Schema()
	if schema.Name != "plan_mode" {
		t.Errorf("Name = %q, want plan_mode", schema.Name)
	}
	props, ok := schema.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %#v", schema.Schema["properties"])
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatal("action property missing")
	}
	enum, ok := action["enum"].([]string)
	if !ok || strings.Join(enum, ",") != "enter,exit" {
		t.Errorf("action enum = %#v, want [enter exit]", action["enum"])
	}
	if required, ok := schema.Schema["required"].([]string); !ok || len(required) != 1 || required[0] != "action" {
		t.Errorf("required = %#v, want [action]", schema.Schema["required"])
	}
}

// TestPlanModeTool_DocsAndExamples pins the embedded docs and examples.
func TestPlanModeTool_DocsAndExamples(t *testing.T) {
	tool := &PlanModeTool{}
	short, long := tool.ShortDoc(), tool.LongDoc()
	if strings.TrimSpace(short) == "" || strings.TrimSpace(long) == "" {
		t.Error("plan_mode docs must be embedded and non-empty")
	}
	if len(long) <= len(short) {
		t.Error("LongDoc must be more detailed than ShortDoc")
	}
	if examples := tool.Examples(); len(examples) != 2 {
		t.Errorf("Examples = %v, want the enter/exit pair", examples)
	}
	if tool.IsRetryable(nil) {
		t.Error("IsRetryable = true, want false")
	}
}

// TestPlanModeTool_Guards pins the not-configured and malformed-input paths.
func TestPlanModeTool_Guards(t *testing.T) {
	if _, err := (&PlanModeTool{}).Execute(`{"action":"enter"}`); err == nil {
		t.Error("plan_mode without state must report not_configured")
	} else if !strings.Contains(err.Error(), "not_configured") {
		t.Errorf("error = %v, want not_configured", err)
	}

	configured := &PlanModeTool{State: plan.NewState()}
	if _, err := configured.Execute(`{`); err == nil {
		t.Error("malformed JSON must be rejected")
	} else if !strings.Contains(err.Error(), "invalid_input") {
		t.Errorf("error = %v, want invalid_input", err)
	}
	if configured.IsRetryable(nil) {
		t.Error("IsRetryable = true, want false")
	}
}

// --- task_outcome tool surface ---

// TestTaskOutcomeTool_ExecuteWrapper pins the plain Execute path (canonical JSON
// plus the StopTurn-signalling result) and the unknown-status rejection.
func TestTaskOutcomeTool_ExecuteWrapper(t *testing.T) {
	tool := &TaskOutcomeTool{}

	out, err := tool.Execute(`{"status":"done","summary":"implemented the endpoint"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not canonical JSON (%v): %q", err, out)
	}
	if parsed["status"] != "done" || parsed["summary"] != "implemented the endpoint" {
		t.Errorf("canonical output = %v", parsed)
	}

	result, err := tool.ExecuteWithResult(`{"status":"blocked","summary":"no credentials"}`)
	if err != nil {
		t.Fatalf("ExecuteWithResult: %v", err)
	}
	if !result.StopTurn {
		t.Error("task_outcome must always stop the turn")
	}

	if _, err := tool.Execute(`{"status":"sideways","summary":"x"}`); err == nil {
		t.Error("unknown status must be rejected")
	}
	if _, err := tool.Execute(`{"status":"blocked"}`); err == nil {
		t.Error("blocked without a summary must be rejected")
	}
}
