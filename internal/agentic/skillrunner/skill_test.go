// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skillrunner

import (
	"testing"
)

type parseSkillMDTestCase struct {
	name      string
	content   string
	skillDir  string
	wantError bool
	check     func(*testing.T, *Skill)
}

func TestParseSkillMD(t *testing.T) {
	for _, tt := range parseSkillMDTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			skill, err := parseSkillMD(tt.content, tt.skillDir)
			if tt.wantError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, skill)
			}
		})
	}
}

func parseSkillMDTestCases() []parseSkillMDTestCase {
	return []parseSkillMDTestCase{
		validSkillCase(),
		{name: "missing_frontmatter_start", content: "name: test\ndescription: test\n---\nInstructions", skillDir: "/skills/test", wantError: true},
		{name: "missing_frontmatter_end", content: "---\nname: test\ndescription: test\n", skillDir: "/skills/test", wantError: true},
		{name: "missing_name", content: "---\ndescription: A test skill\n---\nInstructions", skillDir: "/skills/test", wantError: true},
		{name: "missing_description", content: "---\nname: test-skill\n---\nInstructions", skillDir: "/skills/test", wantError: true},
		emptyInstructionsCase(),
		withInputSchemaCase(),
		{name: "invalid_input_schema_json", content: "---\nname: test-skill\ndescription: A test skill\ninput-schema: {invalid json}\n---\nInstructions", skillDir: "/skills/test-skill", wantError: true},
		multilineInstructionsCase(),
		extraWhitespaceCase(),
		ignoresInvalidFrontmatterCase(),
	}
}

func validSkillCase() parseSkillMDTestCase {
	return parseSkillMDTestCase{
		name:     "valid_skill",
		content:  "---\nname: test-skill\ndescription: A test skill\n---\nThis is the instruction content.",
		skillDir: "/skills/test-skill",
		check: func(t *testing.T, s *Skill) {
			if s.Name != "test-skill" {
				t.Errorf("Name = %q, want %q", s.Name, "test-skill")
			}
			if s.Description != "A test skill" {
				t.Errorf("Description = %q, want %q", s.Description, "A test skill")
			}
			if s.Instructions != "This is the instruction content." {
				t.Errorf("Instructions = %q, want %q", s.Instructions, "This is the instruction content.")
			}
			if s.Path != "/skills/test-skill" {
				t.Errorf("Path = %q, want %q", s.Path, "/skills/test-skill")
			}
		},
	}
}

func emptyInstructionsCase() parseSkillMDTestCase {
	return parseSkillMDTestCase{
		name:     "empty_instructions",
		content:  "---\nname: test-skill\ndescription: A test skill\n---\n",
		skillDir: "/skills/test-skill",
		check: func(t *testing.T, s *Skill) {
			if s.Instructions != "" {
				t.Errorf("Instructions should be empty, got %q", s.Instructions)
			}
		},
	}
}

func withInputSchemaCase() parseSkillMDTestCase {
	return parseSkillMDTestCase{
		name:     "with_input_schema",
		content:  "---\nname: test-skill\ndescription: A test skill\ninput-schema: {\"type\": \"object\", \"properties\": {\"query\": {\"type\": \"string\"}}}\n---\nInstructions here.",
		skillDir: "/skills/test-skill",
		check: func(t *testing.T, s *Skill) {
			if s.InputSchema == nil {
				t.Error("InputSchema should not be nil")
			}
			if s.InputSchema["type"] != "object" {
				t.Errorf("InputSchema type = %v, want %q", s.InputSchema["type"], "object")
			}
		},
	}
}

func multilineInstructionsCase() parseSkillMDTestCase {
	return parseSkillMDTestCase{
		name:     "multiline_instructions",
		content:  "---\nname: test-skill\ndescription: A test skill\n---\nLine 1\nLine 2\nLine 3",
		skillDir: "/skills/test-skill",
		check: func(t *testing.T, s *Skill) {
			expected := "Line 1\nLine 2\nLine 3"
			if s.Instructions != expected {
				t.Errorf("Instructions = %q, want %q", s.Instructions, expected)
			}
		},
	}
}

func extraWhitespaceCase() parseSkillMDTestCase {
	return parseSkillMDTestCase{
		name:     "extra_whitespace_in_frontmatter",
		content:  "---\n  name: test-skill  \n  description: A test skill  \n---\nInstructions",
		skillDir: "/skills/test-skill",
		check: func(t *testing.T, s *Skill) {
			if s.Name != "test-skill" {
				t.Errorf("Name = %q, want %q", s.Name, "test-skill")
			}
		},
	}
}

func ignoresInvalidFrontmatterCase() parseSkillMDTestCase {
	return parseSkillMDTestCase{
		name:     "ignores_invalid_frontmatter_lines",
		content:  "---\nname: test-skill\ndescription: A test skill\ninvalid line without colon\n---\nInstructions",
		skillDir: "/skills/test-skill",
		check: func(t *testing.T, s *Skill) {
			if s.Name != "test-skill" {
				t.Errorf("Name = %q, want %q", s.Name, "test-skill")
			}
		},
	}
}

func TestSkillStruct(t *testing.T) {
	skill := &Skill{
		Name:         "test",
		Description:  "desc",
		Instructions: "instructions",
		InputSchema:  map[string]interface{}{"type": "object"},
		Path:         "/path",
	}

	if skill.Name != "test" {
		t.Errorf("Name = %q, want %q", skill.Name, "test")
	}
	if skill.Description != "desc" {
		t.Errorf("Description = %q, want %q", skill.Description, "desc")
	}
}

func TestParseSkillMDWithSkills(t *testing.T) {
	for _, tt := range parseSkillMDStringListCases() {
		t.Run(tt.name, func(t *testing.T) {
			skill, err := parseSkillMD(tt.content, "/skills/test-skill")
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			assertStringSliceEquals(t, "Skills", skill.Skills, tt.want)
		})
	}
}

func TestParseSkillMDWithTools(t *testing.T) {
	for _, tt := range parseSkillMDToolCases() {
		t.Run(tt.name, func(t *testing.T) {
			skill, err := parseSkillMD(tt.content, "/skills/test-skill")
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			assertStringSliceEquals(t, "Tools", skill.Tools, tt.want)
		})
	}
}

type skillStringListCase struct {
	name    string
	content string
	want    []string
	wantErr bool
}

func parseSkillMDStringListCases() []skillStringListCase {
	return []skillStringListCase{
		{
			name:    "with_skills",
			content: "---\nname: test-skill\ndescription: A test skill\nskills: [\"skill-a\", \"skill-b\"]\n---\nInstructions",
			want:    []string{"skill-a", "skill-b"},
		},
		{
			name:    "without_skills",
			content: "---\nname: test-skill\ndescription: A test skill\n---\nInstructions",
			want:    nil,
		},
		{
			name:    "invalid_skills_json",
			content: "---\nname: test-skill\ndescription: A test skill\nskills: not-json\n---\nInstructions",
			wantErr: true,
		},
		{
			name:    "empty_skills",
			content: "---\nname: test-skill\ndescription: A test skill\nskills: []\n---\nInstructions",
			want:    []string{},
		},
	}
}

func parseSkillMDToolCases() []skillStringListCase {
	return []skillStringListCase{
		{
			name:    "with_tools",
			content: "---\nname: test-skill\ndescription: A test skill\ntools: [\"tool1\", \"tool2\"]\n---\nInstructions",
			want:    []string{"tool1", "tool2"},
		},
		{
			name:    "without_tools",
			content: "---\nname: test-skill\ndescription: A test skill\n---\nInstructions",
			want:    nil,
		},
		{
			name:    "invalid_tools_json",
			content: "---\nname: test-skill\ndescription: A test skill\ntools: not-json\n---\nInstructions",
			wantErr: true,
		},
	}
}

func assertStringSliceEquals(t *testing.T, name string, got, want []string) {
	if len(want) != len(got) {
		t.Errorf("%s = %v, want %v", name, got, want)
		return
	}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Errorf("%s[%d] = %v, want %v", name, i, got, want)
			return
		}
	}
}
