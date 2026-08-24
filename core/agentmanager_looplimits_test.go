// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

// TestBuildAgenticConfig_RunawayLoopMaxRepeats verifies the persisted
// execution.runaway_loop_max_repeats value reaches the agent config, so the
// cross-turn runaway-loop guardrail honors it from the next built agent on.
func TestBuildAgenticConfig_RunawayLoopMaxRepeats(t *testing.T) {
	cfg := &config.Config{}
	cfg.Execution.RunawayLoopMaxRepeats = 4
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	got := am.buildAgenticConfig(agenticprovider.Model{}, agenticprovider.StreamOptions{}, "system prompt", nil, cfg)
	if got.RunawayLoopMaxRepeats != 4 {
		t.Errorf("agentic RunawayLoopMaxRepeats = %d, want 4", got.RunawayLoopMaxRepeats)
	}
}
