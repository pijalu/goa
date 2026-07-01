// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/perms"
)

type echoSoloTool struct{ BaseTool }

func (echoSoloTool) Schema() ToolSchema {
	return ToolSchema{Name: "echo", Description: "echo"}
}
func (echoSoloTool) Execute(input string) (string, error) {
	return input, nil
}

func makeSoloAgent(base string, autonomy internal.AutonomyLevel) *Agent {
	return NewAgent(Config{
		Model:       agenticprovider.Model{ID: "test"},
		Tools:       []Tool{echoSoloTool{}},
		ProjectDir:  base,
		GetAutonomy: func() internal.AutonomyLevel { return autonomy },
	})
}

func TestAgent_SoloMode_BlocksBashOutsidePath(t *testing.T) {
	base := t.TempDir()
	a := makeSoloAgent(base, internal.AutonomySolo)

	_, err := a.executeToolWithResult(context.Background(), "bash", "cat /etc/passwd")
	if err == nil {
		t.Fatal("expected SOLO mode to block bash outside path")
	}
	if !strings.Contains(err.Error(), "SOLO mode restriction") {
		t.Errorf("expected SOLO restriction error, got %v", err)
	}
}

func TestAgent_SoloMode_AllowsBashInsidePath(t *testing.T) {
	base := t.TempDir()
	a := makeSoloAgent(base, internal.AutonomySolo)

	// The SOLO guard should allow the command; because no "bash" tool is
	// registered, execution then fails with "unknown tool" rather than the
	// SOLO restriction error.
	_, err := a.executeToolWithResult(context.Background(), "bash", "cat file.txt")
	if err == nil {
		t.Fatal("expected error because bash tool is not registered")
	}
	if strings.Contains(err.Error(), "SOLO mode restriction") {
		t.Errorf("did not expect SOLO restriction for inside path, got %v", err)
	}
}

func TestAgent_SoloMode_BlocksGitPush(t *testing.T) {
	base := t.TempDir()
	a := makeSoloAgent(base, internal.AutonomySolo)

	_, err := a.executeToolWithResult(context.Background(), "git", "push origin main")
	if err == nil {
		t.Fatal("expected SOLO mode to block git push")
	}
	if !strings.Contains(err.Error(), "SOLO mode restriction") {
		t.Errorf("expected SOLO restriction error, got %v", err)
	}
}

func TestAgent_SoloMode_NotActiveWhenOtherAutonomy(t *testing.T) {
	base := t.TempDir()
	a := makeSoloAgent(base, internal.AutonomyYolo)

	// Outside path is allowed outside SOLO mode; tool lookup fails because
	// no "bash" tool is registered.
	_, err := a.executeToolWithResult(context.Background(), "bash", "cat /etc/passwd")
	if err == nil {
		t.Fatal("expected error because bash tool is not registered")
	}
	if strings.Contains(err.Error(), "SOLO mode restriction") {
		t.Errorf("did not expect SOLO restriction outside SOLO mode, got %v", err)
	}
}

func TestAgent_ConfirmMode_CallsConfirmTool(t *testing.T) {
	base := t.TempDir()
	called := false
	a := NewAgent(Config{
		Model:      agenticprovider.Model{ID: "test"},
		Tools:      []Tool{echoSoloTool{}},
		ProjectDir: base,
		GetAutonomy: func() internal.AutonomyLevel { return internal.AutonomyConfirm },
		ConfirmTool: func(ctx context.Context, toolName, input string) (bool, error) {
			called = true
			return true, nil
		},
	})

	// bash inside project in confirm mode should trigger ConfirmTool.
	_, err := a.executeToolWithResult(context.Background(), "bash", `{"command":"ls"}`)
	if err == nil {
		t.Fatal("expected error because bash tool is not registered")
	}
	if !called {
		t.Error("expected ConfirmTool to be called in confirm mode")
	}
}

func TestAgent_ConfirmMode_DeniesWhenConfirmToolReturnsFalse(t *testing.T) {
	base := t.TempDir()
	a := NewAgent(Config{
		Model:      agenticprovider.Model{ID: "test"},
		Tools:      []Tool{echoSoloTool{}},
		ProjectDir: base,
		GetAutonomy: func() internal.AutonomyLevel { return internal.AutonomyConfirm },
		ConfirmTool: func(ctx context.Context, toolName, input string) (bool, error) {
			return false, nil
		},
	})

	_, err := a.executeToolWithResult(context.Background(), "bash", `{"command":"ls"}`)
	if err == nil {
		t.Fatal("expected error when ConfirmTool denies")
	}
	if !strings.Contains(err.Error(), "not approved") {
		t.Errorf("expected approval error, got %v", err)
	}
}

func plannerGuard() perms.GuardConfig {
	return perms.GuardConfig{
		Rules: []perms.GuardRule{
			{
				Tools: []string{"write", "edit"},
				Expr:  `regexMatch(path, ` + "`" + `\.goa/plan` + "`" + `) || regexMatch(path, ` + "`" + `\.agents/plan` + "`" + `) || regexMatch(path, ` + "`" + `(?i)plan[^/]*\.md$` + "`" + `)`,
				Message: "Planner mode restricts writes to plan directories (.goa/plan, .agents/plan) or markdown files with \"plan\" in the filename.",
			},
			{
				Tools: []string{"bash"},
				Expr:  `regexMatch(path, ` + "`" + `\.goa/plan` + "`" + `) || regexMatch(path, ` + "`" + `\.agents/plan` + "`" + `)`,
				Message: "Planner mode restricts bash commands to plan directories (.goa/plan, .agents/plan).",
			},
		},
	}
}

func makePlanAgent(base string) *Agent {
	return NewAgent(Config{
		Model:          agenticprovider.Model{ID: "test"},
		Tools:          []Tool{echoSoloTool{}},
		ProjectDir:     base,
		GetGuardConfig: func() perms.GuardConfig { return plannerGuard() },
	})
}

func TestAgent_PlanMode_BlocksWriteOutsidePlanDir(t *testing.T) {
	base := t.TempDir()
	a := makePlanAgent(base)

	_, err := a.executeToolWithResult(context.Background(), "write", `{"path":"/etc/passwd"}`)
	if err == nil {
		t.Fatal("expected plan mode to block write outside plan dir")
	}
	if !strings.Contains(err.Error(), "Planner mode restricts") {
		t.Errorf("expected plan restriction error, got %v", err)
	}
}

func TestAgent_PlanMode_AllowsWriteInsidePlanDir(t *testing.T) {
	base := t.TempDir()
	a := makePlanAgent(base)

	// The plan guard allows the write; because no "write" tool is registered,
	// execution fails with "unknown tool" rather than the plan restriction.
	_, err := a.executeToolWithResult(context.Background(), "write", `{"path":"`+base+`/.goa/plan/notes.md"}`)
	if err == nil {
		t.Fatal("expected error because write tool is not registered")
	}
	if strings.Contains(err.Error(), "Planner mode restricts") {
		t.Errorf("did not expect plan restriction for inside path, got %v", err)
	}
}

func TestAgent_PlanMode_AllowsPlanMarkdown(t *testing.T) {
	base := t.TempDir()
	a := makePlanAgent(base)

	// The plan guard allows PLAN.md; because no "write" tool is registered,
	// execution fails with "unknown tool" rather than the plan restriction.
	_, err := a.executeToolWithResult(context.Background(), "write", `{"path":"`+base+`/PLAN.md"}`)
	if err == nil {
		t.Fatal("expected error because write tool is not registered")
	}
	if strings.Contains(err.Error(), "Planner mode restricts") {
		t.Errorf("did not expect plan restriction for PLAN.md, got %v", err)
	}
}

func TestAgent_PlanMode_NotActiveWithoutGuard(t *testing.T) {
	base := t.TempDir()
	a := NewAgent(Config{
		Model:          agenticprovider.Model{ID: "test"},
		Tools:          []Tool{echoSoloTool{}},
		ProjectDir:     base,
		GetGuardConfig: func() perms.GuardConfig { return perms.GuardConfig{} },
	})

	_, err := a.executeToolWithResult(context.Background(), "write", `{"path":"/etc/passwd"}`)
	if err == nil {
		t.Fatal("expected error because write tool is not registered")
	}
	if strings.Contains(err.Error(), "Planner mode restricts") {
		t.Errorf("did not expect plan restriction without guard, got %v", err)
	}
}
