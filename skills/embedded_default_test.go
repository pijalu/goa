// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTestSkill creates a <dir>/<name>/SKILL.md file-based skill for tests.
func writeTestSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// names returns the set of skill names in a summary slice.
func summaryNames(summaries []SkillSummary) map[string]bool {
	out := make(map[string]bool, len(summaries))
	for _, s := range summaries {
		out[s.Name] = true
	}
	return out
}

// TestDefaultEmbeddedOffNames verifies the default-off set is every embedded
// skill except telegram, derived from the embedded FS.
func TestDefaultEmbeddedOffNames(t *testing.T) {
	off := DefaultEmbeddedOffNames(EmbeddedSkillsFS)
	if len(off) == 0 {
		t.Fatal("DefaultEmbeddedOffNames returned no skills")
	}
	offSet := make(map[string]bool, len(off))
	for _, n := range off {
		offSet[n] = true
	}
	if offSet[DefaultOnEmbeddedSkill] {
		t.Errorf("default-off set must not contain %q", DefaultOnEmbeddedSkill)
	}
	// Hidden/internal skills (e.g. dream) must NOT be default-off: internal
	// features load them by name via Get.
	if offSet["dream"] {
		t.Error("hidden internal skill dream must not be default-off")
	}
	// Every agent-facing (non-hidden) embedded skill except telegram is off.
	agentFacing := map[string]bool{
		"commit-msg": true, "debug": true, "document": true, "explain": true,
		"refactor": true, "review": true, "test-gen": true,
	}
	for n := range agentFacing {
		if !offSet[n] {
			t.Errorf("agent-facing embedded skill %q missing from default-off set", n)
		}
	}
}

// TestEmbeddedDefaultOff_LoadsOnlyTelegramAndInternal is the regression test
// for all embedded skills except telegram disabled by default: with
// the default-off set applied and no user opt-in, the agent-facing embedded
// skills are off; only telegram (agent-facing) and dream (hidden/internal)
// load.
func TestEmbeddedDefaultOff_LoadsOnlyTelegramAndInternal(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded := summaryNames(reg.List())
	if !loaded[DefaultOnEmbeddedSkill] {
		t.Errorf("telegram must load by default; loaded=%v", loaded)
	}
	if !loaded["dream"] {
		t.Errorf("hidden internal skill dream must still load; loaded=%v", loaded)
	}
	for _, n := range DefaultEmbeddedOffNames(EmbeddedSkillsFS) {
		if loaded[n] {
			t.Errorf("embedded skill %q must be OFF by default, but it loaded", n)
		}
	}
	// The agent-facing gate agrees: a default-off skill is not retrievable.
	if _, ok := reg.Get("review"); ok {
		t.Error("Get(review) must fail while review is default-off")
	}
	// But the internal dream skill remains retrievable by name.
	if _, ok := reg.Get("dream"); !ok {
		t.Error("Get(dream) must succeed — internal features load it by name")
	}
}

// TestEmbeddedDefaultOff_ReenabledViaEmbeddedEnabled verifies the embedded-
// scoped opt-in re-enables a default-off skill.
func TestEmbeddedDefaultOff_ReenabledViaEmbeddedEnabled(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEmbeddedEnabled([]string{"review"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	loaded := summaryNames(reg.List())
	if !loaded["review"] {
		t.Errorf("review must load after embedded opt-in; loaded=%v", loaded)
	}
	// Others stay off.
	if loaded["refactor"] {
		t.Error("refactor must remain default-off")
	}
}

// TestEmbeddedDefaultOff_ReenabledViaGlobalAllowlist verifies the global
// Enabled allowlist also re-enables a default-off embedded skill.
func TestEmbeddedDefaultOff_ReenabledViaGlobalAllowlist(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEnabled([]string{"debug"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("debug"); !ok {
		t.Error("debug must load when named in the global Enabled allowlist")
	}
}

// TestEmbeddedDefaultOff_ExplicitDisableWins verifies an explicit Disabled
// entry beats an embedded opt-in (explicit off wins).
func TestEmbeddedDefaultOff_ExplicitDisableWins(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEmbeddedEnabled([]string{"review"})
	reg.SetDisabled([]string{"review"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("review"); ok {
		t.Error("explicit Disabled must win over the embedded opt-in")
	}
}

// TestEmbeddedDefaultOff_TelegramStillDisableable verifies the one default-ON
// embedded skill (telegram) can still be explicitly disabled.
func TestEmbeddedDefaultOff_TelegramStillDisableable(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetDisabled([]string{"telegram"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("telegram"); ok {
		t.Error("explicitly-disabled telegram must not load")
	}
}

// TestListEmbeddedDiscoverable verifies the toggle-menu enumeration returns
// ALL embedded skills (including default-off ones the loader skipped), so the
// /config menu can re-enable them.
func TestListEmbeddedDiscoverable(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	discovered := summaryNames(reg.ListEmbeddedDiscoverable())
	for _, n := range EmbeddedSkillNames(EmbeddedSkillsFS) {
		if !discovered[n] {
			t.Errorf("ListEmbeddedDiscoverable must include %q (even when default-off)", n)
		}
	}
	// IsEmbeddedDefaultOff marks set membership (stable across toggles).
	if !reg.IsEmbeddedDefaultOff("review") {
		t.Error("review must be a default-off member")
	}
	if reg.IsEmbeddedDefaultOff(DefaultOnEmbeddedSkill) {
		t.Error("telegram must NOT be a default-off member")
	}
}

// TestEmbeddedDefaultOff_FileSkillsUnaffected verifies the default-off set
// never suppresses file-based skills (home/project dirs).
func TestEmbeddedDefaultOff_FileSkillsUnaffected(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "my-custom", "A project skill")

	reg := NewSkillRegistry([]string{dir})
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("my-custom"); !ok {
		t.Error("file-based skill must load even with the embedded default-off set active")
	}
}
