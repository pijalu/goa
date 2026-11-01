// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// Regression: the done screen displayed a literal "~/.goa/config.yaml",
// which is wrong under --home / GOA_HOME and was never expanded.
func TestWizardComponent_doneShowsResolvedConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOA_HOME", home)

	loader := NewCascadeLoader(t.TempDir(), "", nil)
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, loader, "/tmp", done)
	w.state = stateDone

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
