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
		t.Fatalf("LoadAll: %v", err)
	}
	if src, ok := reg.SourceOf("refactor"); !ok || src != "embedded" {
		t.Errorf("SourceOf(refactor) = %q, %v; want embedded, true", src, ok)
	}
	if src, ok := reg.SourceOf("nonexistent"); ok {
		t.Errorf("SourceOf(nonexistent) = %q, %v; want '', false", src, ok)
	}

	// File-based skill in a temp dir.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "local-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: local-skill\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg2 := NewSkillRegistry([]string{dir})
	reg2.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg2.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if src, ok := reg2.SourceOf("local-skill"); !ok || src != "file" {
		t.Errorf("SourceOf(local-skill) = %q, %v; want file, true", src, ok)
	}

	// SourceOf still finds a DISABLED skill: it is not loaded but on disk.
	reg3 := NewSkillRegistry([]string{dir})
	reg3.SetEmbeddedFS(EmbeddedSkillsFS)
	reg3.SetDisabled([]string{"local-skill"})
	if err := reg3.LoadAll(); err != nil {
		t.Fatalf("LoadAll with disabled: %v", err)
	}
	if _, ok := reg3.Get("local-skill"); ok {
		t.Fatal("disabled skill should not be loaded")
	}
	if src, ok := reg3.SourceOf("local-skill"); !ok || src != "file" {
		t.Errorf("SourceOf(disabled local-skill) = %q, %v; want file, true", src, ok)
	}
	if src, ok := reg3.SourceOf("refactor"); !ok || src != "embedded" {
		t.Errorf("SourceOf(refactor) = %q, %v; want embedded, true", src, ok)
	}
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
	write := func(name, frontmatter string) {
		sd := filepath.Join(dir, name)
		if err := os.MkdirAll(sd, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(frontmatter+"\nbody"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("plain-k", "---\nname: plain-k\ndescription: P\ncategory: knowledge\n---")
	write("fm-sticky", "---\nname: fm-sticky\ndescription: F\ncategory: knowledge\nsticky: true\n---")
	write("forced-off", "---\nname: forced-off\ndescription: O\ncategory: knowledge\nsticky: true\n---")
	write("off-wins", "---\nname: off-wins\ndescription: W\ncategory: knowledge\n---")
	write("plain-action", "---\nname: plain-action\ndescription: A2\ncategory: action\n---")

	reg := NewSkillRegistry([]string{dir})
	reg.SetStickyOverrides([]string{"plain-k", "off-wins"}, []string{"forced-off", "off-wins"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	stickyNames := map[string]bool{}
	for _, s := range reg.StickySkills() {
		stickyNames[s.Meta.Name] = true
	}
	if !stickyNames["plain-k"] {
		t.Error("plain-k should be sticky via skills.sticky override")
	}
	if !stickyNames["fm-sticky"] {
		t.Error("fm-sticky should stay sticky from frontmatter")
	}
	if stickyNames["forced-off"] {
		t.Error("forced-off should not be sticky (skills.sticky_off override)")
	}
	if stickyNames["off-wins"] {
		t.Error("off-wins: sticky_off must win over sticky")
	}
	if stickyNames["plain-action"] {
		t.Error("action skills are never sticky")
	}

	if fm, ok := reg.FrontmatterSticky("forced-off"); !ok || !fm {
		t.Error("FrontmatterSticky(forced-off) = true, true; want the pristine frontmatter value")
	}
	if fm, ok := reg.FrontmatterSticky("plain-k"); !ok || fm {
		t.Error("FrontmatterSticky(plain-k) = false, true; want the pristine frontmatter value")
	}
	if _, ok := reg.FrontmatterSticky("missing"); ok {
		t.Error("FrontmatterSticky(missing) should report ok=false")
	}

	for _, s := range reg.List() {
		switch s.Name {
		case "plain-k", "fm-sticky":
			if !s.Sticky {
				t.Errorf("List() Sticky for %s should be true", s.Name)
			}
		case "forced-off", "off-wins", "plain-action":
			if s.Sticky {
				t.Errorf("List() Sticky for %s should be false", s.Name)
			}
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
		name        string
		frontmatter string
		wantModel   bool
		wantUser    bool
	}{
		{name: "defaults both true", frontmatter: "name: s\ndescription: d", wantModel: true, wantUser: true},
		{name: "model only false", frontmatter: "name: s\ndescription: d\nmodel_invocable: false", wantModel: false, wantUser: true},
		{name: "user only false", frontmatter: "name: s\ndescription: d\nuser_invocable: false", wantModel: true, wantUser: false},
		{name: "both false", frontmatter: "name: s\ndescription: d\nmodel_invocable: false\nuser_invocable: false", wantModel: false, wantUser: false},
		{name: "explicit true", frontmatter: "name: s\ndescription: d\nmodel_invocable: true\nuser_invocable: true", wantModel: true, wantUser: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := "---\n" + tt.frontmatter + "\n---\n\nbody"
			skill := parseSkill("s", content, "embedded", "skills/s/SKILL.md")
			if skill == nil {
				t.Fatal("parseSkill returned nil")
			}
			if skill.Meta.ModelInvocable != tt.wantModel {
				t.Errorf("ModelInvocable = %v, want %v", skill.Meta.ModelInvocable, tt.wantModel)
			}
			if skill.Meta.UserInvocable != tt.wantUser {
				t.Errorf("UserInvocable = %v, want %v", skill.Meta.UserInvocable, tt.wantUser)
			}
			if skill.IsModelInvocable() != (tt.wantModel && tt.wantUser) {
				t.Errorf("IsModelInvocable = %v, want %v (model predicate must require both flags)", skill.IsModelInvocable(), tt.wantModel && tt.wantUser)
			}
			if skill.IsUserInvocable() != tt.wantUser {
				t.Errorf("IsUserInvocable = %v, want %v", skill.IsUserInvocable(), tt.wantUser)
			}
		})
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
