// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"strings"
	"testing"
)

type fakeRenderer struct {
	lastData interface{}
	rendered string
}

func (f *fakeRenderer) Render(name string, data interface{}) (string, error) {
	f.lastData = data
	return f.rendered, nil
}

func TestRenderAvailableSkills_ExecuteToolPerCategory(t *testing.T) {
	fr := &fakeRenderer{rendered: "rendered"}
	skills := []SkillSummary{
		{Name: "inline", Description: "Inline", Category: "knowledge"},
		{Name: "action", Description: "Action", Category: "action"},
		{Name: "sub", Description: "Sub", RequiresSubAgent: true},
	}
	_ = RenderAvailableSkills(fr, skills)
	if fr.lastData == nil {
		t.Fatal("expected renderer to receive data")
	}
	rendered, ok := fr.lastData.([]safeSkill)
	if !ok {
		t.Fatalf("expected []safeSkill, got %T", fr.lastData)
	}
	want := map[string]string{
		"inline": "read",
		"action": "run_skill",
		"sub":    "run_skill",
	}
	for _, s := range rendered {
		if s.ExecuteTool != want[s.Name] {
			t.Errorf("%s ExecuteTool = %q, want %q", s.Name, s.ExecuteTool, want[s.Name])
		}
	}
}

func TestEscapeSkills_XMLEscaping(t *testing.T) {
	skills := []SkillSummary{
		{Name: "a&b", Description: "x<y", Category: "action"},
	}
	out := escapeSkills(skills)
	if len(out) != 1 {
		t.Fatal("expected one safe skill")
	}
	if !strings.Contains(out[0].Name, "&amp;") {
		t.Errorf("expected XML-escaped name, got %q", out[0].Name)
	}
	if !strings.Contains(out[0].Description, "&lt;") {
		t.Errorf("expected XML-escaped description, got %q", out[0].Description)
	}
}
