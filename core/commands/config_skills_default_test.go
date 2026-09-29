// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/skills"
)

// shippedDefaultSkillsCfg builds the in-memory skills config exactly as a
// defaults-only cascade load produces it: the shipped [telegram thoughtfull]
// list flagged as default-provided.
func shippedDefaultSkillsCfg() *config.Config {
	return &config.Config{Skills: config.SkillsConfig{
		Enabled:             []string{"telegram", "thoughtfull"},
		EnabledFromDefaults: true,
	}}
}

// realEmbeddedRegistry is a registry over the embedded FS only, with the
// shipped default-off set applied (no gates) — SourceOf resolves embedded
// names for the skillEnabledIn matrix.
func realEmbeddedRegistry(t *testing.T) *skills.SkillRegistry {
	t.Helper()
	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return reg
}

// TestSkillEnabledIn_DefaultProvidedAllowlistIsEmbeddedScoped is the toggle-
// view regression for the shipped skills default: the default-provided
// skills.enabled list turns ONLY the listed embedded skills on; other embedded
// skills stay off (unless opted in), and file-based skills are unaffected by
// the list (they stay on unless disabled).
func TestSkillEnabledIn_DefaultProvidedAllowlistIsEmbeddedScoped(t *testing.T) {
	reg := realEmbeddedRegistry(t)
	regWithFiles := registryWithProjectSkill(t, "proj-skill")

	cfg := shippedDefaultSkillsCfg()
	if !skillEnabledIn(cfg, "telegram", "embedded", reg) {
		t.Error("telegram must be on under the shipped default")
	}
	if !skillEnabledIn(cfg, "thoughtfull", "embedded", reg) {
		t.Error("thoughtfull must be on under the shipped default")
	}
	if skillEnabledIn(cfg, "refactor", "embedded", reg) {
		t.Error("refactor must stay default-off")
	}
	if skillEnabledIn(cfg, "dream", "embedded", reg) {
		t.Error("dream must stay default-off")
	}
	if !skillEnabledIn(cfg, "proj-skill", "file", regWithFiles) {
		t.Error("file skills must be unaffected by the default-provided list")
	}
	if !skillEnabledIn(cfg, "proj-skill", "", regWithFiles) {
		t.Error("unknown-source file skills must be unaffected by the default-provided list")
	}
	// The embedded opt-in still applies on top of the default list.
	cfg.Skills.EmbeddedEnabled = []string{"refactor"}
	if !skillEnabledIn(cfg, "refactor", "embedded", reg) {
		t.Error("embedded opt-in must turn refactor on alongside the default")
	}
}

// TestSkillEnabledIn_DefaultListExplicitOffWins verifies the precedence rule
// on top of the shipped default: an explicit Disabled entry keeps the skill
// off even while the default-provided list contains it.
func TestSkillEnabledIn_DefaultListExplicitOffWins(t *testing.T) {
	cfg := shippedDefaultSkillsCfg()
	cfg.Skills.Disabled = []string{"telegram"}
	if skillEnabledIn(cfg, "telegram", "embedded", realEmbeddedRegistry(t)) {
		t.Error("explicit Disabled must keep telegram off")
	}
}

// TestSkillEnabledIn_ExplicitPinIsRealAllowlist verifies that once a config
// layer pins skills.enabled, the legacy global-allowlist semantics apply:
// every source not in the list is off, including the shipped telegram default.
func TestSkillEnabledIn_ExplicitPinIsRealAllowlist(t *testing.T) {
	reg := realEmbeddedRegistry(t)
	regWithFiles := registryWithProjectSkill(t, "proj-skill")
	cfg := &config.Config{Skills: config.SkillsConfig{Enabled: []string{"refactor"}}}
	if !skillEnabledIn(cfg, "refactor", "embedded", reg) {
		t.Error("pinned refactor must be on")
	}
	if skillEnabledIn(cfg, "telegram", "embedded", reg) {
		t.Error("telegram must be off when a pin replaces the shipped default")
	}
	if skillEnabledIn(cfg, "proj-skill", "file", regWithFiles) {
		t.Error("a pin must gate file skills like any allowlist")
	}
}

// registryWithProjectSkill builds a registry over a temp dir containing one
// file-based skill with the given name.
func registryWithProjectSkill(t *testing.T, name string) *skills.SkillRegistry {
	t.Helper()
	dir := t.TempDir()
	skillPath := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: project skill\n---\nbody"
	if err := os.WriteFile(skillPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := skills.NewSkillRegistry([]string{dir})
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll files: %v", err)
	}
	return reg
}

// TestSkillAllowListActive_DefaultListIsNotAPin verifies the toggle routing:
// with the shipped default list live, enabling must NOT treat the list as a
// user allowlist (it would persist a pin and suppress file skills); an
// explicit pin or a persisted-layer pin still counts.
func TestSkillAllowListActive_DefaultListIsNotAPin(t *testing.T) {
	ctx, _, _, _ := newMenuTestContext(t, shippedDefaultSkillsCfg())
	if skillAllowListActive(*ctx, "refactor", "embedded", true) {
		t.Error("the default-provided list must not count as an active allowlist")
	}
	ctx.Config.Skills.EnabledFromDefaults = false
	if !skillAllowListActive(*ctx, "refactor", "embedded", true) {
		t.Error("an explicit pin must count as an active allowlist")
	}
}

// TestPersistSkillToggle_DefaultListNeverPinned is the persistence regression
// for the shipped default: toggling the shipped-on telegram skill off writes
// ONLY a Disabled entry to the home layer — the default-owned Enabled list
// must never be persisted (a persisted pin would flip later loads into
// global-allowlist mode and suppress the user's file skills).
func TestPersistSkillToggle_DefaultListNeverPinned(t *testing.T) {
	cfg := shippedDefaultSkillsCfg()
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"telegram": embeddedTestSkill("telegram", "telegraphic style"),
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)
	sr.onSel("telegram", true) // disable the shipped-on skill

	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	data, err := os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	raw := string(data)
	if !strings.Contains(raw, "disabled") || !strings.Contains(raw, "telegram") {
		t.Errorf("disabling telegram must persist skills.disabled: [telegram], got:\n%s", raw)
	}
	if strings.Contains(raw, "enabled") {
		t.Errorf("the default-owned enabled list must not be persisted as a pin, got:\n%s", raw)
	}

	// Re-enabling drops the Disabled entry and routes through the embedded
	// opt-in — the home config must not grow an enabled/disabled pin either.
	sr.onSel("telegram", true)
	data, err = os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("re-read home config: %v", err)
	}
	raw = string(data)
	if !strings.Contains(raw, "embedded_enabled") || !strings.Contains(raw, "telegram") {
		t.Errorf("re-enable must persist the embedded opt-in, got:\n%s", raw)
	}
	if strings.Contains(raw, "disabled") || strings.Contains(raw, "\n  enabled:") {
		t.Errorf("re-enable must not persist enabled/disabled pins, got:\n%s", raw)
	}
}

// TestConfigMenu_SkillsShippedDefaultsOn is the menu-level regression
// for the shipped default: with a defaults-only cascade load, the Skills
// sub-menu reports exactly the shipped-on skills (telegram, thoughtfull) on
// and every other embedded skill off.
func TestConfigMenu_SkillsShippedDefaultsOn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(skills.DefaultEmbeddedOffNames(skills.EmbeddedSkillsFS))
	allow, embeddedScoped := cfg.Skills.SkillGateLists()
	reg.SetEnabled(allow)
	reg.SetEmbeddedEnabled(embeddedScoped)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	ctx.SkillRegistry = reg

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)

	total := len(skills.EmbeddedSkillNames(skills.EmbeddedSkillsFS))
	wantLabel := fmt.Sprintf("2/%d on", total)
	for _, item := range sr.options {
		if item.Value == "embedded" && item.Description != wantLabel {
			t.Errorf("embedded source description = %q, want %q", item.Description, wantLabel)
		}
	}

	sr.onSel("embedded", true)
	seen := map[string]string{}
	for _, o := range sr.options {
		seen[o.Value] = o.Description
	}
	if seen["telegram"] != "on" {
		t.Errorf("telegram must read on under the shipped default, got %q", seen["telegram"])
	}
	if seen["thoughtfull"] != "on" {
		t.Errorf("thoughtfull must read on under the shipped default, got %q", seen["thoughtfull"])
	}
	for name, desc := range seen {
		if name != "telegram" && name != "thoughtfull" && desc != "off" {
			t.Errorf("embedded skill %s = %q, want off (only telegram and thoughtfull ship on)", name, desc)
		}
	}
}
