// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/skills"
)

// TestDreamCommand_SkillDisabledReportsHowToEnable: dream is OFF by default like
// every other embedded skill, so /dream must explain how to enable it (naming
// skills.embedded_enabled / /config → Skills and the restart caveat) instead of
// the bare "dream skill not found", which reads as a bug.
func TestDreamCommand_SkillDisabledReportsHowToEnable(t *testing.T) {
	ctx := dreamTestContext(t)
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{}) // dream not loaded

	err := (&DreamCommand{}).Run(ctx, nil)
	if err == nil {
		t.Fatal("Run must fail while the dream skill is disabled")
	}
	msg := err.Error()
	for _, want := range []string{"dream is disabled", "skills.embedded_enabled", "/config", "restart"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message must contain %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "dream skill not found") {
		t.Errorf("the bare not-found message must be gone:\n%s", msg)
	}
}

// TestDreamCommand_RunsWhenSkillOptedIn: the positive counterpart — with the
// skill loaded (skills.embedded_enabled: [dream]) the command works instead of
// reporting a config complaint.
func TestDreamCommand_RunsWhenSkillOptedIn(t *testing.T) {
	ctx := dreamTestContext(t)
	skill := &skills.Skill{Meta: skills.SkillMeta{Name: "dream", Hidden: true}, Source: "embedded", Body: "consolidate"}
	ctx.SkillRegistry = newSkillRegistry(map[string]*skills.Skill{"dream": skill})

	if err := (&DreamCommand{}).Run(ctx, []string{"status"}); err != nil {
		t.Fatalf("status must work once the skill is loaded: %v", err)
	}
	out := ctx.OutputBuffer.String()
	if strings.Contains(out, "dream is disabled") {
		t.Errorf("the skill was loaded, so the disabled message must not appear:\n%s", out)
	}
	if !strings.Contains(out, "Dream mode:") {
		t.Errorf("status output missing:\n%s", out)
	}
}

// dreamTestContext builds a minimal context satisfying DreamCommand's
// prerequisites (memory enabled + store, provider manager, skill registry) so the
// skill gate — not a prerequisite — is what the test observes.
func dreamTestContext(t *testing.T) core.Context {
	t.Helper()
	var buf strings.Builder
	ctx := skillTestContext(&buf)
	ctx.Config = &config.Config{Memory: config.MemoryConfig{Enabled: true}}
	ctx.Config.ConfigDir = t.TempDir()
	ctx.MemoryStore = newFakeMemoryStore()
	ctx.ProviderManager = newTestProviderManager()
	return ctx
}
