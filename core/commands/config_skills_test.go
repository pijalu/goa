// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/skills"
)

func embeddedTestSkill(name, desc string) *skills.Skill {
	return &skills.Skill{
		Meta:   skills.SkillMeta{Name: name, Description: desc},
		Source: "embedded",
	}
}

func localTestSkill(name, desc string) *skills.Skill {
	return &skills.Skill{
		Meta:   skills.SkillMeta{Name: name, Description: desc},
		Source: "file",
	}
}

// TestConfigMenu_SkillsShowsSubmenus verifies the Skills sub-menu exposes the
// execution-mode entry plus the embedded (global) and local (per-project)
// toggle submenus.
func TestConfigMenu_SkillsShowsSubmenus(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"qa-e2e":   localTestSkill("qa-e2e", "Run e2e QA"),
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)

	if sr.title != "Skills settings:" {
		t.Fatalf("title = %q, want Skills settings:", sr.title)
	}
	want := []string{"execution_mode", "embedded", "local"}
	if len(sr.options) != len(want) {
		t.Fatalf("expected %d options, got %d: %+v", len(want), len(sr.options), sr.options)
	}
	for i, w := range want {
		if sr.options[i].Value != w {
			t.Errorf("option[%d].Value = %q, want %q", i, sr.options[i].Value, w)
		}
	}
}

// TestConfigMenu_SkillSourceListsOnlySource verifies each skill submenu lists
// only skills of its origin, with the current enabled state as description.
func TestConfigMenu_SkillSourceListsOnlySource(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"telegram": embeddedTestSkill("telegram", "Telegram style"),
		"qa-e2e":   localTestSkill("qa-e2e", "Run e2e QA"),
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)

	if sr.title != "Embedded skills (toggle on/off):" {
		t.Fatalf("title = %q, want embedded toggle list", sr.title)
	}
	if len(sr.options) != 2 {
		t.Fatalf("expected 2 embedded skills, got %d: %+v", len(sr.options), sr.options)
	}
	for _, o := range sr.options {
		if o.Value != "refactor" && o.Value != "telegram" {
			t.Errorf("unexpected embedded skill %q", o.Value)
		}
		if o.Description != "on" {
			t.Errorf("skill %s description = %q, want on", o.Value, o.Description)
		}
	}

	// Back to the Skills menu, then open Local.
	sr.onSel("", false)
	if sr.title != "Skills settings:" {
		t.Fatalf("expected Skills settings: after back, got %q", sr.title)
	}
	sr.onSel("local", true)
	if sr.title != "Local skills (toggle on/off):" {
		t.Fatalf("title = %q, want local toggle list", sr.title)
	}
	if len(sr.options) != 1 || sr.options[0].Value != "qa-e2e" {
		t.Fatalf("expected only qa-e2e, got: %+v", sr.options)
	}
	if sr.options[0].Description != "on" {
		t.Errorf("qa-e2e description = %q, want on", sr.options[0].Description)
	}
}

// TestConfigMenu_SkillToggleEmbeddedPersistsToHome verifies toggling an
// embedded skill writes the change to the HOME (global) config per the gold
// rules, and re-enabling removes it.
func TestConfigMenu_SkillToggleEmbeddedPersistsToHome(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)
	sr.onSel("refactor", true)

	if skillEnabled(cfg, "refactor") {
		t.Error("refactor should be disabled after toggle")
	}
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("cfg.Skills.Disabled should contain refactor")
	}

	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	data, err := os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	if !strings.Contains(string(data), "refactor") {
		t.Errorf("home config should disable refactor, got:\n%s", data)
	}

	// Re-enable: the home key must be removed.
	sr.onSel("refactor", true)
	if !skillEnabled(cfg, "refactor") {
		t.Error("refactor should be enabled after second toggle")
	}
	data, err = os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config after re-enable: %v", err)
	}
	if strings.Contains(string(data), "refactor") {
		t.Errorf("home config should no longer mention refactor, got:\n%s", data)
	}
}

// TestConfigMenu_SkillToggleLocalPersistsToProject verifies toggling a
// file-based skill writes the change to the PROJECT config per the gold rules
// and never touches the home config.
func TestConfigMenu_SkillToggleLocalPersistsToProject(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"qa-e2e": localTestSkill("qa-e2e", "Run e2e QA"),
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("local", true)
	sr.onSel("qa-e2e", true)

	if skillEnabled(cfg, "qa-e2e") {
		t.Error("qa-e2e should be disabled after toggle")
	}

	projectCfg := filepath.Join(projectDir, ".goa", "config.yaml")
	data, err := os.ReadFile(projectCfg)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if !strings.Contains(string(data), "qa-e2e") {
		t.Errorf("project config should disable qa-e2e, got:\n%s", data)
	}

	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	if _, err := os.Stat(homeCfg); err == nil {
		homeData, _ := os.ReadFile(homeCfg)
		if strings.Contains(string(homeData), "qa-e2e") {
			t.Errorf("home config should not mention qa-e2e, got:\n%s", homeData)
		}
	}
}

// TestSkillEnableDisableCommand verifies /skill:enable and /skill:disable
// toggle the skill and persist the change to the correct config layer.
func TestSkillEnableDisableCommand(t *testing.T) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	cfg := ctx.Config
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"qa-e2e":   localTestSkill("qa-e2e", "Run e2e QA"),
	})

	// Disable an embedded skill → home config.
	cmd := &SkillsCommand{}
	if err := cmd.Run(ctx, []string{"disable", "refactor"}); err != nil {
		t.Fatalf("disable refactor: %v", err)
	}
	if skillEnabled(cfg, "refactor") {
		t.Error("refactor should be disabled")
	}
	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	homeData, err := os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	if !strings.Contains(string(homeData), "refactor") {
		t.Errorf("home config should disable refactor, got:\n%s", homeData)
	}

	// Re-enable it → home key removed.
	buf.Reset()
	if err := cmd.Run(ctx, []string{"enable", "refactor"}); err != nil {
		t.Fatalf("enable refactor: %v", err)
	}
	if !skillEnabled(cfg, "refactor") {
		t.Error("refactor should be enabled")
	}
	homeData, err = os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config after enable: %v", err)
	}
	if strings.Contains(string(homeData), "refactor") {
		t.Errorf("home config should no longer mention refactor, got:\n%s", homeData)
	}

	// Disable a local skill → project config.
	buf.Reset()
	if err := cmd.Run(ctx, []string{"disable", "qa-e2e"}); err != nil {
		t.Fatalf("disable qa-e2e: %v", err)
	}
	if skillEnabled(cfg, "qa-e2e") {
		t.Error("qa-e2e should be disabled")
	}
	projectCfg := filepath.Join(projectDir, ".goa", "config.yaml")
	projectData, err := os.ReadFile(projectCfg)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if !strings.Contains(string(projectData), "qa-e2e") {
		t.Errorf("project config should disable qa-e2e, got:\n%s", projectData)
	}
}

// TestSkillEnableDisableCommand_Errors verifies usage errors and unknown-skill
// handling for /skill:enable and /skill:disable.
func TestSkillEnableDisableCommand_Errors(t *testing.T) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
	})

	cmd := &SkillsCommand{}
	if err := cmd.Run(ctx, []string{"enable"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("enable with no args should return usage error, got %v", err)
	}
	if err := cmd.Run(ctx, []string{"disable"}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("disable with no args should return usage error, got %v", err)
	}

	buf.Reset()
	if err := cmd.Run(ctx, []string{"enable", "nonexistent"}); err != nil {
		t.Fatalf("enable nonexistent: %v", err)
	}
	if !strings.Contains(buf.String(), "Skill not found") {
		t.Errorf("expected Skill not found, got: %s", buf.String())
	}
}

// TestSkillEnableDisableCommand_RealRegistry exercises the production path:
// disabling removes the skill from the (reloaded) registry, and re-enabling
// resolves its source via SourceOf so the toggle lands in the right config
// layer — including cross-source sequences that previously polluted layers.
func TestSkillEnableDisableCommand_RealRegistry(t *testing.T) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	cfg := ctx.Config
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)

	// Real registry: embedded skills + one file skill in a temp dir.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "local-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local-skill\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := skills.NewSkillRegistry([]string{dir})
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	ctx.SkillRegistry = reg

	cmd := &SkillsCommand{}
	// Disable an embedded skill → home config only.
	if err := cmd.Run(ctx, []string{"disable", "refactor"}); err != nil {
		t.Fatalf("disable refactor: %v", err)
	}
	if skillEnabled(cfg, "refactor") {
		t.Error("refactor should be disabled")
	}
	// Disable a file skill → project config only (no home pollution).
	if err := cmd.Run(ctx, []string{"disable", "local-skill"}); err != nil {
		t.Fatalf("disable local-skill: %v", err)
	}
	if skillEnabled(cfg, "local-skill") {
		t.Error("local-skill should be disabled")
	}

	// Simulate the reload ReloadHandler performs: a fresh registry built with
	// the disabled list, so the disabled skills are no longer loaded.
	reloaded := skills.NewSkillRegistry([]string{dir})
	reloaded.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reloaded.SetDisabled(cfg.Skills.Disabled)
	if err := reloaded.LoadAll(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	ctx.SkillRegistry = reloaded
	if _, ok := reloaded.Get("refactor"); ok {
		t.Fatal("refactor should not be loaded after disable")
	}
	if _, ok := reloaded.Get("local-skill"); ok {
		t.Fatal("local-skill should not be loaded after disable")
	}

	// Re-enable both — SourceOf resolves their origin.
	if err := cmd.Run(ctx, []string{"enable", "refactor"}); err != nil {
		t.Fatalf("enable refactor: %v", err)
	}
	if !skillEnabled(cfg, "refactor") {
		t.Error("refactor should be re-enabled")
	}
	if err := cmd.Run(ctx, []string{"enable", "local-skill"}); err != nil {
		t.Fatalf("enable local-skill: %v", err)
	}
	if !skillEnabled(cfg, "local-skill") {
		t.Error("local-skill should be re-enabled")
	}

	// Both config layers must be clean after re-enabling.
	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	if data, err := os.ReadFile(homeCfg); err == nil && strings.Contains(string(data), "local-skill") {
		t.Errorf("home config must not contain local-skill (per-project), got:\n%s", data)
	}
	projectCfg := filepath.Join(projectDir, ".goa", "config.yaml")
	if data, err := os.ReadFile(projectCfg); err == nil && strings.Contains(string(data), "refactor") {
		t.Errorf("project config must not contain refactor (embedded/global), got:\n%s", data)
	}
}

// TestSkillEnableCompletions verifies /skill:enable completes disabled skills
// (from cfg.Skills.Disabled — disabled skills are not loaded in the registry).
func TestSkillEnableCompletions(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{
		Disabled: []string{"telegram", "review"},
	}}
	ctx := skillTestContext(&strings.Builder{})
	ctx.Config = cfg
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
	})

	comps := skillEnableCompletions("enable", "", ctx)
	if len(comps) != 2 {
		t.Fatalf("expected 2 enable completions, got %d: %+v", len(comps), comps)
	}
	for _, c := range comps {
		if c.Value != "enable:telegram" && c.Value != "enable:review" {
			t.Errorf("unexpected enable completion %q", c.Value)
		}
	}

	filtered := skillEnableCompletions("enable", "tele", ctx)
	if len(filtered) != 1 || filtered[0].Value != "enable:telegram" {
		t.Errorf("prefix filter failed, got: %+v", filtered)
	}
}

// TestSkillDisableCompletions verifies /skill:disable completes only enabled
// skills from the registry.
func TestSkillDisableCompletions(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{
		Disabled: []string{"telegram"},
	}}
	ctx := skillTestContext(&strings.Builder{})
	ctx.Config = cfg
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"telegram": embeddedTestSkill("telegram", "Telegram style"),
	})

	comps := skillDisableCompletions("disable", "", ctx)
	if len(comps) != 1 || comps[0].Value != "disable:refactor" {
		t.Fatalf("expected only disable:refactor, got: %+v", comps)
	}
}

// TestSkillSourceForToggle verifies the toggle layer resolution: loaded skills
// report their own source; disabled skills fall back to the registry scan.
func TestSkillSourceForToggle(t *testing.T) {
	ctx := skillTestContext(&strings.Builder{})
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
	})
	if src := skillSourceForToggle(ctx, "refactor"); src != "embedded" {
		t.Errorf("skillSourceForToggle(refactor) = %q, want embedded", src)
	}
	if src := skillSourceForToggle(ctx, "unknown"); src != "" {
		t.Errorf("skillSourceForToggle(unknown) = %q, want empty", src)
	}
}

// TestSetSkillEnabled verifies the in-memory list transitions for toggles,
// including allowlist (Enabled non-empty) semantics.
func TestSetSkillEnabled(t *testing.T) {
	cfg := &config.Config{}
	setSkillEnabled(cfg, "refactor", false)
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	setSkillEnabled(cfg, "refactor", true)
	if stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("enable should remove from Disabled")
	}
	if len(cfg.Skills.Enabled) != 0 {
		t.Errorf("enable without allowlist should not grow Enabled, got %v", cfg.Skills.Enabled)
	}

	// Allowlist mode: enabling adds to the allowlist.
	cfg.Skills.Enabled = []string{"telegram"}
	setSkillEnabled(cfg, "refactor", true)
	if !stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Errorf("enable with allowlist should add to Enabled, got %v", cfg.Skills.Enabled)
	}
	// Disabling removes from the allowlist and adds to Disabled.
	setSkillEnabled(cfg, "refactor", false)
	if stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Error("disable should remove from Enabled")
	}
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
}
