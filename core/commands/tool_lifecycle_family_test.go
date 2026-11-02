// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"sort"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools"
)

// B51 RED tests: the mcp tool and the schedule/session gated families must be
// toggleable through the SAME primitive as every other tool
// (bugs.md B51: "agent-mcp `mcp` tool invisible in /config and not seen by the
// agent").

// registryNames returns the sorted schema names currently registered.
func registryNames(reg *tools.ToolRegistry) []string {
	out := make([]string, 0)
	for _, tool := range reg.All() {
		out = append(out, tool.Schema().Name)
	}
	sort.Strings(out)
	return out
}

// familyFactory builds a factory that can construct the named probe tools
// (family members keyed by their member name) and nothing else.
func familyFactory(names ...string) func(string) (agentic.Tool, bool) {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) (agentic.Tool, bool) {
		if set[name] {
			return &probeTool{name: name, description: "family member", deferred: true}, true
		}
		return nil, false
	}
}

// TestSetter_ToolsEnabledGenericKeys pins the generic tools.enabled.* setter
// fallback: any configurable tool name must be settable through
// /config set:tools.enabled.<name>, including the inverted
// ask_user_question flag and the smartsearch key.
func TestSetter_ToolsEnabledGenericKeys(t *testing.T) {
	cfg := &config.Config{}
	cases := []struct {
		key  string
		val  string
		want bool
	}{
		{"tools.enabled.mcp", "true", true},
		{"tools.enabled.mcp", "false", false},
		{"tools.enabled.mcp", "true", true}, // final: mcp left ON
		{"tools.enabled.schedule", "true", true},
		{"tools.enabled.session", "true", true},
		{"tools.enabled.webfetch", "false", false},
		{"tools.enabled.ask_user_question", "false", true}, // inverted: clarify_disabled=true
		{"tools.smartsearch.enabled", "true", true},
	}
	for _, tc := range cases {
		if err := setConfigField(cfg, strings.Split(tc.key, "."), tc.val); err != nil {
			t.Fatalf("setConfigField(%s): %v", tc.key, err)
		}
	}
	if !cfg.Tools.Enabled.AgentMCP {
		t.Error("tools.enabled.mcp=true did not flip AgentMCP")
	}
	if cfg.Tools.Enabled.AgentMCP && getToolEnabled(cfg, "mcp") != true {
		t.Error("getToolEnabled(mcp) disagrees with the flipped flag")
	}
	if !cfg.Tools.Enabled.Schedule {
		t.Error("tools.enabled.schedule=true did not flip Schedule")
	}
	if !cfg.Tools.Enabled.Session {
		t.Error("tools.enabled.session=true did not flip Session")
	}
	if cfg.Tools.Enabled.WebFetch {
		t.Error("tools.enabled.webfetch=false did not flip WebFetch")
	}
	if getToolEnabled(cfg, "ask_user_question") {
		t.Errorf("ask_user_question=false must disable it, still enabled (ClarifyDisabled=%v)", cfg.Tools.Enabled.ClarifyDisabled)
	}
	if !cfg.Tools.SmartSearch.Enabled {
		t.Error("tools.smartsearch.enabled=true did not flip SmartSearch.Enabled")
	}
}

// TestSetter_ToolsEnabledUnknownToolRejected pins that the generic fallback
// does NOT accept arbitrary keys: only configurable tool names route through.
func TestSetter_ToolsEnabledUnknownToolRejected(t *testing.T) {
	cfg := &config.Config{}
	if err := setConfigField(cfg, strings.Split("tools.enabled.not_a_tool", "."), "true"); err == nil {
		t.Fatal("tools.enabled.<unknown> must be rejected")
	}
}

// TestFamilyToggle_ScheduleRegistersAllMembers pins family lifecycle: enabling
// `schedule` registers ALL THREE schedule_* tools; disabling unregisters every
// member — never a partial family (which would leave the model calling a
// create tool whose list/delete siblings are missing).
func TestFamilyToggle_ScheduleRegistersAllMembers(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarness(t, cfg)
	h.ctx.ToolFactory = familyFactory("schedule_create", "schedule_delete", "schedule_list")
	h.menu = newConfigMenu(*h.ctx)

	if _, err := ApplyToolToggle(*h.ctx, "schedule", true); err != nil {
		t.Fatalf("enable schedule: %v", err)
	}
	got := registryNames(h.reg)
	for _, m := range []string{"schedule_create", "schedule_delete", "schedule_list"} {
		if !containsName(got, m) {
			t.Errorf("enabling schedule did not register member %q: registry = %v", m, got)
		}
	}
	if !cfg.Tools.Enabled.Schedule {
		t.Error("config flag Schedule not flipped to true")
	}

	if _, err := ApplyToolToggle(*h.ctx, "schedule", false); err != nil {
		t.Fatalf("disable schedule: %v", err)
	}
	got = registryNames(h.reg)
	for _, m := range []string{"schedule_create", "schedule_delete", "schedule_list"} {
		if containsName(got, m) {
			t.Errorf("disabling schedule left member %q registered: registry = %v", m, got)
		}
	}
	if cfg.Tools.Enabled.Schedule {
		t.Error("config flag Schedule not flipped to false")
	}
}

// TestFamilyToggle_SessionRegistersAllMembers is the session-family twin of
// the schedule test: one flag, two member tools, both sides of the toggle.
func TestFamilyToggle_SessionRegistersAllMembers(t *testing.T) {
	cfg := &config.Config{}
	h := newLiveToggleHarness(t, cfg)
	h.ctx.ToolFactory = familyFactory("session_search", "session_event_read")
	h.menu = newConfigMenu(*h.ctx)

	if _, err := ApplyToolToggle(*h.ctx, "session", true); err != nil {
		t.Fatalf("enable session: %v", err)
	}
	got := registryNames(h.reg)
	for _, m := range []string{"session_search", "session_event_read"} {
		if !containsName(got, m) {
			t.Errorf("enabling session did not register member %q: registry = %v", m, got)
		}
	}

	if _, err := ApplyToolToggle(*h.ctx, "session", false); err != nil {
		t.Fatalf("disable session: %v", err)
	}
	got = registryNames(h.reg)
	for _, m := range []string{"session_search", "session_event_read"} {
		if containsName(got, m) {
			t.Errorf("disabling session left member %q registered: registry = %v", m, got)
		}
	}
}

// TestToggle_MCPRuntimeEnableWithoutRestart is the user-visible B51 fix: with
// tools.enabled.mcp=false at start, /tools:mcp:on must build and register the
// mcp tool at runtime and push it to the running agent — no restart, no
// "could not be instantiated" message. The /tools COMMAND path is what the
// user types: parseToolToggleArgs must accept mcp (configurable) and route to
// the toggle primitive, not fall through to runToolDirect.
func TestToggle_MCPRuntimeEnableWithoutRestart(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("mcp", false)
	h := newLiveToggleHarness(t, cfg)
	h.ctx.ToolFactory = familyFactory("mcp")
	h.menu = newConfigMenu(*h.ctx)

	name, onOff, ok := parseToolToggleArgs([]string{"mcp:on"})
	if !ok || name != "mcp" || onOff != "on" {
		t.Fatalf("parseToolToggleArgs(mcp:on) = (%q,%q,%v), want the toggle path", name, onOff, ok)
	}
	if err := toggleTool(*h.ctx, "mcp", "on"); err != nil {
		t.Fatalf("toggleTool mcp on: %v", err)
	}
	if _, ok := h.reg.Get("mcp"); !ok {
		t.Fatal("mcp was not registered at runtime")
	}
	if !containsName(h.agentToolNames(), "mcp") {
		t.Errorf("mcp was not pushed to the running agent: %v", h.agentToolNames())
	}
	if h.rec.hasContaining("restart") {
		t.Errorf("runtime mcp enable must not mention a restart: %v", h.rec.snapshot())
	}
}

// TestCompletions_IncludeAllConfigurableToolKeys pins that /config completion
// derives the tools.enabled.* keys from the configurable-tool list instead of
// hardcoding a single entry (the B51 audit found only tools.enabled.goal).
func TestCompletions_IncludeAllConfigurableToolKeys(t *testing.T) {
	got := map[string]bool{}
	for _, c := range configKeyCompletions("tools.enabled.") {
		got[c.Value] = true
	}
	for _, name := range tools.ConfigurableToolNames() {
		key := "tools.enabled." + name
		if !got[key] {
			t.Errorf("configKeyCompletions missing %q (configurable tool)", key)
		}
	}
}
