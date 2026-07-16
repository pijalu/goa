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
	_ = RenderAvailableSkills(fr, skills, true)
	if fr.lastData == nil {
		t.Fatal("expected renderer to receive data")
	}
	rendered, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	want := map[string]string{
		"inline": "read",
		"action": "run_skill",
		"sub":    "run_skill",
	}
	for _, s := range rendered.Skills {
		if s.ExecuteTool != want[s.Name] {
			t.Errorf("%s ExecuteTool = %q, want %q", s.Name, s.ExecuteTool, want[s.Name])
		}
	}
}

// When the run_skill tool is not registered (inline execution mode), action
// skills must not be advertised with tool="run_skill" — the model would call
// a nonexistent tool. They are invocable via the /skill:run:<name> command.
func TestRenderAvailableSkills_NoRunSkillTool(t *testing.T) {
	fr := &fakeRenderer{rendered: "rendered"}
	skills := []SkillSummary{
		{Name: "inline", Description: "Inline", Category: "knowledge"},
		{Name: "action", Description: "Action", Category: "action"},
		{Name: "sub", Description: "Sub", RequiresSubAgent: true},
	}
	_ = RenderAvailableSkills(fr, skills, false)
	rendered, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	for _, s := range rendered.Skills {
		if s.ExecuteTool == "run_skill" {
			t.Errorf("%s: must not advertise run_skill when the tool is unavailable", s.Name)
		}
	}
	want := map[string]string{
		"inline": "read",
		"action": "/skill:run:action",
		"sub":    "/skill:run:sub",
	}
	for _, s := range rendered.Skills {
		if s.ExecuteTool != want[s.Name] {
			t.Errorf("%s ExecuteTool = %q, want %q", s.Name, s.ExecuteTool, want[s.Name])
		}
	}
}

func TestEscapeSkills_XMLEscaping(t *testing.T) {
	skills := []SkillSummary{
		{Name: "a&b", Description: "x<y", Category: "action"},
	}
	out := escapeSkills(skills, true)
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
