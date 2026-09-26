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
	"github.com/pijalu/goa/internal/event"
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
		if o.Description != "off" {
			t.Errorf("skill %s description = %q, want off (every embedded skill is default-off)", o.Value, o.Description)
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
// rules, and toggling it back removes the entry. Every embedded skill is OFF by
// default, so the first toggle ENABLES it (embedded_enabled opt-in) and the
// second disables it again — no Disabled entry is written, so a later enable
// cannot be shadowed by a stale pin.
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

	// Default state: off (nothing compiled in is active).
	if skillEnabledIn(cfg, "refactor", "embedded", ctx.SkillRegistry) {
		t.Fatal("refactor must be OFF before the first toggle")
	}

	// First toggle: ENABLE via the embedded opt-in, persisted at HOME level.
	sr.onSel("refactor", true)
	if !skillEnabledIn(cfg, "refactor", "embedded", ctx.SkillRegistry) {
		t.Error("refactor should be enabled after the first toggle")
	}
	if !stringInSlice(cfg.Skills.EmbeddedEnabled, "refactor") {
		t.Errorf("cfg.Skills.EmbeddedEnabled should contain refactor, got %v", cfg.Skills.EmbeddedEnabled)
	}
	if stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Errorf("enabling must not write a Disabled entry, got %v", cfg.Skills.Disabled)
	}

	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	data, err := os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	if !strings.Contains(string(data), "refactor") {
		t.Errorf("home config should opt refactor in, got:\n%s", data)
	}

	// Second toggle: DISABLE by dropping the opt-in; the home entry must go.
	sr.onSel("refactor", true)
	if skillEnabledIn(cfg, "refactor", "embedded", ctx.SkillRegistry) {
		t.Error("refactor should be disabled after the second toggle")
	}
	if stringInSlice(cfg.Skills.EmbeddedEnabled, "refactor") {
		t.Errorf("disabling should drop the opt-in, got %v", cfg.Skills.EmbeddedEnabled)
	}
	data, err = os.ReadFile(homeCfg)
	if err != nil {
		t.Fatalf("read home config after disable: %v", err)
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

	// Enable an embedded skill (off by default) → home layer. After a "restart",
	// the same state must be computed from the merged config.
	sr.onSel("embedded", true)
	sr.onSel("refactor", true)
	if got := reload(); !skillEnabledIn(got, "refactor", "embedded", nil) {
		t.Errorf("after enable+reload, refactor should be on (home=%s)", home)
	}
	if got := reload(); !skillEnabledIn(got, "qa-e2e", "local", nil) {
		t.Error("after enable+reload, qa-e2e should still be on (untouched)")
	}

	// Disable it again → the opt-in must be gone after a restart as well.
	menu.settingSkills()
	sr.onSel("embedded", true)
	sr.onSel("refactor", true)
	if got := reload(); skillEnabledIn(got, "refactor", "embedded", nil) {
		t.Errorf("after disable+reload, refactor should be off (opt-in=%v disabled=%v)", got.Skills.EmbeddedEnabled, got.Skills.Disabled)
	}

	// Disable a local skill → project layer; restart; must stay off.
	menu.settingSkills()
	sr.onSel("local", true)
	sr.onSel("qa-e2e", true)
	if data, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.yaml")); err == nil {
		t.Logf("project config after disabling qa-e2e:\n%s", data)
	}
	if got := reload(); skillEnabledIn(got, "qa-e2e", "local", nil) {
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
	if !skillEnabledIn(got, "refactor", "embedded", nil) {
		t.Errorf("after re-enable+reload, refactor should be on (embedded_enabled=%v disabled=%v enabled=%v)", got.Skills.EmbeddedEnabled, got.Skills.Disabled, got.Skills.Enabled)
	}
	if !skillEnabledIn(got, "qa-e2e", "local", nil) {
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
	if got := reload(); skillEnabledIn(got, "review", "embedded", nil) {
		t.Fatal("review should be off under allow-list [refactor]")
	}

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)

	// Disable refactor explicitly, then re-enable it.
	sr.onSel("refactor", true)
	if got := reload(); skillEnabledIn(got, "refactor", "embedded", nil) {
		t.Error("refactor should be off after disable+reload")
	}
	sr.onSel("refactor", true)
	got := reload()
	if !skillEnabledIn(got, "refactor", "embedded", nil) {
		t.Error("refactor should be on after re-enable+reload")
	}
	// The allow-list must be restored: review must still be off. If the
	// round-trip deleted skills.enabled, review flips on — the state loss.
	if skillEnabledIn(got, "review", "embedded", nil) {
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
// toggle the skill and persist the change to the correct config layer. Embedded
// skills are OFF by default, so their round trip is enable (opt-in written) →
// disable (opt-in removed); file skills keep the Disabled-entry semantics.
func TestSkillEnableDisableCommand(t *testing.T) {
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	projectDir := t.TempDir()
	ctx.ConfigSaver = config.NewCascadeLoader(projectDir, "", nil)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{
		"refactor": embeddedTestSkill("refactor", "Refactor code"),
		"qa-e2e":   localTestSkill("qa-e2e", "Run e2e QA"),
	})

	homeCfg := filepath.Join(os.Getenv("HOME"), ".goa", "config.yaml")
	projectCfg := filepath.Join(projectDir, ".goa", "config.yaml")

	// Embedded skill, off by default: enable → HOME opt-in, disable → removed.
	assertSkillToggle(t, ctx, "enable", "refactor", "embedded", true)
	assertFileMentions(t, homeCfg, "refactor", true)

	assertSkillToggle(t, ctx, "disable", "refactor", "embedded", false)
	assertFileMentions(t, homeCfg, "refactor", false)

	// Disable a local skill → project config.
	assertSkillToggle(t, ctx, "disable", "qa-e2e", "local", false)
	assertFileMentions(t, projectCfg, "qa-e2e", true)
}

// assertSkillToggle runs /skill:<verb> <name> and asserts the resulting
// effective enablement for the given source matches want.
func assertSkillToggle(t *testing.T, ctx core.Context, verb, name, source string, want bool) {
	t.Helper()
	cmd := &SkillsCommand{}
	if err := cmd.Run(ctx, []string{verb, name}); err != nil {
		t.Fatalf("%s %s: %v", verb, name, err)
	}
	if got := skillEnabledIn(ctx.Config, name, source, nil); got != want {
		t.Errorf("%s %s: enabled = %v, want %v", verb, name, got, want)
	}
}

// assertFileMentions reads path and requires needle to be present (want=true)
// or absent (want=false).
func assertFileMentions(t *testing.T, path, needle string, want bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	has := strings.Contains(string(data), needle)
	if has != want {
		t.Errorf("%s contains %q = %v, want %v, got:\n%s", path, needle, has, want, data)
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
	// Embedded skills are OFF by default, so they start disabled and this call is
	// the explicit no-op path; local-skill (file) starts on and must be turned off.
	if err := cmd.Run(*ctx, []string{"disable", "local-skill"}); err != nil {
		t.Fatal(err)
	}
	if skillEnabledIn(cfg, "local-skill", "local", nil) {
		t.Error("local-skill should be disabled")
	}
	if err := cmd.Run(*ctx, []string{"disable", "refactor"}); err != nil {
		t.Fatal(err)
	}
	if skillEnabledIn(cfg, "refactor", "embedded", nil) {
		t.Error("refactor should be disabled (embedded skills are off by default)")
	}
	reloaded := skills.NewSkillRegistry([]string{dir})
	reloaded.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reloaded.SetDisabled(cfg.Skills.Disabled)
	reloaded.SetEmbeddedDefaultDisabled(skills.DefaultEmbeddedOffNames(skills.EmbeddedSkillsFS))
	reloaded.SetEmbeddedEnabled(cfg.Skills.EmbeddedEnabled)
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
	// Embedded and file skills are both re-enabled through their own rules:
	// refactor via the embedded opt-in, local-skill by clearing its Disabled entry.
	reEnablingSources := map[string]string{"refactor": "embedded", "local-skill": "local"}
	for _, name := range []string{"refactor", "local-skill"} {
		if err := cmd.Run(*ctx, []string{"enable", name}); err != nil {
			t.Fatal(err)
		}
		if !skillEnabledIn(cfg, name, reEnablingSources[name], nil) {
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
// routing now that ALL embedded skills are default-off: enabling an embedded
// skill opts it in via EmbeddedEnabled WITHOUT touching the global Enabled
// allowlist (which would suppress file skills); disabling drops the opt-in and
// writes no Disabled entry, so a later enable cannot be shadowed by a stale pin.
// Enabling also clears a legacy Disabled entry (configs written under the old
// telegram-default-ON policy).
func TestSetSkillEnabled_EmbeddedRouting(t *testing.T) {
	// Enable a default-off embedded skill: EmbeddedEnabled grows, Enabled stays
	// empty (no global allowlist), Disabled untouched.
	cfg := &config.Config{}
	setSkillEnabled(cfg, "review", true, false, true)
	if !stringInSlice(cfg.Skills.EmbeddedEnabled, "review") {
		t.Errorf("enabling an embedded skill should add to EmbeddedEnabled, got %v", cfg.Skills.EmbeddedEnabled)
	}
	if len(cfg.Skills.Enabled) != 0 {
		t.Errorf("enabling embedded skill must not activate the global allowlist, got %v", cfg.Skills.Enabled)
	}

	// Disable it again: the opt-in is dropped; no Disabled entry is written.
	setSkillEnabled(cfg, "review", false, false, true)
	if stringInSlice(cfg.Skills.EmbeddedEnabled, "review") {
		t.Errorf("disabling an embedded skill should drop the opt-in, got %v", cfg.Skills.EmbeddedEnabled)
	}
	if stringInSlice(cfg.Skills.Disabled, "review") {
		t.Errorf("disabling an embedded skill needs no Disabled entry, got %v", cfg.Skills.Disabled)
	}

	// Telegram is no longer special: disabling it drops the opt-in like any other
	// embedded skill (no Disabled entry), enabling it opts back in and clears a
	// legacy Disabled entry left by the old default-ON policy.
	setSkillEnabled(cfg, "telegram", true, false, true)
	cfg.Skills.Disabled = append(cfg.Skills.Disabled, "telegram") // simulate the legacy pin
	setSkillEnabled(cfg, "telegram", false, false, true)
	if stringInSlice(cfg.Skills.EmbeddedEnabled, "telegram") {
		t.Errorf("disabling telegram should drop the opt-in, got %v", cfg.Skills.EmbeddedEnabled)
	}
	setSkillEnabled(cfg, "telegram", true, false, true)
	if stringInSlice(cfg.Skills.Disabled, "telegram") {
		t.Errorf("re-enabling telegram must clear the legacy Disabled entry, got %v", cfg.Skills.Disabled)
	}
	if !stringInSlice(cfg.Skills.EmbeddedEnabled, "telegram") {
		t.Errorf("re-enabling telegram must opt it in, got %v", cfg.Skills.EmbeddedEnabled)
	}
}

func TestSetSkillEnabled(t *testing.T) {
	cfg := &config.Config{}
	setSkillEnabled(cfg, "refactor", false, false, false)
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	setSkillEnabled(cfg, "refactor", true, false, false)
	if stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("enable should remove from Disabled")
	}
	if len(cfg.Skills.Enabled) != 0 {
		t.Errorf("enable without allowlist should not grow Enabled, got %v", cfg.Skills.Enabled)
	}

	// Allowlist mode: enabling adds to the allowlist.
	cfg.Skills.Enabled = []string{"telegram"}
	setSkillEnabled(cfg, "refactor", true, true, false)
	if !stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Errorf("enable with allowlist should add to Enabled, got %v", cfg.Skills.Enabled)
	}
	// Disabling removes from the allowlist and adds to Disabled.
	setSkillEnabled(cfg, "refactor", false, true, false)
	if stringInSlice(cfg.Skills.Enabled, "refactor") {
		t.Error("disable should remove from Enabled")
	}
	if !stringInSlice(cfg.Skills.Disabled, "refactor") {
		t.Error("disable should add to Disabled")
	}
	// Re-enabling the last allow-listed skill restores membership when the
	// caller knows the allowlist mode is active (from the persisted layer).
	setSkillEnabled(cfg, "refactor", true, true, false)
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

// TestConfigMenu_SkillsShowEmbeddedOffByDefault is the menu-level regression for
// "all embedded skills should be disabled by default": the Skills sub-menu reports
// 0/N on for the embedded source and every embedded entry reads "off", including
// telegram (whose sticky body used to be injected into every session) and dream.
func TestConfigMenu_SkillsShowEmbeddedOffByDefault(t *testing.T) {
	cfg := &config.Config{}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(skills.DefaultEmbeddedOffNames(skills.EmbeddedSkillsFS))
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	ctx.SkillRegistry = reg

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)

	total := len(skills.EmbeddedSkillNames(skills.EmbeddedSkillsFS))
	wantLabel := fmt.Sprintf("0/%d on", total)
	for _, item := range sr.options {
		if item.Value == "embedded" && item.Description != wantLabel {
			t.Errorf("embedded source description = %q, want %q", item.Description, wantLabel)
		}
	}

	sr.onSel("embedded", true)
	if len(sr.options) != total {
		t.Fatalf("embedded list has %d entries, want all %d discoverable", len(sr.options), total)
	}
	seen := map[string]string{}
	for _, o := range sr.options {
		seen[o.Value] = o.Description
		if o.Description != "off" {
			t.Errorf("embedded skill %s = %q, want off by default", o.Value, o.Description)
		}
	}
	for _, want := range []string{"telegram", "dream"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("%s must be listed (so it can be enabled)", want)
		}
	}
}

// TestSkillToggle_ReportsRestartWhenNotApplied: skills are re-scanned by
// ReloadHandler.ReloadSkills; when that hook is missing the running registry keeps
// its old contents, so a toggle that could not take effect must say a restart is
// required instead of flashing success (the user's "if it requires a restart -
// inform the user"). Disabling is the case a static registry cannot honour.
func TestSkillToggle_ReportsRestartWhenNotApplied(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{EmbeddedEnabled: []string{"dream"}}}
	ctx, sr, _, events := newMenuTestContext(t, cfg)
	// The registry still holds dream and has no reload hook, so the disable
	// cannot become live.
	ctx.SkillRegistry = newRegistryKeepingEverySkill(map[string]*skills.Skill{
		"dream": embeddedTestSkill("dream", "Dream consolidation"),
	})

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)
	sr.onSel("dream", true) // seeded as ON → this toggle disables it

	flashes := drainFlashTexts(events)
	if len(flashes) == 0 {
		t.Fatal("toggle must flash an outcome")
	}
	last := flashes[len(flashes)-1]
	if !strings.Contains(last, "restart") {
		t.Errorf("flash = %q, want an explicit restart notice", last)
	}
	if strings.Contains(last, "Skill dream off") {
		t.Errorf("flash must not claim success when nothing changed live: %q", last)
	}
	if !stringInSlice(cfg.Skills.EmbeddedEnabled, "dream") == false {
		// The opt-in must have been dropped in the config even though the live
		// registry still holds the skill: the change applies on restart.
		t.Errorf("config opt-in = %v, want dream removed", cfg.Skills.EmbeddedEnabled)
	}
}

// TestSkillToggle_AppliedInSessionClaimsSuccess is the counterpart: with a reload
// handler that DOES apply the change, the normal flash is kept (no false restart
// notice).
func TestSkillToggle_AppliedInSessionClaimsSuccess(t *testing.T) {
	cfg := &config.Config{Skills: config.SkillsConfig{EmbeddedEnabled: []string{"dream"}}}
	ctx, sr, _, events := newMenuTestContext(t, cfg)
	ctx.SkillRegistry = newToggleableSkillRegistry(map[string]*skills.Skill{
		"dream": embeddedTestSkill("dream", "Dream consolidation"),
	})
	ctx.ReloadHandler = &stubReloadHandler{reg: ctx.SkillRegistry, removing: true}

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("skills", true)
	sr.onSel("embedded", true)
	sr.onSel("dream", true) // seeded as ON → disable, and the reload applies it

	flashes := drainFlashTexts(events)
	if len(flashes) == 0 {
		t.Fatal("toggle must flash an outcome")
	}
	last := flashes[len(flashes)-1]
	if last != "Skill dream off" {
		t.Errorf("flash = %q, want the normal success flash", last)
	}
}

// drainFlashTexts returns the flash texts queued on the chat event bus.
func drainFlashTexts(events *event.Bus) []string {
	var out []string
	for {
		select {
		case ev := <-events.Chat:
			if ev.Flash != nil {
				out = append(out, ev.Flash.Text)
			}
		default:
			return out
		}
	}
}
