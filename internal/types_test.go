// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"testing"
)

func TestModeState_String(t *testing.T) {
	tests := []struct {
		name     string
		state    ModeState
		expected string
	}{
		{
			name:     "coder with test-gen skill and yolo autonomy",
			state:    ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo},
			expected: "coder+test-gen (yolo)",
		},
		{
			name:     "planner with no skills and review autonomy",
			state:    ModeState{Major: MajorPlanner, Autonomy: AutonomyReview},
			expected: "planner (review)",
		},
		{
			name:     "reviewer with document skill and confirm autonomy",
			state:    ModeState{Major: MajorReviewer, Skills: []string{"document"}, Autonomy: AutonomyConfirm},
			expected: "reviewer+document (confirm)",
		},
		{
			name:     "multiple skills",
			state:    ModeState{Major: MajorCoder, Skills: []string{"test-gen", "document"}, Autonomy: AutonomyYolo},
			expected: "coder+test-gen,document (yolo)",
		},
		{
			name:     "empty mode state",
			state:    ModeState{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.state.String()
			if got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestModeState_IsZero(t *testing.T) {
	tests := []struct {
		name     string
		state    ModeState
		expected bool
	}{
		{
			name:     "zero when major is empty",
			state:    ModeState{},
			expected: true,
		},
		{
			name:     "not zero when major is set",
			state:    ModeState{Major: MajorCoder},
			expected: false,
		},
		{
			name:     "not zero when major is set with skills",
			state:    ModeState{Major: MajorPlanner, Skills: []string{"document"}},
			expected: false,
		},
		{
			name:     "not zero when major is set with autonomy",
			state:    ModeState{Major: MajorReviewer, Autonomy: AutonomyReview},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.state.IsZero()
			if got != tt.expected {
				t.Errorf("IsZero() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestModeState_WithMajor(t *testing.T) {
	original := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	updated := original.WithMajor(MajorPlanner)

	// Updated has the new major
	if updated.Major != MajorPlanner {
		t.Errorf("WithMajor: got Major = %q, want %q", updated.Major, MajorPlanner)
	}
	// Original is unchanged (immutability)
	if original.Major != MajorCoder {
		t.Errorf("WithMajor changed original: got Major = %q, want %q", original.Major, MajorCoder)
	}
	// Other fields preserved
	if updated.Autonomy != original.Autonomy {
		t.Errorf("WithMajor changed Autonomy from %q to %q", original.Autonomy, updated.Autonomy)
	}
	if len(updated.Skills) != 1 || updated.Skills[0] != "test-gen" {
		t.Errorf("WithMajor changed Skills to %v", updated.Skills)
	}
}

func TestModeState_WithSkills(t *testing.T) {
	original := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	newSkills := []string{"document", "lint"}
	updated := original.WithSkills(newSkills)

	// Updated has the new skills
	if len(updated.Skills) != 2 || updated.Skills[0] != "document" || updated.Skills[1] != "lint" {
		t.Errorf("WithSkills: got Skills = %v, want [document lint]", updated.Skills)
	}
	// Original is unchanged
	if len(original.Skills) != 1 || original.Skills[0] != "test-gen" {
		t.Errorf("WithSkills changed original: got Skills = %v", original.Skills)
	}
	// Modifying the input slice after calling WithSkills should not affect the stored slice
	newSkills[0] = "hacked"
	if updated.Skills[0] == "hacked" {
		t.Errorf("WithSkills did not copy the input slice — aliasing detected")
	}
	// Other fields preserved
	if updated.Major != original.Major {
		t.Errorf("WithSkills changed Major")
	}
	if updated.Autonomy != original.Autonomy {
		t.Errorf("WithSkills changed Autonomy")
	}
}

func TestModeState_WithAutonomy(t *testing.T) {
	original := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	updated := original.WithAutonomy(AutonomyReview)

	// Updated has the new autonomy
	if updated.Autonomy != AutonomyReview {
		t.Errorf("WithAutonomy: got Autonomy = %q, want %q", updated.Autonomy, AutonomyReview)
	}
	// Original is unchanged
	if original.Autonomy != AutonomyYolo {
		t.Errorf("WithAutonomy changed original: got Autonomy = %q, want %q", original.Autonomy, AutonomyYolo)
	}
	// Other fields preserved
	if updated.Major != original.Major {
		t.Errorf("WithAutonomy changed Major")
	}
	if len(updated.Skills) != 1 || updated.Skills[0] != "test-gen" {
		t.Errorf("WithAutonomy changed Skills")
	}
}

func TestModeState_AddSkill(t *testing.T) {
	original := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	updated := original.AddSkill("document")

	// Updated has the new skill appended
	if len(updated.Skills) != 2 {
		t.Errorf("AddSkill: got %d skills, want 2", len(updated.Skills))
	}
	if updated.Skills[0] != "test-gen" || updated.Skills[1] != "document" {
		t.Errorf("AddSkill: got Skills = %v, want [test-gen document]", updated.Skills)
	}
	// Original is unchanged
	if len(original.Skills) != 1 || original.Skills[0] != "test-gen" {
		t.Errorf("AddSkill changed original: got Skills = %v", original.Skills)
	}
	// Other fields preserved
	if updated.Major != original.Major {
		t.Errorf("AddSkill changed Major")
	}
	if updated.Autonomy != original.Autonomy {
		t.Errorf("AddSkill changed Autonomy")
	}
}

func TestModeState_AddSkill_Duplicate(t *testing.T) {
	// AddSkill should not add a duplicate skill
	state := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	updated := state.AddSkill("test-gen")

	if len(updated.Skills) != 1 {
		t.Errorf("AddSkill duplicate: got %d skills, want 1", len(updated.Skills))
	}
}

func TestModeState_RemoveSkill(t *testing.T) {
	original := ModeState{Major: MajorCoder, Skills: []string{"test-gen", "document"}, Autonomy: AutonomyYolo}
	updated := original.RemoveSkill("test-gen")

	// Updated has the skill removed
	if len(updated.Skills) != 1 || updated.Skills[0] != "document" {
		t.Errorf("RemoveSkill: got Skills = %v, want [document]", updated.Skills)
	}
	// Original is unchanged
	if len(original.Skills) != 2 {
		t.Errorf("RemoveSkill changed original: got len = %d, want 2", len(original.Skills))
	}
	// Other fields preserved
	if updated.Major != original.Major {
		t.Errorf("RemoveSkill changed Major")
	}
	if updated.Autonomy != original.Autonomy {
		t.Errorf("RemoveSkill changed Autonomy")
	}
}

func TestModeState_RemoveSkill_NotPresent(t *testing.T) {
	// Removing a skill that doesn't exist should return a copy unchanged
	state := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	updated := state.RemoveSkill("nonexistent")

	if len(updated.Skills) != 1 || updated.Skills[0] != "test-gen" {
		t.Errorf("RemoveSkill nonexistent: got Skills = %v, want [test-gen]", updated.Skills)
	}
}

func TestModeState_RemoveSkill_LastSkill(t *testing.T) {
	// Removing the last skill should leave an empty (nil) slice
	state := ModeState{Major: MajorCoder, Skills: []string{"test-gen"}, Autonomy: AutonomyYolo}
	updated := state.RemoveSkill("test-gen")

	if len(updated.Skills) != 0 {
		t.Errorf("RemoveSkill last: got %d skills, want 0", len(updated.Skills))
	}
}

func TestModeState_ChainedCalls(t *testing.T) {
	// Verify that chained With* calls produce the expected result
	base := ModeState{}
	result := base.
		WithMajor(MajorCoder).
		WithAutonomy(AutonomyYolo).
		WithSkills([]string{"test-gen"})

	if result.Major != MajorCoder {
		t.Errorf("Chained: Major = %q, want %q", result.Major, MajorCoder)
	}
	if result.Autonomy != AutonomyYolo {
		t.Errorf("Chained: Autonomy = %q, want %q", result.Autonomy, AutonomyYolo)
	}
	if len(result.Skills) != 1 || result.Skills[0] != "test-gen" {
		t.Errorf("Chained: Skills = %v, want [test-gen]", result.Skills)
	}

	// Base should still be zero
	if !base.IsZero() {
		t.Errorf("Chained modified base: IsZero() = false, want true")
	}
}

func TestModeState_DeepCopyOnWithSkills(t *testing.T) {
	// Verify that two WithSkills calls produce independent slices
	base := ModeState{Major: MajorCoder, Autonomy: AutonomyYolo}
	s1 := base.WithSkills([]string{"a", "b"})
	s2 := base.WithSkills([]string{"c", "d"})

	if len(s1.Skills) != 2 || s1.Skills[0] != "a" {
		t.Errorf("s1 Skills = %v, want [a b]", s1.Skills)
	}
	if len(s2.Skills) != 2 || s2.Skills[0] != "c" {
		t.Errorf("s2 Skills = %v, want [c d]", s2.Skills)
	}
}

func TestMajorModeConstants(t *testing.T) {
	if MajorCoder != "coder" {
		t.Errorf("MajorCoder = %q, want %q", MajorCoder, "coder")
	}
	if MajorPlanner != "planner" {
		t.Errorf("MajorPlanner = %q, want %q", MajorPlanner, "planner")
	}
	if MajorReviewer != "reviewer" {
		t.Errorf("MajorReviewer = %q, want %q", MajorReviewer, "reviewer")
	}
}

func TestAutonomyLevelConstants(t *testing.T) {
	if AutonomyYolo != "yolo" {
		t.Errorf("AutonomyYolo = %q, want %q", AutonomyYolo, "yolo")
	}
	if AutonomyConfirm != "confirm" {
		t.Errorf("AutonomyConfirm = %q, want %q", AutonomyConfirm, "confirm")
	}
	if AutonomyReview != "review" {
		t.Errorf("AutonomyReview = %q, want %q", AutonomyReview, "review")
	}
}
