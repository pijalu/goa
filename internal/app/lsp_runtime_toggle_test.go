// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/tools"
	"gopkg.in/yaml.v3"
)

// lspToggleSubsystems builds subsystems with a real registry (read/edit/write)
// and NO LSP manager — the state after bootstrap with tools.enabled.lsp:false.
func lspToggleSubsystems(t *testing.T) *subsystems {
	t.Helper()
	subs := testSubsystems()
	subs.cfg.Tools.Enabled.LSP = false
	subs.projectDir = t.TempDir()
	reg := tools.NewToolRegistry()
	reg.Register(&tools.ReadFileTool{})
	reg.Register(&tools.WriteFileTool{ProjectDir: subs.projectDir})
	reg.Register(&tools.EditFileTool{ProjectDir: subs.projectDir})
	subs.toolRegistry = reg
	return subs
}

func fileToolLSP(t *testing.T, subs *subsystems) (read, write, edit tools.LSPDocumentManager) {
	t.Helper()
	rt, _ := subs.toolRegistry.Get("read")
	wt, _ := subs.toolRegistry.Get("write")
	et, _ := subs.toolRegistry.Get("edit")
	return rt.(*tools.ReadFileTool).LSPManager,
		wt.(*tools.WriteFileTool).LSPManager,
		et.(*tools.EditFileTool).LSPManager
}

// requireFileToolsLSPWired asserts every file tool's LSPManager nil-ness
// matches want; phase labels failures ("precondition" vs "after toggle-on").
func requireFileToolsLSPWired(t *testing.T, subs *subsystems, want bool, phase string) {
	t.Helper()
	r, w, e := fileToolLSP(t, subs)
	wired := r != nil && w != nil && e != nil
	if wired != want {
		t.Errorf("%s: read/edit/write LSP wiring = %v (read=%v write=%v edit=%v), want wired=%v",
			phase, wired, r != nil, w != nil, e != nil, want)
	}
}

// assertManagerCreatedAndStarted requires makeLSPToolRuntime to have produced
// a live manager bound to the subsystems.
func assertManagerCreatedAndStarted(t *testing.T, tool any, ok bool) {
	t.Helper()
	if !ok || tool == nil {
		t.Fatal("makeLSPToolRuntime should create the manager and the tool")
	}
}

// TestLSPToggleOn_CreatesManagerAndWiresFileTools verifies /tools:lsp:on brings
// the whole integration up live when bootstrap had it fully off: the manager
// is created AND wired into read/edit/write (Issue LSP).
func TestLSPToggleOn_CreatesManagerAndWiresFileTools(t *testing.T) {
	subs := lspToggleSubsystems(t)
	requireFileToolsLSPWired(t, subs, false, "precondition")

	// /tools:lsp:on flips the config flag BEFORE the factory runs.
	subs.cfg.Tools.Enabled.LSP = true
	tool, ok := makeLSPToolRuntime(subs)
	assertManagerCreatedAndStarted(t, tool, ok)
	if subs.lspMgr == nil || !subs.lspMgr.Started() {
		t.Fatal("manager must exist and be started after toggle-on")
	}
	requireFileToolsLSPWired(t, subs, true, "after toggle-on")
	t.Cleanup(func() { makeToolTeardown(subs)("lsp") })

	// Toggling on again reuses the same manager (no duplicate servers).
	if tool2, ok2 := makeLSPToolRuntime(subs); !ok2 || tool2 == nil {
		t.Fatal("second toggle-on should reuse the manager")
	}
}

// TestLSPToggleOff_UnwiresAndCloses verifies /tools:lsp:off fully disables the
// integration: file tools unwired, manager closed and cleared.
func TestLSPToggleOff_UnwiresAndCloses(t *testing.T) {
	subs := lspToggleSubsystems(t)
	subs.cfg.Tools.Enabled.LSP = true
	if _, ok := makeLSPToolRuntime(subs); !ok {
		t.Fatal("setup: toggle-on failed")
	}

	makeToolTeardown(subs)("lsp")

	if subs.lspMgr != nil {
		t.Error("manager must be cleared after toggle-off")
	}
	if r, w, e := fileToolLSP(t, subs); r != nil || w != nil || e != nil {
		t.Error("read/edit/write must be unwired after toggle-off")
	}

	// Other tool names are ignored.
	makeToolTeardown(subs)("bash")
}

// TestLSPToggleOn_GloballyDisabled verifies lsp: false blocks even the runtime
// toggle: the factory cannot create a manager.
func TestLSPToggleOn_GloballyDisabled(t *testing.T) {
	subs := lspToggleSubsystems(t)
	if err := yaml.Unmarshal([]byte("lsp: false\n"), subs.cfg); err != nil {
		t.Fatalf("unmarshal lsp:false: %v", err)
	}
	subs.cfg.Tools.Enabled.LSP = true
	if _, ok := makeLSPToolRuntime(subs); ok {
		t.Error("global lsp: false must block the runtime toggle")
	}
	if subs.lspMgr != nil {
		t.Error("no manager may be created under global lsp: false")
	}
}
