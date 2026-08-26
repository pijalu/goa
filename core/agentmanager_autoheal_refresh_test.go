// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	internal "github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

// Regression for the bugs.md entry "/config tool-call fixing is not applied to
// ongoing sessions": buildAgenticConfig snapshots execution.auto_heal_tool_calls
// into the agent at StartSession; the manager must be able to push a later
// /config change into that same live agent instead of waiting for restart.
func TestAgentManager_RefreshAutoHeal_PushesConfigToLiveAgent(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{Major: internal.MajorCoder}), event.MakeBus(4, 4, 4, 4), "")

	mdl := agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if am.CurrentAgent().AutoHealEnabled() {
		t.Fatal("precondition: session should start with auto-heal off")
	}

	// What the /config menu and /config:set do to the shared *config.Config.
	cfg.Execution.AutoHealToolCalls = true
	am.RefreshAutoHeal()
	if !am.CurrentAgent().AutoHealEnabled() {
		t.Fatal("RefreshAutoHeal did not push the enabled value to the live session")
	}

	cfg.Execution.AutoHealToolCalls = false
	am.RefreshAutoHeal()
	if am.CurrentAgent().AutoHealEnabled() {
		t.Fatal("RefreshAutoHeal did not push the disabled value to the live session")
	}
}

// TestAgentManager_RefreshAutoHeal_NoSession verifies the nil-safe path:
// refreshing without an active session must not panic.
func TestAgentManager_RefreshAutoHeal_NoSession(t *testing.T) {
	cfg := &config.Config{Execution: config.ExecutionConfig{AutoHealToolCalls: true}}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{Major: internal.MajorCoder}), event.MakeBus(4, 4, 4, 4), "")
	am.RefreshAutoHeal() // must not panic
}
