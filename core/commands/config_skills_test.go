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

// TestConfigMenu_SkillToggleSurvivesReload reproduces bugs.md "Skill
// enable/disable state is lost / unstable across sessions": a toggle must
// round-trip through the cascade — persist, then a fresh load (simulated
// restart) must reflect the same enabled state.
func TestConfigMenu_SkillToggleSurvivesReload(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"qa-e2e":   localTestSkill("qa-e2e", "Run e2e QA"),
	})
	home := os.Getenv("HOME")

	reload := func() *config.Config {
		t.Helper()
		reloaded, err := config.NewCascadeLoader(projectDir, "", nil).Load()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return reloaded
	}

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)

	// Disable an embedded skill → home layer. After a "restart", the same
	// state must be computed from the merged config.
	sr.onSel("embedded", true)
	sr.onSel("refactor", true)
	if got := reload(); skillEnabled(got, "refactor") {
		t.Errorf("after disable+reload, refactor should be off (home=%s)", home)
	}
	if got := reload(); !skillEnabled(got, "qa-e2e") {
		t.Error("after disable+reload, qa-e2e should still be on (untouched)")
	}

	// Disable a local skill → project layer; restart; must stay off.
	menu.settingSkills()
	sr.onSel("local", true)
	sr.onSel("qa-e2e", true)
	if data, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.yaml")); err == nil {
		t.Logf("project config after disabling qa-e2e:\n%s", data)
	}
	if got := reload(); skillEnabled(got, "qa-e2e") {
		t.Error("after disable+reload, qa-e2e should be off")
	}

	// Re-enable both; restart; both must be on again and neither list may
	// resurrect them as disabled.
	menu.settingSkills()
	sr.onSel("embedded", true)
	sr.onSel("refactor", true)
	menu.settingSkills()
	sr.onSel("local", true)
	sr.onSel("qa-e2e", true)
	got := reload()
	if !skillEnabled(got, "refactor") {
		t.Errorf("after re-enable+reload, refactor should be on (disabled=%v enabled=%v)", got.Skills.Disabled, got.Skills.Enabled)
	}
	if !skillEnabled(got, "qa-e2e") {
		t.Errorf("after re-enable+reload, qa-e2e should be on (disabled=%v enabled=%v)", got.Skills.Disabled, got.Skills.Enabled)
	}
}

// TestConfigMenu_SkillAllowListSurvivesDisableReenable reproduces the
// unstable-across-sessions report for allow-list mode: with skills.enabled
// set to a single skill, disabling then re-enabling it must restore the
// allow-list — otherwise the merged config flips from "only this skill" to
// "all skills on" (bugs.md: enabled/disabled state is lost/unstable).
func TestConfigMenu_SkillAllowListSurvivesDisableReenable(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{Enabled: []string{"refactor"}}}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	// Seed the home config with the allow-list so a reload reproduces the
	// in-memory starting state (the user's pre-existing configuration).
	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	writeTestConfig(t, homeCfg, "skills:\n  enabled:\n    - refactor\n")
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"review":   embeddedTestSkill("review", "Review code"),
	})

	reload := func() *config.Config {
		t.Helper()
		reloaded, err := config.NewCascadeLoader(projectDir, "", nil).Load()
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return reloaded
	}

	// Initially: allow-list active — review is implicitly off.
	if got := reload(); skillEnabled(got, "review") {
		t.Fatal("review should be off under allow-list [refactor]")
	}

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)

	// Disable refactor explicitly, then re-enable it.
	sr.onSel("refactor", true)
	if got := reload(); skillEnabled(got, "refactor") {
		t.Error("refactor should be off after disable+reload")
	}
	sr.onSel("refactor", true)
	got := reload()
	if !skillEnabled(got, "refactor") {
		t.Error("refactor should be on after re-enable+reload")
	}
	// The allow-list must be restored: review must still be off. If the
	// round-trip deleted skills.enabled, review flips on — the state loss.
	if skillEnabled(got, "review") {
		t.Errorf("review flipped on after disable/re-enable round trip; allow-list was lost (enabled=%v disabled=%v)",
			got.Skills.Enabled, got.Skills.Disabled)
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
	setSkillEnabled(cfg, "refactor", false, false)
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	setSkillEnabled(cfg, "refactor", true, false)
	if stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("enable should remove from Disabled")
	}
	if len(cfg.Skills.Enabled) != 0 {
		t.Errorf("enable without allowlist should not grow Enabled, got %v", cfg.Skills.Enabled)
	}

	// Allowlist mode: enabling adds to the allowlist.
	cfg.Skills.Enabled = []string{"telegram"}
	setSkillEnabled(cfg, "refactor", true, true)
	if !stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Errorf("enable with allowlist should add to Enabled, got %v", cfg.Skills.Enabled)
	}
	// Disabling removes from the allowlist and adds to Disabled.
	setSkillEnabled(cfg, "refactor", false, true)
	if stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Error("disable should remove from Enabled")
	}
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	// Re-enabling the last allow-listed skill restores membership when the
	// caller knows the allowlist mode is active (from the persisted layer).
	setSkillEnabled(cfg, "refactor", true, true)
	if !stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Errorf("re-enable with active allowlist should restore Enabled, got %v", cfg.Skills.Enabled)
	}
}

// TestSkillToggle_CrossSessionConsistency is the regression test for bugs.md
// must-fix #5 (skills enable/disable inconsistent across sessions): after any
// sequence of toggles in the running session (which mutate the in-memory config
// and persist per-source partitions), a FRESH session — built from a clean
// cascade load of the same config files — must compute identical skill
// on/off decisions for every skill. ReloadSkills() re-authorizes from disk, so
// the running session and a parallel session can never diverge.
func TestSkillToggle_CrossSessionConsistency(t *testing.T) {
	// HOME must be set BEFORE any CascadeLoader is created: the loader resolves
	// and caches the home dir at construction time via internal.GoaHome(), so a
	// later t.Setenv would leave the loader pointing at the real user home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	projectDir := t.TempDir()

	var buf strings.Builder
	ctx := skillTestContext(&buf)
	cfg := ctx.Config
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)

	// Real registry: several embedded skills + a file skill.
	dir := t.TempDir()
	for _, n := range []string{"qa-e2e", "go-debug"} {
		sd := filepath.Join(dir, n)
		os.MkdirAll(sd, 0o755)
		os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte("---\nname: "+n+"\n---\nbody"), 0o644)
	}
	reg := skills.NewSkillRegistry([]string{dir})
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatal(err)
	}
	ctx.SkillRegistry = reg

	// Seed an allowlist in BOTH layers so toggle logic operates in allowlist
	// mode (the configuration shape that produced the 4-vs-13 divergence).
	writeTestConfig(t, filepath.Join(home, ".goa", "config.yaml"),
		"skills:\n  enabled:\n    - refactor\n    - review\n")
	writeTestConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"),
		"skills:\n  enabled:\n    - qa-e2e\n")

	loaded, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Skills = loaded.Skills

	cmd := &SkillsCommand{}
	// Exercise a mix of toggles across both sources.
	for _, op := range []string{"disable review", "enable telegram", "disable qa-e2e", "enable review"} {
		if err := cmd.Run(ctx, strings.Fields(op)); err != nil {
			t.Fatalf("%q: %v", op, err)
		}
	}

	// Probe: every skill the registry can discover must agree between the
	// running (in-memory) session and a fresh (disk) load.
	probe := []string{"refactor", "review", "telegram", "qa-e2e", "go-debug", "debug", "document"}
	fresh, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatal(err)
	}
	var diverged []string
	for _, name := range probe {
		inMem := skillEnabled(cfg, name)
		disk := skillEnabled(fresh, name)
		if inMem != disk {
			diverged = append(diverged, fmt.Sprintf("%s(in-mem=%v,disk=%v)", name, inMem, disk))
		}
	}
	if len(diverged) > 0 {
		t.Errorf("skill decisions diverge across sessions: %s\n  in-mem enabled=%v disabled=%v\n  fresh  enabled=%v disabled=%v",
			strings.Join(diverged, ", "), cfg.Skills.Enabled, cfg.Skills.Disabled, fresh.Skills.Enabled, fresh.Skills.Disabled)
	}
}
