// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseSkillBasic verifies parsing a simple SKILL.md with frontmatter.
func TestParseSkillBasic(t *testing.T) {
	content := `---
name: test-skill
description: A test skill
inline: false
mode: coder
temperature: 0.1
---

# Test Skill

This is the skill body.`
	skill := parseSkill("test-skill", content, "embedded", "skills/test-skill/SKILL.md")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if skill.FilePath != "skills/test-skill/SKILL.md" {
		t.Errorf("FilePath = %q, want populated path", skill.FilePath)
	}
	if skill.Meta.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", skill.Meta.Name, "test-skill")
	}
	if skill.Meta.Description != "A test skill" {
		t.Errorf("Description = %q, want %q", skill.Meta.Description, "A test skill")
	}
	if skill.Meta.Inline {
		t.Error("Inline should be false")
	}
	if skill.Meta.Mode != "coder" {
		t.Errorf("Mode = %q, want %q", skill.Meta.Mode, "coder")
	}
	if skill.Meta.Temperature != 0.1 {
		t.Errorf("Temperature = %f, want 0.1", skill.Meta.Temperature)
	}
	if skill.Body != "# Test Skill\n\nThis is the skill body." {
		t.Errorf("Body = %q", skill.Body)
	}
}

// TestParseSkillInline verifies inline skill parsing.
func TestParseSkillInline(t *testing.T) {
	content := `---
name: inline-skill
description: An inline skill
inline: true
---

Inline skill instructions here.`
	skill := parseSkill("inline-skill", content, "embedded", "")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if !skill.Meta.Inline {
		t.Error("Inline should be true")
	}
}

// TestParseSkillNoFrontmatter verifies skills without frontmatter still work.
func TestParseSkillNoFrontmatter(t *testing.T) {
	content := `# Skill without frontmatter`
	skill := parseSkill("bare", content, "embedded", "")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if skill.Meta.Name != "bare" {
		t.Errorf("Name = %q, want %q", skill.Meta.Name, "bare")
	}
	if skill.Body != "# Skill without frontmatter" {
		t.Errorf("Body = %q", skill.Body)
	}
}

// TestParseSkillEmptyContent verifies empty content handling.
func TestParseSkillEmptyContent(t *testing.T) {
	skill := parseSkill("empty", "", "embedded", "")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if skill.Body != "" {
		t.Errorf("Body = %q, want empty", skill.Body)
	}
}

// TestSkillRegistryGetAndList verifies basic registry operations.
func TestSkillRegistryGetAndList(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Get existing skill
	skill, ok := reg.Get("refactor")
	if !ok {
		t.Fatal("Get('refactor') should succeed")
	}
	if skill.Meta.Name != "refactor" {
		t.Errorf("Name = %q, want %q", skill.Meta.Name, "refactor")
	}

	// Get non-existent skill
	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("Get('nonexistent') should return false")
	}

	// List all skills — embedded discovery walks */SKILL.md
	summaries := reg.List()
	if len(summaries) < 7 {
		t.Fatalf("List = %d, want at least 7 embedded skills", len(summaries))
	}
}

// TestSkillRegistrySourceOf verifies SourceOf resolves the origin of a skill
// even when the skill is disabled (not loaded but still discoverable on disk).
func TestSkillRegistrySourceOf(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatal(err)
	}
	assertSkillSource(t, reg, "refactor", "embedded", true)
	assertSkillSource(t, reg, "nonexistent", "", false)
	dir := writeLocalSkill(t)
	reg2 := loadedRegistry(t, dir, nil)
	assertSkillSource(t, reg2, "local-skill", "file", true)
	reg3 := loadedRegistry(t, dir, []string{"local-skill"})
	if _, ok := reg3.Get("local-skill"); ok {
		t.Fatal("disabled skill should not be loaded")
	}
	assertSkillSource(t, reg3, "local-skill", "file", true)
	assertSkillSource(t, reg3, "refactor", "embedded", true)
}

func assertSkillSource(t *testing.T, reg *SkillRegistry, name, want string, wantOK bool) {
	if got, ok := reg.SourceOf(name); ok != wantOK || got != want {
		t.Errorf("SourceOf(%s) = %q, %v; want %q, %v", name, got, ok, want, wantOK)
	}
}

func writeLocalSkill(t *testing.T) string {
	dir := t.TempDir()
	path := filepath.Join(dir, "local-skill")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: local-skill\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func loadedRegistry(t *testing.T, dir string, disabled []string) *SkillRegistry {
	reg := NewSkillRegistry([]string{dir})
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetDisabled(disabled)
	if err := reg.LoadAll(); err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestSkillRegistryListSource verifies List summaries carry the skill origin
// so callers can separate embedded (global) from file-based (local) skills.
func TestSkillRegistryListSource(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	for _, s := range reg.List() {
		if s.Source != "embedded" {
			t.Errorf("skill %s Source = %q, want embedded", s.Name, s.Source)
		}
	}
}

// TestSkillRegistrySetDisabled verifies skills can be disabled via
// configuration for ALL sources: disabled built-ins and disabled file-based
// skills are never registered (prompt listing, banner, <available_skills>
// catalog all read from this registry). A disabled built-in can no longer be
// shadowed by a same-named file skill — the explicit off wins.
func TestSkillRegistrySetDisabled(t *testing.T) {
	// Disabled embedded skill is gone.
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetDisabled([]string{"refactor", "telegram"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	for _, name := range []string{"refactor", "telegram"} {
		if _, ok := reg.Get(name); ok {
			t.Errorf("Get(%q) should fail — skill is disabled", name)
		}
	}
	if _, ok := reg.Get("review"); !ok {
		t.Error("Get('review') should succeed — not disabled")
	}

	// A file-based skill with the same name as a disabled built-in is also
	// disabled (disabled now gates file-based sources too); a differently-named
	// file skill still loads.
	dir := t.TempDir()
	for _, name := range []string{"refactor", "test-gen"} {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: user override\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg2 := NewSkillRegistry([]string{dir})
	reg2.SetEmbeddedFS(EmbeddedSkillsFS)
	reg2.SetDisabled([]string{"refactor"})
	if err := reg2.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg2.Get("refactor"); ok {
		t.Error("file-based 'refactor' should NOT load — disabled now gates file-based skills")
	}
	s, ok := reg2.Get("test-gen")
	if !ok {
		t.Fatal("file-based 'test-gen' should load — not disabled")
	}
	if s.Meta.Description != "user override" {
		t.Errorf("Description = %q, want user override", s.Meta.Description)
	}
}

// TestSkillRegistrySetEnabled verifies the allowlist selects which skills load
// from ALL sources: only listed skills are registered, unlisted embedded and
// file-based skills are skipped.
func TestSkillRegistrySetEnabled(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"refactor", "test-gen"} {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: file " + name + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reg := NewSkillRegistry([]string{dir})
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEnabled([]string{"review", "refactor"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// Listed embedded skill loads.
	if _, ok := reg.Get("review"); !ok {
		t.Error("Get('review') should succeed — listed in allowlist")
	}
	// Listed file-based skill loads (file-based 'refactor' wins the name).
	if s, ok := reg.Get("refactor"); !ok || s.Meta.Description != "file refactor" {
		t.Errorf("Get('refactor') = %+v, want the file-based copy", s)
	}
	// Unlisted embedded + file-based skills are excluded.
	if _, ok := reg.Get("telegram"); ok {
		t.Error("Get('telegram') should fail — not in allowlist")
	}
	if _, ok := reg.Get("test-gen"); ok {
		t.Error("Get('test-gen') should fail — not in allowlist")
	}
}

// TestSkillRegistryEnabledDisabledConflict pins the precedence: a name in both
// lists is disabled (explicit off wins).
func TestSkillRegistryEnabledDisabledConflict(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEnabled([]string{"refactor", "review"})
	reg.SetDisabled([]string{"refactor"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("refactor"); ok {
		t.Error("Get('refactor') should fail — in both lists, disabled wins")
	}
	if _, ok := reg.Get("review"); !ok {
		t.Error("Get('review') should succeed — in allowlist, not disabled")
	}
}

// TestSkillRegistryIsInline verifies inline skill detection.
func TestSkillRegistryIsInline(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if reg.IsInline("telegram") != true {
		t.Error("IsInline('telegram') should be true")
	}
	if reg.IsInline("refactor") != false {
		t.Error("IsInline('refactor') should be false")
	}
	if reg.IsInline("nonexistent") != false {
		t.Error("IsInline('nonexistent') should be false")
	}
}

// TestSkillRegistryEmbeddedDiscovery verifies built-in skills are discovered
// from the embedded filesystem by directory walk.
func TestSkillRegistryEmbeddedDiscovery(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	expected := []string{"refactor", "test-gen", "document", "review", "explain", "commit-msg", "debug", "telegram"}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("Missing embedded skill: %s", name)
		}
	}
	telegram, ok := reg.Get("telegram")
	if !ok {
		t.Fatal("telegram skill not found")
	}
	if !telegram.Meta.Inline {
		t.Errorf("telegram should be inline")
	}
	if telegram.Meta.Command != "telegram" {
		t.Errorf("telegram command = %q, want 'telegram'", telegram.Meta.Command)
	}
}

func TestSkillLinkedMode(t *testing.T) {
	skill := &Skill{
		Meta: SkillMeta{
			Name: "reviewer",
			Mode: "reviewer",
		},
	}
	if linked := skill.LinkedMode(); linked != "reviewer" {
		t.Errorf("LinkedMode() = %q, want %q", linked, "reviewer")
	}
}

// TestEmbeddedSkillDescriptionCeiling is a build-time context guard: embedded
// skill descriptions are listed in <available_skills> in every system prompt,
// so each must stay ≤ 200 chars. Covers only embedded skills — user/project
// skills are theirs to size (goa never refuses to run).
func TestEmbeddedSkillDescriptionCeiling(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	const ceiling = 200
	for _, s := range reg.List() {
		if len(s.Description) > ceiling {
			t.Errorf("embedded skill %q description = %d chars, ceiling %d — listed in every system prompt; keep it terse",
				s.Name, len(s.Description), ceiling)
		}
	}
}

func TestSkillSuggestedSkills(t *testing.T) {
	skill := &Skill{
		Meta: SkillMeta{
			Name:   "review",
			Skills: []string{"lint", "document"},
		},
	}
	skills := skill.SuggestedSkills()
	if len(skills) != 2 || skills[0] != "lint" {
		t.Errorf("SuggestedSkills() = %v, want [lint document]", skills)
	}
}

// TestSkillRegistrySubSkillsFromDirectory verifies that skills in a skills/
// subdirectory are loaded as sub-skills for the parent skill.
func TestSkillRegistrySubSkillsFromDirectory(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	parentBody := `---
name: parent
description: Parent skill
---
# Parent
`
	if err := os.WriteFile(filepath.Join(parentDir, "SKILL.md"), []byte(parentBody), 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}

	subDir := filepath.Join(parentDir, "skills", "child")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	childBody := `---
name: child
description: Child skill
---
# Child
`
	if err := os.WriteFile(filepath.Join(subDir, "SKILL.md"), []byte(childBody), 0644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if !reg.HasSubSkills("parent") {
		t.Error("expected parent to have sub-skills")
	}
	subs := reg.SubSkills("parent")
	if len(subs) != 1 || subs[0].Meta.Name != "child" {
		t.Errorf("SubSkills(parent) = %v, want [child]", subs)
	}
	for _, s := range reg.List() {
		if s.Name == "child" {
			t.Error("sub-skill should not appear in main skill list")
		}
	}
}

// TestSkillRegistryImportedSkills verifies that skills listed in the skills
// frontmatter are returned as imported skills.
func TestSkillRegistryImportedSkills(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"parent", "helper"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n", name, name, name)
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	parentPath := filepath.Join(dir, "parent", "SKILL.md")
	parentBody := `---
name: parent
description: Parent
skills: [helper]
---
# Parent
`
	if err := os.WriteFile(parentPath, []byte(parentBody), 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}

	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	imports := reg.ImportedSkills("parent")
	if len(imports) != 1 || imports[0].Meta.Name != "helper" {
		t.Errorf("ImportedSkills(parent) = %v, want [helper]", imports)
	}
}

// TestSkillRegistryRequiresSubAgentForSubSkills verifies that a skill with
// sub-skills is marked as requiring sub-agent execution.
func TestSkillRegistryRequiresSubAgentForSubSkills(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "SKILL.md"), []byte("---\nname: parent\ndescription: Parent\n---\n"), 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	childDir := filepath.Join(parentDir, "skills", "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "SKILL.md"), []byte("---\nname: child\ndescription: Child\n---\n"), 0644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	for _, s := range reg.List() {
		if s.Name == "parent" && !s.RequiresSubAgent {
			t.Error("expected parent to require sub-agent")
		}
	}
}

// TestParseSkillSticky verifies the sticky frontmatter flag parses.
func TestParseSkillSticky(t *testing.T) {
	content := `---
name: sticky-skill
description: A sticky knowledge skill
inline: true
category: knowledge
sticky: true
---

Always-on instructions.`
	skill := parseSkill("sticky-skill", content, "embedded", "")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if !skill.Meta.Sticky {
		t.Error("Sticky should be true")
	}
}

// TestParseSkillStickyDefaultFalse verifies sticky defaults to false.
func TestParseSkillStickyDefaultFalse(t *testing.T) {
	skill := parseSkill("plain", "---\nname: plain\ndescription: Plain\n---\nbody", "embedded", "")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if skill.Meta.Sticky {
		t.Error("Sticky should default to false")
	}
}

// TestSkillRegistryStickyOverrides verifies config-level sticky overrides
// (skills.sticky force-on, skills.sticky_off force-off) applied at load:
//   - sticky_off wins over sticky and over frontmatter (explicit off wins),
//   - sticky turns a plain knowledge skill sticky,
//   - FrontmatterSticky still reports the pristine frontmatter value so
//     toggles can decide which list to write,
//   - SkillSummary.Sticky reflects the effective state.
func TestSkillRegistryStickyOverrides(t *testing.T) {
	dir := t.TempDir()
	writeTestSkills(t, dir)
	reg := NewSkillRegistry([]string{dir})
	reg.SetStickyOverrides([]string{"plain-k", "off-wins"}, []string{"forced-off", "off-wins"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	assertStickyNames(t, reg)
	assertFrontmatterSticky(t, reg)
	assertListedSticky(t, reg)
}

func writeTestSkills(t *testing.T, dir string) {
	skills := map[string]string{
		"plain-k": "description: P\ncategory: knowledge", "fm-sticky": "description: F\ncategory: knowledge\nsticky: true",
		"forced-off": "description: O\ncategory: knowledge\nsticky: true", "off-wins": "description: W\ncategory: knowledge", "plain-action": "description: A2\ncategory: action",
	}
	for name, body := range skills {
		sd := filepath.Join(dir, name)
		if err := os.MkdirAll(sd, 0755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("---\nname: %s\n%s\n---\nbody", name, body)
		if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertStickyNames(t *testing.T, reg *SkillRegistry) {
	names := map[string]bool{}
	for _, skill := range reg.StickySkills() {
		names[skill.Meta.Name] = true
	}
	for _, name := range []string{"plain-k", "fm-sticky"} {
		if !names[name] {
			t.Errorf("%s should be sticky", name)
		}
	}
	for _, name := range []string{"forced-off", "off-wins", "plain-action"} {
		if names[name] {
			t.Errorf("%s should not be sticky", name)
		}
	}
}

func assertFrontmatterSticky(t *testing.T, reg *SkillRegistry) {
	for _, tc := range []struct {
		name string
		want bool
	}{{"forced-off", true}, {"plain-k", false}} {
		got, ok := reg.FrontmatterSticky(tc.name)
		if !ok || got != tc.want {
			t.Errorf("FrontmatterSticky(%s) = %v, %v; want %v, true", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := reg.FrontmatterSticky("missing"); ok {
		t.Error("missing should report ok=false")
	}
}

func assertListedSticky(t *testing.T, reg *SkillRegistry) {
	for _, skill := range reg.List() {
		want := skill.Name == "plain-k" || skill.Name == "fm-sticky"
		if skill.Sticky != want {
			t.Errorf("List() Sticky for %s = %v, want %v", skill.Name, skill.Sticky, want)
		}
	}
}

// TestSkillRegistryStickySkills verifies StickySkills returns only sticky
// knowledge skills, sorted by name, excluding hidden and action skills.
func TestSkillRegistryStickySkills(t *testing.T) {
	dir := t.TempDir()
	write := func(name, frontmatter string) {
		sd := filepath.Join(dir, name)
		if err := os.MkdirAll(sd, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(frontmatter+"\nbody of "+name), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("sticky-b", "---\nname: sticky-b\ndescription: B\nsticky: true\n---")
	write("sticky-a", "---\nname: sticky-a\ndescription: A\nsticky: true\n---")
	write("not-sticky", "---\nname: not-sticky\ndescription: N\n---")
	write("sticky-hidden", "---\nname: sticky-hidden\ndescription: H\nsticky: true\nhidden: true\n---")
	write("sticky-action", "---\nname: sticky-action\ndescription: X\nsticky: true\ncategory: action\n---")

	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got := reg.StickySkills()
	var names []string
	for _, s := range got {
		names = append(names, s.Meta.Name)
	}
	want := []string{"sticky-a", "sticky-b"}
	if len(names) != len(want) {
		t.Fatalf("StickySkills = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("StickySkills = %v, want sorted %v", names, want)
		}
	}
}

// TestSkillRegistryStickyBodies verifies StickyBodies renders the dedup key
// blocks: stable, name-labelled, byte-identical across calls.
func TestSkillRegistryStickyBodies(t *testing.T) {
	dir := t.TempDir()
	sd := filepath.Join(dir, "myskill")
	if err := os.MkdirAll(sd, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte("---\nname: myskill\ndescription: M\nsticky: true\n---\n\nAlways do X."), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	bodies := reg.StickyBodies()
	if len(bodies) != 1 {
		t.Fatalf("StickyBodies = %d blocks, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], "myskill") || !strings.Contains(bodies[0], "Always do X.") {
		t.Errorf("block missing name/body: %q", bodies[0])
	}
	again := reg.StickyBodies()
	if again[0] != bodies[0] {
		t.Errorf("StickyBodies not byte-stable across calls")
	}
}

// TestParseSkillInvocationPolicy covers the P16 frontmatter policy:
// model_invocable / user_invocable default to true when omitted, and
// explicit false values are honored.
func TestParseSkillInvocationPolicy(t *testing.T) {
	tests := []struct {
		name, frontmatter string
		model, user       bool
	}{
		{"defaults both true", "name: s\ndescription: d", true, true}, {"model only false", "name: s\ndescription: d\nmodel_invocable: false", false, true}, {"user only false", "name: s\ndescription: d\nuser_invocable: false", true, false}, {"both false", "name: s\ndescription: d\nmodel_invocable: false\nuser_invocable: false", false, false}, {"explicit true", "name: s\ndescription: d\nmodel_invocable: true\nuser_invocable: true", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertInvocationPolicy(t, tt.frontmatter, tt.model, tt.user) })
	}
}

func assertInvocationPolicy(t *testing.T, frontmatter string, wantModel, wantUser bool) {
	skill := parseSkill("s", "---\n"+frontmatter+"\n---\n\nbody", "embedded", "skills/s/SKILL.md")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if skill.Meta.ModelInvocable != wantModel {
		t.Errorf("ModelInvocable = %v, want %v", skill.Meta.ModelInvocable, wantModel)
	}
	if skill.Meta.UserInvocable != wantUser {
		t.Errorf("UserInvocable = %v, want %v", skill.Meta.UserInvocable, wantUser)
	}
	if skill.IsModelInvocable() != (wantModel && wantUser) {
		t.Errorf("IsModelInvocable = %v, want %v", skill.IsModelInvocable(), wantModel && wantUser)
	}
	if skill.IsUserInvocable() != wantUser {
		t.Errorf("IsUserInvocable = %v, want %v", skill.IsUserInvocable(), wantUser)
	}
}

// TestSkillInvocationPolicyPredicates verifies the surface predicates on
// SkillSummary: IsModelInvocable requires BOTH flags (P16 acceptance — a
// user_invocable:false skill never appears in the model's tool schema),
// while IsUserInvocable reads only the user flag (model_invocable:false
// skills still run from the UI).
func TestSkillInvocationPolicyPredicates(t *testing.T) {
	tests := []struct {
		name      string
		model     bool
		user      bool
		wantModel bool
		wantUser  bool
	}{
		{name: "both true", model: true, user: true, wantModel: true, wantUser: true},
		{name: "model false user true", model: false, user: true, wantModel: false, wantUser: true},
		{name: "model true user false", model: true, user: false, wantModel: false, wantUser: false},
		{name: "both false", model: false, user: false, wantModel: false, wantUser: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := SkillSummary{ModelInvocable: tt.model, UserInvocable: tt.user}
			if got := s.IsModelInvocable(); got != tt.wantModel {
				t.Errorf("IsModelInvocable = %v, want %v", got, tt.wantModel)
			}
			if got := s.IsUserInvocable(); got != tt.wantUser {
				t.Errorf("IsUserInvocable = %v, want %v", got, tt.wantUser)
			}
		})
	}
}

// TestSkillRegistryListCarriesInvocationPolicy verifies List() surfaces the
// parsed policy on summaries so model/user consumers can filter.
func TestSkillRegistryListCarriesInvocationPolicy(t *testing.T) {
	dir := t.TempDir()
	writeInvPolicySkill(t, dir, "plain", "name: plain\ndescription: P")
	writeInvPolicySkill(t, dir, "model-off", "name: model-off\ndescription: M\nmodel_invocable: false")
	writeInvPolicySkill(t, dir, "user-off", "name: user-off\ndescription: U\nuser_invocable: false")

	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	got := map[string]SkillSummary{}
	for _, s := range reg.List() {
		got[s.Name] = s
	}
	if !got["plain"].IsModelInvocable() || !got["plain"].IsUserInvocable() {
		t.Errorf("plain skill should be fully invocable: %+v", got["plain"])
	}
	if got["model-off"].IsModelInvocable() {
		t.Errorf("model-off skill must not be model-invocable: %+v", got["model-off"])
	}
	if !got["model-off"].IsUserInvocable() {
		t.Errorf("model-off skill must remain user-invocable: %+v", got["model-off"])
	}
	if got["user-off"].IsUserInvocable() {
		t.Errorf("user-off skill must not be user-invocable: %+v", got["user-off"])
	}
	if got["user-off"].IsModelInvocable() {
		t.Errorf("user-off skill must not be model-invocable (P16 acceptance): %+v", got["user-off"])
	}
}

// writeInvPolicySkill creates a SKILL.md under dir/<name>/.
func writeInvPolicySkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	sd := filepath.Join(dir, name)
	if err := os.MkdirAll(sd, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\n" + frontmatter + "\n---\n\nbody"
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
