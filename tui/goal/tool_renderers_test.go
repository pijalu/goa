// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/tuirender"
)

func TestCreateGoalRenderer(t *testing.T) {
	r := CreateGoalRenderer{}
	call := r.RenderCall(map[string]any{"objective": "Fix tests"}, tuirender.RenderContext{})
	if !strings.Contains(call, "Started goal") {
		t.Errorf("call = %q", call)
	}
	res := r.RenderResult(`{"goal":{"objective":"Fix tests","status":"active","turnsUsed":0,"tokensUsed":0,"wallClockMs":0}}`, tuirender.RenderContext{})
	if !strings.Contains(res, "Fix tests") {
		t.Errorf("result = %q", res)
	}
	if r.PreviewLines() != 3 || r.HideResultWhenCollapsed() {
		t.Error("unexpected renderer meta")
	}
}

func TestUpdateGoalRenderer(t *testing.T) {
	r := UpdateGoalRenderer{}
	for status, want := range map[string]string{
		"complete": "complete",
		"blocked":  "blocked",
		"paused":   "Paused",
		"active":   "Resumed",
		"unknown":  "Updated",
	} {
		call := r.RenderCall(map[string]any{"status": status}, tuirender.RenderContext{})
		if !strings.Contains(call, want) {
			t.Errorf("status %s call = %q", status, call)
		}
	}
	if r.RenderResult("", tuirender.RenderContext{}) != "" || r.PreviewLines() != 0 || !r.HideResultWhenCollapsed() {
		t.Error("unexpected renderer meta")
	}
}

func TestGetGoalRenderer(t *testing.T) {
	r := GetGoalRenderer{}
	if !strings.Contains(r.RenderCall(nil, tuirender.RenderContext{}), "Checked goal") {
		t.Error("unexpected call")
	}
	res := r.RenderResult(`{"goal":null}`, tuirender.RenderContext{})
	if !strings.Contains(res, "No current goal") {
		t.Errorf("result = %q", res)
	}
}

func TestSetGoalBudgetRenderer(t *testing.T) {
	r := SetGoalBudgetRenderer{}
	call := r.RenderCall(map[string]any{"value": 5.0, "unit": "turns"}, tuirender.RenderContext{})
	if !strings.Contains(call, "Set goal budget") || !strings.Contains(call, "turns") {
		t.Errorf("call = %q", call)
	}
}

func TestRenderGoalSummary_InvalidJSON(t *testing.T) {
	if got := renderGoalSummary("not-json"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestExtractArg(t *testing.T) {
	if got := extractArg(map[string]any{"k": "v"}, "k"); got != "v" {
		t.Errorf("string arg = %q", got)
	}
	if got := extractArg(map[string]any{"k": 3.5}, "k"); got != "3.5" {
		t.Errorf("float arg = %q", got)
	}
	if got := extractArg(map[string]any{"k": true}, "k"); got != "true" {
		t.Errorf("bool arg = %q", got)
	}
	if got := extractArg(map[string]any{}, "k"); got != "" {
		t.Errorf("missing arg = %q", got)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{
		500:       "500",
		1500:      "1.5k",
		2_500_000: "2.5M",
	}
	for in, want := range cases {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	if got := formatElapsed(65000); got != "1m05s" {
		t.Errorf("formatElapsed = %q", got)
	}
}
