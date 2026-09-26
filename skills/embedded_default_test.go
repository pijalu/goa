// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"os"
	"path/filepath"
	"strings"
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

// TestDefaultEmbeddedOffNames_CoversEveryEmbeddedSkill pins the shipped policy:
// EVERY embedded skill is OFF by default — the agent-facing set, telegram (whose
// sticky body used to be injected into each session) and the hidden/internal
// dream skill alike. Derived from the embedded FS, so a new built-in is off by
// construction.
func TestDefaultEmbeddedOffNames_CoversEveryEmbeddedSkill(t *testing.T) {
	off := DefaultEmbeddedOffNames(EmbeddedSkillsFS)
	if len(off) == 0 {
		t.Fatal("DefaultEmbeddedOffNames returned no skills")
	}
	offSet := make(map[string]bool, len(off))
	for _, n := range off {
		offSet[n] = true
	}

	all := EmbeddedSkillNames(EmbeddedSkillsFS)
	if len(off) != len(all) {
		t.Fatalf("default-off set has %d entries, want all %d embedded skills: %v", len(off), len(all), all)
	}
	for _, n := range all {
		if !offSet[n] {
			t.Errorf("embedded skill %q missing from the default-off set", n)
		}
	}
	// The two former exceptions must now be OFF as well.
	for _, n := range []string{"telegram", "dream"} {
		if !offSet[n] {
			t.Errorf("%q must be default-off (no exception for telegram or hidden skills)", n)
		}
	}
	// The old policy's telegram exception must be gone from the API.
	if offSet["telegram"] == false {
		t.Error("telegram must be part of the default-off set")
	}
}

// TestShippedEmbeddedSkills_AllOffByDefault is the regression test for the
// reported behavior: with the default-off set applied and no user opt-in, NO
// embedded skill loads — so nothing compiled in is advertised to the model and,
// critically, StickyBodies() is empty (telegram is a sticky knowledge skill, so
// it used to be persisted into every agent's history).
func TestShippedEmbeddedSkills_AllOffByDefault(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if loaded := summaryNames(reg.List()); len(loaded) != 0 {
		t.Errorf("no embedded skill may load by default, got %v", loaded)
	}
	if bodies := reg.StickyBodies(); len(bodies) != 0 {
		t.Errorf("no sticky body may be injected by default, got %d block(s)", len(bodies))
	}
	// The agent-facing gate agrees for both the visible and the hidden skill.
	for _, n := range []string{"review", "telegram", "dream"} {
		if _, ok := reg.Get(n); ok {
			t.Errorf("Get(%s) must fail while it is default-off", n)
		}
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

// TestEmbeddedSkill_OptInTelegram verifies the embedded-scoped opt-in turns the
// telegram skill back on with its sticky body — the user's explicit choice is the
// only way the telegraphic style reaches a session now.
func TestEmbeddedSkill_OptInTelegram(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEmbeddedEnabled([]string{"telegram"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded := summaryNames(reg.List())
	if !loaded["telegram"] {
		t.Fatalf("telegram must load after the opt-in; loaded=%v", loaded)
	}
	if len(loaded) != 1 {
		t.Errorf("only telegram should be on, got %v", loaded)
	}
	if bodies := reg.StickyBodies(); len(bodies) != 1 || !strings.Contains(bodies[0], "name=\"telegram\"") {
		t.Errorf("the opted-in telegram sticky body must be injected, got %d block(s): %v", len(bodies), bodies)
	}
}

// TestEmbeddedSkill_OptInDream verifies the hidden/internal dream skill is
// loadable through the same opt-in list, so /dream and --dream work when the
// user asks for them.
func TestEmbeddedSkill_OptInDream(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEmbeddedEnabled([]string{"dream"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// Criterion (9): a dormant hidden skill resolves through Get once opted in…
	skill, ok := reg.Get("dream")
	if !ok {
		t.Fatal("Get(dream) must succeed after the opt-in")
	}
	if skill.Source != "embedded" || len(skill.Body) == 0 {
		t.Errorf("dream skill shape = source %q, body %d bytes", skill.Source, len(skill.Body))
	}
	// …and stays invisible to the model (hidden skills are never advertised).
	if skill.IsModelInvocable() {
		t.Error("dream must stay model-invisible even when loaded")
	}
	if loaded := summaryNames(reg.List()); len(loaded) != 1 || !loaded["dream"] {
		t.Errorf("only dream should be on, got %v", loaded)
	}
}

// TestHiddenEmbeddedSkill_ResolvableWhenOptedIn is the explicit gate for
// criterion (9): while dream is OFF by default Get must miss (that is what makes
// /dream report "enable it"), and the opt-in must make it resolvable again.
func TestHiddenEmbeddedSkill_ResolvableWhenOptedIn(t *testing.T) {
	off := NewSkillRegistry(nil)
	off.SetEmbeddedFS(EmbeddedSkillsFS)
	off.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	if err := off.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := off.Get("dream"); ok {
		t.Fatal("dream must NOT be resolvable while it is off by default")
	}

	on := NewSkillRegistry(nil)
	on.SetEmbeddedFS(EmbeddedSkillsFS)
	on.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	on.SetEmbeddedEnabled([]string{"dream"})
	if err := on.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := on.Get("dream"); !ok {
		t.Fatal("dream must be resolvable after skills.embedded_enabled: [dream]")
	}
}

// TestLegacyTelegramDisableStillHonored: configs written under the old
// telegram-default-ON policy carry skills.disabled: [telegram]. That entry must
// keep the skill off even when the embedded opt-in is present (explicit off wins)
// — and TestReenableAfterLegacyDisable proves the user can still turn it back on
// by dropping the entry.
func TestLegacyTelegramDisableStillHonored(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEmbeddedEnabled([]string{"telegram"})
	reg.SetDisabled([]string{"telegram"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("telegram"); ok {
		t.Error("a legacy skills.disabled entry must win over the embedded opt-in")
	}
}

// TestReenableAfterLegacyDisable: dropping the legacy Disabled entry together
// with the opt-in turns telegram back on.
func TestReenableAfterLegacyDisable(t *testing.T) {
	reg := NewSkillRegistry(nil)
	reg.SetEmbeddedFS(EmbeddedSkillsFS)
	reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
	reg.SetEmbeddedEnabled([]string{"telegram"})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := reg.Get("telegram"); !ok {
		t.Error("telegram must load once the legacy Disabled entry is gone")
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
	if !reg.IsEmbeddedDefaultOff("telegram") {
		t.Error("telegram must be a default-off member (no default-ON exception)")
	}
	if !reg.IsEmbeddedDefaultOff("dream") {
		t.Error("dream must be a default-off member (hidden skills are not exempt)")
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

// TestNoStickyBodiesByDefault is the focused gate for the reported symptom: the
// telegram skill is a STICKY knowledge skill, so while it was default-ON its body
// was persisted into every agent's history. With the shipped default-off set
// StickyBodies() must be empty, and it must contain the opted-in skill only when
// the user asks for it.
func TestNoStickyBodiesByDefault(t *testing.T) {
	shipped := func(optIn ...string) *SkillRegistry {
		reg := NewSkillRegistry(nil)
		reg.SetEmbeddedFS(EmbeddedSkillsFS)
		reg.SetEmbeddedDefaultDisabled(DefaultEmbeddedOffNames(EmbeddedSkillsFS))
		reg.SetEmbeddedEnabled(optIn)
		if err := reg.LoadAll(); err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		return reg
	}

	if bodies := shipped("").StickyBodies(); len(bodies) != 0 {
		t.Fatalf("no sticky instruction may be injected by default, got %d block(s): %v", len(bodies), bodies)
	}
	// The embedded sticky skill still exists and injects exactly once when asked.
	bodies := shipped("telegram").StickyBodies()
	if len(bodies) != 1 {
		t.Fatalf("opted-in telegram must inject exactly one sticky block, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "name=\"telegram\"") {
		t.Errorf("unexpected sticky block: %q", bodies[0])
	}
}
