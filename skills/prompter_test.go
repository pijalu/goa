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
		{Name: "inline", Description: "Inline", Category: "knowledge", ModelInvocable: true, UserInvocable: true},
		{Name: "action", Description: "Action", Category: "action", ModelInvocable: true, UserInvocable: true},
		{Name: "sub", Description: "Sub", RequiresSubAgent: true, ModelInvocable: true, UserInvocable: true},
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
		{Name: "inline", Description: "Inline", Category: "knowledge", ModelInvocable: true, UserInvocable: true},
		{Name: "action", Description: "Action", Category: "action", ModelInvocable: true, UserInvocable: true},
		{Name: "sub", Description: "Sub", RequiresSubAgent: true, ModelInvocable: true, UserInvocable: true},
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

// TestRenderAvailableSkills_FiltersModelInvocable is the P16 acceptance that
// the model-facing <available_skills> catalog excludes skills the model
// cannot invoke: model_invocable:false, and (per the acceptance) any
// user_invocable:false skill.
func TestRenderAvailableSkills_FiltersModelInvocable(t *testing.T) {
	fr := &fakeRenderer{rendered: "rendered"}
	skills := []SkillSummary{
		{Name: "plain", Description: "P", Category: "action", ModelInvocable: true, UserInvocable: true},
		{Name: "model-off", Description: "M", Category: "action", ModelInvocable: false, UserInvocable: true},
		{Name: "user-off", Description: "U", Category: "action", ModelInvocable: true, UserInvocable: false},
	}
	_ = RenderAvailableSkills(fr, skills, true)
	rendered, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	got := map[string]bool{}
	for _, s := range rendered.Skills {
		got[s.Name] = true
	}
	if !got["plain"] {
		t.Errorf("plain skill should be advertised to the model: %v", got)
	}
	if got["model-off"] {
		t.Errorf("model_invocable:false skill must not be advertised to the model: %v", got)
	}
	if got["user-off"] {
		t.Errorf("user_invocable:false skill must not be advertised to the model (P16 acceptance): %v", got)
	}
}
