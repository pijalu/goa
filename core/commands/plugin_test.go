// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/tui"
)

func newTestPluginManager(t *testing.T) *plugins.Manager {
	t.Helper()
	root := t.TempDir()
	m, err := plugins.NewManager(root, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	m.SetCloneFunc(func(url, dir string) error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		manifest := `id: test-plugin
name: Test Plugin
version: 1.0.0
entry: plugin.js
`
		_ = os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "plugin.js"), []byte("// plugin"), 0o600)
		return nil
	})
	return m
}

func TestPluginCommand_Install(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	if err := cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"}); err != nil {
		t.Fatalf("install: %v", err)
	}
	entries := mgr.List()
	if len(entries) != 1 || entries[0].ID != "test-plugin" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestPluginCommand_ListEmpty(t *testing.T) {
	mgr, _ := plugins.NewManager(t.TempDir(), nil)
	cmd := &PluginCommand{Manager: mgr}
	if err := cmd.Run(core.Context{}, nil); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestPluginCommand_NoManager(t *testing.T) {
	cmd := &PluginCommand{}
	if err := cmd.Run(core.Context{}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestPluginCommand_EnableDisableRemove(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"})

	if err := cmd.Run(core.Context{}, []string{"enable", "test-plugin"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !mgr.IsEnabled("test-plugin") {
		t.Fatal("plugin not enabled")
	}

	if err := cmd.Run(core.Context{}, []string{"disable", "test-plugin"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if mgr.IsEnabled("test-plugin") {
		t.Fatal("plugin still enabled")
	}

	if err := cmd.Run(core.Context{}, []string{"remove", "test-plugin"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(mgr.List()) != 0 {
		t.Fatal("plugin not removed")
	}
}

func TestPluginCommand_EnableRequiresTrust(t *testing.T) {
	trustMgr := trust.NewManager(filepath.Join(t.TempDir(), "trust.json"))
	root := t.TempDir()
	mgr, _ := plugins.NewManager(root, trustMgr)
	mgr.SetCloneFunc(func(url, dir string) error {
		_ = os.MkdirAll(dir, 0o700)
		manifest := `id: untrusted
name: Untrusted
version: 1.0.0
entry: plugin.js
`
		_ = os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "plugin.js"), []byte("// plugin"), 0o600)
		return nil
	})
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/u.git"})
	if err := cmd.Run(core.Context{}, []string{"enable", "untrusted"}); err == nil {
		t.Fatal("expected trust error")
	}
}

func TestPluginCommand_UnknownSubcommand(t *testing.T) {
	mgr, _ := plugins.NewManager(t.TempDir(), nil)
	cmd := &PluginCommand{Manager: mgr}
	if err := cmd.Run(core.Context{}, []string{"foo"}); err == nil {
		t.Fatal("expected error")
	}
}

// TestPluginCommand_InteractiveOpensSelector verifies bare /plugin opens an
// interactive selector (like /config → Tools) with one row per plugin showing
// its on/off state — instead of dumping raw text that corrupts the screen.
func TestPluginCommand_InteractiveOpensSelector(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"})

	var items []tui.SelectorItem
	var title string
	ctx := core.Context{
		SelectOptionFunc: func(ti string, it []tui.SelectorItem, _ string, _ func(string, bool)) {
			title, items = ti, it
		},
	}
	if err := cmd.Run(ctx, []string{}); err != nil {
		t.Fatalf("interactive: %v", err)
	}
	if title == "" {
		t.Fatal("bare /plugin should open a selector, not dump text")
	}
	if len(items) != 1 || items[0].Value != "test-plugin" {
		t.Fatalf("selector items = %+v, want one test-plugin row", items)
	}
	if !strings.Contains(items[0].Description, "off") {
		t.Fatalf("disabled plugin row should show 'off': %+v", items[0])
	}
}

// TestPluginCommand_ToggleFlipsStateAndPersists covers the toggle handler:
// selecting a plugin flips enabled→disabled→enabled and persists via the
// manager's lockfile.
func TestPluginCommand_ToggleFlipsStateAndPersists(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"})

	// Cancel the selector that toggle() re-opens, so each cmd.toggle call makes
	// exactly one manager state change and the recursion terminates.
	ctx := core.Context{
		SelectOptionFunc: func(_ string, _ []tui.SelectorItem, _ string, onSel func(string, bool)) {
			onSel("", false)
		},
	}
	// Initially disabled → first toggle enables.
	cmd.toggle(ctx, "test-plugin")
	if !mgr.IsEnabled("test-plugin") {
		t.Fatal("toggle should enable a disabled plugin")
	}
	// Toggle again → disables.
	cmd.toggle(ctx, "test-plugin")
	if mgr.IsEnabled("test-plugin") {
		t.Fatal("toggle should disable an enabled plugin")
	}
	// State is persisted in the manager's lockfile (survives a fresh manager).
	mgr2, err := plugins.NewManager(mgr.Root(), nil)
	if err != nil {
		t.Fatalf("reopen manager: %v", err)
	}
	for _, e := range mgr2.List() {
		if e.ID == "test-plugin" && e.Enabled {
			t.Fatal("disabled state not persisted to lockfile")
		}
	}
}

// TestPluginCommand_InteractiveEmptyManager shows a friendly message (no
// selector) when nothing is installed.
func TestPluginCommand_InteractiveEmptyManager(t *testing.T) {
	mgr, _ := plugins.NewManager(t.TempDir(), nil)
	cmd := &PluginCommand{Manager: mgr}
	opened := false
	ctx := core.Context{
		SelectOptionFunc: func(string, []tui.SelectorItem, string, func(string, bool)) { opened = true },
	}
	if err := cmd.Run(ctx, []string{}); err != nil {
		t.Fatalf("interactive empty: %v", err)
	}
	if opened {
		t.Fatal("selector should not open when no plugins are installed")
	}
}

// TestPluginCommand_CompletesSubcommandsAndIDs covers the completion contract:
// subcommand names with no arg, plugin ids after enable/disable/remove.
func TestPluginCommand_CompletesSubcommandsAndIDs(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"})

	subs := cmd.CompleteArgs(core.Context{}, "")
	if len(subs) == 0 {
		t.Fatal("no subcommand completions for empty prefix")
	}
	foundEnable := false
	for _, s := range subs {
		if s.Value == "enable" {
			foundEnable = true
		}
	}
	if !foundEnable {
		t.Fatalf("enable not offered: %v", subs)
	}

	// Prefix-filtered subcommand.
	d := cmd.CompleteArgs(core.Context{}, "di")
	if len(d) != 1 || d[0].Value != "disable" {
		t.Fatalf("prefix di = %v, want [disable]", d)
	}

	// Plugin id completion after a subcommand: enable offers the disabled
	// test-plugin; disable does NOT (it's not enabled yet).
	ids := cmd.CompleteArgs(core.Context{}, "enable te")
	if len(ids) != 1 || ids[0].Value != "test-plugin" {
		t.Fatalf("enable completion = %v, want [test-plugin]", ids)
	}
	if got := cmd.CompleteArgs(core.Context{}, "disable te"); len(got) != 0 {
		t.Fatalf("disable should not offer a disabled plugin, got %v", got)
	}
}

// TestPluginCommand_CompletionFiltersByState covers the requirement that
// /plugin enable lists only DISABLED plugins and /plugin disable only ENABLED
// ones, over a mixed-state set.
func TestPluginCommand_CompletionFiltersByState(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"})
	// test-plugin starts disabled; enable it, then install a second (disabled).
	if err := cmd.Manager.Enable("test-plugin"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// enable → only disabled (none of the enabled test-plugin).
	for _, c := range cmd.CompleteArgs(core.Context{}, "enable ") {
		if mgr.IsEnabled(c.Value) {
			t.Errorf("enable offered enabled plugin %q", c.Value)
		}
	}
	// disable → only enabled (test-plugin present).
	found := false
	for _, c := range cmd.CompleteArgs(core.Context{}, "disable ") {
		if !mgr.IsEnabled(c.Value) {
			t.Errorf("disable offered disabled plugin %q", c.Value)
		}
		if c.Value == "test-plugin" {
			found = true
		}
	}
	if !found {
		t.Error("disable should offer the enabled test-plugin")
	}
}
