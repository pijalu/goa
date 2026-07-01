// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/prompts"
)

func TestNewModeRegistry_HasBuiltins(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	// Built-in majors should be resolvable
	spec, err := r.Resolve(internal.MajorCoder)
	if err != nil {
		t.Fatalf("Resolve(coder) returned error: %v", err)
	}
	if spec.Major != internal.MajorCoder {
		t.Errorf("Resolve(coder).Major = %q, want %q", spec.Major, internal.MajorCoder)
	}

	spec, err = r.Resolve(internal.MajorPlanner)
	if err != nil {
		t.Fatalf("Resolve(planner) returned error: %v", err)
	}
	if spec.Major != internal.MajorPlanner {
		t.Errorf("Resolve(planner).Major = %q, want %q", spec.Major, internal.MajorPlanner)
	}

	spec, err = r.Resolve(internal.MajorReviewer)
	if err != nil {
		t.Fatalf("Resolve(reviewer) returned error: %v", err)
	}
	if spec.Major != internal.MajorReviewer {
		t.Errorf("Resolve(reviewer).Major = %q, want %q", spec.Major, internal.MajorReviewer)
	}
}

func TestNewModeRegistry_BuiltinCount(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	// Should have exactly 4 built-in majors (coder, planner, reviewer, coding-posture)
	count := 0
	for _, m := range []internal.MajorMode{internal.MajorCoder, internal.MajorPlanner, internal.MajorReviewer, "coding-posture"} {
		if _, err := r.Resolve(m); err == nil {
			count++
		}
	}
	if count != 4 {
		t.Errorf("expected 4 resolvable built-in majors, got %d", count)
	}
}

func TestModeRegistry_Resolve_Coder(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	spec, err := r.Resolve(internal.MajorCoder)
	if err != nil {
		t.Fatalf("Resolve(coder): %v", err)
	}

	if spec.Name == "" {
		t.Error("Resolve(coder).Name is empty")
	}
	if spec.DefaultAutonomy != internal.AutonomySolo {
		t.Errorf("Resolve(coder).DefaultAutonomy = %q, want %q", spec.DefaultAutonomy, internal.AutonomySolo)
	}
}

func TestModeRegistry_Resolve_Planner(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	spec, err := r.Resolve(internal.MajorPlanner)
	if err != nil {
		t.Fatalf("Resolve(planner): %v", err)
	}

	if spec.Name == "" {
		t.Error("Resolve(planner).Name is empty")
	}
	if spec.DefaultAutonomy != internal.AutonomyReview {
		t.Errorf("Resolve(planner).DefaultAutonomy = %q, want %q", spec.DefaultAutonomy, internal.AutonomyReview)
	}
}

func TestModeRegistry_Resolve_Reviewer(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	spec, err := r.Resolve(internal.MajorReviewer)
	if err != nil {
		t.Fatalf("Resolve(reviewer): %v", err)
	}

	if spec.Name == "" {
		t.Error("Resolve(reviewer).Name is empty")
	}
	if spec.DefaultAutonomy != internal.AutonomyReview {
		t.Errorf("Resolve(reviewer).DefaultAutonomy = %q, want %q", spec.DefaultAutonomy, internal.AutonomyReview)
	}
}

func TestModeRegistry_Resolve_CodingPosture(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	spec, err := r.Resolve("coding-posture")
	if err != nil {
		t.Fatalf("Resolve(coding-posture): %v", err)
	}

	if spec.Name == "" {
		t.Error("Resolve(coding-posture).Name is empty")
	}
	if spec.DefaultAutonomy != internal.AutonomySolo {
		t.Errorf("Resolve(coding-posture).DefaultAutonomy = %q, want %q", spec.DefaultAutonomy, internal.AutonomySolo)
	}
	if !strings.Contains(spec.Body, "Coding Posture") {
		t.Error("Resolve(coding-posture).Body missing Coding Posture preamble")
	}
}

func TestModeRegistry_Resolve_PlannerGuard(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	spec, err := r.Resolve(internal.MajorPlanner)
	if err != nil {
		t.Fatalf("Resolve(planner): %v", err)
	}

	if len(spec.Guard.Rules) == 0 {
		t.Fatal("expected planner mode to have guard rules")
	}
	foundWrite := false
	for _, rule := range spec.Guard.Rules {
		for _, tool := range rule.Tools {
			if tool == "write" {
				foundWrite = true
			}
		}
	}
	if !foundWrite {
		t.Error("expected planner guard to restrict write tool")
	}
}

func TestModeRegistry_Resolve_Unknown(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	_, err := r.Resolve("hacker")
	if err == nil {
		t.Fatal("Resolve(hacker): expected error, got nil")
	}
}

func TestModeRegistry_Resolve_Custom(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	r.RegisterMajor(MajorModeSpec{
		Major:           "hacker",
		Name:            "Hacker Mode",
		DefaultSkills:   []string{"exploit"},
		AllowedTools:    []string{"bash", "search"},
		BlockedPaths:    []string{"/etc"},
		DefaultAutonomy: internal.AutonomyYolo,
	})

	spec, err := r.Resolve("hacker")
	if err != nil {
		t.Fatalf("Resolve(hacker): %v", err)
	}
	if spec.Name != "Hacker Mode" {
		t.Errorf("Resolve(hacker).Name = %q, want %q", spec.Name, "Hacker Mode")
	}
	if spec.DefaultAutonomy != internal.AutonomyYolo {
		t.Errorf("Resolve(hacker).DefaultAutonomy = %q, want %q", spec.DefaultAutonomy, internal.AutonomyYolo)
	}
	if len(spec.AllowedTools) != 2 || spec.AllowedTools[0] != "bash" {
		t.Errorf("Resolve(hacker).AllowedTools = %v", spec.AllowedTools)
	}
	if len(spec.BlockedPaths) != 1 || spec.BlockedPaths[0] != "/etc" {
		t.Errorf("Resolve(hacker).BlockedPaths = %v", spec.BlockedPaths)
	}
}

func TestModeRegistry_Validate_Valid(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	err := r.Validate(internal.ModeState{Major: internal.MajorCoder})
	if err != nil {
		t.Fatalf("Validate(coder): %v", err)
	}

	err = r.Validate(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})
	if err != nil {
		t.Fatalf("Validate(planner, review): %v", err)
	}
}

func TestModeRegistry_Validate_UnknownMajor(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	err := r.Validate(internal.ModeState{Major: "hacker"})
	if err == nil {
		t.Fatal("Validate(hacker): expected error, got nil")
	}
}

func TestModeRegistry_Validate_EmptyMajor(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	err := r.Validate(internal.ModeState{})
	if err == nil {
		t.Fatal("Validate(empty): expected error, got nil")
	}
}

func TestModeRegistry_Validate_UnknownSkill(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	err := r.Validate(internal.ModeState{
		Major:  internal.MajorCoder,
		Skills: []string{"nonexistent-skill"},
	})
	if err == nil {
		t.Fatal("Validate(coder with unknown skill): expected error, got nil")
	}
}

func TestModeRegistry_Validate_KnownSkill(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	r.RegisterSkill(SkillSpec{
		Name:        "test-gen",
		Description: "Generate tests",
	})
	err := r.Validate(internal.ModeState{
		Major:  internal.MajorCoder,
		Skills: []string{"test-gen"},
	})
	if err != nil {
		t.Fatalf("Validate(coder with known skill): %v", err)
	}
}

func TestModeRegistry_DefaultForMajor_Coder(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	ms := r.DefaultForMajor(internal.MajorCoder)
	if ms.Major != internal.MajorCoder {
		t.Errorf("DefaultForMajor(coder).Major = %q, want %q", ms.Major, internal.MajorCoder)
	}
	if ms.Autonomy != internal.AutonomySolo {
		t.Errorf("DefaultForMajor(coder).Autonomy = %q, want %q", ms.Autonomy, internal.AutonomySolo)
	}
}

func TestModeRegistry_DefaultForMajor_Planner(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	ms := r.DefaultForMajor(internal.MajorPlanner)
	if ms.Major != internal.MajorPlanner {
		t.Errorf("DefaultForMajor(planner).Major = %q, want %q", ms.Major, internal.MajorPlanner)
	}
	if ms.Autonomy != internal.AutonomyReview {
		t.Errorf("DefaultForMajor(planner).Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyReview)
	}
}

func TestModeRegistry_DefaultForMajor_Reviewer(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	ms := r.DefaultForMajor(internal.MajorReviewer)
	if ms.Major != internal.MajorReviewer {
		t.Errorf("DefaultForMajor(reviewer).Major = %q, want %q", ms.Major, internal.MajorReviewer)
	}
	if ms.Autonomy != internal.AutonomyReview {
		t.Errorf("DefaultForMajor(reviewer).Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyReview)
	}
}

func TestModeRegistry_DefaultForMajor_Custom(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	r.RegisterMajor(MajorModeSpec{
		Major:           "hacker",
		Name:            "Hacker Mode",
		DefaultAutonomy: internal.AutonomyConfirm,
	})

	ms := r.DefaultForMajor("hacker")
	if ms.Major != "hacker" {
		t.Errorf("DefaultForMajor(hacker).Major = %q, want %q", ms.Major, "hacker")
	}
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("DefaultForMajor(hacker).Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyConfirm)
	}
}

func TestModeRegistry_DefaultForMajor_UnknownReturnsZero(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	ms := r.DefaultForMajor("unknown")
	if !ms.IsZero() {
		t.Errorf("DefaultForMajor(unknown).IsZero() = false, want true")
	}
}

func TestModeRegistry_RegisterMajor_Overwrite(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	// Register a custom version of an existing major
	r.RegisterMajor(MajorModeSpec{
		Major:           internal.MajorCoder,
		Name:            "Custom Coder",
		DefaultAutonomy: internal.AutonomyReview,
	})

	spec, err := r.Resolve(internal.MajorCoder)
	if err != nil {
		t.Fatalf("Resolve(coder) after overwrite: %v", err)
	}
	if spec.Name != "Custom Coder" {
		t.Errorf("After overwrite Name = %q, want %q", spec.Name, "Custom Coder")
	}
	if spec.DefaultAutonomy != internal.AutonomyReview {
		t.Errorf("After overwrite DefaultAutonomy = %q, want %q", spec.DefaultAutonomy, internal.AutonomyReview)
	}
}

func TestModeRegistry_RegisterSkill(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	r.RegisterSkill(SkillSpec{
		Name:            "lint",
		Description:     "Run a linter on the codebase",
		LinkedMajor:     internal.MajorReviewer,
		DefaultAutonomy: internal.AutonomyYolo,
	})

	// Skill registration doesn't affect major resolution
	spec, err := r.Resolve(internal.MajorCoder)
	if err != nil {
		t.Fatalf("Resolve(coder) after skill registration: %v", err)
	}
	if spec.Major != internal.MajorCoder {
		t.Errorf("Resolve(coder).Major = %q, want %q", spec.Major, internal.MajorCoder)
	}
}

func TestModeRegistry_SystemPrompt(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	body := r.SystemPrompt(internal.MajorCoder)
	if body == "" {
		t.Error("SystemPrompt(coder) should return non-empty body")
	}
	if body2 := r.SystemPrompt("unknown"); body2 != "" {
		t.Error("SystemPrompt(unknown) should return empty string")
	}
}

func TestModeRegistry_DefaultModeHasDescription(t *testing.T) {
	r := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	spec, err := r.Resolve(internal.MajorPlanner)
	if err != nil {
		t.Fatalf("Resolve(planner): %v", err)
	}
	if spec.Description == "" {
		t.Error("expected planner mode to have a description loaded from embedded mode file")
	}
}
