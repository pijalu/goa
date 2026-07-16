// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/perms"
)

// Sub-agents must inherit the same safety gating as the main agent (C2). A
// spawned coder/explore sub-agent must carry the session's autonomy level,
// guard rules, tool-confirmation callback, and project scope — otherwise it
// would run tools unconfirmed even when the main agent has to ask first.
func TestAgentPool_SubAgent_InheritsSafetyPolicy(t *testing.T) {
	var m provider.Model
	pool := NewAgentPool(m, provider.StreamOptions{}, nil)

	wantAutonomy := internal.AutonomyConfirm
	confirmCalled := false
	pool.GetAutonomy = func() internal.AutonomyLevel { return wantAutonomy }
	pool.GetGuardConfig = func() perms.GuardConfig {
		return perms.GuardConfig{} // non-nil source; content irrelevant to wiring
	}
	pool.ConfirmTool = func(ctx context.Context, toolName, input string) (bool, error) {
		confirmCalled = true
		return true, nil
	}
	pool.ProjectDir = "/tmp/proj"

	agent, err := pool.CreateTaskAgent("coder-task-test", AgentConfig{})
	if err != nil {
		t.Fatalf("CreateTaskAgent: %v", err)
	}

	getAutonomy, getGuard, confirm, projectDir := agent.PolicyConfigForTest()
	if getAutonomy == nil {
		t.Error("sub-agent missing GetAutonomy — runs ungated")
	} else if got := getAutonomy(); got != wantAutonomy {
		t.Errorf("sub-agent autonomy = %v, want %v", got, wantAutonomy)
	}
	if getGuard == nil {
		t.Error("sub-agent missing GetGuardConfig")
	}
	if confirm == nil {
		t.Error("sub-agent missing ConfirmTool — tool calls run unconfirmed")
	} else {
		if _, err := confirm(context.Background(), "bash", "{}"); err != nil {
			t.Errorf("ConfirmTool: %v", err)
		}
		if !confirmCalled {
			t.Error("sub-agent ConfirmTool does not reach the host callback")
		}
	}
	if projectDir != "/tmp/proj" {
		t.Errorf("sub-agent ProjectDir = %q, want /tmp/proj", projectDir)
	}
}
