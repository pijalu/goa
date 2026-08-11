// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// Regression: the Memory Dreams screen ignored Up/Down (no stateDreamModel
// case in handleUp/handleDown), leaving the selection frozen.
func TestWizardComponent_dreamModelUpDownTogglesSelection(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateDreamModel
	w.dreamEnabled = 1

	w.HandleInput(tui.KeyDown)
	if w.dreamEnabled != 0 {
		t.Errorf("after Down: dreamEnabled = %d, want 0", w.dreamEnabled)
	}
	w.HandleInput(tui.KeyUp)
	if w.dreamEnabled != 1 {
		t.Errorf("after Up: dreamEnabled = %d, want 1", w.dreamEnabled)
	}
}

// Regression: Esc on the Memory Dreams screen was a no-op (stateDreamModel
// missing from staticPreviousStates), freezing the wizard.
func TestWizardComponent_dreamModelEscapeGoesWebFetchSummary(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateDreamModel

	w.HandleInput(tui.KeyEscape)
	if w.state != stateWebFetchSummary {
		t.Errorf("state = %d, want stateWebFetchSummary (%d)", w.state, stateWebFetchSummary)
	}
}

// Regression: stepForState had no stateDreamModel case and fell through to
// the default, rendering "Step 1/10" on the Memory Dreams screen.
func TestWizardComponent_dreamModelStepNumber(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	if got := w.stepForState(stateDreamModel); got <= 1 {
		t.Errorf("stepForState(stateDreamModel) = %d, want > 1", got)
	}
}

// Quick-pick keys follow the visible list order: 1 = Yes (enable), 2 = No.
func TestWizardComponent_dreamModelQuickPick(t *testing.T) {
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateDreamModel
	w.dreamEnabled = 0

	w.HandleInput("1")
	if w.dreamEnabled != 1 {
		t.Errorf("after '1': dreamEnabled = %d, want 1 (Yes)", w.dreamEnabled)
	}
	w.HandleInput("2")
	if w.dreamEnabled != 0 {
		t.Errorf("after '2': dreamEnabled = %d, want 0 (No)", w.dreamEnabled)
	}
}

// The dream selection must reach the saved config.
func TestWizardComponent_dreamModelSelectionPersisted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled int
		want    bool
	}{
		{"enabled", 1, true},
		{"disabled", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			done := make(chan *WizardResult, 1)
			w := newWizardComponent(cfg, nil, "/tmp", done)
			// applyProviderConfig returns early without a provider; set one so
			// the dream preferences are actually applied.
			w.main.providerID = "openai"
			w.main.modelID = "gpt-4o"
			w.main.modelName = "gpt-4o"
			w.dreamEnabled = tc.enabled
			w.saveConfig()

			if cfg.Memory.Dream.Enabled != tc.want {
				t.Errorf("Memory.Dream.Enabled = %v, want %v", cfg.Memory.Dream.Enabled, tc.want)
			}
		})
	}
}

// Regression: the done screen displayed a literal "~/.goa/config.yaml",
// which is wrong under --home / GOA_HOME and was never expanded.
func TestWizardComponent_doneShowsResolvedConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)

	loader := NewCascadeLoader(t.TempDir(), "", nil)
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, loader, "/tmp", done)
	w.state = stateDone
	w.selectedMode = 0

	lines := w.renderDone(80)
	var configLine string
	for _, l := range lines {
		if strings.Contains(l, "Config:") {
			configLine = l
		}
	}
	if configLine == "" {
		t.Fatal("no Config: line rendered")
	}
	if strings.Contains(configLine, "~") {
		t.Errorf("Config line contains literal ~: %q", configLine)
	}
	want := filepath.Join(home, ".goa", "config.yaml")
	if !strings.Contains(configLine, want) {
		t.Errorf("Config line = %q, want it to contain %q", configLine, want)
	}
}
