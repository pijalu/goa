// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
)

// Bugs.md "Dream is not wired into the agent session": dream must be FULLY
// disabled by default on the running-session surfaces until wired end-to-end.
// These tests pin every default-off surface:
//
//  1. no auto-dream scheduler goroutine with the shipped defaults,
//  2. the embedded dream skill is hidden: never in the model's
//     <available_skills> catalog, never runnable via run_skill,
//  3. explicit user invocation (/dream) stays available and documented.

// TestDreamScheduler_NilWithShippedDefaults pins surface 1: the scheduler is
// created only when BOTH memory.dream.enabled and memory.dream.auto are on;
// the embedded default config (both false) must produce nil — no goroutine,
// no timers, no consolidation runs behind the user's back.
func TestDreamScheduler_NilWithShippedDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cl := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := cl.Load() // embedded defaults only: no home, no project overrides
	if err != nil {
		t.Fatalf("load shipped defaults: %v", err)
	}
	if cfg.Memory.Dream.Enabled || cfg.Memory.Dream.Auto {
		t.Fatalf("shipped defaults must have dream disabled (enabled=%v auto=%v)",
			cfg.Memory.Dream.Enabled, cfg.Memory.Dream.Auto)
	}
	if s := newDreamScheduler(&subsystems{cfg: cfg}); s != nil {
		t.Error("newDreamScheduler must return nil with the shipped defaults")
	}

	// Both flags on → scheduler exists. Either flag alone is not enough.
	on := *cfg
	on.Memory.Dream.Enabled = true
	on.Memory.Dream.Auto = true
	if s := newDreamScheduler(&subsystems{cfg: &on}); s == nil {
		t.Error("newDreamScheduler must create a scheduler when enabled+auto")
	}
	half := *cfg
	half.Memory.Dream.Enabled = true
	if s := newDreamScheduler(&subsystems{cfg: &half}); s != nil {
		t.Error("enabled without auto must NOT create a scheduler")
	}
}

// TestDreamSkill_HiddenFromAgentSurfaces pins surface 2 through the real
// prompt assembly: a hidden skill loaded from disk never reaches the model's
// <available_skills> catalog, while non-hidden skills still do.
func TestDreamSkill_HiddenFromAgentSurfaces(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "review", "name: review\ndescription: Review code\ncategory: action")
	writeTestSkill(t, dir, "internal-thing", "name: internal-thing\ndescription: Internal\nhidden: true")

	skillReg := skills.NewSkillRegistry([]string{filepath.Join(dir, ".goa", "skills")})
	if err := skillReg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	// Hidden skills stay LOADED for internal feature use (Get by name).
	if s, ok := skillReg.Get("internal-thing"); !ok || !s.Meta.Hidden {
		t.Fatalf("hidden skill must stay loaded with Meta.Hidden set (ok=%v)", ok)
	}

	subs := newTestSubsystems(dir)
	subs.skillRegistry = skillReg
	subs.toolRegistry = tools.NewToolRegistry()
	subs.toolRegistry.Register(&mockTool{name: "run_skill"})

	got := availableSkillsSection(subs)
	if got == "" {
		t.Fatal("expected a rendered available_skills section")
	}
	if strings.Contains(got, "internal-thing") {
		t.Errorf("hidden skill leaked into the model catalog:\n%s", got)
	}
	if !strings.Contains(got, `name="review"`) {
		t.Errorf("non-hidden skill missing from catalog:\n%s", got)
	}
}

// TestDreamEmbeddedSkill_IsHidden pins that the SHIPPED embedded dream skill
// itself carries hidden:true so production sessions can never advertise it.
func TestDreamEmbeddedSkill_IsHidden(t *testing.T) {
	reg := skills.NewSkillRegistry(nil) // embedded FS only
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load embedded skills: %v", err)
	}
	skill, ok := reg.Get("dream")
	if !ok {
		t.Skip("no embedded dream skill in this build")
	}
	if !skill.Meta.Hidden {
		t.Error("embedded dream skill must be hidden (hidden: true)")
	}
	if skill.IsModelInvocable() {
		t.Error("embedded dream skill must never be model-invocable")
	}
}
