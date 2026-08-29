// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"testing"
)

// TestMergeExecution_LoopAutoResumeSurvivesCascade verifies the loop
// auto-resume fields are copied across the merge so a value persisted to any
// layer survives the cascade.
func TestMergeExecution_LoopAutoResumeSurvivesCascade(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{Mode: "yolo"}}
	overlay := &Config{Execution: ExecutionConfig{
		LoopAutoResume:        boolPtr(true),
		LoopAutoResumeMessage: "resume now please",
		LoopAutoResumeMax:     5,
	}}

	mergeExecution(&base.Execution, &overlay.Execution)

	if !base.Execution.LoopAutoResumeEnabled() {
		t.Fatal("mergeExecution dropped LoopAutoResume")
	}
	if base.Execution.LoopAutoResumeMessage != "resume now please" {
		t.Fatalf("LoopAutoResumeMessage = %q, want %q", base.Execution.LoopAutoResumeMessage, "resume now please")
	}
	if base.Execution.LoopAutoResumeMax != 5 {
		t.Fatalf("LoopAutoResumeMax = %d, want 5", base.Execution.LoopAutoResumeMax)
	}
}

// TestMergeExecution_LoopAutoResumeMessageEmptyPreserves verifies an empty
// overlay message does not clobber a lower-layer value.
func TestMergeExecution_LoopAutoResumeMessageEmptyPreserves(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{LoopAutoResumeMessage: "keep me"}}
	overlay := &Config{Execution: ExecutionConfig{LoopAutoResume: boolPtr(true)}}

	mergeExecution(&base.Execution, &overlay.Execution)

	if base.Execution.LoopAutoResumeMessage != "keep me" {
		t.Fatalf("empty overlay clobbered message: got %q", base.Execution.LoopAutoResumeMessage)
	}
}

// TestDefaultConfig_LoopAutoResume verifies the embedded defaults: feature
// off, default message, default cap.
func TestDefaultConfig_LoopAutoResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Execution.LoopAutoResumeEnabled() {
		t.Error("Execution.LoopAutoResumeEnabled() = true, want false (default off)")
	}
	if cfg.Execution.LoopAutoResumeMessage != "loop detected and you were stopped - resume now" {
		t.Errorf("default LoopAutoResumeMessage = %q", cfg.Execution.LoopAutoResumeMessage)
	}
	if cfg.Execution.LoopAutoResumeMax != 3 {
		t.Errorf("default LoopAutoResumeMax = %d, want 3", cfg.Execution.LoopAutoResumeMax)
	}
}

// TestValidate_LoopAutoResumeMaxNegative verifies a negative cap is rejected.
func TestValidate_LoopAutoResumeMaxNegative(t *testing.T) {
	cfg := &Config{}
	cfg.Execution.LoopAutoResumeMax = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate should reject negative loop_auto_resume_max")
	}
}

// TestMergeExecution_LoopAutoResumeTriState pins the tri-state contract
// (bugs.md: loop auto-resume toggle not saved across sessions): a higher
// cascade layer that OMITS loop_auto_resume must NOT clobber a lower layer's
// explicit value; an explicit value at a higher layer wins. A plain bool
// cannot distinguish "absent" from "explicit false", so the field is *bool
// and the merge is nil-gated.
func TestMergeExecution_LoopAutoResumeTriState(t *testing.T) {
	truthy := true
	falsy := false
	cases := []struct {
		name string
		base *bool // lower layer (e.g. home)
		src  *bool // higher layer (e.g. project)
		want bool
	}{
		{"lower true, higher omits", &truthy, nil, true},     // the reported bug
		{"lower true, higher false", &truthy, &falsy, false}, // explicit opt-out wins
		{"lower false, higher true", &falsy, &truthy, true},
		{"lower omits, higher true", nil, &truthy, true},
		{"lower omits, higher false", nil, &falsy, false},
		{"both omit", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := &Config{Execution: ExecutionConfig{LoopAutoResume: tc.base}}
			overlay := &Config{Execution: ExecutionConfig{LoopAutoResume: tc.src}}
			mergeExecution(&base.Execution, &overlay.Execution)
			if got := base.Execution.LoopAutoResumeEnabled(); got != tc.want {
				t.Fatalf("LoopAutoResumeEnabled() = %v, want %v (base=%v src=%v)",
					got, tc.want, boolPtrVal(tc.base), boolPtrVal(tc.src))
			}
		})
	}
}

// TestLoopAutoResumeCascade_HomeTrueProjectOmits is the end-to-end repro of
// the reported bug: home states loop_auto_resume:true and the project layer
// is a stale full-struct dump whose execution section predates the feature
// (so it omits the key). The merged config must resolve TRUE.
func TestLoopAutoResumeCascade_HomeTrueProjectOmits(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), "execution:\n  loop_auto_resume: true\n")
	// Stale pre-feature project dump: execution section WITHOUT the key.
	writeConfig(t, filepath.Join(project, ".goa", "config.yaml"), "execution:\n  retries: 15\n  mode: yolo\n")

	loader := NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.Execution.LoopAutoResumeEnabled() {
		t.Error("home loop_auto_resume:true must survive a project layer that omits the key")
	}
}

// TestLoopAutoResumeCascade_ProjectExplicitFalseWins verifies the deliberate
// opt-out direction: an explicit project-layer false shadows home's true.
func TestLoopAutoResumeCascade_ProjectExplicitFalseWins(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), "execution:\n  loop_auto_resume: true\n")
	writeConfig(t, filepath.Join(project, ".goa", "config.yaml"), "execution:\n  loop_auto_resume: false\n")

	loader := NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Execution.LoopAutoResumeEnabled() {
		t.Error("explicit project loop_auto_resume:false must win over home's true")
	}
}

func boolPtrVal(p *bool) any {
	if p == nil {
		return "nil"
	}
	return *p
}
