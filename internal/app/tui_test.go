// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
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

func resetSpinnerState() {
	tui.SetSpinner(spinner.Definition{})
}
