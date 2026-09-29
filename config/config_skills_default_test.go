// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDefaultConfig_ShippedSkillsEnabledByDefault pins the shipped skill
// policy: the embedded default config lists exactly the shipped-on sticky
// knowledge skills (telegram, thoughtfull) in skills.enabled, so both apply
// out of the box while every other embedded skill stays default-off (opt-in
// via skills.embedded_enabled).
func TestDefaultConfig_ShippedSkillsEnabledByDefault(t *testing.T) {
	yamlText, err := DefaultConfigYAML()
	if err != nil {
		t.Fatalf("load embedded default: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlText), &cfg); err != nil {
		t.Fatalf("unmarshal embedded default: %v", err)
	}
	wantEnabled := []string{"telegram", "thoughtfull"}
	if len(cfg.Skills.Enabled) != len(wantEnabled) {
		t.Errorf("embedded default skills.enabled = %v, want %v", cfg.Skills.Enabled, wantEnabled)
	}
	for i, want := range wantEnabled {
		if i < len(cfg.Skills.Enabled) && cfg.Skills.Enabled[i] != want {
			t.Errorf("embedded default skills.enabled = %v, want %v", cfg.Skills.Enabled, wantEnabled)
			break
		}
	}
	if len(cfg.Skills.Disabled) != 0 {
		t.Errorf("embedded default must not ship skills.disabled, got %v", cfg.Skills.Disabled)
	}
	if len(cfg.Skills.EmbeddedEnabled) != 0 {
		t.Errorf("embedded default must not ship skills.embedded_enabled, got %v", cfg.Skills.EmbeddedEnabled)
	}
	if !cfg.Skills.Embedded {
		t.Error("embedded default must keep skills.embedded: true")
	}
}

// TestCascade_SkillsEnabledFromDefaults verifies a defaults-only cascade
// carries the shipped skills.enabled default (telegram, thoughtfull) AND the
// default-provided provenance flag, so the registry wiring can apply it
// embedded-scoped.
func TestCascade_SkillsEnabledFromDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wantEnabled := []string{"telegram", "thoughtfull"}
	if len(cfg.Skills.Enabled) != len(wantEnabled) {
		t.Errorf("shipped cascade skills.enabled = %v, want %v", cfg.Skills.Enabled, wantEnabled)
	}
	for i, want := range wantEnabled {
		if i < len(cfg.Skills.Enabled) && cfg.Skills.Enabled[i] != want {
			t.Errorf("shipped cascade skills.enabled = %v, want %v", cfg.Skills.Enabled, wantEnabled)
			break
		}
	}
	if !cfg.Skills.EnabledFromDefaults {
		t.Error("shipped cascade must mark skills.enabled as default-provided")
	}
}

// TestCascade_HomeSkillsPinOverridesDefault verifies the precedence rule: a
// home config that explicitly pins skills.enabled replaces the shipped
// default list entirely — telegram is NOT forced on top of the user's pin
// (and the list stops being default-provided, becoming a real allowlist).
func TestCascade_HomeSkillsPinOverridesDefault(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home+"/.goa/config.yaml", "skills:\n  enabled:\n    - refactor\n")
	t.Setenv("HOME", home)
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Skills.Enabled) != 1 || cfg.Skills.Enabled[0] != "refactor" {
		t.Errorf("home pin must replace the shipped default, got %v", cfg.Skills.Enabled)
	}
	if cfg.Skills.EnabledFromDefaults {
		t.Error("an explicit pin must clear the default-provided provenance")
	}
}

// TestCascade_HomeSkillsDisablePinsTelegramOff verifies the other pin
// direction: a home config listing telegram in skills.disabled keeps the
// skill off (explicit off wins over the shipped default).
func TestCascade_HomeSkillsDisablePinsTelegramOff(t *testing.T) {
	home := t.TempDir()
	writeTestFile(t, home+"/.goa/config.yaml", "skills:\n  disabled:\n    - telegram\n")
	t.Setenv("HOME", home)
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Skills.Disabled) != 1 || cfg.Skills.Disabled[0] != "telegram" {
		t.Errorf("home skills.disabled = %v, want [telegram]", cfg.Skills.Disabled)
	}
	wantEnabled := []string{"telegram", "thoughtfull"}
	if len(cfg.Skills.Enabled) != len(wantEnabled) {
		t.Errorf("shipped default enabled list must survive, got %v", cfg.Skills.Enabled)
	}
	for i, want := range wantEnabled {
		if i < len(cfg.Skills.Enabled) && cfg.Skills.Enabled[i] != want {
			t.Errorf("shipped default enabled list must survive, got %v", cfg.Skills.Enabled)
			break
		}
	}
}

// TestDeepMergeSkillsEnabled_DefaultOwnedReplacedByPin verifies the merge
// rule introduced with the shipped telegram default: when the accumulated
// list is default-provided, an explicit pin replaces it; once a pin exists,
// later layers concatenate (the toggle persistence partitions the allowlist
// across the home/project layers by skill source).
func TestDeepMergeSkillsEnabled_DefaultOwnedReplacedByPin(t *testing.T) {
	base := &Config{Skills: SkillsConfig{Enabled: []string{"telegram"}, EnabledFromDefaults: true}}
	home := &Config{Skills: SkillsConfig{Enabled: []string{"refactor"}}}
	base.DeepMerge(home)
	if len(base.Skills.Enabled) != 1 || base.Skills.Enabled[0] != "refactor" {
		t.Fatalf("pin must replace the default-owned list, got %v", base.Skills.Enabled)
	}
	if base.Skills.EnabledFromDefaults {
		t.Error("pin must clear EnabledFromDefaults")
	}

	project := &Config{Skills: SkillsConfig{Enabled: []string{"refactor", "qa-e2e"}}}
	base.DeepMerge(project)
	if len(base.Skills.Enabled) != 2 || base.Skills.Enabled[0] != "refactor" || base.Skills.Enabled[1] != "qa-e2e" {
		t.Fatalf("user layers must concatenate, got %v", base.Skills.Enabled)
	}
}

// TestSkillGateLists partitions the merged gate: the default-provided list is
// embedded-scoped (merged with the explicit opt-ins, never a global
// allowlist); an explicit pin is returned as the allowlist verbatim.
func TestSkillGateLists(t *testing.T) {
	def := SkillsConfig{Enabled: []string{"telegram"}, EnabledFromDefaults: true, EmbeddedEnabled: []string{"dream"}}
	allow, embedded := def.SkillGateLists()
	if allow != nil {
		t.Errorf("default-provided list must not become a global allowlist, got %v", allow)
	}
	if len(embedded) != 2 || embedded[0] != "dream" || embedded[1] != "telegram" {
		t.Errorf("embedded-scoped gate = %v, want [dream telegram]", embedded)
	}

	pin := SkillsConfig{Enabled: []string{"refactor"}, EmbeddedEnabled: []string{"dream"}}
	allow, embedded = pin.SkillGateLists()
	if len(allow) != 1 || allow[0] != "refactor" {
		t.Errorf("explicit pin must be the allowlist, got %v", allow)
	}
	if len(embedded) != 1 || embedded[0] != "dream" {
		t.Errorf("explicit opt-ins must pass through, got %v", embedded)
	}

	empty := SkillsConfig{}
	allow, embedded = empty.SkillGateLists()
	if allow != nil || embedded != nil {
		t.Errorf("empty config must yield nil gates, got %v / %v", allow, embedded)
	}
}
