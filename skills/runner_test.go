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

// writeRunnerTestSkill creates a SKILL.md under dir/<name>/ and returns a
// registry loaded from dir.
func writeRunnerTestSkill(t *testing.T, dir, name, body string) *SkillRegistry {
	t.Helper()
	skillRoot := filepath.Join(dir, name)
	if err := os.MkdirAll(skillRoot, 0755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\ncategory: action\n---\n" + body
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	reg := NewSkillRegistry([]string{dir})
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	return reg
}

// TestSkillRunnerTool_InlineReturnsInstructions is the regression test for
// the exported session where the model could not execute skills: with
// execution_mode=inline the run_skill tool was never registered, so action
// skills were advertised via the user-only /skill:run:<name> command and the
// model had no invocation path. In inline mode the tool must return the
// skill instructions as its result — without needing an AgentPool.
func TestSkillRunnerTool_InlineReturnsInstructions(t *testing.T) {
	reg := writeRunnerTestSkill(t, t.TempDir(), "commit-msg", "Generate a conventional commit message.")

	// nil pool: inline execution must not touch the sub-agent pool.
	tool := NewSkillRunnerTool(reg, nil, nil, true)

	out, err := tool.Execute(`{"skill_name":"commit-msg","task":"write message for staged changes"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "# Skill: commit-msg") {
		t.Errorf("missing skill header:\n%s", out)
	}
	if !strings.Contains(out, "Generate a conventional commit message.") {
		t.Errorf("missing skill body:\n%s", out)
	}
	if !strings.Contains(out, "write message for staged changes") {
		t.Errorf("missing task:\n%s", out)
	}
}

// TestSkillRunnerTool_InlineRendersTemplate verifies the inline result goes
// through the skill_inline_result template when a renderer is available.
func TestSkillRunnerTool_InlineRendersTemplate(t *testing.T) {
	reg := writeRunnerTestSkill(t, t.TempDir(), "review", "Review the code.")

	var gotName, gotMode string
	renderer := &captureRenderer{render: func(name string, data map[string]string) string {
		gotName = name
		gotMode = data["SkillName"]
		return "[rendered] " + data["Output"]
	}}
	tool := NewSkillRunnerTool(reg, nil, renderer, true)

	out, err := tool.Execute(`{"skill_name":"review","task":"review tui/"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotName != "skill_inline_result" {
		t.Errorf("template = %q, want skill_inline_result", gotName)
	}
	if gotMode != "review" {
		t.Errorf("SkillName = %q, want review", gotMode)
	}
	if !strings.HasPrefix(out, "[rendered] ") {
		t.Errorf("expected rendered output, got:\n%s", out)
	}
}

// TestSkillRunnerTool_SubAgentModeRequiresPool verifies sub-agent mode still
// fails fast with a clear error when no pool is configured (inline mode must
// not have this requirement).
func TestSkillRunnerTool_SubAgentModeRequiresPool(t *testing.T) {
	reg := writeRunnerTestSkill(t, t.TempDir(), "review", "Review the code.")
	tool := NewSkillRunnerTool(reg, nil, nil, false)

	_, err := tool.Execute(`{"skill_name":"review","task":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "AgentPool") {
		t.Errorf("expected AgentPool error in sub-agent mode, got: %v", err)
	}
}

// TestSkillRunnerTool_UnknownSkill verifies the error path is identical in
// both modes.
func TestSkillRunnerTool_UnknownSkill(t *testing.T) {
	reg := writeRunnerTestSkill(t, t.TempDir(), "review", "Review the code.")
	tool := NewSkillRunnerTool(reg, nil, nil, true)

	_, err := tool.Execute(`{"skill_name":"nope","task":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

// captureRenderer implements PromptRenderer capturing the template payload.
type captureRenderer struct {
	render func(name string, data map[string]string) string
}

func (c *captureRenderer) Render(name string, data interface{}) (string, error) {
	m, _ := data.(map[string]string)
	return c.render(name, m), nil
}

// TestSkillRunnerTool_InlineStripsLicenseHeaders is the regression for
// "run_skill should bypass all headers": SKILL.md files carrying an SPDX
// HTML comment header and a "[Skill: name]" marker must produce clean output
// containing only actionable instructions.
func TestSkillRunnerTool_InlineStripsLicenseHeaders(t *testing.T) {
	body := `<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

[Skill: commit-msg]

# Commit Message Skill

Generate a conventional commit message from the current git diff.
`
	reg := writeRunnerTestSkill(t, t.TempDir(), "commit-msg", body)
	tool := NewSkillRunnerTool(reg, nil, nil, true)

	out, err := tool.Execute(`{"skill_name":"commit-msg","task":"write message"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, noise := range []string{"SPDX-License", "Copyright (C)", "<!--", "-->", "[Skill: commit-msg]"} {
		if strings.Contains(out, noise) {
			t.Errorf("inline result contains header noise %q:\n%s", noise, out)
		}
	}
	if !strings.Contains(out, "# Commit Message Skill") {
		t.Errorf("actionable content was stripped along with the noise:\n%s", out)
	}
	if !strings.Contains(out, "Generate a conventional commit message") {
		t.Errorf("skill instructions missing:\n%s", out)
	}
}

// TestSkillRunnerTool_InlineFramesExecution is the regression for
// "inline mode is not clear — the LLM does not understand what to do": the
// result must frame the skill as an ACTIVE execution context (imperative,
// do-not-just-read) rather than a bare markdown dump with a passive trailer.
func TestSkillRunnerTool_InlineFramesExecution(t *testing.T) {
	reg := writeRunnerTestSkill(t, t.TempDir(), "commit-msg", "Generate a commit message.")
	tool := NewSkillRunnerTool(reg, nil, nil, true)

	out, err := tool.Execute(`{"skill_name":"commit-msg","task":"write message"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "You are now executing") {
		t.Errorf("missing execution framing:\n%s", out)
	}
	if !strings.Contains(out, "Do NOT quote, summarize, or describe") {
		t.Errorf("missing anti-documentation instruction:\n%s", out)
	}
	if !strings.Contains(out, "Begin executing now") {
		t.Errorf("missing begin-now directive:\n%s", out)
	}
	if strings.Contains(out, "Follow the skill instructions above") {
		t.Errorf("old passive trailer still present:\n%s", out)
	}
}

// TestStripSkillNoise covers the noise stripper directly: multiple leading
// comment blocks, markers, and preservation of real content.
func TestStripSkillNoise(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		noise []string
	}{
		{
			name:  "spdx header",
			in:    "<!--\nSPDX-License-Identifier: GPL-3.0-or-later\n-->\n\n# Real\nDo things.",
			want:  "# Real\nDo things.",
			noise: []string{"SPDX", "<!--", "-->"},
		},
		{
			name:  "multiple comment blocks",
			in:    "<!-- a -->\n<!-- b -->\n# Content",
			want:  "# Content",
			noise: []string{"<!--"},
		},
		{
			name:  "skill marker lines",
			in:    "[Skill: foo]\n# Content\n[Skill: bar]\ntext",
			want:  "# Content\ntext",
			noise: []string{"[Skill:"},
		},
		{
			name:  "no noise unchanged",
			in:    "# Content\nBody text.",
			want:  "# Content\nBody text.",
			noise: nil,
		},
		{
			name:  "unterminated comment keeps content",
			in:    "<!-- broken\n# Content",
			want:  "<!-- broken\n# Content",
			noise: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripSkillNoise(tt.in)
			if got != tt.want {
				t.Errorf("StripSkillNoise() = %q, want %q", got, tt.want)
			}
			for _, n := range tt.noise {
				if strings.Contains(got, n) {
					t.Errorf("output still contains %q: %q", n, got)
				}
			}
		})
	}
}
