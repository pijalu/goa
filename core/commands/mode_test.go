// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/tui"
)

func newModeTestContext() core.Context {
	r := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	return core.Context{
		Config:       &config.Config{},
		ModeRegistry: r,
	}
}

func TestModeCommand_NoArgs_ShowsPicker(t *testing.T) {
	var capturedTitle string
	var capturedOptions []tui.SelectorItem
	var capturedCurrent string

	ctx := newModeTestContext()
	am := newTestAgentManager()
	am.SetMode(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	ctx.AgentManager = am
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		capturedTitle = title
		capturedOptions = options
		capturedCurrent = current
		onSelected("", false)
	}

	cmd := &ModeCommand{}
	err := cmd.Run(ctx, []string{})
	if err != nil {
		t.Fatalf("Run with no args: %v", err)
	}

	if capturedTitle != "Select mode:" {
		t.Errorf("title = %q, want %q", capturedTitle, "Select mode:")
	}
	if len(capturedOptions) < 3 {
		t.Errorf("expected at least 3 options, got %d", len(capturedOptions))
	}
	if capturedCurrent != "coder" {
		t.Errorf("current = %q, want %q", capturedCurrent, "coder")
	}
	for i := 1; i < len(capturedOptions); i++ {
		if capturedOptions[i].Label < capturedOptions[i-1].Label {
			t.Error("options not sorted alphabetically")
			break
		}
	}
}

func TestModeCommand_SwitchMajor(t *testing.T) {
	ctx := newModeTestContext()
	am := newTestAgentManager()
	ctx.AgentManager = am
	cmd := &ModeCommand{}

	err := cmd.Run(ctx, []string{"planner"})
	if err != nil {
		t.Fatalf("Run with 'planner': %v", err)
	}

	current := am.CurrentMode()
	if current.Major != internal.MajorPlanner {
		t.Errorf("Major = %q, want %q", current.Major, internal.MajorPlanner)
	}
}

func TestModeCommand_SwitchMajor_Persists(t *testing.T) {
	ctx := newModeTestContext()
	am := newTestAgentManager()
	ctx.AgentManager = am
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	cmd := &ModeCommand{}

	err := cmd.Run(ctx, []string{"planner"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if ctx.Config.Mode.Default.Major != internal.MajorPlanner {
		t.Errorf("Mode.Default.Major = %q, want %q", ctx.Config.Mode.Default.Major, internal.MajorPlanner)
	}
	if saver.savedCfg == nil {
		t.Fatal("expected project config to be saved")
	}
	if saver.savedCfg.Mode.Default.Major != internal.MajorPlanner {
		t.Errorf("saved Mode.Default.Major = %q, want %q", saver.savedCfg.Mode.Default.Major, internal.MajorPlanner)
	}
}

func TestModeCommand_SwitchMajor_Unknown(t *testing.T) {
	ctx := newModeTestContext()
	am := newTestAgentManager()
	ctx.AgentManager = am
	cmd := &ModeCommand{}

	err := cmd.Run(ctx, []string{"hacker"})
	if err == nil {
		t.Fatal("Expected error for unknown major 'hacker'")
	}
}

func TestModeCommand_CompleteArgs_TopLevel(t *testing.T) {
	cmd := &ModeCommand{}
	ctx := newModeTestContext()
	comps := cmd.CompleteArgs(ctx, "")
	if len(comps) == 0 {
		t.Fatal("expected completions for empty prefix")
	}
	var foundCoder bool
	for _, c := range comps {
		if c.Value == "coder" {
			foundCoder = true
		}
	}
	if !foundCoder {
		t.Error("expected 'coder' in completions")
	}
}

func TestAutonomyCommand_NoArgs_ShowsCurrent(t *testing.T) {
	cmd := &AutonomyCommand{}
	am := newTestAgentManager()
	ctx := newModeTestContext()
	ctx.AgentManager = am

	err := cmd.Run(ctx, []string{})
	if err != nil {
		t.Fatalf("Run with no args: %v", err)
	}
}

func TestAutonomyCommand_SwitchAutonomy(t *testing.T) {
	ctx := newModeTestContext()
	am := newTestAgentManager()
	ctx.AgentManager = am
	cmd := &AutonomyCommand{}

	err := cmd.Run(ctx, []string{"confirm"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	current := am.CurrentMode()
	if current.Autonomy != internal.AutonomyConfirm {
		t.Errorf("Autonomy = %q, want %q", current.Autonomy, internal.AutonomyConfirm)
	}
}

func TestAutonomyCommand_Invalid(t *testing.T) {
	ctx := newModeTestContext()
	am := newTestAgentManager()
	ctx.AgentManager = am
	cmd := &AutonomyCommand{}

	err := cmd.Run(ctx, []string{"invalid"})
	if err == nil {
		t.Fatal("Expected error for invalid autonomy")
	}
}

func TestProfileCommand_NoArgs_ShowsPicker(t *testing.T) {
	var capturedTitle string
	var capturedOptions []tui.SelectorItem
	var capturedCurrent string

	ctx := newModeTestContext()
	am := newTestAgentManager()
	am.SetMode(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	ctx.AgentManager = am
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		capturedTitle = title
		capturedOptions = options
		capturedCurrent = current
		onSelected("", false)
	}

	cmd := &ProfileCommand{}
	err := cmd.Run(ctx, []string{})
	if err != nil {
		t.Fatalf("Run with no args: %v", err)
	}

	if capturedTitle != "Select mode:" {
		t.Errorf("title = %q, want %q", capturedTitle, "Select mode:")
	}
	if len(capturedOptions) < 3 {
		t.Errorf("expected at least 3 options, got %d", len(capturedOptions))
	}
	if capturedCurrent != "coder" {
		t.Errorf("current = %q, want %q", capturedCurrent, "coder")
	}
}

func TestProfileCommand_StatusShowsCurrent(t *testing.T) {
	cmd := &ProfileCommand{}
	ctx := newModeTestContext()
	ctx.Config.SetActiveMajor("reviewer")
	got := cmd.Status(ctx)
	if !strings.Contains(got, "reviewer") {
		t.Errorf("Status() = %q, want substring reviewer", got)
	}
}

// newTestAgentManager creates an AgentManager with a SessionState for testing.
func newTestAgentManager() *core.AgentManager {
	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	return core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, "")
}
