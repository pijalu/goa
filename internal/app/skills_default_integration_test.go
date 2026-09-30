// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/skills"
)

// TestShippedDefault_ShippedSkillsOnWithoutGatingFileSkills is the end-to-end
// regression for the shipped skills default: a defaults-only cascade builds
// a registry where (a) the shipped-on sticky knowledge skills (telegram,
// thoughtfull) are loaded with their sticky bodies, (b) every other embedded
// skill stays off, and (c) home/project
// file-based skills are unaffected — the default-provided skills.enabled list
// must never act as a global allowlist.
func TestShippedDefault_ShippedSkillsOnWithoutGatingFileSkills(t *testing.T) {
	project := shippedDefaultTestProject(t)

	loader := config.NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	subs := InitSubsystems(cfg, loader, project, RuntimeOptions{})

	bodies := subs.skillRegistry.StickyBodies()
	assertStickyShippedOn(t, subs.skillRegistry, bodies)
	for _, name := range []string{"refactor", "review", "dream", "debug"} {
		if _, ok := subs.skillRegistry.Get(name); ok {
			t.Errorf("embedded skill %q must stay default-off", name)
		}
	}
}

// assertStickyShippedOn checks the two shipped-on skills load, are sticky and
// inject their bodies, and that unrelated project file skills are not gated by
// the default-provided skills.enabled list.
func assertStickyShippedOn(t *testing.T, reg *skills.SkillRegistry, bodies []string) {
	t.Helper()
	for _, name := range []string{"telegram", "thoughtfull"} {
		s, ok := reg.Get(name)
		if !ok {
			t.Errorf("%s must load under the shipped default", name)
			continue
		}
		if !s.IsSticky() {
			t.Errorf("the shipped-on %s skill must be sticky", name)
		}
	}
	if len(bodies) != 2 {
		t.Errorf("both shipped sticky bodies must be injected, got %d block(s): %v", len(bodies), bodies)
	}
	if _, ok := reg.Get("proj-helper"); !ok {
		t.Error("project file skills must not be suppressed by the shipped default list")
	}
}

// TestShippedDefault_HomePinReplacesDefault verifies the precedence half at
// the app wiring level: a home skills.enabled pin replaces the shipped
// default — the pin is a real allowlist, so telegram (not listed) stays off
// and unpinned file skills are gated.
func TestShippedDefault_HomePinReplacesDefault(t *testing.T) {
	project := shippedDefaultTestProject(t)
	goadir := internal.GoaHomeDir()
	if goadir == "" {
		t.Fatal("no goa home")
	}
	homeCfg := filepath.Join(goadir, "config.yaml")
	if err := os.WriteFile(homeCfg, []byte("skills:\n  enabled:\n    - refactor\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := config.NewCascadeLoader(project, "", nil)
	pinned, err := loader.Load()
	if err != nil {
		t.Fatalf("load pinned: %v", err)
	}
	if len(pinned.Skills.Enabled) != 1 || pinned.Skills.Enabled[0] != "refactor" || pinned.Skills.EnabledFromDefaults {
		t.Fatalf("home pin must replace the shipped default, got %v (default-only=%v)",
			pinned.Skills.Enabled, pinned.Skills.EnabledFromDefaults)
	}
	reg := newSkillRegistry(pinned, project, nil, false, nil)
	if _, ok := reg.Get("telegram"); ok {
		t.Error("a home skills.enabled pin must replace the shipped default (telegram off)")
	}
	if _, ok := reg.Get("thoughtfull"); ok {
		t.Error("a home skills.enabled pin must replace the shipped default (thoughtfull off)")
	}
	if _, ok := reg.Get("refactor"); !ok {
		t.Error("the pinned skill must load")
	}
	if _, ok := reg.Get("proj-helper"); ok {
		t.Error("a home pin is a global allowlist: unpinned file skills are gated")
	}
}

// shippedDefaultTestProject isolates the goa home, creates a trusted project
// file skill (proj-helper) under the default relative skills dir, and points
// the test CWD at the project so the default ".goa/skills" entry resolves.
func shippedDefaultTestProject(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()
	internal.SetGoaHome(home)
	t.Cleanup(func() { internal.SetGoaHome("") })

	skillDir := filepath.Join(project, ".goa", "skills", "proj-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\nname: proj-helper\ndescription: A project skill\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	goadir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(goadir, 0o755); err != nil {
		t.Fatal(err)
	}
	trustJSON := `{"decisions": {"proj-helper": "trusted"}}`
	if err := os.WriteFile(filepath.Join(goadir, "trust.json"), []byte(trustJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	// The default skills.dirs entry is the project-relative ".goa/skills";
	// resolve it against the project like a real session does.
	t.Chdir(project)
	return project
}
