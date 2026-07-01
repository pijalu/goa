// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skillrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	agentic "github.com/pijalu/goa/internal/agentic"
)

func TestLoadSkill(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}

	skillMD := `---
name: test-skill
description: A test skill
---
Test instructions here.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	skill, err := LoadSkill(skillDir)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if skill.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", skill.Name, "test-skill")
	}
}

func TestLoadSkillErrors(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		skillDir string
	}{
		{
			name:     "nonexistent_directory",
			skillDir: filepath.Join(tmpDir, "nonexistent"),
		},
		{
			name:     "missing_skill_md",
			skillDir: tmpDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadSkill(tt.skillDir)
			if err == nil {
				t.Error("Expected error, got nil")
			}
		})
	}
}

func TestLoadSkillNameMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "wrong-name")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create test dir: %v", err)
	}
	skillMD := `---
name: different-name
description: A test skill
---
Instructions
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	_, err := LoadSkill(skillDir)
	if err == nil {
		t.Error("Expected error for name mismatch, got nil")
	}
}

func TestNewFileSkillsLoader(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}

	// Create valid skill
	validSkillDir := filepath.Join(skillsDir, "valid-skill")
	if err := os.MkdirAll(validSkillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	validSkillMD := `---
name: valid-skill
description: A valid skill
---
Instructions
`
	if err := os.WriteFile(filepath.Join(validSkillDir, "SKILL.md"), []byte(validSkillMD), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Create invalid skill (missing description)
	invalidSkillDir := filepath.Join(skillsDir, "invalid-skill")
	if err := os.MkdirAll(invalidSkillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	invalidSkillMD := `---
name: invalid-skill
---
Instructions
`
	if err := os.WriteFile(filepath.Join(invalidSkillDir, "SKILL.md"), []byte(invalidSkillMD), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Create a file (not directory) - should be skipped
	if err := os.WriteFile(filepath.Join(skillsDir, "not-a-dir"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	logger := agentic.NewLogger(agentic.Error)

	loader := NewFileSkillsLoader([]string{skillsDir})
	skills := loader.Load(logger)

	if len(skills) != 1 {
		t.Errorf("Load() returned %d skills, want 1", len(skills))
	}
	if len(skills) > 0 && skills[0].Name != "valid-skill" {
		t.Errorf("Skill.Name = %q, want %q", skills[0].Name, "valid-skill")
	}
}

func TestNewFileSkillsLoaderNonexistentDir(t *testing.T) {
	logger := agentic.NewLogger(agentic.Error)
	loader := NewFileSkillsLoader([]string{"/nonexistent/path"})
	skills := loader.Load(logger)

	if len(skills) != 0 {
		t.Errorf("Load() returned %d skills, want 0 for nonexistent dir", len(skills))
	}
}

func TestNewFileSkillsLoaderMultipleDirs(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir1 := filepath.Join(tmpDir, "skills1")
	skillsDir2 := filepath.Join(tmpDir, "skills2")

	if err := os.MkdirAll(skillsDir1, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}
	if err := os.MkdirAll(skillsDir2, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}

	// Create skill in dir1
	skill1Dir := filepath.Join(skillsDir1, "skill1")
	if err := os.MkdirAll(skill1Dir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(`---
name: skill1
description: Skill 1
---
Instructions
`), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	// Create skill in dir2
	skill2Dir := filepath.Join(skillsDir2, "skill2")
	if err := os.MkdirAll(skill2Dir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(`---
name: skill2
description: Skill 2
---
Instructions
`), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	logger := agentic.NewLogger(agentic.Error)
	loader := NewFileSkillsLoader([]string{skillsDir1, skillsDir2})
	skills := loader.Load(logger)

	if len(skills) != 2 {
		t.Errorf("Load() returned %d skills, want 2", len(skills))
	}
}

func TestLoadSkillWithSubSkills(t *testing.T) {
	parentDir := createSkillTree(t)

	skill, err := LoadSkill(parentDir)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	assertSkillName(t, skill, "parent-skill")
	assertSubSkillNames(t, skill, []string{"sub-skill-a", "sub-skill-b"})
	assertNestedSkill(t, skill, "sub-skill-a", "nested-skill")
}

func createSkillTree(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "parent-skill")
	createSkillDir(t, parentDir, "parent-skill", "Parent skill", "Parent instructions")
	createSkillDir(t, filepath.Join(parentDir, "sub-skill-a"), "sub-skill-a", "Sub-skill A", "Sub-skill A instructions")
	createSkillDir(t, filepath.Join(parentDir, "sub-skill-b"), "sub-skill-b", "Sub-skill B", "Sub-skill B instructions")
	createSkillDir(t, filepath.Join(parentDir, "sub-skill-a", "nested-skill"), "nested-skill", "Nested skill", "Nested instructions")
	os.MkdirAll(filepath.Join(parentDir, "not-a-skill"), 0755)
	return parentDir
}

func createSkillDir(t *testing.T, dir, name, description, instructions string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("Failed to create dir %s: %v", dir, err)
	}
	md := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s\n", name, description, instructions)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md in %s: %v", dir, err)
	}
}

func assertSkillName(t *testing.T, skill *Skill, want string) {
	t.Helper()
	if skill.Name != want {
		t.Errorf("Name = %q, want %q", skill.Name, want)
	}
}

func assertSubSkillNames(t *testing.T, skill *Skill, want []string) {
	t.Helper()
	if len(skill.SubSkills) != len(want) {
		t.Fatalf("Expected %d sub-skills, got %d", len(want), len(skill.SubSkills))
	}
	subNames := make(map[string]bool)
	for _, sub := range skill.SubSkills {
		subNames[sub.Name] = true
	}
	for _, name := range want {
		if !subNames[name] {
			t.Errorf("Expected %s", name)
		}
	}
}

func assertNestedSkill(t *testing.T, parent *Skill, parentName, childName string) {
	t.Helper()
	var sub *Skill
	for _, s := range parent.SubSkills {
		if s.Name == parentName {
			sub = s
			break
		}
	}
	if sub == nil {
		t.Fatalf("%s not found", parentName)
	}
	if len(sub.SubSkills) != 1 {
		t.Fatalf("Expected 1 nested sub-skill, got %d", len(sub.SubSkills))
	}
	if sub.SubSkills[0].Name != childName {
		t.Errorf("Nested skill name = %q, want %q", sub.SubSkills[0].Name, childName)
	}
}

func TestFileSkillsLoaderOnlyTopLevel(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}

	// Create top-level skill with sub-skill
	parentDir := filepath.Join(skillsDir, "parent-skill")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("Failed to create parent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "SKILL.md"), []byte("---\nname: parent-skill\ndescription: Parent\n---\nInstr"), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	subDir := filepath.Join(parentDir, "sub-skill")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create sub dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "SKILL.md"), []byte("---\nname: sub-skill\ndescription: Sub\n---\nInstr"), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	logger := agentic.NewLogger(agentic.Error)
	loader := NewFileSkillsLoader([]string{skillsDir})
	skills := loader.Load(logger)

	if len(skills) != 1 {
		t.Fatalf("Expected 1 top-level skill, got %d", len(skills))
	}
	if skills[0].Name != "parent-skill" {
		t.Errorf("Top-level skill name = %q, want %q", skills[0].Name, "parent-skill")
	}
	if len(skills[0].SubSkills) != 1 {
		t.Errorf("Expected 1 sub-skill, got %d", len(skills[0].SubSkills))
	}
}

func TestEmbeddedSkillsLoaderWithSubSkills(t *testing.T) {
	mapFS := fstest.MapFS{
		"skills/parent-skill/SKILL.md": {
			Data: []byte("---\nname: parent-skill\ndescription: Parent\n---\nParent instructions"),
		},
		"skills/parent-skill/child-skill/SKILL.md": {
			Data: []byte("---\nname: child-skill\ndescription: Child\n---\nChild instructions"),
		},
		"skills/parent-skill/child-skill/grandchild-skill/SKILL.md": {
			Data: []byte("---\nname: grandchild-skill\ndescription: Grandchild\n---\nGrandchild instructions"),
		},
		"skills/other-skill/SKILL.md": {
			Data: []byte("---\nname: other-skill\ndescription: Other\n---\nOther instructions"),
		},
	}

	loader := NewEmbeddedSkillsLoader(mapFS, "skills")
	logger := agentic.NewLogger(agentic.Error)
	skills := loader.Load(logger)

	if len(skills) != 2 {
		t.Fatalf("Expected 2 top-level skills, got %d", len(skills))
	}

	skillMap := make(map[string]*Skill)
	for _, s := range skills {
		skillMap[s.Name] = s
	}

	parent, ok := skillMap["parent-skill"]
	if !ok {
		t.Fatal("Expected parent-skill")
	}
	if len(parent.SubSkills) != 1 {
		t.Fatalf("Expected 1 sub-skill for parent, got %d", len(parent.SubSkills))
	}
	if parent.SubSkills[0].Name != "child-skill" {
		t.Errorf("Sub-skill name = %q, want %q", parent.SubSkills[0].Name, "child-skill")
	}

	child := parent.SubSkills[0]
	if len(child.SubSkills) != 1 {
		t.Fatalf("Expected 1 nested sub-skill for child, got %d", len(child.SubSkills))
	}
	if child.SubSkills[0].Name != "grandchild-skill" {
		t.Errorf("Nested skill name = %q, want %q", child.SubSkills[0].Name, "grandchild-skill")
	}

	if _, ok := skillMap["other-skill"]; !ok {
		t.Error("Expected other-skill")
	}
}
