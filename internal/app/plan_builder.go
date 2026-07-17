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
	"github.com/pijalu/goa/internal/event"
)

// planRunRuntime is the subset of *orchestrator.Runtime the plan binder
// drives. Declared as an interface so plan-execution lifecycle tests can
// substitute failing/panicking fakes.
type planRunRuntime interface {
	SetIDGenerator(gen func() string)
	SetPlanID(id string)
	Run(ctx context.Context, objective string) error
}

// planRuntimeFactory builds the runtime a plan executes on.
type planRuntimeFactory interface {
	NewRuntime(oCfg config.OrchestratorConfig, rootDir string) (planRunRuntime, error)
}

// orchestratorPlanFactory adapts OrchestratorAdapter to planRuntimeFactory.
type orchestratorPlanFactory struct {
	adapter *OrchestratorAdapter
}

func (f orchestratorPlanFactory) NewRuntime(oCfg config.OrchestratorConfig, rootDir string) (planRunRuntime, error) {
	return f.adapter.NewRuntime(oCfg, rootDir)
}

// PlanBinder wires plan commands into the application.
type PlanBinder struct {
	factory   planRuntimeFactory
	cfg       *config.Config
	rootDir   string
	promptDir string
	events    *event.Bus // may be nil (tests); flash degrades to a no-op

	// runDone, when set, is called at the very end of runPlanExecution.
	// Test seam for synchronizing on background run completion.
	runDone func()
}

// NewPlanBinder creates a binder for plan-related commands. The event bus is
// used to surface execution progress in the chat; it may be nil.
func NewPlanBinder(adapter *OrchestratorAdapter, cfg *config.Config, rootDir, promptDir string, events *event.Bus) *PlanBinder {
	return &PlanBinder{
		factory:   orchestratorPlanFactory{adapter: adapter},
		cfg:       cfg,
		rootDir:   rootDir,
		promptDir: promptDir,
		events:    events,
	}
}

// BindPlanCommand configures a PlanCommand with its execution wiring.
func (b *PlanBinder) BindPlanCommand(cmd *commands.PlanCommand) {
	cmd.RootDir = b.rootDir
	cmd.Cfg = b.cfg
	cmd.StartExecution = b.startExecution
	cmd.OnPlanApproved = func(planID string) {
		if err := b.startExecutionByID(planID); err != nil {
			b.flash(fmt.Sprintf("plan %s: start execution: %v", planID, err))
		}
	}
}

// flash surfaces a transient message in the chat area; nil-bus safe.
func (b *PlanBinder) flash(text string) {
	if b.events == nil {
		return
	}
	select {
	case b.events.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
	default:
	}
}

// startExecution wires plan approval → orchestrator run. It takes ownership
// of the caller's open plan store: the store stays open for the whole run and
// is closed when the run ends. Accepting the already-open store (instead of
// opening a second handle) avoids divergent in-memory state and sequence
// counters between two handles on the same plan directory.
//
// On success the run continues in a background goroutine; the goroutine owns
// the store from that point. On error the store is untouched — the caller
// keeps ownership and closes it.
func (b *PlanBinder) startExecution(store *plan.Store) error {
	p := store.Plan()
	if p == nil {
		return fmt.Errorf("plan %q not found", store.ID())
	}

	// Build an execution orchestrator.
	oCfg := defaultOrchestratorConfig(b.cfg)
	rt, err := b.factory.NewRuntime(oCfg, b.rootDir)
	if err != nil {
		return fmt.Errorf("new runtime: %w", err)
	}

	// Generate a run ID and make the runtime use it.
	runID := internal.PrefixedHexID("run", 4)
	rt.SetIDGenerator(func() string { return runID })

	// Bind the plan to the run.
	planID := store.ID()
	rt.SetPlanID(planID)
	objective := fmt.Sprintf("Execute plan %s (%s): %s", p.Name, planID, p.Objective)

	// Start execution on the plan store (records runID).
	if err := store.StartExecution(runID); err != nil {
		return fmt.Errorf("start execution: %w", err)
	}

	b.flash(fmt.Sprintf("▷ plan %s execution started (run %s)", planID, runID))
	go b.runPlanExecution(rt, store, planID, objective)
	return nil
}

// startExecutionByID starts execution for a plan identified by ID, opening a
// fresh store handle. Used when approval happened through the review pager,
// which keeps its own store until the pager closes.
func (b *PlanBinder) startExecutionByID(planID string) error {
	store, err := plan.Open(b.plansDir(), planID)
	if err != nil {
		return fmt.Errorf("open plan %q: %w", planID, err)
	}
	if err := b.startExecution(store); err != nil {
		_ = store.Close()
		return err
	}
	return nil
}

// runPlanExecution owns the plan-bound run lifecycle: it records the terminal
// plan state on the store and closes the store exactly once when the run
// ends. A panicking runtime is converted into a failed plan instead of
// crashing the process.
func (b *PlanBinder) runPlanExecution(rt planRunRuntime, store *plan.Store, planID, objective string) {
	defer store.Close()
	defer func() {
		if b.runDone != nil {
			b.runDone()
		}
	}()
	runErr := b.runGuarded(rt, objective)

	if runErr != nil {
		if err := store.Fail(fmt.Sprintf("run failed: %v", runErr)); err != nil {
			b.flash(fmt.Sprintf("✗ plan %s failed (%v) — could not record failure: %v", planID, runErr, err))
			return
		}
		b.flash(fmt.Sprintf("✗ plan %s execution failed: %v", planID, runErr))
		return
	}

	// Finish requires every item terminal; items go terminal only when run
	// agents report task_outcome. If they did not, the plan stays in
	// "executing" and the user is told to inspect it instead of silently
	// dropping the completion.
	if err := store.Finish(); err != nil {
		b.flash(fmt.Sprintf("■ plan %s run finished; plan left in %q (%v)", planID, store.Plan().Status, err))
		return
	}
	b.flash(fmt.Sprintf("✓ plan %s completed", planID))
}

// runGuarded runs the orchestrator, converting a panic into an error so the
// background goroutine can never take the process down.
func (b *PlanBinder) runGuarded(rt planRunRuntime, objective string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("orchestrator run panicked: %v", r)
		}
	}()
	// The run ctx is tied to the process lifetime: plans are long-running
	// background work with no UI cancel affordance yet. Flash notifications
	// keep the user informed until a stop command exists.
	return rt.Run(context.Background(), objective)
}

// plansDir returns the directory holding plan stores.
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
