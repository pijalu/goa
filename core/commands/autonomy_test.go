// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func newAutonomyTestContext() core.Context {
	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	return core.Context{
		Config:       cfg,
		AgentManager: core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, ""),
	}
}

func TestAutonomyCommand_Switch_Persists(t *testing.T) {
	ctx := newAutonomyTestContext()
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	cmd := &AutonomyCommand{}

	err := cmd.Run(ctx, []string{"confirm"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ctx.Config.Mode.Defaults == nil {
		t.Fatal("Mode.Defaults is nil")
	}
	if ctx.Config.Mode.Defaults[internal.MajorCoder] != internal.AutonomyConfirm {
		t.Errorf("Mode.Defaults[coder] = %q, want %q", ctx.Config.Mode.Defaults[internal.MajorCoder], internal.AutonomyConfirm)
	}
	if saver.savedCfg == nil {
		t.Fatal("expected project config to be saved")
	}
	if saver.savedCfg.Mode.Defaults[internal.MajorCoder] != internal.AutonomyConfirm {
		t.Errorf("saved Mode.Defaults[coder] = %q, want %q", saver.savedCfg.Mode.Defaults[internal.MajorCoder], internal.AutonomyConfirm)
	}
}

func TestAutonomyCommand_Picker_Persists(t *testing.T) {
	var callback func(string, bool)
	ctx := newAutonomyTestContext()
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		callback = onSelected
	}
	cmd := &AutonomyCommand{}

	if err := cmd.Run(ctx, []string{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callback == nil {
		t.Fatal("picker callback not set")
	}

	callback("solo", true)

	if ctx.Config.Mode.Defaults[internal.MajorCoder] != internal.AutonomySolo {
		t.Errorf("Mode.Defaults[coder] = %q, want %q", ctx.Config.Mode.Defaults[internal.MajorCoder], internal.AutonomySolo)
	}
	if saver.savedCfg == nil {
		t.Fatal("expected project config to be saved")
	}
	if saver.savedCfg.Mode.Defaults[internal.MajorCoder] != internal.AutonomySolo {
		t.Errorf("saved Mode.Defaults[coder] = %q, want %q", saver.savedCfg.Mode.Defaults[internal.MajorCoder], internal.AutonomySolo)
	}
}

func TestAutonomyCommand_CompleteArgs(t *testing.T) {
	ctx := newAutonomyTestContext()
	cmd := &AutonomyCommand{}

	// Yolo is current: should not be proposed.
	vals := cmd.CompleteArgs(ctx, "")
	if len(vals) != 3 {
		t.Errorf("yolo current: got %d completions, want 3", len(vals))
	}
	for _, v := range vals {
		if v.Value == "yolo" {
			t.Error("current autonomy yolo should not be proposed")
		}
	}
}
