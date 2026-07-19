// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
)

func spinnerIsEmpty(t *testing.T) bool {
	t.Helper()
	// SpinnerText returns the static diamond when no spinner is configured
	// and the status is not spinning. Show it so we can inspect the frame.
	sm := tui.NewStatusMsg()
	sm.Show("x")
	text := sm.SpinnerText()
	sm.Clear()
	return text == "◆"
}

func TestInitSpinner_DefaultWhenEmpty(t *testing.T) {
	defer resetSpinnerState()

	cfg := &config.Config{}
	cfg.TUI.Spinner = ""
	initSpinner(cfg)

	if spinnerIsEmpty(t) {
		t.Error("expected default spinner for empty config, got disabled")
	}
}

func TestInitSpinner_DisabledForNone(t *testing.T) {
	defer resetSpinnerState()

	cfg := &config.Config{}
	cfg.TUI.Spinner = "none"
	initSpinner(cfg)

	if !spinnerIsEmpty(t) {
		t.Error("expected spinner disabled for 'none', got active spinner")
	}
}

func TestInitSpinner_UsesConfiguredName(t *testing.T) {
	defer resetSpinnerState()

	cfg := &config.Config{}
	cfg.TUI.Spinner = "dots"
	initSpinner(cfg)

	if spinnerIsEmpty(t) {
		t.Error("expected 'dots' spinner, got disabled")
	}
}

// TestCollectCmdNames_HidesAliases is the regression for the duplicate
// /plugin + /plugins completion entries: the popup must list one entry per
// command (the canonical name) while aliases keep resolving when typed.
func TestCollectCmdNames_HidesAliases(t *testing.T) {
	reg := core.NewCommandRegistry()
	if err := reg.Register(&commands.PluginCommand{}); err != nil {
		t.Fatal(err)
	}
	names, descriptions := collectCmdNames(reg)
	for _, n := range names {
		if n == "/plugins" {
			t.Fatalf("alias /plugins listed as a completion entry: %v", names)
		}
	}
	found := false
	for _, n := range names {
		if n == "/plugin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("/plugin missing from completion names: %v", names)
	}
	// The alias keeps a description so help tooling stays informative.
	if descriptions["/plugins"] == "" {
		t.Fatal("alias description lost")
	}
	// And the alias still resolves through the registry.
	if _, ok := reg.Resolve("plugins"); !ok {
		t.Fatal("/plugins alias no longer resolves")
	}
}

func resetSpinnerState() {
	tui.SetSpinner(spinner.Definition{})
}
