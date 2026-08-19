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
	"github.com/pijalu/goa/core"
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
	want := []string{"execution_mode", "embedded", "local", "sticky"}
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

	if skillEnabled(cfg, "refactor", nil) {
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
	if !skillEnabled(cfg, "refactor", nil) {
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

// TestConfigMenu_SkillToggleSurvivesReload reproduces "Skill
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
	if got := reload(); skillEnabled(got, "refactor", nil) {
		t.Errorf("after disable+reload, refactor should be off (home=%s)", home)
	}
	if got := reload(); !skillEnabled(got, "qa-e2e", nil) {
		t.Error("after disable+reload, qa-e2e should still be on (untouched)")
	}

	// Disable a local skill → project layer; restart; must stay off.
	menu.settingSkills()
	sr.onSel("local", true)
	sr.onSel("qa-e2e", true)
	if data, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.yaml")); err == nil {
		t.Logf("project config after disabling qa-e2e:\n%s", data)
	}
	if got := reload(); skillEnabled(got, "qa-e2e", nil) {
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
	if !skillEnabled(got, "refactor", nil) {
		t.Errorf("after re-enable+reload, refactor should be on (disabled=%v enabled=%v)", got.Skills.Disabled, got.Skills.Enabled)
	}
	if !skillEnabled(got, "qa-e2e", nil) {
		t.Errorf("after re-enable+reload, qa-e2e should be on (disabled=%v enabled=%v)", got.Skills.Disabled, got.Skills.Enabled)
	}
}

// TestConfigMenu_SkillAllowListSurvivesDisableReenable reproduces the
// unstable-across-sessions report for allow-list mode: with skills.enabled
// set to a single skill, disabling then re-enabling it must restore the
// allow-list — otherwise the merged config flips from "only this skill" to
// "all skills on" (enabled/disabled state is lost/unstable).
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
	if got := reload(); skillEnabled(got, "review", nil) {
		t.Fatal("review should be off under allow-list [refactor]")
	}

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)

	// Disable refactor explicitly, then re-enable it.
	sr.onSel("refactor", true)
	if got := reload(); skillEnabled(got, "refactor", nil) {
		t.Error("refactor should be off after disable+reload")
	}
	sr.onSel("refactor", true)
	got := reload()
	if !skillEnabled(got, "refactor", nil) {
		t.Error("refactor should be on after re-enable+reload")
	}
	// The allow-list must be restored: review must still be off. If the
	// round-trip deleted skills.enabled, review flips on — the state loss.
	if skillEnabled(got, "review", nil) {
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

	if skillEnabled(cfg, "qa-e2e", nil) {
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
	if skillEnabled(cfg, "refactor", nil) {
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
	if !skillEnabled(cfg, "refactor", nil) {
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
	if skillEnabled(cfg, "qa-e2e", nil) {
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

// knowledgeTestSkill returns a knowledge-category test skill (sticky applies
// only to knowledge skills).
func knowledgeTestSkill(name, desc string) *skills.Skill {
	s := embeddedTestSkill(name, desc)
	s.Meta.Category = skills.SkillCategoryKnowledge
	return s
}

// TestSkillStickyToggleCommand verifies /skill:sticky flips the always-on
// state and persists it at PROJECT level (skills.sticky / skills.sticky_off),
// including the minimal-entry rule for frontmatter-sticky skills.
func TestSkillStickyToggleCommand(t *testing.T) {
	t.Run("plain skill", testStickyPlainSkill)
	t.Run("frontmatter skill", testStickyFrontmatterSkill)
	t.Run("invalid skills", testStickyInvalidSkills)
}

func testStickyContext(t *testing.T) (*core.Context, *SkillsCommand, *strings.Builder, string) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"plain-k": knowledgeTestSkill("plain-k", "Plain knowledge"),
		"always-k": func() *skills.Skill {
			s := knowledgeTestSkill("always-k", "Frontmatter sticky")
			s.Meta.Sticky = true
			return s
		}(),
		"refactor": embeddedTestSkill("refactor", "Action skill"),
	})
	return &ctx, &SkillsCommand{}, &buf, filepath.Join(projectDir, ".goa", "config.yaml")
}

func testStickyPlainSkill(t *testing.T) {
	ctx, cmd, _, projectCfg := testStickyContext(t)
	if err := cmd.Run(*ctx, []string{"sticky", "plain-k"}); err != nil {
		t.Fatal(err)
	}
	if !stringInSlice(ctx.Config.Skills.Sticky, "plain-k") || !skillStickyEffective(*ctx, "plain-k") {
		t.Error("plain-k should be sticky")
	}
	data, err := os.ReadFile(projectCfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sticky") || !strings.Contains(string(data), "plain-k") {
		t.Errorf("sticky config missing: %s", data)
	}
	if err := cmd.Run(*ctx, []string{"sticky", "plain-k"}); err != nil {
		t.Fatal(err)
	}
	if stringInSlice(ctx.Config.Skills.Sticky, "plain-k") {
		t.Error("plain-k should be off")
	}
	data, err = os.ReadFile(projectCfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "plain-k") {
		t.Errorf("plain-k remains in config: %s", data)
	}
}

func testStickyFrontmatterSkill(t *testing.T) {
	ctx, cmd, _, projectCfg := testStickyContext(t)
	if err := cmd.Run(*ctx, []string{"sticky", "always-k"}); err != nil {
		t.Fatal(err)
	}
	if !stringInSlice(ctx.Config.Skills.StickyOff, "always-k") || skillStickyEffective(*ctx, "always-k") {
		t.Error("always-k should be disabled")
	}
	data, err := os.ReadFile(projectCfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sticky_off") || !strings.Contains(string(data), "always-k") {
		t.Errorf("sticky_off config missing: %s", data)
	}
}

func testStickyInvalidSkills(t *testing.T) {
	ctx, cmd, buf, _ := testStickyContext(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"sticky", "refactor"}, "knowledge"}, {[]string{"sticky", "nope"}, "not found"}, {[]string{"sticky"}, "usage:"},
	} {
		buf.Reset()
		if err := cmd.Run(*ctx, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %v: error %v", tc.args, err)
		}
	}
}

// TestBuildStickyToggleItems verifies the /config sticky toggle list only
// offers knowledge skills and reports the effective sticky state.
func TestBuildStickyToggleItems(t *testing.T) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	ctx.Config.Skills.Sticky = []string{"plain-k"}
	plain := knowledgeTestSkill("plain-k", "P")
	other := knowledgeTestSkill("other-k", "O")
	action := embeddedTestSkill("refactor", "Action")
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"plain-k":  plain,
		"other-k":  other,
		"refactor": action,
	})

	items := buildStickyToggleItems(ctx)
	if len(items) != 2 {
		t.Fatalf("expected 2 knowledge items, got %d: %+v", len(items), items)
	}
	byName := map[string]string{}
	for _, it := range items {
		byName[it.Value] = it.Description
	}
	if byName["plain-k"] != "on" {
		t.Errorf("plain-k description = %q, want on", byName["plain-k"])
	}
	if byName["other-k"] != "off" {
		t.Errorf("other-k description = %q, want off", byName["other-k"])
	}
	if _, ok := byName["refactor"]; ok {
		t.Error("action skill must not appear in sticky toggle list")
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
	ctx, cfg, cmd, dir := realRegistrySkillContext(t)
	disableRealSkills(t, ctx, cfg, cmd, dir)
	enableRealSkills(t, ctx, cfg, cmd, dir)
}

func realRegistrySkillContext(t *testing.T) (*core.Context, *config.Config, *SkillsCommand, string) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
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
		t.Fatal(err)
	}
	ctx.SkillRegistry = reg
	return &ctx, ctx.Config, &SkillsCommand{}, dir
}

func disableRealSkills(t *testing.T, ctx *core.Context, cfg *config.Config, cmd *SkillsCommand, dir string) {
	if err := cmd.Run(*ctx, []string{"disable", "refactor"}); err != nil {
		t.Fatal(err)
	}
	if skillEnabled(cfg, "refactor", nil) {
		t.Error("refactor should be disabled")
	}
	if err := cmd.Run(*ctx, []string{"disable", "local-skill"}); err != nil {
		t.Fatal(err)
	}
	if skillEnabled(cfg, "local-skill", nil) {
		t.Error("local-skill should be disabled")
	}
	reloaded := skills.NewSkillRegistry([]string{dir})
	reloaded.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reloaded.SetDisabled(cfg.Skills.Disabled)
	if err := reloaded.LoadAll(); err != nil {
		t.Fatal(err)
	}
	ctx.SkillRegistry = reloaded
	for _, name := range []string{"refactor", "local-skill"} {
		if _, ok := reloaded.Get(name); ok {
			t.Fatalf("%s should not be loaded after disable", name)
		}
	}
}

func enableRealSkills(t *testing.T, ctx *core.Context, cfg *config.Config, cmd *SkillsCommand, dir string) {
	for _, name := range []string{"refactor", "local-skill"} {
		if err := cmd.Run(*ctx, []string{"enable", name}); err != nil {
			t.Fatal(err)
		}
		if !skillEnabled(cfg, name, nil) {
			t.Errorf("%s should be re-enabled", name)
		}
	}
	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	if data, err := os.ReadFile(homeCfg); err == nil && strings.Contains(string(data), "local-skill") {
		t.Error("home config must not contain local-skill")
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
// TestSetSkillEnabled_EmbeddedRouting verifies the embedded-scoped toggle
// routing (embedded default-off except telegram): enabling a
// default-off embedded skill opts it in via EmbeddedEnabled WITHOUT touching
// the global Enabled allowlist; disabling removes the opt-in; disabling the
// default-ON telegram writes an explicit Disabled entry.
func TestSetSkillEnabled_EmbeddedRouting(t *testing.T) {
	// Enable a default-off embedded skill: EmbeddedEnabled grows, Enabled stays
	// empty (no global allowlist), Disabled untouched.
	cfg := &config.Config{}
	setSkillEnabled(cfg, "review", true, false, true, true)
	if !stringInSlice(cfg.Skills.EmbeddedEnabled, "review") {
		t.Errorf("enabling default-off embedded skill should add to EmbeddedEnabled, got %v", cfg.Skills.EmbeddedEnabled)
	}
	if len(cfg.Skills.Enabled) != 0 {
		t.Errorf("enabling embedded skill must not activate the global allowlist, got %v", cfg.Skills.Enabled)
	}

	// Disable it again: the opt-in is dropped; no Disabled entry needed.
	setSkillEnabled(cfg, "review", false, false, true, true)
	if stringInSlice(cfg.Skills.EmbeddedEnabled, "review") {
		t.Errorf("disabling default-off embedded skill should drop the opt-in, got %v", cfg.Skills.EmbeddedEnabled)
	}
	if stringInSlice(cfg.Skills.Disabled, "review") {
		t.Errorf("disabling a default-off embedded skill needs no Disabled entry, got %v", cfg.Skills.Disabled)
	}

	// Disable the default-ON telegram: an explicit Disabled entry is required.
	setSkillEnabled(cfg, "telegram", false, false, true, false)
	if !stringInSlice(cfg.Skills.Disabled, "telegram") {
		t.Errorf("disabling default-ON telegram should add to Disabled, got %v", cfg.Skills.Disabled)
	}
	// Re-enable telegram: the Disabled entry is removed and it is opted in.
	setSkillEnabled(cfg, "telegram", true, false, true, false)
	if stringInSlice(cfg.Skills.Disabled, "telegram") {
		t.Error("re-enabling telegram should remove it from Disabled")
	}
}

func TestSetSkillEnabled(t *testing.T) {
	cfg := &config.Config{}
	setSkillEnabled(cfg, "refactor", false, false, false, false)
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	setSkillEnabled(cfg, "refactor", true, false, false, false)
	if stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("enable should remove from Disabled")
	}
	if len(cfg.Skills.Enabled) != 0 {
		t.Errorf("enable without allowlist should not grow Enabled, got %v", cfg.Skills.Enabled)
	}

	// Allowlist mode: enabling adds to the allowlist.
	cfg.Skills.Enabled = []string{"telegram"}
	setSkillEnabled(cfg, "refactor", true, true, false, false)
	if !stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Errorf("enable with allowlist should add to Enabled, got %v", cfg.Skills.Enabled)
	}
	// Disabling removes from the allowlist and adds to Disabled.
	setSkillEnabled(cfg, "refactor", false, true, false, false)
	if stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Error("disable should remove from Enabled")
	}
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	// Re-enabling the last allow-listed skill restores membership when the
	// caller knows the allowlist mode is active (from the persisted layer).
	setSkillEnabled(cfg, "refactor", true, true, false, false)
	if !stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Errorf("re-enable with active allowlist should restore Enabled, got %v", cfg.Skills.Enabled)
	}
}

// TestSkillToggle_CrossSessionConsistency is the regression test for
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
		inMem := skillEnabled(cfg, name, nil)
		disk := skillEnabled(fresh, name, nil)
		if inMem != disk {
			diverged = append(diverged, fmt.Sprintf("%s(in-mem=%v,disk=%v)", name, inMem, disk))
		}
	}
	if len(diverged) > 0 {
		t.Errorf("skill decisions diverge across sessions: %s\n  in-mem enabled=%v disabled=%v\n  fresh  enabled=%v disabled=%v",
			strings.Join(diverged, ", "), cfg.Skills.Enabled, cfg.Skills.Disabled, fresh.Skills.Enabled, fresh.Skills.Disabled)
	}
}
