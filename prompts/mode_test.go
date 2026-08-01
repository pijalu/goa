// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package prompts

import (
	"strings"
	"testing"
)

func TestLoadMode_ParsesGuard(t *testing.T) {
	r := NewRegistry(EmbeddedFS())
	def, err := r.LoadMode("planner")
	if err != nil {
		t.Fatalf("LoadMode(planner): %v", err)
	}
	if len(def.Guard.Rules) == 0 {
		t.Fatal("expected planner guard rules")
	}
	foundWrite := false
	for _, rule := range def.Guard.Rules {
		for _, tool := range rule.Tools {
			if tool == "write" {
				foundWrite = true
			}
		}
	}
	if !foundWrite {
		t.Error("expected planner guard to include write tool")
	}
}

func TestLoadMode_CodingPosture(t *testing.T) {
	r := NewRegistry(EmbeddedFS())
	def, err := r.LoadMode("coding-posture")
	if err != nil {
		t.Fatalf("LoadMode(coding-posture): %v", err)
	}
	if def.DefaultAutonomy != "solo" {
		t.Errorf("DefaultAutonomy = %q, want solo", def.DefaultAutonomy)
	}
	if !strings.Contains(def.Body, "Coding Posture") {
		t.Error("expected Coding Posture body")
	}
}

func TestLoadMode_MissingGuardIsEmpty(t *testing.T) {
	r := NewRegistry(EmbeddedFS())
	def, err := r.LoadMode("coder")
	if err != nil {
		t.Fatalf("LoadMode(coder): %v", err)
	}
	if len(def.Guard.Rules) != 0 {
		t.Errorf("expected coder guard to be empty, got %d rules", len(def.Guard.Rules))
	}
}

// TestLoadOrchestratePrompt_HubAntiFinalizeNudge is the F3 regression guard:
// the hub orchestrator prompt must carry the explicit "do not finalize while
// required sub-tasks remain" nudge (qwen3.5-9b showed a single-delegation bias,
// closing out after the first specialist result on soft objectives).
func TestLoadOrchestratePrompt_HubAntiFinalizeNudge(t *testing.T) {
	prompt, err := LoadOrchestratePrompt("hub_orchestrator", "")
	if err != nil {
		t.Fatalf("LoadOrchestratePrompt(hub_orchestrator): %v", err)
	}
	if !strings.Contains(strings.ToLower(prompt), "do not finalize while required sub-tasks remain") {
		t.Error("hub orchestrator prompt missing the anti-finalize nudge (F3)")
	}
}

// TestModeBodySizeCeiling is a build-time context guard: the active mode body
// is the first section of every system prompt, so each embedded mode body
// stays ≤ 3000 chars and carries no HTML comments (stripped at parse).
func TestModeBodySizeCeiling(t *testing.T) {
	r := NewRegistry(EmbeddedFS())
	modes, err := r.ListModes()
	if err != nil {
		t.Fatalf("ListModes: %v", err)
	}
	if len(modes) == 0 {
		t.Fatal("no embedded modes found")
	}
	const ceiling = 3000
	for _, major := range modes {
		def, err := r.LoadMode(major)
		if err != nil {
			t.Errorf("LoadMode(%q): %v", major, err)
			continue
		}
		if len(def.Body) > ceiling {
			t.Errorf("mode %q body = %d chars, ceiling %d — it heads every system prompt in that mode; keep it dense",
				major, len(def.Body), ceiling)
		}
		for _, banned := range []string{"SPDX-License-Identifier", "<!--"} {
			if strings.Contains(def.Body, banned) {
				t.Errorf("mode %q body contains %q — comments must be stripped", major, banned)
			}
		}
	}
}
