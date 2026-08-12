// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/team"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
)

// Regression (bug: companion stuck after team use): applying the team review
// policy turns agent-driven companion state ON, and the off path must turn it
// back OFF. Today ReviewApplyOff resets the orchestrator mode but leaves
// AgentDrivenEnabled=true — which is persisted and later re-asserts companion
// on every restart (impossible to disable).
func TestTeamReviewController_OffDisablesAgentDriven(t *testing.T) {
	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(8, 8, 8, 8), "")
	am.SetForegroundOrchestrator(orch)

	rc := &teamReviewController{am: am, orch: orch}

	// Activate an agent-driven review (as a team with review:agent does).
	if err := rc.ApplyReview(team.ReviewApplyAgent, nil); err != nil {
		t.Fatalf("ApplyReview(agent): %v", err)
	}
	if !am.AgentDrivenEnabled() {
		t.Fatal("after ApplyReview(agent): AgentDrivenEnabled=false, want true")
	}

	// Deactivate / restore to off must disable agent-driven again.
	if err := rc.ApplyReview(team.ReviewApplyOff, nil); err != nil {
		t.Fatalf("ApplyReview(off): %v", err)
	}
	if am.AgentDrivenEnabled() {
		t.Error("after ApplyReview(off): AgentDrivenEnabled=true (leaked), want false")
	}
}

// Regression (bug: restart re-asserts companion): the startup restore must not
// force the companion minor mode merely because AgentDrivenEnabled was left
// true by a prior team activation. Only an explicit MinorMode=="companion"
// should restore the companion minor mode label.
func TestRestoreSessionState_AgentDrivenAloneDoesNotForceCompanion(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(8, 8, 8, 8), "")
	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	am.SetForegroundOrchestrator(multiagent.NewForegroundOrchestrator(pool))

	snap := core.SessionStateSnapshot{
		MinorMode:          "",   // no companion minor mode
		AgentDrivenEnabled: true, // leftover from a team review apply
	}
	restoreSessionState(am, snap, nil, nil, &config.Config{})

	if am.MinorMode() == "companion" {
		t.Errorf("restoreSessionState forced companion minor mode from a bare AgentDrivenEnabled=true; MinorMode=%q, want \"\"", am.MinorMode())
	}
}

// The explicit companion minor mode must still restore (the legit case).
func TestRestoreSessionState_CompanionMinorModeRestores(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(8, 8, 8, 8), "")
	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	am.SetForegroundOrchestrator(multiagent.NewForegroundOrchestrator(pool))

	snap := core.SessionStateSnapshot{MinorMode: "companion", AgentDrivenEnabled: true}
	restoreSessionState(am, snap, nil, nil, &config.Config{})

	if am.MinorMode() != "companion" {
		t.Errorf("restoreSessionState did not restore an explicit companion minor mode; MinorMode=%q", am.MinorMode())
	}
}
