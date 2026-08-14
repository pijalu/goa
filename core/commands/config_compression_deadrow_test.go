// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
)

// Dead-row regression tests for the compression menu rework (bugs.md §3):
// the reported bug was read-only derived rows whose selection fell through
// the openers/switch and silently closed the overlay ("returns to main
// menu"). Every selectable row in the Compression menu AND its Advanced
// submenu must either open a picker or apply a set — never a silent no-op.

// TestCompressionMenu_NoDeadRows drives the Compression menu and asserts
// every selectable item either opens a new picker (title changes away from
// the menu title) or applies a config change. No empty Value allowed either
// (ctx.SelectOption treats "" as cancel).
func TestCompressionMenu_NoDeadRows(t *testing.T) {
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		Enabled:        boolPtr(true),
		OnContextError: true,
	}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()

	if len(sr.options) == 0 {
		t.Fatal("compression menu has no items")
	}
	for _, it := range sr.options {
		if it.Value == "" {
			t.Errorf("selectable item %q has empty Value (acts as cancel)", it.Label)
		}
	}
	// Each row must open a picker: the selector title moves off "Compression:".
	for _, it := range sr.options {
		menu.settingCompression()
		sr.onSel(it.Value, true)
		if sr.title == "Compression:" && sr.options[0].Value == it.Value {
			t.Errorf("row %q (%s) is dead: no picker opened after selection", it.Label, it.Value)
		}
	}
}

// TestCompressionMenu_AdvancedNoDeadRows does the same for the Advanced
// submenu: opener rows must open a picker, toggle rows must apply a set.
func TestCompressionMenu_AdvancedNoDeadRows(t *testing.T) {
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		Enabled:        boolPtr(true),
		OnContextError: true,
	}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()
	sr.onSel("advanced", true)

	if len(sr.options) == 0 {
		t.Fatal("advanced compression menu has no items")
	}
	toggles := map[string]bool{
		"enabled":       true,
		"cache_gate":    true,
		"micro_enabled": true,
	}
	for _, it := range sr.options {
		if it.Value == "" {
			t.Errorf("selectable item %q has empty Value (acts as cancel)", it.Label)
		}
		if toggles[it.Value] {
			continue
		}
		// Opener rows: selecting must move the selector off the advanced title.
		menu.settingCompressionAdvanced()
		sr.onSel(it.Value, true)
		if sr.title == "Compression — advanced:" && sr.options[0].Value == it.Value {
			t.Errorf("advanced row %q (%s) is dead: no picker opened after selection", it.Label, it.Value)
		}
	}
	// Toggle rows must apply a set (config flips), even though the title stays.
	menu.settingCompressionAdvanced()
	before := cfg.ContextCompression.EnabledValue()
	sr.onSel("enabled", true)
	if cfg.ContextCompression.EnabledValue() == before {
		t.Error("enabled toggle did not apply")
	}
}

// TestCompressionMenu_OnErrorPicker pins the On error row semantics:
// "off" turns OnContextError off; a method turns it on and sets the strategy.
func TestCompressionMenu_OnErrorPicker(t *testing.T) {
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
		Enabled:          boolPtr(true),
		OnContextError:   true,
		OnErrorStrategy:  "hybrid",
	}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.settingCompression()

	sr.onSel("on_error", true)
	if sr.title != "On context error:" {
		t.Fatalf("title = %q, want On context error:", sr.title)
	}
	if findOption(sr.options, "off") == nil {
		t.Fatalf("on-error picker missing off, got %v", sr.options)
	}
	for _, s := range []string{"micro", "tool_elision", "selective", "hybrid", "summarize"} {
		if findOption(sr.options, s) == nil {
			t.Fatalf("on-error picker missing %s", s)
		}
	}

	// off → OnContextError false.
	sr.onSel("off", true)
	if cfg.ContextCompression.OnContextError {
		t.Error("OnContextError = true after selecting off, want false")
	}

	// summarize → OnContextError true + OnErrorStrategy summarize.
	menu.settingCompression()
	sr.onSel("on_error", true)
	sr.onSel("summarize", true)
	if !cfg.ContextCompression.OnContextError {
		t.Error("OnContextError = false after selecting summarize, want true")
	}
	if got := cfg.ContextCompression.OnErrorStrategy; got != "summarize" {
		t.Errorf("OnErrorStrategy = %q, want summarize", got)
	}
}

// TestCompressionMenu_CeilingPickersPerLayer pins the two ceiling rows:
// selecting 60 updates the matching threshold and 0 disables.
func TestCompressionMenu_CeilingPickersPerLayer(t *testing.T) {
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(true)}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.settingCompression()
	sr.onSel("soft_percent", true)
	if findOption(sr.options, "60") == nil || findOption(sr.options, "0") == nil {
		t.Fatalf("soft ceiling picker missing 0/60 options, got %v", sr.options)
	}
	sr.onSel("60", true)
	if got := cfg.ContextCompression.Thresholds.SoftPercent; got != 60 {
		t.Errorf("SoftPercent = %d, want 60", got)
	}

	menu.settingCompression()
	sr.onSel("hard_percent", true)
	sr.onSel("60", true)
	if got := cfg.ContextCompression.Thresholds.HardPercent; got != 60 {
		t.Errorf("HardPercent = %d, want 60", got)
	}

	// 0 = disabled on each layer.
	menu.settingCompression()
	sr.onSel("hard_percent", true)
	sr.onSel("0", true)
	if got := cfg.ContextCompression.Thresholds.HardPercent; got != 0 {
		t.Errorf("HardPercent = %d, want 0 (disabled)", got)
	}
}

// TestCompressionMenu_MethodPickersAllStrategies pins that both method rows
// offer every method (the all-methods soft rework) and apply per layer.
func TestCompressionMenu_MethodPickersAllStrategies(t *testing.T) {
	cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{Enabled: boolPtr(true)}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)

	menu.settingCompression()
	sr.onSel("soft_method", true)
	for _, s := range []string{"micro", "tool_elision", "selective", "hybrid", "summarize"} {
		if findOption(sr.options, s) == nil {
			t.Fatalf("soft method picker missing %s, got %v", s, sr.options)
		}
	}
	sr.onSel("summarize", true)
	if got := cfg.ContextCompression.Strategies.Soft; got != "summarize" {
		t.Errorf("Strategies.Soft = %q, want summarize (no zero-LLM degradation)", got)
	}

	menu.settingCompression()
	sr.onSel("hard_method", true)
	sr.onSel("micro", true)
	if got := cfg.ContextCompression.Strategies.Hard; got != "micro" {
		t.Errorf("Strategies.Hard = %q, want micro", got)
	}
}
