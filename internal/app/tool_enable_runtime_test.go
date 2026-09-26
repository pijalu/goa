// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	commands "github.com/pijalu/goa/core/commands"
	internal "github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// scriptedSelector replays a fixed sequence of /config menu selections the way
// a user would drive the real menu: each SelectOption call stores the pending
// callback, so the test can pick the next row synchronously. Once the script is
// exhausted every further selector is CANCELLED (Escape), which unwinds the
// menu stack and closes it — the toggle under test has already happened.
type scriptedSelector struct {
	t       *testing.T
	choices []string
	i       int
	cancels int
	title   string
}

func (s *scriptedSelector) install(ctx *core.Context) {
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		s.title = title
		if s.i >= len(s.choices) {
			s.cancels++
			if s.cancels > 8 {
				s.t.Fatalf("menu never closed: cancelling at %q did not unwind the stack", title)
			}
			onSelected("", false)
			return
		}
		choice := s.choices[s.i]
		s.i++
		onSelected(choice, choice != "")
	}
}

// configMenuHarness wires a realistic host context (real command context
// builder, real tool factory, real registry and running session) with a
// scripted /config menu driver.
type configMenuHarness struct {
	subs *subsystems
	ctx  core.Context
	sel  *scriptedSelector
}

// newConfigMenuHarness builds the harness with cfg as the session config and
// startTools pre-registered in the live registry.
func newConfigMenuHarness(t *testing.T, cfg *config.Config, startTools ...agentic.Tool) *configMenuHarness {
	t.Helper()
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	loader := config.NewCascadeLoader(proj, "", nil)
	subs := runtimeFactorySubsystems(t, cfg)
	subs.loader = loader
	modeReg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	subs.modeRegistry = modeReg
	am := core.NewAgentManager(cfg, nil, core.NewLoopDetector(core.DefaultLoopDetectorConfig()),
		core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}), subs.events, "")
	if _, err := am.StartSession(
		agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions},
		agenticprovider.StreamOptions{}, "sys", append(startTools, subs.toolRegistry.All()...), cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	subs.agentMgr = am
	for _, tool := range startTools {
		subs.toolRegistry.Register(tool)
	}

	h := &configMenuHarness{subs: subs, sel: &scriptedSelector{t: t, choices: []string{"tools", "enabled_tools"}}}
	h.ctx = coreContextForCommand(subs, nil)
	h.sel.install(&h.ctx)
	return h
}

// pick appends the next scripted menu choice.
func (h *configMenuHarness) pick(choice string) { h.sel.choices = append(h.sel.choices, choice) }

// runConfig drives the REAL /config command (root menu → Tools → the scripted
// rows), which is the path a user takes.
func (h *configMenuHarness) runConfig(t *testing.T) {
	t.Helper()
	if err := (&commands.ConfigCommand{}).Run(h.ctx, nil); err != nil {
		t.Fatalf("/config: %v", err)
	}
}

// agentToolNames returns the names of the tools the running agent holds.
func (h *configMenuHarness) agentToolNames() []string {
	agent := h.subs.agentMgr.CurrentAgent()
	if agent == nil {
		return nil
	}
	var out []string
	for _, tool := range agent.Tools() {
		out = append(out, tool.Schema().Name)
	}
	return out
}

// findAgentTool returns the tool instance the running agent holds, which is the
// instance the model would execute.
func (h *configMenuHarness) findAgentTool(name string) agentic.Tool {
	agent := h.subs.agentMgr.CurrentAgent()
	if agent == nil {
		return nil
	}
	for _, tool := range agent.Tools() {
		if tool.Schema().Name == name {
			return tool
		}
	}
	return nil
}

func mustTool(t *testing.T, reg interface {
	Get(string) (agentic.Tool, bool)
}, name string) agentic.Tool {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	return tool
}

func toolNamesContain(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// agentToolNamesOf returns the names of the tools a running agent holds.
func agentToolNamesOf(am *core.AgentManager) []string {
	agent := am.CurrentAgent()
	if agent == nil {
		return nil
	}
	var out []string
	for _, tool := range agent.Tools() {
		out = append(out, tool.Schema().Name)
	}
	return out
}

// probeLiveTool is a minimal disposable tool for the app-level harnesses.
type probeLiveTool struct {
	agentic.BaseTool
	name string
}

func (p *probeLiveTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: p.name, Description: "probe"}
}
func (p *probeLiveTool) Execute(string) (string, error) { return "ok", nil }

// TestLiveToolSetProvider_MatchesSessionStartFilter pins criterion (3): the
// provider wired into core.Context.LiveTools is exactly the call a session
// start makes (filterToolsForCurrentMode over the live registry), so a runtime
// toggle pushes the same mode-filtered set.
func TestLiveToolSetProvider_MatchesSessionStartFilter(t *testing.T) {
	cfg := factoryConfig()
	subs := runtimeFactorySubsystems(t, cfg)
	modeReg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	// A mode that allows exactly one tool: the filter must really filter.
	modeReg.RegisterMajor(core.MajorModeSpec{
		Major:        internal.MajorCoder,
		Name:         "Coder",
		AllowedTools: []string{"read"},
	})
	subs.modeRegistry = modeReg
	subs.toolRegistry.Register(&probeLiveTool{name: "read"})
	subs.toolRegistry.Register(&probeLiveTool{name: "write"})
	subs.agentMgr = core.NewAgentManager(cfg, nil, core.NewLoopDetector(core.DefaultLoopDetectorConfig()),
		core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}), event.MakeBus(8, 8, 8, 8), "")

	ctx := coreContextForCommand(subs, nil)
	if ctx.LiveTools == nil {
		t.Fatal("coreContextForCommand did not wire Context.LiveTools")
	}

	want := filterToolsForCurrentMode(subs, subs.toolRegistry.All())
	got := ctx.LiveTools()
	if namesOfTools(got) != namesOfTools(want) {
		t.Errorf("LiveTools() = %v, want the session-start filter %v", namesOfTools(got), namesOfTools(want))
	}
	// The filter must be doing something: the registry holds a tool the mode
	// disallows, and it must not be in the pushed set.
	if strings.Contains(namesOfTools(got), "write") {
		t.Errorf("LiveTools() = %v, want the mode-disallowed write tool withheld", namesOfTools(got))
	}
}

func namesOfTools(list []agentic.Tool) string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Schema().Name)
	}
	return strings.Join(names, ",")
}

// flashLog drains a bus into a message slice so a test can assert what the user
// was told (flash + durable system messages).
type flashLog struct {
	mu   sync.Mutex
	msgs []string
}

func newFlashLog(bus *event.Bus) *flashLog {
	l := &flashLog{}
	go func() {
		for ev := range bus.Chat {
			if ev.Flash != nil {
				l.add(ev.Flash.Text)
			}
			if ev.SystemMessage != nil {
				l.add(ev.SystemMessage.Text)
			}
		}
	}()
	return l
}

func (l *flashLog) add(s string) {
	l.mu.Lock()
	l.msgs = append(l.msgs, s)
	l.mu.Unlock()
}

func (l *flashLog) contains(want string) bool {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, m := range l.msgs {
			if strings.Contains(strings.ToLower(m), strings.ToLower(want)) {
				l.mu.Unlock()
				return true
			}
		}
		l.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestApplyToolToggle_EveryConfigurableToolRegistersOrSaysRestart is the
// criterion-4 table on the CALLER path: for every tools.ConfigurableTools()
// name, the shared primitive with the REAL host factory either registers a tool
// whose schema name matches, or reports "restart required" — never a silent
// success that the model cannot use. smartsearch is asserted as the documented
// restart-only tool.
func TestApplyToolToggle_EveryConfigurableToolRegistersOrSaysRestart(t *testing.T) {
	cfg := factoryConfig()
	subs := runtimeFactorySubsystems(t, cfg)
	subs.modeRegistry = core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	bus := event.MakeBus(64, 64, 64, 64)
	am := core.NewAgentManager(cfg, nil, core.NewLoopDetector(core.DefaultLoopDetectorConfig()),
		core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}), bus, "")
	if _, err := am.StartSession(
		agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions},
		agenticprovider.StreamOptions{}, "sys", nil, cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	subs.agentMgr = am

	ctx := core.Context{
		Config:       cfg,
		ToolRegistry: subs.toolRegistry,
		ToolFactory:  makeToolFactory(subs),
		AgentManager: am,
		EventBus:     bus,
		LiveTools:    liveToolSetProvider(subs),
	}

	restartNames := map[string]bool{}
	for _, ct := range tools.ConfigurableTools() {
		checkConfigurableToolToggle(t, ctx, subs.toolRegistry, am, ct.Name, restartNames)
	}

	if !restartNames["smartsearch"] {
		t.Errorf("smartsearch must keep the documented restart path; restart set = %v", restartNames)
	}
	for name := range restartNames {
		if name != "smartsearch" {
			t.Errorf("%s unexpectedly reports restart required with a full host fixture", name)
		}
	}
}

// checkConfigurableToolToggle applies one enable through the shared primitive
// and asserts the outcome matches the registry + the pushed agent tool set:
// either the tool is registered (and pushed), or the caller has explicit
// restart wording — never a silent no-op.
func checkConfigurableToolToggle(t *testing.T, ctx core.Context, reg *tools.ToolRegistry, am *core.AgentManager, name string, restartNames map[string]bool) {
	t.Helper()
	outcome, err := commands.ApplyToolToggle(ctx, name, true)
	if err != nil {
		t.Errorf("ApplyToolToggle(%s, true): %v", name, err)
		return
	}
	registered, isRegistered := reg.Get(name)
	if outcome == commands.ToolToggleRestartRequired {
		restartNames[name] = true
		if isRegistered {
			t.Errorf("%s: reported restart required but IS registered", name)
		}
		// The caller must have a message that says so — a restart-required
		// outcome without wording would be the silent no-op again.
		if msg := commands.ToolToggleMessage(name, true, outcome); !strings.Contains(strings.ToLower(msg), "restart") {
			t.Errorf("%s: restart-required outcome renders as %q (no restart claim)", name, msg)
		}
		return
	}
	if !isRegistered {
		t.Errorf("%s: reported success but is not registered (silent no-op)", name)
		return
	}
	if got := registered.Schema().Name; got != name {
		t.Errorf("%s: registered tool has schema name %q", name, got)
	}
	if !toolNamesContain(agentToolNamesOf(am), name) {
		t.Errorf("%s: registered but not pushed to the running agent", name)
	}
}

// TestConfigMenu_SmartSearchEnableReportsRestart drives the REAL /config menu
// for the one documented restart-only tool: enabling smartsearch must tell the
// user a restart is required (flash + durable system message) instead of
// quietly reporting it enabled while the model has no such tool.
func TestConfigMenu_SmartSearchEnableReportsRestart(t *testing.T) {
	cfg := factoryConfig()
	h := newConfigMenuHarness(t, cfg)
	log := newFlashLog(h.subs.events)

	h.pick("smartsearch")
	h.runConfig(t)

	if !cfg.Tools.SmartSearch.Enabled {
		t.Fatal("the flag must be flipped and saved even when the tool needs a restart")
	}
	if _, ok := h.subs.toolRegistry.Get("smartsearch"); ok {
		t.Fatal("smartsearch must not be registered at runtime (documented restart path)")
	}
	if !log.contains("restart") {
		t.Fatalf("enabling smartsearch must tell the user a restart is required; messages = %v", log.msgs)
	}
}
