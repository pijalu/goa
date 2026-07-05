// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/orchestrator"
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

func TestParseNewArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want parsedNew
		err  bool
	}{
		{"bare objective", []string{"do", "thing"}, parsedNew{topology: "", objective: "do thing"}, false},
		{"topology + objective", []string{"fanout", "do"}, parsedNew{topology: "fanout", objective: "do"}, false},
		{"hub + goal", []string{"hub", "goal", "ship it"}, parsedNew{topology: "hub", objective: "ship it", goalObjective: "ship it"}, false},
		{"goal + distinct run obj", []string{"goal", "be useful"}, parsedNew{objective: "be useful", goalObjective: "be useful"}, false},
		{"empty errors", []string{}, parsedNew{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseNewArgs(c.args)
			if c.err {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.topology != c.want.topology || got.objective != c.want.objective || got.goalObjective != c.want.goalObjective {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestOrchestrateCommand_NewFanout(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)

	if err := c.Run(ctx, []string{"new", "fanout", "test objective"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The run launches in a goroutine; wait for it to finish via Active.Clear.
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

func TestOrchestrateCommand_SteerActiveRuntime(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)

	if err := c.Run(ctx, []string{"new", "pipeline", "obj"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Steer while the run may still be active.
	if rt := c.Active.Get(); rt != nil {
		rt.SteerAll("hello") // must not panic
	}
	// Steer with no active run after completion → error.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && c.Active.Get() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Run(ctx, []string{"steer", "all", "x"}); err == nil {
		t.Errorf("expected error steering with no active run")
	}
}

func TestOrchestrateCommand_NoRolesErrors(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := core.Context{Config: &config.Config{}}
	if err := c.Run(ctx, []string{"new", "obj"}); err == nil {
		t.Errorf("expected error with no roles configured")
	}
}

func TestOrchestrateCommand_UsageNoArgs(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	if err := c.Run(testCtx(t), nil); err != nil {
		t.Fatalf("usage: %v", err)
	}
}

func TestOrchestrateCommand_NoBuilder(t *testing.T) {
	c := &OrchestrateCommand{} // no builder
	if err := c.Run(testCtx(t), []string{"new", "x"}); err != nil {
		t.Fatalf("expected nil error (graceful), got %v", err)
	}
}

// guard against accidental removal of used imports if file evolves.
var _ = strings.TrimSpace
