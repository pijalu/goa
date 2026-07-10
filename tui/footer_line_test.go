// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestFormatFooterLine_BuildsStatsModelLine verifies the shared builder
// combines pre-formatted stats, a provider-prefixed model, thinking level,
// and busy activity into a single footer line.
func TestFormatFooterLine_BuildsStatsModelLine(t *testing.T) {
	line := FormatFooterLine("↑10k ↓5k CH96.2%", "gemma", "google", "high", "tool", true, true)
	stripped := ansi.Strip(line)

	if !strings.Contains(stripped, "↑10k") || !strings.Contains(stripped, "↓5k") {
		t.Errorf("stats missing from line: %q", stripped)
	}
	if !strings.Contains(stripped, "(google)") || !strings.Contains(stripped, "gemma") {
		t.Errorf("provider-prefixed model missing: %q", stripped)
	}
	if !strings.Contains(stripped, "high") {
		t.Errorf("thinking level missing: %q", stripped)
	}
	if !strings.Contains(stripped, "tool") {
		t.Errorf("activity missing: %q", stripped)
	}
	if !strings.Contains(line, ansi.Fg("#3fb950")) {
		t.Errorf("active model should be green: %q", line)
	}
	if !strings.Contains(line, CurrentSpinnerFrame()) && CurrentSpinnerFrame() != "" {
		t.Errorf("busy model should have spinner frame: %q", line)
	}
}

// TestFormatFooterLine_NoProviderWhenModelHasPrefix verifies the provider is
// not duplicated when the model string already carries a provider prefix.
func TestFormatFooterLine_NoProviderWhenModelHasPrefix(t *testing.T) {
	line := FormatFooterLine("↑1", "(lmstudio) llama3", "lmstudio", "", "", false, false)
	stripped := ansi.Strip(line)
	if strings.Count(stripped, "lmstudio") != 1 {
		t.Errorf("provider should not be duplicated: %q", stripped)
	}
}

// TestFormatFooterLine_DropsOffThinking verifies an "off" thinking level is
// omitted from the rendered model part.
func TestFormatFooterLine_DropsOffThinking(t *testing.T) {
	line := ansi.Strip(FormatFooterLine("", "gemma", "", "off", "", false, false))
	if strings.Contains(line, "off") {
		t.Errorf("'off' thinking should be hidden: %q", line)
	}
}

// TestFormatFooterLine_NoActivityWhenIdle verifies activity text is only shown
// when the model is busy.
func TestFormatFooterLine_NoActivityWhenIdle(t *testing.T) {
	line := ansi.Strip(FormatFooterLine("", "gemma", "", "", "streaming", false, false))
	if strings.Contains(line, "streaming") {
		t.Errorf("activity should not appear when idle: %q", line)
	}
}

// TestFormatFooterLine_StatsOnlyNoModel verifies the builder tolerates empty
// model and stats and still produces a valid line.
func TestFormatFooterLine_StatsOnlyNoModel(t *testing.T) {
	line := ansi.Strip(FormatFooterLine("↑1", "", "", "", "", false, false))
	if !strings.Contains(line, "↑1") {
		t.Errorf("stats-only line should keep stats: %q", line)
	}
}
