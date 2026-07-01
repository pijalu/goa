// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/prompts"
)

func TestPopulateModeDefaults_LoadsFromRegistry(t *testing.T) {
	cfg := &config.Config{}
	reg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	populateModeDefaults(cfg, reg)

	if cfg.Mode.Defaults == nil {
		t.Fatal("expected Mode.Defaults to be initialized")
	}
	if cfg.Mode.Defaults["coding-posture"] != internal.AutonomySolo {
		t.Errorf("coding-posture default = %q, want %q", cfg.Mode.Defaults["coding-posture"], internal.AutonomySolo)
	}
	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomyReview {
		t.Errorf("planner default = %q, want %q", cfg.Mode.Defaults[internal.MajorPlanner], internal.AutonomyReview)
	}
}

func TestPopulateModeDefaults_PreservesExisting(t *testing.T) {
	cfg := &config.Config{}
	cfg.Mode.Defaults = map[internal.MajorMode]internal.AutonomyLevel{
		internal.MajorPlanner: internal.AutonomySolo,
	}
	reg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	populateModeDefaults(cfg, reg)

	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomySolo {
		t.Errorf("existing planner default overwritten: got %q", cfg.Mode.Defaults[internal.MajorPlanner])
	}
}
