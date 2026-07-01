// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
)

func TestModeChangeMsg_ImplementsTeaMsg(t *testing.T) {
	msg := ModeChangeMsg{
		OldMode: internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo},
		NewMode: internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview},
		Source:  "user",
	}
	// tea.Msg is an empty interface — any type satisfies it.
	// This test verifies the struct is constructable and fields are accessible.
	if msg.OldMode.Major != internal.MajorCoder {
		t.Errorf("OldMode.Major = %q, want %q", msg.OldMode.Major, internal.MajorCoder)
	}
	if msg.NewMode.Major != internal.MajorPlanner {
		t.Errorf("NewMode.Major = %q, want %q", msg.NewMode.Major, internal.MajorPlanner)
	}
	if msg.Source != "user" {
		t.Errorf("Source = %q, want %q", msg.Source, "user")
	}
}

func TestSkillActivateMsg_Constructor(t *testing.T) {
	msg := SkillActivateMsg{
		Skill: "test-gen",
		Mode:  internal.ModeState{Major: internal.MajorCoder, Skills: []string{"test-gen"}, Autonomy: internal.AutonomyYolo},
	}
	if msg.Skill != "test-gen" {
		t.Errorf("Skill = %q, want %q", msg.Skill, "test-gen")
	}
	if msg.Mode.Major != internal.MajorCoder {
		t.Errorf("Mode.Major = %q, want %q", msg.Mode.Major, internal.MajorCoder)
	}
}

func TestSkillDeactivateMsg_Constructor(t *testing.T) {
	msg := SkillDeactivateMsg{
		Skill: "document",
		Mode:  internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview},
	}
	if msg.Skill != "document" {
		t.Errorf("Skill = %q, want %q", msg.Skill, "document")
	}
}

func TestAutonomyChangeMsg_Constructor(t *testing.T) {
	msg := AutonomyChangeMsg{
		OldAutonomy: internal.AutonomyYolo,
		NewAutonomy: internal.AutonomyConfirm,
		Source:      "user",
	}
	if msg.OldAutonomy != internal.AutonomyYolo {
		t.Errorf("OldAutonomy = %q, want %q", msg.OldAutonomy, internal.AutonomyYolo)
	}
	if msg.NewAutonomy != internal.AutonomyConfirm {
		t.Errorf("NewAutonomy = %q, want %q", msg.NewAutonomy, internal.AutonomyConfirm)
	}
}

func TestPipelineProgressMsg_Constructor(t *testing.T) {
	msg := PipelineProgressMsg{
		PipelineID: "implement-feature",
		StageID:    "plan",
		Status:     "running",
	}
	if msg.PipelineID != "implement-feature" {
		t.Errorf("PipelineID = %q, want %q", msg.PipelineID, "implement-feature")
	}
	if msg.StageID != "plan" {
		t.Errorf("StageID = %q, want %q", msg.StageID, "plan")
	}
	if msg.Status != "running" {
		t.Errorf("Status = %q, want %q", msg.Status, "running")
	}
}

func TestThinkingBlock_MultiLineNoDuplicates(t *testing.T) {
	tb := newThinkingBlock("line one\nline two\nline three")
	lines := tb.Render(80)
	if len(lines) < 4 {
		t.Fatalf("expected header + 3 lines, got %d lines", len(lines))
	}
	rendered := strings.Join(lines, "\n")
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected %q in rendered output, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "line one line two") {
		t.Errorf("thinking lines should not be joined into a single wrapped paragraph")
	}
}

func TestCollapsibleComponent_ExpandsAndCollapses(t *testing.T) {
	c := newCollapsibleComponent("companion", "body text")
	lines := c.Render(80)
	if len(lines) < 2 {
		t.Fatalf("expected expanded body, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "▾ companion") {
		t.Errorf("expected expanded header, got %q", lines[0])
	}
	if !strings.Contains(strings.Join(lines, "\n"), "body text") {
		t.Errorf("expected body text, got %q", strings.Join(lines, "\n"))
	}

	c.HandleInput(KeyEnter)
	lines = c.Render(80)
	if strings.Contains(strings.Join(lines, "\n"), "body text") {
		t.Errorf("expected body hidden after collapse")
	}

	c.SetDone()
	lines = c.Render(80)
	if !strings.Contains(strings.Join(lines, "\n"), "[done]") {
		t.Errorf("expected done marker, got %q", strings.Join(lines, "\n"))
	}
}

func TestInterAgentMsg_Constructor(t *testing.T) {
	msg := InterAgentMsg{
		From:    "planner",
		To:      "coder",
		Content: "Please implement the auth module",
	}
	if msg.From != "planner" {
		t.Errorf("From = %q, want %q", msg.From, "planner")
	}
	if msg.To != "coder" {
		t.Errorf("To = %q, want %q", msg.To, "coder")
	}
	if msg.Content != "Please implement the auth module" {
		t.Errorf("Content = %q, want %q", msg.Content, "Please implement the auth module")
	}
}
