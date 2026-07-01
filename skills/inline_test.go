// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"strings"
	"testing"
)

// TestInlineInjectorBasic verifies inline skill injection.
func TestInlineInjectorBasic(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	injector := NewInlineSkillInjector(reg)
	result := injector.Inject("You are an assistant.", []string{"telegram"})

	if !strings.Contains(result, "## Skill: telegram") {
		t.Errorf("Result missing skill wrapper: %s", result)
	}
	if !strings.Contains(result, "## End Skill") {
		t.Errorf("Result missing end marker: %s", result)
	}
	if !strings.Contains(result, "You are an assistant.") {
		t.Errorf("Result missing original prompt: %s", result)
	}
}

// TestInlineInjectorNoInline verifies non-inline skills are skipped.
func TestInlineInjectorNoInline(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	injector := NewInlineSkillInjector(reg)
	result := injector.Inject("Original.", []string{"refactor"})

	// refactor is not inline, so should be skipped
	if result != "Original." {
		t.Errorf("Result should be unchanged: %s", result)
	}
}

// TestInlineInjectorNonexistent verifies nonexistent skills are skipped.
func TestInlineInjectorNonexistent(t *testing.T) {
	reg := NewSkillRegistry(nil)
	injector := NewInlineSkillInjector(reg)

	result := injector.Inject("Original.", []string{"nonexistent"})
	if result != "Original." {
		t.Errorf("Result should be unchanged: %s", result)
	}
}
