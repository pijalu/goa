// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package perms

import (
	"strings"
	"testing"
)

func plannerGuard() GuardConfig {
	return GuardConfig{
		Rules: []GuardRule{
			{
				Tools: []string{"write", "edit"},
				Expr:  "regexMatch(path, `\\.goa/plan`) || regexMatch(path, `\\.agents/plan`) || regexMatch(path, `(?i)plan[^/]*\\.md$`)",
				Message: "Planner mode restricts writes to plan directories (.goa/plan, .agents/plan) or markdown files with \"plan\" in the filename.",
			},
			{
				Tools: []string{"bash"},
				Expr:  "regexMatch(path, `\\.goa/plan`) || regexMatch(path, `\\.agents/plan`)",
				Message: "Planner mode restricts bash commands to plan directories (.goa/plan, .agents/plan).",
			},
		},
	}
}

func TestAccessGuard_ReadAllowedAnywhere(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	if err := g.Validate("read", `{"path":"/etc/passwd"}`); err != nil {
		t.Errorf("read should be allowed anywhere, got %v", err)
	}
}

func TestAccessGuard_WriteInsidePlanDirAllowed(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	if err := g.Validate("write", `{"path":"/project/.goa/plan/notes.md"}`); err != nil {
		t.Errorf("write inside .goa/plan should be allowed, got %v", err)
	}
}

func TestAccessGuard_WriteInsideAgentsPlanAllowed(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	if err := g.Validate("write", `{"path":"/project/.agents/plan/notes.md"}`); err != nil {
		t.Errorf("write inside .agents/plan should be allowed, got %v", err)
	}
}

func TestAccessGuard_WritePlanMarkdownAllowed(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	if err := g.Validate("write", `{"path":"/project/PLAN.md"}`); err != nil {
		t.Errorf("write to PLAN.md should be allowed, got %v", err)
	}
}

func TestAccessGuard_WriteOutsideBlockedWithHint(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	err := g.Validate("write", `{"path":"/etc/passwd"}`)
	if err == nil {
		t.Fatal("expected write outside plan rules to be blocked")
	}
	if !strings.Contains(err.Error(), "Planner mode restricts") {
		t.Errorf("expected planner restriction error, got %v", err)
	}
	if !strings.Contains(err.Error(), ".goa/plan") {
		t.Errorf("expected hint to mention .goa/plan, got %v", err)
	}
}

func TestAccessGuard_BashInsideAllowed(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	if err := g.Validate("bash", "cat /project/.goa/plan.md"); err != nil {
		t.Errorf("bash inside .goa/plan should be allowed, got %v", err)
	}
}

func TestAccessGuard_BashOutsideBlocked(t *testing.T) {
	g := NewAccessGuard(plannerGuard())
	err := g.Validate("bash", "cat /etc/passwd")
	if err == nil {
		t.Fatal("expected bash outside plan dirs to be blocked")
	}
	if !strings.Contains(err.Error(), "Planner mode restricts") {
		t.Errorf("expected planner restriction error, got %v", err)
	}
}

func TestAccessGuard_RegexAllow(t *testing.T) {
	cfg := GuardConfig{
		Rules: []GuardRule{
			{
				Tools:   []string{"write"},
				Allow:   []string{`\.goa/plan$`, `PLAN\.md$`},
				Message: "write restricted",
			},
		},
	}
	g := NewAccessGuard(cfg)
	if err := g.Validate("write", `{"path":"/project/.goa/plan"}`); err != nil {
		t.Errorf("regex allow should match, got %v", err)
	}
	if err := g.Validate("write", `{"path":"/project/PLAN.md"}`); err != nil {
		t.Errorf("regex allow should match PLAN.md, got %v", err)
	}
	if err := g.Validate("write", `{"path":"/project/other.md"}`); err == nil {
		t.Fatal("expected regex deny")
	}
}

func TestAccessGuard_NoRulesAllowsAll(t *testing.T) {
	g := NewAccessGuard(GuardConfig{})
	if err := g.Validate("write", `{"path":"/etc/passwd"}`); err != nil {
		t.Errorf("empty guard should allow all, got %v", err)
	}
}
