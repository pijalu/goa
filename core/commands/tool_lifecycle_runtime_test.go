// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	internal "github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// probeTool is a disposable tool used to prove that enabling a tool at runtime
// CONSTRUCTS it (the factory must be called) and REGISTERS it. deferred=true
// makes the tool part of the deferred partition (tools/deferred.go contract).
type probeTool struct {
	agentic.BaseTool
	name        string
	description string
	deferred    bool
}

func (p *probeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: p.name, Description: p.description}
}
func (p *probeTool) Execute(string) (string, error) { return "ok", nil }
func (p *probeTool) Deferred() bool                 { return p.deferred }

// msgRecorder drains the harness event bus into a slice so a test can assert
// what the user was actually told (flash + durable system messages).
type msgRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func newMsgRecorder(bus *event.Bus) *msgRecorder {
	r := &msgRecorder{}
	go func() {
		for ev := range bus.Chat {
			if ev.Flash != nil {
				r.add(ev.Flash.Text)
			}
			if ev.SystemMessage != nil {
				r.add(ev.SystemMessage.Text)
			}
		}
	}()
	return r
}

func (r *msgRecorder) add(s string) {
	r.mu.Lock()
	r.msgs = append(r.msgs, s)
	r.mu.Unlock()
}

func (r *msgRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.msgs...)
}

// hasContaining reports whether any recorded message contains want. It polls
// briefly because flash/system events are delivered by the bus goroutine.
func (r *msgRecorder) hasContaining(want string) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, m := range r.snapshot() {
			if strings.Contains(strings.ToLower(m), strings.ToLower(want)) {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// liveToggleHarness wires a menu-test context with a real AgentManager whose
// session is already running, so pushing a tool set is observable on the agent
// (the model's view) and not just in the registry.
type liveToggleHarness struct {
	ctx     *core.Context
	menu    *configMenu
	reg     *tools.ToolRegistry
	am      *core.AgentManager
	rec     *msgRecorder
	title   string
	options []tui.SelectorItem
	lastSel func(string, bool)
}

// newLiveToggleHarness builds the harness. cfg is the live config the menu
// mutates; startTools are the tools the running session began with.
func newLiveToggleHarness(t *testing.T, cfg *config.Config, startTools ...agentic.Tool) *liveToggleHarness {
	t.Helper()
	reg := tools.NewToolRegistry()
	for _, tool := range startTools {
		reg.Register(tool)
	}
	return newLiveToggleHarnessWithRegistry(t, cfg, reg)
}

// newLiveToggleHarnessWithRegistry is newLiveToggleHarness for tests that need
// to control the registry contents (e.g. a deferred tool set).
func newLiveToggleHarnessWithRegistry(t *testing.T, cfg *config.Config, reg *tools.ToolRegistry) *liveToggleHarness {
	t.Helper()
	ctx, sr, _, bus := newMenuTestContext(t, cfg)
	_ = sr
	am := core.NewAgentManager(cfg, nil, core.NewLoopDetector(core.DefaultLoopDetectorConfig()),
		core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}), bus, "")
	if _, err := am.StartSession(
		agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions},
		agenticprovider.StreamOptions{}, "sys", reg.All(), cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	ctx.Config = cfg
	ctx.ToolRegistry = reg
	ctx.AgentManager = am

	h := &liveToggleHarness{ctx: ctx, reg: reg, am: am, rec: newMsgRecorder(bus)}
	// The menu drives the selector; record the pending callback so the test can
	// replay a selection synchronously, exactly as the TUI would.
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		h.title = title
		h.options = options
		h.lastSel = onSelected
	}
	h.menu = newConfigMenu(*ctx)
	return h
}

// select replays one menu selection on the pending selector callback.
func (h *liveToggleHarness) pick(t *testing.T, value string, ok bool) {
	t.Helper()
	if h.lastSel == nil {
		t.Fatalf("no selector pending: cannot select %q", value)
	}
	onSel := h.lastSel
	h.lastSel = nil
	onSel(value, ok)
}

// openTools navigates the menu from the root to the tool list.
func (h *liveToggleHarness) openTools(t *testing.T) {
	t.Helper()
	if err := h.menu.showRoot(); err != nil {
		t.Fatalf("showRoot: %v", err)
	}
	h.pick(t, "tools", true)         // Tools settings:
	h.pick(t, "enabled_tools", true) // Toggle optional tools:
}

// agentToolNames returns the names of the tools the running agent holds — the
// model's actual view.
func (h *liveToggleHarness) agentToolNames() []string {
	agent := h.am.CurrentAgent()
	if agent == nil {
		return nil
	}
	var out []string
	for _, tool := range agent.Tools() {
		out = append(out, tool.Schema().Name)
	}
	return out
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// sortedNames returns the sorted schema names of a tool slice.
func sortedNames(list []agentic.Tool) []string {
	out := make([]string, 0, len(list))
	for _, tool := range list {
		out = append(out, tool.Schema().Name)
	}
	sort.Strings(out)
	return out
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// TestConfigMenu_EnableToolRegistersIt is the core regression for bugs.md
// "Enabling a tool during a session does not enable it": toggling a tool
// OFF→ON in /config → Tools must CONSTRUCT it through ctx.ToolFactory,
// register it in the registry, push it to the running agent, and tell the user
// nothing about a restart. ON→OFF must unregister it and call ToolTeardown.
func TestConfigMenu_EnableToolRegistersIt(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarness(t, cfg) // bg_exec starts disabled

	built := 0
	h.ctx.ToolFactory = func(name string) (agentic.Tool, bool) {
		if name != "bg_exec" {
			return nil, false
		}
		built++
		return &probeTool{name: "bg_exec", description: "background process execution"}, true
	}
	tornDown := 0
	h.ctx.ToolTeardown = func(name string) {
		if name == "bg_exec" {
			tornDown++
		}
	}
	h.menu = newConfigMenu(*h.ctx)

	h.openTools(t)
	h.pick(t, "bg_exec", true) // OFF → ON

	if built == 0 {
		t.Fatal("enabling bg_exec did not construct it through ToolFactory")
	}
	if !cfg.Tools.Enabled.BGExec {
		t.Fatal("config flag not flipped to enabled")
	}
	if _, ok := h.reg.Get("bg_exec"); !ok {
		t.Fatal("enabling bg_exec did not register it in the tool registry")
	}
	if !containsName(h.agentToolNames(), "bg_exec") {
		t.Errorf("enabled tool was not pushed to the running agent: agent tools = %v", h.agentToolNames())
	}
	if h.rec.hasContaining("restart") {
		t.Errorf("successful runtime enable must not claim a restart is required: %v", h.rec.snapshot())
	}

	h.pick(t, "bg_exec", true) // ON → OFF

	if cfg.Tools.Enabled.BGExec {
		t.Fatal("config flag not flipped to disabled")
	}
	if _, ok := h.reg.Get("bg_exec"); ok {
		t.Fatal("disabling bg_exec did not unregister it")
	}
	if tornDown == 0 {
		t.Error("disabling bg_exec did not call ToolTeardown")
	}
	if containsName(h.agentToolNames(), "bg_exec") {
		t.Errorf("disabled tool still pushed to the agent: %v", h.agentToolNames())
	}
}

// TestConfigMenu_EnableToolWithoutFactorySaysRestart pins the no-silent-no-op
// contract: when the tool cannot be built at runtime (no factory, or a factory
// that cannot build it) the user must be told explicitly that a restart is
// required instead of /config quietly reporting it enabled.
func TestConfigMenu_EnableToolWithoutFactorySaysRestart(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarness(t, cfg)
	h.ctx.ToolFactory = nil // host wired no factory at all
	h.menu = newConfigMenu(*h.ctx)

	h.openTools(t)
	h.pick(t, "bg_exec", true)

	if _, ok := h.reg.Get("bg_exec"); ok {
		t.Fatal("no factory: the tool must not be registered")
	}
	if !h.rec.hasContaining("restart") {
		t.Fatalf("enabling a tool that cannot be built at runtime must tell the user a restart is required; messages = %v",
			h.rec.snapshot())
	}
}

// TestConfigMenu_ToggleMatchesToolsCommand pins that /config → Tools and
// /tools:<name>:on are the SAME primitive: both entry points must end with the
// same registry content and the same pushed agent tool set.
func TestConfigMenu_ToggleMatchesToolsCommand(t *testing.T) {
	build := func() (*liveToggleHarness, *int) {
		cfg := &config.Config{}
		h := newLiveToggleHarness(t, cfg, &probeTool{name: "read"})
		built := 0
		h.ctx.ToolFactory = func(name string) (agentic.Tool, bool) {
			if name != "verify" {
				return nil, false
			}
			built++
			return &probeTool{name: "verify", description: "run the test suite"}, true
		}
		h.menu = newConfigMenu(*h.ctx)
		return h, &built
	}

	// Path A: the /config menu.
	menuHarness, menuBuilt := build()
	menuHarness.openTools(t)
	menuHarness.pick(t, "verify", true)

	// Path B: /tools:verify:on.
	cmdHarness, cmdBuilt := build()
	if err := toggleTool(*cmdHarness.ctx, "verify", "on"); err != nil {
		t.Fatalf("toggleTool: %v", err)
	}

	if *menuBuilt == 0 || *cmdBuilt == 0 {
		t.Fatalf("both paths must construct the tool: menu=%d cmd=%d", *menuBuilt, *cmdBuilt)
	}
	menuReg := sortedNames(menuHarness.reg.All())
	cmdReg := sortedNames(cmdHarness.reg.All())
	if strings.Join(menuReg, ",") != strings.Join(cmdReg, ",") {
		t.Errorf("registry diverges between /config (%v) and /tools (%v)", menuReg, cmdReg)
	}
	if !containsName(menuHarness.agentToolNames(), "verify") ||
		!containsName(cmdHarness.agentToolNames(), "verify") {
		t.Errorf("pushed agent sets diverge: menu=%v cmd=%v",
			menuHarness.agentToolNames(), cmdHarness.agentToolNames())
	}
	if !menuHarness.ctx.Config.Tools.Enabled.Verify || !cmdHarness.ctx.Config.Tools.Enabled.Verify {
		t.Error("both paths must flip the persisted flag")
	}
}

// TestRefreshToolRegistry_UsesLiveProvider pins that the pushed set is the one
// a session would start with (Context.LiveTools, wired by internal/app to the
// mode filter) rather than the raw registry: a tool the active mode disallows
// must not be pushed by a runtime toggle.
func TestRefreshToolRegistry_UsesLiveProvider(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarness(t, cfg, &probeTool{name: "read"}, &probeTool{name: "write"})

	calls := 0
	h.ctx.LiveTools = func() []agentic.Tool {
		calls++
		return []agentic.Tool{&probeTool{name: "read"}}
	}

	refreshToolRegistry(*h.ctx)

	if calls == 0 {
		t.Fatal("refreshToolRegistry ignored the live tool-set provider")
	}
	got := h.agentToolNames()
	if !containsName(got, "read") {
		t.Errorf("pushed tool set = %v, want the provider's mode-filtered slice to contain read", got)
	}
	if containsName(got, "write") {
		t.Errorf("pushed tool set = %v, want the write tool withheld by the mode filter", got)
	}
}

// TestRefreshToolRegistry_FallsBackToRegistry pins the fallback documented on
// Context.LiveTools: with no provider wired the registry is pushed.
func TestRefreshToolRegistry_FallsBackToRegistry(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarness(t, cfg, &probeTool{name: "read"}, &probeTool{name: "write"})
	h.ctx.LiveTools = nil

	refreshToolRegistry(*h.ctx)

	got := sortedStrings(h.agentToolNames())
	for _, want := range sortedNames(h.reg.All()) {
		if !containsName(got, want) {
			t.Errorf("fallback pushed %v, want the registry tool %q", got, want)
		}
	}
}

// deferredProbeSet builds a registry whose deferral is ACTIVE: the tool_search
// loader plus at least agentic.DeferralThreshold deferred-eligible tools, which
// is what makes the withheld partition real at session start.
func deferredProbeSet() *tools.ToolRegistry {
	reg := tools.NewToolRegistry()
	reg.Register(&probeTool{name: "read"})
	for i := 0; i < agentic.DeferralThreshold; i++ {
		reg.Register(&probeTool{name: fmt.Sprintf("deferred%d", i), deferred: true})
	}
	reg.Register(tools.NewToolSearchTool(reg))
	return reg
}

// TestRuntimeToggle_KeepsDeferredTail pins that a runtime toggle does not
// disturb the deferred loaded-tail: the set pushed by ApplyToolToggle is the
// same session-shaped set a start would push, so the tools the model already
// pulled through tool_search stay loaded (append-only) instead of reverting to
// "deferred, not loaded" — which would make the next call fail with the
// deferred redirect (bugs.md "Tools are disabled out of the blue", C2).
func TestRuntimeToggle_KeepsDeferredTail(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarnessWithRegistry(t, cfg, deferredProbeSet())

	if _, unloaded := h.am.DeferredStatus("deferred0"); !unloaded {
		t.Fatal("precondition: deferred0 must start deferred and unloaded")
	}
	if got := h.am.LoadDeferredTools([]string{"deferred0"}); len(got) != 1 || got[0] != "deferred0" {
		t.Fatalf("precondition: LoadDeferredTools = %v, want [deferred0]", got)
	}
	before := h.am.LoadedDeferred()

	h.ctx.ToolFactory = func(name string) (agentic.Tool, bool) {
		if name != "verify" {
			return nil, false
		}
		return &probeTool{name: "verify", description: "run the test suite"}, true
	}
	h.menu = newConfigMenu(*h.ctx)

	// Toggle a tool mid-session through the real /config path.
	h.openTools(t)
	h.pick(t, "verify", true)

	if !containsName(h.agentToolNames(), "verify") {
		t.Fatalf("precondition: the toggled tool must have been pushed: %v", h.agentToolNames())
	}
	if _, unloaded := h.am.DeferredStatus("deferred0"); unloaded {
		t.Error("the toggle re-partitioned the tool set: deferred0 reverted to not-loaded")
	}
	if got := h.am.LoadedDeferred(); len(got) != len(before) || (len(got) == 1 && got[0] != before[0]) {
		t.Errorf("LoadedDeferred = %v after the toggle, want %v", got, before)
	}
}

// TestConfigMenu_ToolToggleSaveFailureReportsAndSkipsRuntime pins the failure
// path of the shared primitive: when the config write fails, the user is told
// ("Failed to save config: …") and the runtime is NOT touched — enabling
// something that was never persisted would desync /config from the next start.
func TestConfigMenu_ToolToggleSaveFailureReportsAndSkipsRuntime(t *testing.T) {
	// A project dir where .goa exists as a regular FILE: reading the project
	// config is tolerated (absent), but writing .goa/config.yaml cannot work.
	proj := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cl := config.NewCascadeLoader(proj, "", nil)
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".goa"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed .goa file: %v", err)
	}

	cfg.Tools.Enabled.SetEnabled("bg_exec", false)
	h := newLiveToggleHarness(t, cfg)
	h.ctx.ConfigSaver = cl
	built := 0
	h.ctx.ToolFactory = func(name string) (agentic.Tool, bool) {
		if name != "bg_exec" {
			return nil, false
		}
		built++
		return &probeTool{name: "bg_exec"}, true
	}
	h.menu = newConfigMenu(*h.ctx)

	h.openTools(t)
	h.pick(t, "bg_exec", true)

	if built != 0 {
		t.Error("a failed save must not construct/enable the tool at runtime")
	}
	if _, ok := h.reg.Get("bg_exec"); ok {
		t.Error("a failed save must not register the tool")
	}
	if !h.rec.hasContaining("Failed to save config") {
		t.Fatalf("a failed save must tell the user; messages = %v", h.rec.snapshot())
	}

	// The primitive's own error text stays lowercase (ST1005); the shared
	// renderer is what capitalizes it for the user.
	h.ctx.Config.Tools.Enabled.SetEnabled("bg_exec", false)
	outcome, err := ApplyToolToggle(*h.ctx, "bg_exec", true)
	if err == nil {
		t.Fatal("a failed save must return an error")
	}
	if !strings.HasPrefix(err.Error(), "failed to save config") {
		t.Errorf("error text = %q, want lowercase (ST1005)", err.Error())
	}
	if !strings.HasPrefix(ToolToggleErrorText(err), "Failed to save config") {
		t.Errorf("rendered text = %q, want the config-surface wording", ToolToggleErrorText(err))
	}
	if outcome != ToolToggleUnchanged {
		t.Errorf("outcome = %v, want unchanged on a failed save", outcome)
	}
}
