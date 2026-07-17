// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal"
)

// PlanBinder wires plan commands into the application.
type PlanBinder struct {
	adapter   *OrchestratorAdapter
	cfg       *config.Config
	rootDir   string
	promptDir string
}

// NewPlanBinder creates a binder for plan-related commands.
func NewPlanBinder(adapter *OrchestratorAdapter, cfg *config.Config, rootDir, promptDir string) *PlanBinder {
	return &PlanBinder{
		adapter:   adapter,
		cfg:       cfg,
		rootDir:   rootDir,
		promptDir: promptDir,
	}
}

// BindPlanCommand configures a PlanCommand with its execution wiring.
func (b *PlanBinder) BindPlanCommand(cmd *commands.PlanCommand) {
	cmd.RootDir = b.rootDir
	cmd.Cfg = b.cfg
	cmd.StartExecution = b.startExecution
}

// startExecution is the callback that wires plan approval → orchestrator run.
func (b *PlanBinder) startExecution(planID string) error {
	// Open the plan store.
	plansDir := b.plansDir()
	store, err := plan.Open(plansDir, planID)
	if err != nil {
		return fmt.Errorf("open plan %q: %w", planID, err)
	}
	defer store.Close()

	p := store.Plan()
	if p == nil {
		return fmt.Errorf("plan %q not found", planID)
	}

	// Build an execution orchestrator.
	oCfg := defaultOrchestratorConfig(b.cfg)
	rt, err := b.adapter.NewRuntime(oCfg, b.rootDir)
	if err != nil {
		return fmt.Errorf("new runtime: %w", err)
	}

	// Generate a run ID and make the runtime use it.
	runID := internal.PrefixedHexID("run", 4)
	rt.SetIDGenerator(func() string { return runID })

	// Bind the plan to the run.
	rt.SetPlanID(planID)
	objective := fmt.Sprintf("Execute plan %s (%s): %s", p.Name, planID, p.Objective)

	// Start execution on the plan store (records runID).
	if err := store.StartExecution(runID); err != nil {
		return fmt.Errorf("start execution: %w", err)
	}

	// Run the orchestrator in the background.
	go func() {
		ctx := context.Background()
		if err := rt.Run(ctx, objective); err != nil {
			store.Fail(fmt.Sprintf("run failed: %v", err))
		}
	}()

	return nil
}

func (b *PlanBinder) plansDir() string {
	return b.rootDir + "/.goa/plans"
}

// defaultOrchestratorConfig returns the orchestrator config with defaults.
func defaultOrchestratorConfig(cfg *config.Config) config.OrchestratorConfig {
	oCfg := cfg.Orchestrator
	if len(oCfg.Roles) == 0 && cfg.ActiveModel != "" {
		oCfg.Roles = map[string]config.OrchestratorRole{
			"orchestrator": {Model: cfg.ActiveModel},
			"coder":        {Model: cfg.ActiveModel},
			"reviewer":     {Model: cfg.ActiveModel},
		}
	}
	return oCfg
}
