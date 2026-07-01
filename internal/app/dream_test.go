// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/skills"
)

func TestLoadDreamSkill_Found(t *testing.T) {
	reg := skills.NewSkillRegistry(nil)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	subs := &subsystems{skillRegistry: reg}
	skill, ok := loadDreamSkill(subs)
	if !ok {
		t.Fatalf("expected dream skill to be found")
	}
	if !skill.Meta.Hidden {
		t.Fatalf("expected dream skill to be hidden")
	}
}

func TestLoadDreamSkill_NotFound(t *testing.T) {
	reg := skills.NewSkillRegistry(nil)
	subs := &subsystems{skillRegistry: reg}
	_, ok := loadDreamSkill(subs)
	if ok {
		t.Fatalf("expected dream skill to be missing")
	}
}

func TestValidateDreamPrerequisites(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:      &config.Config{Memory: config.MemoryConfig{Enabled: true}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	if err := validateDreamPrerequisites(subs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDreamPrerequisites_MemoryDisabled(t *testing.T) {
	dir := t.TempDir()
	subs := &subsystems{
		cfg:      &config.Config{Memory: config.MemoryConfig{Enabled: false}},
		memStore: memory.NewMemoryStore(dir, ""),
	}
	if err := validateDreamPrerequisites(subs); err == nil {
		t.Fatalf("expected error when memory disabled")
	}
}

func TestValidateDreamPrerequisites_NoStore(t *testing.T) {
	subs := &subsystems{cfg: &config.Config{Memory: config.MemoryConfig{Enabled: true}}}
	if err := validateDreamPrerequisites(subs); err == nil {
		t.Fatalf("expected error when memory store nil")
	}
}

func TestDreamMemoryStoreHasConsolidated(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewMemoryStore(dir, "")
	if store.HasConsolidated() {
		t.Fatalf("expected no consolidated memory")
	}
	consolidated := filepath.Join(dir, ".goa", "memory.consolidated", "consolidated.md")
	if err := os.MkdirAll(filepath.Dir(consolidated), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(consolidated, []byte("summary: consolidated\n\n# Memory"), 0644); err != nil {
		t.Fatalf("write consolidated: %v", err)
	}
	if !store.HasConsolidated() {
		t.Fatalf("expected consolidated memory")
	}
	content, err := store.ReadConsolidated()
	if err != nil {
		t.Fatalf("read consolidated: %v", err)
	}
	if !strings.Contains(content, "Memory") {
		t.Fatalf("unexpected consolidated content: %s", content)
	}
}
