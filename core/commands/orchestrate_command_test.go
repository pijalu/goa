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
	"github.com/pijalu/goa/internal/event"
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
	ctx := testCtx(t)
	if err := c.Run(ctx, nil); err != nil {
		t.Fatalf("bare menu: %v", err)
	}
}

func TestOrchestrateCommand_BareMenuNonInteractive(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	var buf strings.Builder
	ctx.OutputBuffer = &buf
	if err := c.Run(ctx, nil); err != nil {
		t.Fatalf("bare menu non-interactive: %v", err)
	}
	if !strings.Contains(buf.String(), "Usage:") {
		t.Errorf("expected usage output in non-interactive context, got %q", buf.String())
	}
}

func TestOrchestrateCommand_NewMissingObjectiveNonInteractive(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	if err := c.Run(ctx, []string{"new"}); err == nil {
		t.Fatal("expected error for missing objective in non-interactive context")
	}
}

func TestOrchestrateCommand_ResumeMissingIDNonInteractive(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	if err := c.Run(ctx, []string{"resume"}); err == nil {
		t.Fatal("expected error for missing id in non-interactive context")
	}
}

func TestOrchestrateCommand_DeleteMissingIDNonInteractive(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	if err := c.Run(ctx, []string{"delete"}); err == nil {
		t.Fatal("expected error for missing id in non-interactive context")
	}
}

func TestOrchestrateCommand_SteerMissingArgsNonInteractive(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	if err := c.Run(ctx, []string{"steer"}); err == nil {
		t.Fatal("expected error for missing id/message in non-interactive context")
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

func TestOrchestrateCommand_TabSelectsAndFlashes(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)

	// Hold a built runtime active without running it so c.Active.Get() != nil.
	rt, err := b.NewRuntime(ctx.Config.Orchestrator, t.TempDir())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	c.Active.Set(rt)

	var got string
	c.SelectAgentTab = func(key string) (string, bool) {
		got = key
		if key == "all" {
			return "All", true
		}
		return "", false
	}

	flashes := newFlashCollector(&ctx)

	if err := c.Run(ctx, []string{"tab", "all"}); err != nil {
		t.Fatalf("tab all: %v", err)
	}
	if got != "all" {
		t.Errorf("SelectAgentTab called with %q, want all", got)
	}
	if !flashes.contains("tab: All") {
		t.Errorf("expected 'tab: All' flash, got %v", flashes.snapshot())
	}

	// Unknown key → 'Unknown tab' flash.
	flashes.reset()
	if err := c.Run(ctx, []string{"tab", "nope"}); err != nil {
		t.Fatalf("tab nope: %v", err)
	}
	if !flashes.contains("Unknown tab") {
		t.Errorf("expected 'Unknown tab' flash, got %v", flashes.snapshot())
	}
}

func TestOrchestrateCommand_TabNoActiveRun(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	c.SelectAgentTab = func(string) (string, bool) { return "x", true }
	flashes := newFlashCollector(&ctx)

	if err := c.Run(ctx, []string{"tab", "all"}); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if !flashes.contains("No active orchestration run") {
		t.Errorf("expected no-active-run flash, got %v", flashes.snapshot())
	}
}

func TestOrchestrateCommand_TabNoHostCallback(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	rt, err := b.NewRuntime(ctx.Config.Orchestrator, t.TempDir())
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	c.Active.Set(rt)
	// SelectAgentTab intentionally nil → error.
	if err := c.Run(ctx, []string{"tab", "all"}); err == nil {
		t.Error("expected error when SelectAgentTab is nil")
	}
}

// flashCollector drains the chat event bus for Flash messages.
type flashCollector struct {
	mu   sync.Mutex
	msgs []string
}

func newFlashCollector(ctx *core.Context) *flashCollector {
	fc := &flashCollector{}
	ctx.EventBus = &event.Bus{Chat: make(chan event.ChatEvent, 16)}
	go func() {
		for ev := range ctx.EventBus.Chat {
			if ev.Flash != nil {
				fc.add(ev.Flash.Text)
			}
		}
	}()
	return fc
}

func (f *flashCollector) add(s string) {
	f.mu.Lock()
	f.msgs = append(f.msgs, s)
	f.mu.Unlock()
}

func (f *flashCollector) contains(want string) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, m := range f.msgs {
			if strings.Contains(m, want) {
				f.mu.Unlock()
				return true
			}
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (f *flashCollector) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.msgs...)
}

func (f *flashCollector) reset() {
	f.mu.Lock()
	f.msgs = nil
	f.mu.Unlock()
}

func TestOrchestrateCommand_BrowserOpensOverlay(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	ctx.SelectOptionFunc = func(string, []tui.SelectorItem, string, func(string, bool)) {} // mark interactive
	ctx.ShowInputFunc = func(string, string, func(string, bool)) {}

	opened := false
	c.ShowBrowser = func() { opened = true }
	if err := c.Run(ctx, []string{"browser"}); err != nil {
		t.Fatalf("browser: %v", err)
	}
	if !opened {
		t.Error("ShowBrowser not invoked by /orchestrate:browser")
	}
}

func TestOrchestrateCommand_BrowserNoCallbackErrors(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	ctx.SelectOptionFunc = func(string, []tui.SelectorItem, string, func(string, bool)) {}
	ctx.ShowInputFunc = func(string, string, func(string, bool)) {}
	// ShowBrowser intentionally nil.
	if err := c.Run(ctx, []string{"browser"}); err == nil {
		t.Error("expected error when ShowBrowser is nil")
	}
}

func TestOrchestrateCommand_DefaultRoleSynthesis(t *testing.T) {
	cfg := config.Config{ActiveModel: "gpt4"}
	oCfg, defaulted := effectiveOrchestratorConfig(&cfg)
	if !defaulted {
		t.Fatal("expected default roles to be synthesized")
	}
	if len(oCfg.Roles) != 3 {
		t.Fatalf("expected 3 default roles, got %d", len(oCfg.Roles))
	}
	for _, role := range []string{"orchestrator", "coder", "reviewer"} {
		if oCfg.Roles[role].Model != "gpt4" {
			t.Errorf("role %s model = %q, want gpt4", role, oCfg.Roles[role].Model)
		}
	}
}

func TestOrchestrateCommand_DefaultRoleSynthesisNoActiveModel(t *testing.T) {
	cfg := config.Config{}
	_, defaulted := effectiveOrchestratorConfig(&cfg)
	if defaulted {
		t.Error("expected no default roles when ActiveModel is empty")
	}
}

func TestOrchestrateCommand_DefaultRolesRunUsesActiveModel(t *testing.T) {
	b := &fakeBuilder{}
	c := &OrchestrateCommand{Builder: b, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := core.Context{Config: &config.Config{ActiveModel: "gpt4"}}
	var mu sync.Mutex
	var flashes []string
	ctx.EventBus = &event.Bus{Chat: make(chan event.ChatEvent, 10)}
	go func() {
		for ev := range ctx.EventBus.Chat {
			if ev.Flash != nil {
				mu.Lock()
				flashes = append(flashes, ev.Flash.Text)
				mu.Unlock()
			}
		}
	}()

	if err := c.Run(ctx, []string{"new", "objective=obj"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && c.Active.Get() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	var found bool
	for _, f := range flashes {
		if strings.Contains(f, "No orchestrator roles configured") {
			found = true
			break
		}
	}
	mu.Unlock()
	if !found {
		t.Errorf("expected warning flash, got %v", flashes)
	}
	if b.cfg.Roles["coder"].Model != "gpt4" {
		t.Errorf("coder role model = %q, want gpt4", b.cfg.Roles["coder"].Model)
	}
}

func TestOrchestrateCommand_AsyncErrorFlashed(t *testing.T) {
	c := &OrchestrateCommand{Builder: &fakeBuilder{}, Active: orchestrator.NewActiveRuntime(), RootDir: t.TempDir()}
	ctx := testCtx(t)
	var mu sync.Mutex
	var flashes []string
	ctx.EventBus = &event.Bus{Chat: make(chan event.ChatEvent, 10)}
	go func() {
		for ev := range ctx.EventBus.Chat {
			if ev.Flash != nil {
				mu.Lock()
				flashes = append(flashes, ev.Flash.Text)
				mu.Unlock()
			}
		}
	}()
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		onSelected("", false)
	}
	step := 0
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		step++
		if step == 1 {
			onSubmit("all", true)
			return
		}
		onSubmit("x", true)
	}

	if err := c.Run(ctx, []string{"steer"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(flashes) > 0
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	var found bool
	for _, f := range flashes {
		if strings.Contains(f, "no active orchestration") {
			found = true
			break
		}
	}
	mu.Unlock()
	if !found {
		t.Errorf("expected no-active-run flash, got %v", flashes)
	}
}
