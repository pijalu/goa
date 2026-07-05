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
)

func TestThinkingCommand_Name(t *testing.T) {
	cmd := &ThinkingCommand{}
	if cmd.Name() != "thinking" {
		t.Errorf("expected name 'thinking', got %q", cmd.Name())
	}
}

func TestThinkingCommand_CompleteArgs(t *testing.T) {
	cmd := &ThinkingCommand{}
	// No current level: all levels offered.
	comps := cmd.CompleteArgs(core.Context{}, "")
	if len(comps) != 6 {
		t.Errorf("expected 6 completions, got %d", len(comps))
	}
	comps = cmd.CompleteArgs(core.Context{}, "h")
	if len(comps) != 1 || comps[0].Value != "high" {
		t.Errorf("expected 1 'high' completion, got %+v", comps)
	}

	// Current level medium: medium excluded.
	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, "")
	_ = am.SetThinkingLevel("medium")
	ctx := core.Context{AgentManager: am}
	comps = cmd.CompleteArgs(ctx, "")
	if len(comps) != 5 {
		t.Errorf("expected 5 completions when medium is current, got %d", len(comps))
	}
	for _, c := range comps {
		if c.Value == "medium" {
			t.Error("current thinking level should not be proposed")
		}
	}
}

func TestThinkingLevelDesc(t *testing.T) {
	tests := []struct {
		level string
		want  string
	}{
		{"off", "no reasoning"},
		{"minimal", "very brief reasoning (~1k tokens)"},
		{"low", "light reasoning (~2k tokens)"},
		{"medium", "moderate reasoning (~8k tokens)"},
		{"high", "deep reasoning (~16k tokens)"},
		{"xhigh", "maximum reasoning (~32k tokens)"},
		{"invalid", ""},
	}
	for _, tt := range tests {
		got := thinkingLevelDesc(tt.level)
		if got != tt.want {
			t.Errorf("thinkingLevelDesc(%q) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestIsValidThinkingLevel(t *testing.T) {
	for _, level := range internal.AllThinkingLevels() {
		if !internal.IsValidThinkingLevel(string(level)) {
			t.Errorf("expected %q to be valid", level)
		}
	}
	if internal.IsValidThinkingLevel("invalid") {
		t.Error("expected 'invalid' to be invalid")
	}
}
