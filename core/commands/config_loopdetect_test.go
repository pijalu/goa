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

	if err := setConfigField(cfg, []string{"execution", "disable_stream_loop_detection"}, "true"); err != nil {
		t.Fatalf("set stream disable: %v", err)
	}
	if cfg.Execution.DisableStreamLoopDetection == nil || !*cfg.Execution.DisableStreamLoopDetection {
		t.Error("DisableStreamLoopDetection not set to true")
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

// TestApplyConfigSet_StreamLoopDetectionDisablesRuntime verifies the stream
// loop detection switch syncs to the live detector like the other kinds.
func TestApplyConfigSet_StreamLoopDetectionDisablesRuntime(t *testing.T) {
	ctx := newModeTestContext()
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	ctx.LoopDetector = ld
	ctx.ConfigSaver = &fakeConfigSaver{}

	if ld.Disabled("stream") {
		t.Fatal("precondition: stream detection should start enabled")
	}
	if err := applyConfigSet(ctx, "execution.disable_stream_loop_detection", "true"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if !ld.Disabled("stream") {
		t.Error("live detector not disabled after persistent set")
	}
	if ld.TempOverride("stream") {
		t.Error("persistent disable must not set the session temp override")
	}
	if err := applyConfigSet(ctx, "execution.disable_stream_loop_detection", "false"); err != nil {
		t.Fatalf("applyConfigSet re-enable: %v", err)
	}
	if ld.Disabled("stream") {
		t.Error("live detector still disabled after re-enable")
	}
}

// TestHandleConfigTemp_StreamLoopDetection verifies the
// /config:temp:stream_loop_detection:on|off session override.
func TestHandleConfigTemp_StreamLoopDetection(t *testing.T) {
	ctx := newModeTestContext()
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	ctx.LoopDetector = ld

	if err := handleConfigTemp(ctx, []string{"stream_loop_detection", "off"}); err != nil {
		t.Fatalf("handleConfigTemp off: %v", err)
	}
	if !ld.TempOverride("stream") {
		t.Error("temp override not set after stream_loop_detection:off")
	}
	if err := handleConfigTemp(ctx, []string{"stream_loop_detection", "on"}); err != nil {
		t.Fatalf("handleConfigTemp on: %v", err)
	}
	if ld.TempOverride("stream") {
		t.Error("temp override not cleared after stream_loop_detection:on")
	}
}

// TestLoopDetectorStreamMaxRepeats verifies the stream-loop repeat threshold
// defaults to 5 and follows live updates (0 or invalid restores the default).
func TestLoopDetectorStreamMaxRepeats(t *testing.T) {
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	if got := ld.StreamMaxRepeats(); got != 5 {
		t.Errorf("default StreamMaxRepeats = %d, want 5", got)
	}
	ld.SetStreamMaxRepeats(7)
	if got := ld.StreamMaxRepeats(); got != 7 {
		t.Errorf("StreamMaxRepeats = %d, want 7", got)
	}
	ld.SetStreamMaxRepeats(0)
	if got := ld.StreamMaxRepeats(); got != 5 {
		t.Errorf("StreamMaxRepeats after 0 = %d, want default 5", got)
	}

	// A zero config value also defaults to 5 at construction.
	ld = core.NewLoopDetector(core.LoopDetectorConfig{})
	if got := ld.StreamMaxRepeats(); got != 5 {
		t.Errorf("zero-config StreamMaxRepeats = %d, want default 5", got)
	}
}

// TestApplyConfigSet_StreamLoopMaxRepeats verifies /config set
// execution.stream_loop_max_repeats syncs the live detector threshold.
func TestApplyConfigSet_StreamLoopMaxRepeats(t *testing.T) {
	ctx := newModeTestContext()
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	ctx.LoopDetector = ld
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "execution.stream_loop_max_repeats", "7"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if got := ld.StreamMaxRepeats(); got != 7 {
		t.Errorf("StreamMaxRepeats = %d, want 7 after config set", got)
	}
	if got := ctx.Config.Execution.StreamLoopMaxRepeats; got != 7 {
		t.Errorf("config value = %d, want 7", got)
	}
}

// TestLoopDetectorStreamMinPeriod verifies the stream-loop minimum period
// defaults to 50 and follows live updates (0 or below-floor restores the
// default).
func TestLoopDetectorStreamMinPeriod(t *testing.T) {
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	if got := ld.StreamMinPeriod(); got != 50 {
		t.Errorf("default StreamMinPeriod = %d, want 50", got)
	}
	ld.SetStreamMinPeriod(80)
	if got := ld.StreamMinPeriod(); got != 80 {
		t.Errorf("StreamMinPeriod = %d, want 80", got)
	}
	ld.SetStreamMinPeriod(0)
	if got := ld.StreamMinPeriod(); got != 50 {
		t.Errorf("StreamMinPeriod after 0 = %d, want default 50", got)
	}
	ld.SetStreamMinPeriod(3)
	if got := ld.StreamMinPeriod(); got != 50 {
		t.Errorf("StreamMinPeriod after below-floor 3 = %d, want default 50", got)
	}

	// A zero config value also defaults to 50 at construction.
	ld = core.NewLoopDetector(core.LoopDetectorConfig{})
	if got := ld.StreamMinPeriod(); got != 50 {
		t.Errorf("zero-config StreamMinPeriod = %d, want default 50", got)
	}
}

// TestApplyConfigSet_StreamLoopMinPeriod verifies /config set
// execution.stream_loop_min_period syncs the live detector floor.
func TestApplyConfigSet_StreamLoopMinPeriod(t *testing.T) {
	ctx := newModeTestContext()
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	ctx.LoopDetector = ld
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "execution.stream_loop_min_period", "80"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if got := ld.StreamMinPeriod(); got != 80 {
		t.Errorf("StreamMinPeriod = %d, want 80 after config set", got)
	}
	if got := ctx.Config.Execution.StreamLoopMinPeriod; got != 80 {
		t.Errorf("config value = %d, want 80", got)
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

	// The stream kind behaves the same (persist + temp stack independently).
	ld.SetPersistOverride("stream", true)
	if !ld.Disabled("stream") {
		t.Error("stream detection should be disabled after SetPersistOverride")
	}
	ld.SetTempOverride("stream", true)
	ld.SetTempOverride("stream", false)
	if !ld.Disabled("stream") {
		t.Error("stream persistent disable must survive temp override toggling")
	}
	ld.SetPersistOverride("stream", false)
	if ld.Disabled("stream") {
		t.Error("stream detection should be re-enabled after SetPersistOverride(false)")
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
	if got := loopDetectionConfigKey("stream"); got != "execution.disable_stream_loop_detection" {
		t.Errorf("stream key = %q", got)
	}
}

// TestApplyConfigSet_LoopThresholdsSyncRuntime verifies /config set
// execution.loop_warning / execution.loop_interrupt updates the live tool-loop
// detector thresholds immediately (mirrors the stream_loop_max_repeats sync).
func TestApplyConfigSet_LoopThresholdsSyncRuntime(t *testing.T) {
	ctx := newModeTestContext()
	ld := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	ctx.LoopDetector = ld
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "execution.loop_warning", "4"); err != nil {
		t.Fatalf("applyConfigSet loop_warning: %v", err)
	}
	if got := ctx.Config.Execution.LoopWarning; got != 4 {
		t.Errorf("config LoopWarning = %d, want 4", got)
	}
	// The live detector must warn on the 4th identical call, not the 7th.
	for i := 0; i < 3; i++ {
		if lvl := ld.RecordToolCall("read", `{"path":"x"}`); lvl != core.LoopOK {
			t.Fatalf("call %d: got %v, want LoopOK", i+1, lvl)
		}
	}
	if lvl := ld.RecordToolCall("read", `{"path":"x"}`); lvl != core.LoopWarning {
		t.Errorf("call 4: got %v, want LoopWarning after live threshold update", lvl)
	}

	if err := applyConfigSet(ctx, "execution.loop_interrupt", "6"); err != nil {
		t.Fatalf("applyConfigSet loop_interrupt: %v", err)
	}
	if got := ctx.Config.Execution.LoopInterrupt; got != 6 {
		t.Errorf("config LoopInterrupt = %d, want 6", got)
	}
	// Continuing the streak: call 5 warns, call 6 interrupts.
	if lvl := ld.RecordToolCall("read", `{"path":"x"}`); lvl != core.LoopWarning {
		t.Errorf("call 5: got %v, want LoopWarning", lvl)
	}
	if lvl := ld.RecordToolCall("read", `{"path":"x"}`); lvl != core.LoopInterrupt {
		t.Errorf("call 6: got %v, want LoopInterrupt after live threshold update", lvl)
	}
}

// TestApplyConfigSet_RunawayLoopMaxRepeats verifies /config set
// execution.runaway_loop_max_repeats updates the persisted value the
// runaway-loop guardrail reads per agent build, and that an invalid value
// fails whole-config validation without mutating the live config.
func TestApplyConfigSet_RunawayLoopMaxRepeats(t *testing.T) {
	ctx := newModeTestContext()
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "execution.runaway_loop_max_repeats", "4"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if got := ctx.Config.Execution.RunawayLoopMaxRepeats; got != 4 {
		t.Errorf("config value = %d, want 4", got)
	}

	if err := applyConfigSet(ctx, "execution.runaway_loop_max_repeats", "-1"); err != nil {
		t.Fatalf("applyConfigSet invalid value: %v", err)
	}
	if got := ctx.Config.Execution.RunawayLoopMaxRepeats; got != 4 {
		t.Errorf("config mutated by invalid set = %d, want 4 (unchanged)", got)
	}
}

// TestConfigMenu_LoopThresholdsIncludeRunawayRow verifies the thresholds
// submenu exposes the runaway-loop repeat limit showing the effective default.
func TestConfigMenu_LoopThresholdsIncludeRunawayRow(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("loop_detection", true)
	sr.onSel("thresholds", true)

	var found bool
	for _, opt := range sr.options {
		if opt.Value == "runaway_repeats" {
			found = true
			if opt.Description != "2 (default)" {
				t.Errorf("runaway_repeats description = %q, want %q", opt.Description, "2 (default)")
			}
		}
	}
	if !found {
		t.Error("thresholds menu missing runaway_repeats row")
	}
}
