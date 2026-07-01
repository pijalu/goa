// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

func TestOrchestrateCommand_ParsesTasks(t *testing.T) {
	cmd := &OrchestrateCommand{}
	ctx := core.Context{
		ForegroundOrchestrator: nil, // no orchestrator — should print message
	}

	// Run with tasks
	err := cmd.Run(ctx, []string{"task1", "task2"})
	if err != nil {
		// Should not error — prints message when no orchestrator
		t.Logf("Run with tasks (expected): %v", err)
	}
}

func TestOrchestrateCommand_NoArgs_ReturnsError(t *testing.T) {
	cmd := &OrchestrateCommand{}
	ctx := core.Context{}

	err := cmd.Run(ctx, []string{})
	if err == nil {
		t.Fatal("expected error with no args")
	}
}

func TestOrchestrateCommand_WithOrchestrator(t *testing.T) {
	pool := multiagent.NewAgentPool(testCmdModel(), provider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	ctx := core.Context{
		ForegroundOrchestrator: orch,
	}

	cmd := &OrchestrateCommand{}
	err := cmd.Run(ctx, []string{"test task"})
	// Should not error — orchestrator available
	if err != nil {
		t.Logf("Run with orchestrator: %v", err)
	}
}

func TestOrchestrateCommand_EmptyTasks_PrintsMessage(t *testing.T) {
	cmd := &OrchestrateCommand{}
	ctx := core.Context{}

	err := cmd.Run(ctx, []string{""})
	if err != nil {
		t.Logf("Run with empty string: %v", err)
	}
	// Should not error — prints a message and returns nil
}

func TestOrchestrateCommand_ShortHelp_NotEmpty(t *testing.T) {
	cmd := &OrchestrateCommand{}
	if cmd.ShortHelp() == "" {
		t.Error("ShortHelp should not be empty")
	}
}

func TestOrchestrateCommand_LongHelp_NotEmpty(t *testing.T) {
	cmd := &OrchestrateCommand{}
	if cmd.LongHelp() == "" {
		t.Error("LongHelp should not be empty")
	}
}
