// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// TestSetConfigField_LoopDetectionDisable verifies the persistent loop
// detection switches can be set via /config set.
func TestSetConfigField_LoopDetectionDisable(t *testing.T) {
	cfg := &config.Config{}

	if err := setConfigField(cfg, []string{"execution", "disable_thinking_loop_detection"}, "true"); err != nil {
		t.Fatalf("set thinking disable: %v", err)
	}
	if cfg.Execution.DisableThinkingLoopDetection == nil || !*cfg.Execution.DisableThinkingLoopDetection {
		t.Error("DisableThinkingLoopDetection not set to true")
	}

	if err := setConfigField(cfg, []string{"execution", "disable_tool_loop_detection"}, "off"); err != nil {
		t.Fatalf("set tool disable: %v", err)
	}
	if cfg.Execution.DisableToolLoopDetection == nil || *cfg.Execution.DisableToolLoopDetection {
		t.Error("DisableToolLoopDetection not set to false")
	}
}

// TestApplyConfigSet_LoopDetectionDisablesRuntime verifies that setting the
// persistent key also flips the live loop detector (runtime sync).
func TestApplyConfigSet_LoopDetectionDisablesRuntime(t *testing.T) {
	ctx := newModeTestContext()
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	ctx.LoopDetector = ld
	ctx.ConfigSaver = &fakeConfigSaver{}

	if ld.Disabled("think") {
		t.Fatal("precondition: thinking detection should start enabled")
	}

	if err := applyConfigSet(ctx, "execution.disable_thinking_loop_detection", "true"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if !ld.Disabled("think") {
		t.Error("live detector not disabled after persistent set")
	}
	// Persistent disable must not be reported as a session temp override.
	if ld.TempOverride("think") {
		t.Error("persistent disable must not set the session temp override")
	}
	// The detector must short-circuit on a repeated reasoning line.
	for i := 0; i < 8; i++ {
		if lvl := ld.RecordThinkingDelta("the exact same reasoning line repeated many times over here\n"); lvl != core.LoopOK {
			t.Fatalf("disabled detector returned %d, want LoopOK", lvl)
		}
	}

	// Re-enable via the same key.
	if err := applyConfigSet(ctx, "execution.disable_thinking_loop_detection", "false"); err != nil {
		t.Fatalf("applyConfigSet re-enable: %v", err)
	}
	if ld.Disabled("think") {
		t.Error("live detector still disabled after re-enable")
	}
}

// TestLoopDetectorPersistOverride verifies persistent disable/enable behaviour
// independent of the session temp override.
func TestLoopDetectorPersistOverride(t *testing.T) {
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())

	ld.SetPersistOverride("tool", true)
	if !ld.Disabled("tool") {
		t.Error("tool detection should be disabled after SetPersistOverride")
	}
	for i := 0; i < 12; i++ {
		if lvl := ld.RecordToolCall("bash", `ls`); lvl != core.LoopOK {
			t.Fatalf("persist-disabled tool detection returned %d, want LoopOK", lvl)
		}
	}

	// A session temp override stacks on top; clearing the temp one must not
	// clear the persistent one.
	ld.SetTempOverride("tool", true)
	ld.SetTempOverride("tool", false)
	if !ld.Disabled("tool") {
		t.Error("persistent disable must survive temp override toggling")
	}

	ld.SetPersistOverride("tool", false)
	if ld.Disabled("tool") {
		t.Error("tool detection should be re-enabled after SetPersistOverride(false)")
	}
}

// TestLoopDetectionStatusLabel verifies the menu status label distinguishes
// session-only and saved disables.
func TestLoopDetectionStatusLabel(t *testing.T) {
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	if got := loopDetectionStatusLabel(ld, "think"); got != "on" {
		t.Errorf("default = %q, want on", got)
	}
	ld.SetTempOverride("think", true)
	if got := loopDetectionStatusLabel(ld, "think"); got != "off (session)" {
		t.Errorf("temp off = %q, want off (session)", got)
	}
	ld.SetTempOverride("think", false)
	ld.SetPersistOverride("think", true)
	if got := loopDetectionStatusLabel(ld, "think"); got != "off (saved)" {
		t.Errorf("persist off = %q, want off (saved)", got)
	}
	if got := loopDetectionStatusLabel(nil, "think"); got != "on" {
		t.Errorf("nil detector = %q, want on", got)
	}
}

// TestLoopDetectionConfigKey maps kinds to the persisted config keys.
func TestLoopDetectionConfigKey(t *testing.T) {
	if got := loopDetectionConfigKey("think"); got != "execution.disable_thinking_loop_detection" {
		t.Errorf("think key = %q", got)
	}
	if got := loopDetectionConfigKey("tool"); got != "execution.disable_tool_loop_detection" {
		t.Errorf("tool key = %q", got)
	}
}
