// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
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
	skill := parseSkill("test-skill", content, "embedded", "embedded:/test-skill/SKILL.md")
	if skill == nil {
		t.Fatal("parseSkill returned nil")
	}
	if skill.FilePath != "embedded:/test-skill/SKILL.md" {
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
