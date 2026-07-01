// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestThinkingLevelSeparatorColor_BuiltInLevels(t *testing.T) {
	TheTheme = DarkTheme()
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	seen := map[string]bool{}
	for _, lvl := range levels {
		c := ThinkingLevelSeparatorColor(lvl)
		if c == "" {
			t.Errorf("level %q returned empty color", lvl)
		}
		seen[c] = true
	}
	if len(seen) < len(levels) {
		t.Errorf("expected distinct colors for each level, got %d unique", len(seen))
	}
}

func TestThinkingLevelSeparatorColor_Fallback(t *testing.T) {
	TheTheme = DarkTheme()
	fallback := TheTheme.ColorHex("separator")
	c := ThinkingLevelSeparatorColor("")
	if c != fallback {
		t.Errorf("empty level color = %q, want fallback %q", c, fallback)
	}
}

func TestThinkingLevelSeparatorColor_CustomThemeMissingToken(t *testing.T) {
	TheTheme = &Theme{
		Name:   "minimal",
		Colors: map[string]ColorToken{"separator": {Hex: "#111111"}},
	}
	c := ThinkingLevelSeparatorColor("xhigh")
	if c != "#111111" {
		t.Errorf("missing level token should fall back to separator, got %q", c)
	}
}

func TestEditorBorder_UsesThinkingLevelColor(t *testing.T) {
	TheTheme = DarkTheme()
	e := NewEditor()
	e.SetThinkingLevel("high")
	lines := e.Render(20)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	border := lines[0]
	want := TheTheme.ColorHex("separator_high")
	if !strings.Contains(border, ansi.Fg(want)) {
		t.Errorf("top border %q missing high-level color %q", border, want)
	}
}
