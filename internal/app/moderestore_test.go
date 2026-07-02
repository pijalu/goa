// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
)

// TestEffectiveModeState_PrefersLiveSessionMode verifies the helper used by
// every footer/prompt builder returns the live (runtime) mode rather than the
// static config default. This is the crux of the "mode change not saved" bug:
// the footer must reflect the session mode restored from state.json.
func TestEffectiveModeState_PrefersLiveSessionMode(t *testing.T) {
	cfg := &config.Config{}
	cfg.Mode.Default.Major = internal.MajorCoder // config default is coder

	ss := core.NewSessionState(internal.ModeState{Major: "coding-posture", Autonomy: internal.AutonomySolo})
	am := core.NewAgentManager(cfg, nil, nil, ss, event.MakeBus(2, 2, 2, 2), "")

	s := &subsystems{cfg: cfg, agentMgr: am}

	got := s.effectiveModeState()
	if got.Major != "coding-posture" {
		t.Errorf("Major = %q, want %q (live session mode)", got.Major, "coding-posture")
	}
	if got.Autonomy != internal.AutonomySolo {
		t.Errorf("Autonomy = %q, want %q", got.Autonomy, internal.AutonomySolo)
	}
}

// TestEffectiveModeState_FallsBackToConfig ensures that when no live session
// mode is available, the configured default is used.
func TestEffectiveModeState_FallsBackToConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Mode.Default.Major = internal.MajorPlanner

	// No agent manager wired → must fall back to config.
	s := &subsystems{cfg: cfg}
	got := s.effectiveModeState()
	if got.Major != internal.MajorPlanner {
		t.Errorf("Major = %q, want %q (config fallback)", got.Major, internal.MajorPlanner)
	}
}

// TestInitAgentBundle_RestoresModeFromStateJSON verifies the startup path reads
// the persisted mode from .goa/state.json instead of falling back to the
// config default. This is what makes a mode change survive a restart.
func TestInitAgentBundle_RestoresModeFromStateJSON(t *testing.T) {
	projectDir := t.TempDir()

	// Seed state.json with a non-default mode.
	store := core.NewStateStore(projectDir)
	desired := internal.ModeState{Major: "coding-posture", Autonomy: internal.AutonomySolo}
	if err := store.Save(core.SessionStateSnapshot{ModeState: desired}); err != nil {
		t.Fatalf("seed state.json: %v", err)
	}

	// Config default deliberately differs from the persisted mode.
	cfg := &config.Config{}
	cfg.Mode.Default.Major = internal.MajorCoder

	bundle := initAgentBundle(cfg, projectDir)
	got := bundle.agentMgr.CurrentMode()
	if got.Major != "coding-posture" {
		t.Errorf("restored Major = %q, want %q", got.Major, "coding-posture")
	}
	if got.Autonomy != internal.AutonomySolo {
		t.Errorf("restored Autonomy = %q, want %q", got.Autonomy, internal.AutonomySolo)
	}

	// Ensure the state file path is exactly <project>/.goa/state.json so the
	// restore and the save agree on location.
	if _, err := filepath.Abs(filepath.Join(projectDir, ".goa", "state.json")); err != nil {
		t.Fatalf("abs: %v", err)
	}
}

// TestInitAgentBundle_FallsBackToConfigWhenNoState confirms that without a
// state.json the config default mode is used.
func TestInitAgentBundle_FallsBackToConfigWhenNoState(t *testing.T) {
	projectDir := t.TempDir()
	cfg := &config.Config{}
	cfg.Mode.Default.Major = internal.MajorPlanner

	bundle := initAgentBundle(cfg, projectDir)
	got := bundle.agentMgr.CurrentMode()
	if got.Major != internal.MajorPlanner {
		t.Errorf("Major = %q, want %q (config default)", got.Major, internal.MajorPlanner)
	}
}
