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
