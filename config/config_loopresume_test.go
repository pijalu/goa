// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "testing"

// TestMergeExecution_LoopAutoResumeSurvivesCascade verifies the loop
// auto-resume fields are copied across the merge so a value persisted to any
// layer survives the cascade.
func TestMergeExecution_LoopAutoResumeSurvivesCascade(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{Mode: "yolo"}}
	overlay := &Config{Execution: ExecutionConfig{
		LoopAutoResume:        true,
		LoopAutoResumeMessage: "resume now please",
		LoopAutoResumeMax:     5,
	}}

	mergeExecution(&base.Execution, &overlay.Execution)

	if !base.Execution.LoopAutoResume {
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
	overlay := &Config{Execution: ExecutionConfig{LoopAutoResume: true}}

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
	if cfg.Execution.LoopAutoResume {
		t.Error("Execution.LoopAutoResume = true, want false (default off)")
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
