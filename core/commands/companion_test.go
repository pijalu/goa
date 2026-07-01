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
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
)

func TestCompanionToggleCommand_AgentEnablesReview(t *testing.T) {
	ctx := newCompanionTestContext(t)
	cmd := &CompanionToggleCommand{}

	var buf strings.Builder
	ctx.OutputBuffer = &buf
	if err := cmd.Run(ctx, []string{"agent"}); err != nil {
		t.Fatalf("Run(agent): %v", err)
	}

	if !ctx.AgentManager.AgentDrivenEnabled() {
		t.Error("expected agent-driven enabled")
	}
	if !strings.Contains(ctx.AgentManager.SystemPrompt(), "Companion review is enabled") {
		t.Errorf("system prompt missing enabled text: %q", ctx.AgentManager.SystemPrompt())
	}
}

func TestCompanionToggleCommand_OffDisablesReview(t *testing.T) {
	ctx := newCompanionTestContext(t)
	cmd := &CompanionToggleCommand{}

	var buf strings.Builder
	ctx.OutputBuffer = &buf
	if err := cmd.Run(ctx, []string{"agent"}); err != nil {
		t.Fatalf("Run(agent): %v", err)
	}
	if err := cmd.Run(ctx, []string{"off"}); err != nil {
		t.Fatalf("Run(off): %v", err)
	}

	if strings.Contains(ctx.AgentManager.SystemPrompt(), "Companion review is enabled") {
		t.Errorf("system prompt should not contain enabled text: %q", ctx.AgentManager.SystemPrompt())
	}
	if !strings.Contains(ctx.AgentManager.SystemPrompt(), "Companion review is disabled") {
		t.Errorf("system prompt missing disabled text: %q", ctx.AgentManager.SystemPrompt())
	}
}

// TestCompanionToggleCommand_StatusMatchesAction verifies the bug where
// /companion:on then /companion reported 'disabled' (the on/agent branch
// never set the orchestrator mode that showCompanionStatus reads), and
// /companion:off after :framework still reported 'enabled'.
func TestCompanionToggleCommand_StatusMatchesAction(t *testing.T) {
	ctx := newCompanionTestContext(t)
	cmd := &CompanionToggleCommand{}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"agent enables", []string{"agent"}, "enabled (agent-driven)"},
		{"on enables", []string{"on"}, "enabled (agent-driven)"},
		{"framework enables", []string{"framework"}, "enabled (framework-driven)"},
		{"off disables", []string{"off"}, "disabled"},
	}

	for _, c := range cases {
		var runBuf strings.Builder
		ctx.OutputBuffer = &runBuf
		if err := cmd.Run(ctx, c.args); err != nil {
			t.Fatalf("Run(%v): %v", c.args, err)
		}

		var statusBuf strings.Builder
		ctx.OutputBuffer = &statusBuf
		if err := cmd.Run(ctx, nil); err != nil {
			t.Fatalf("status Run: %v", err)
		}
		status := statusBuf.String()
		if !strings.Contains(status, c.want) {
			t.Errorf("%s: status after action = %q, want substring %q", c.name, status, c.want)
		}
	}
}

func newCompanionTestContext(t *testing.T) core.Context {
	t.Helper()
	cfg := &config.Config{}
	sessionState := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := core.NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	am.SetForegroundOrchestrator(orch)

	mdl := agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "You are helpful.", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	agent := am.CurrentAgent()
	agent.SetTools([]agentic.Tool{
		&agentic.SendMessageTool{Bus: am.AgentBus(), FromName: "main"},
	})

	return core.Context{
		Config:                 cfg,
		AgentManager:           am,
		ForegroundOrchestrator: orch,
	}
}
