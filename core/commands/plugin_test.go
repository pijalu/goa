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

// TestPluginCommand_ListShowsToggleHint verifies bare /plugin output tells the
// user how to enable/disable each plugin (the one-command management UX).
func TestPluginCommand_ListShowsToggleHint(t *testing.T) {
	mgr := newTestPluginManager(t)
	cmd := &PluginCommand{Manager: mgr}
	_ = cmd.Run(core.Context{}, []string{"install", "https://example.com/p.git"})

	var buf strings.Builder
	if err := cmd.Run(core.Context{OutputBuffer: &buf}, []string{}); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "test-plugin") {
		t.Fatalf("list missing plugin: %q", out)
	}
	if !strings.Contains(out, "/plugin enable test-plugin") {
		t.Fatalf("list missing enable hint for disabled plugin: %q", out)
	}
	if err := cmd.Run(core.Context{}, []string{"enable", "test-plugin"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	buf.Reset()
	if err := cmd.Run(core.Context{OutputBuffer: &buf}, []string{}); err != nil {
		t.Fatalf("list after enable: %v", err)
	}
	if !strings.Contains(buf.String(), "/plugin disable test-plugin") {
		t.Fatalf("list missing disable hint for enabled plugin: %q", buf.String())
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

	// Plugin id completion after a subcommand.
	ids := cmd.CompleteArgs(core.Context{}, "disable te")
	if len(ids) != 1 || ids[0].Value != "test-plugin" {
		t.Fatalf("id completion = %v, want [test-plugin]", ids)
	}
}
