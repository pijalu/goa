// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
)

// fakeBuilder returns a Runtime backed by a fake factory so the command can be
// exercised without a live provider.
type fakeBuilder struct {
	mu  sync.Mutex
	rt  *orchestrator.Runtime
	cfg config.OrchestratorConfig
}

func (b *fakeBuilder) NewRuntime(cfg config.OrchestratorConfig, rootDir string) (*orchestrator.Runtime, error) {
	factory := func(role, model string) (*orchestrator.AgentHandle, error) {
		h := orchestrator.NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			h.Stats.AddUsage(5, 3, 0, 0)
			return nil
		}
		return h, nil
	}
	pool := orchestrator.NewBoundedAgentPool(cfg, factory)
	rt, err := orchestrator.NewRuntime(cfg, pool, nil, rootDir)
	if err != nil {
		return nil, err
	}
	rt.SetIDGenerator(func() string { return "test-run" })
	b.mu.Lock()
	b.rt = rt
	b.cfg = cfg
	b.mu.Unlock()
	return rt, nil
}

func baseCfg() config.Config {
	return config.Config{
		Orchestrator: config.OrchestratorConfig{
			Roles: map[string]config.OrchestratorRole{
				"orchestrator": {Model: "m"},
				"coder":        {Model: "m"},
				"reviewer":     {Model: "m"},
			},
			Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
			Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyFanout},
		},
	}
}

func testCtx(t *testing.T) core.Context {
	t.Helper()
	return core.Context{Config: &config.Config{Orchestrator: config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyFanout},
	}}}
}

func TestOrchestrateCommand_NewFanout(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)

	if err := c.Run(ctx, []string{"new", "topology=fanout", "objective=test objective"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Active.Get() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c.Active.Get() != nil {
		t.Fatalf("run did not complete / clear active holder")
	}
	if b.cfg.Defaults.Topology != config.OrchestratorTopologyFanout {
		t.Errorf("topology = %q, want fanout", b.cfg.Defaults.Topology)
	}
}

func TestOrchestrateCommand_NewWithCustomName(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)

	if err := c.Run(ctx, []string{"new", "name=custom.id", "objective=do it"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && c.Active.Get() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if b.rt == nil || b.rt.Name() != "custom.id" {
		t.Errorf("run name = %q, want custom.id", b.rt.Name())
	}
}

func TestOrchestrateCommand_SteerActiveRuntime(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)

	if err := c.Run(ctx, []string{"new", "topology=pipeline", "objective=obj"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rt := c.Active.Get(); rt != nil {
		rt.SteerAll("hello") // must not panic
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && c.Active.Get() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Run(ctx, []string{"steer", "id=all", "message=x"}); err == nil {
		t.Errorf("expected error steering with no active run")
	}
}

func TestOrchestrateCommand_NoRolesErrors(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := core.Context{Config: &config.Config{}}
	if err := c.Run(ctx, []string{"new", "objective=obj"}); err == nil {
		t.Errorf("expected error with no roles configured")
	}
}

func TestOrchestrateCommand_BareMenu(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	if err := c.Run(testCtx(t), nil); err != nil {
		t.Fatalf("bare menu: %v", err)
	}
}

func TestOrchestrateCommand_NoBuilder(t *testing.T) {
	c := &OrchestrateCommand{} // no builder
	if err := c.Run(testCtx(t), []string{"new", "objective=x"}); err != nil {
		t.Fatalf("expected nil error (graceful), got %v", err)
	}
}

func TestOrchestrateCommand_DeleteSingleRun(t *testing.T) {
	b := &fakeBuilder{}
	rootDir := t.TempDir()
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: rootDir}
	ctx := testCtx(t)

	// Create a persisted run manually.
	store := orchestrator.NewFileEventStore(rootDir, "run-1")
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{"name": "happy.hare", "objective": "obj"}})

	if err := c.Run(ctx, []string{"delete", "id=happy.hare", "confirm=true"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "run-1")); !os.IsNotExist(err) {
		t.Errorf("run directory still exists after delete")
	}
}

func TestOrchestrateCommand_DeleteAllRuns(t *testing.T) {
	b := &fakeBuilder{}
	rootDir := t.TempDir()
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: rootDir}
	ctx := testCtx(t)

	for _, id := range []string{"run-1", "run-2"} {
		store := orchestrator.NewFileEventStore(rootDir, id)
		_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{}})
	}

	if err := c.Run(ctx, []string{"delete", "id=*", "confirm=true"}); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	entries, _ := os.ReadDir(rootDir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}
}

func TestOrchestrateCommand_ResumeRun(t *testing.T) {
	b := &fakeBuilder{}
	rootDir := t.TempDir()
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: rootDir}
	ctx := testCtx(t)

	store := orchestrator.NewFileEventStore(rootDir, "run-1")
	_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{"name": "happy.hare", "objective": "obj", "topology": "fanout"}})

	if err := c.Run(ctx, []string{"resume", "id=happy.hare"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Active.Get() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOrchestrateCommand_ListRuns(t *testing.T) {
	rootDir := t.TempDir()
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: rootDir}
	ctx := testCtx(t)

	for _, id := range []string{"run-1", "run-2"} {
		store := orchestrator.NewFileEventStore(rootDir, id)
		_ = store.Append(orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{"name": id, "objective": "obj"}})
	}

	var captured []tui.SelectorItem
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		captured = options
		onSelected("", false)
	}

	if err := c.Run(ctx, []string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(captured) != 2 {
		t.Errorf("list options = %d, want 2", len(captured))
	}
}

// guard against accidental removal of used imports if file evolves.
var _ = strings.TrimSpace
