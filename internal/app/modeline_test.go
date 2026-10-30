// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
)

// TestNewFooter_AppliesConfiguredModeLine reproduces B26 through the production
// config-to-footer boundary: configuring segments must change the rendered
// footer, not merely survive YAML loading and cascade merging.
func TestNewFooter_AppliesConfiguredModeLine(t *testing.T) {
	a := &App{subs: &subsystems{
		projectDir: t.TempDir(),
		cfg: &config.Config{TUI: config.TUIConfig{
			ModeLine: config.ModeLineSegmentConfig{
				Left:  []string{"app-badge"},
				Right: []string{"help-hint"},
			},
		}},
	}}

	rendered := strings.Join(a.newFooter().Render(160), "\n")
	for _, want := range []string{"⬡ goa", "/help"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("configured footer does not render %q:\n%s", want, rendered)
		}
	}
}
