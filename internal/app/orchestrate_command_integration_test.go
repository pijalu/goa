// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestrateCommand_LiveNewRun drives the real /orchestrate new command
// path against LMStudio: command → adapter → bounded pool → live agents →
// events → active-runtime cleared. Skips when no local model is reachable.
func TestOrchestrateCommand_LiveNewRun(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live /orchestrate test")
	}
	cfg, _, pool := loadLiveConfig(t)
	rootDir := t.TempDir()
	adapter := NewOrchestratorAdapter(pool, cfg, "")
	cmd := &commands.OrchestrateCommand{
		Builder: adapter,
		Active:  orchestrator.NewActiveRuntime(),
		RootDir: rootDir,
	}

	cfg.Orchestrator = config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"summarizer": {Model: cfg.ActiveModel},
			"coder":      {Model: cfg.ActiveModel},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyFanout},
	}
	ctx := core.Context{Config: cfg}

	if err := cmd.Run(ctx, []string{"new", "topology=fanout", "objective=Reply with the single word: ready"}); err != nil {
		t.Fatalf("/orchestrate new: %v", err)
	}

	waitForActiveClear(cmd.Active, 90*time.Second, t)
	assertRunSnapshotFinished(t, rootDir, 2)
}

// waitForActiveClear polls the active runtime holder until it is nil or the
// deadline expires.
func waitForActiveClear(active *orchestrator.ActiveRuntime, timeout time.Duration, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if active.Get() == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("/orchestrate new run did not complete within timeout")
}
