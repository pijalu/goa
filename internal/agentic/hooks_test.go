// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/hooks"
)

func TestExecuteToolWithResult_BeforeToolVeto(t *testing.T) {
	called := false
	hookEngine := hooks.NewEngine(
		&hooks.Config{Hooks: []hooks.Hook{
			{
				Event:   hooks.EventBeforeTool,
				Command: "sh",
				Args:    []string{"-c", "exit 1"},
			},
		}},
		nil,
	)
	agent := NewAgent(Config{
		Tools:      []Tool{hookMockTool{}},
		HookEngine: hookEngine,
	})

	_, err := agent.executeToolWithResult(context.Background(), "hookmock", `{}`, "call_1")
	if err == nil {
		t.Fatal("expected beforeTool hook veto")
	}
	_ = called
}

func TestExecuteToolWithResult_AfterToolFires(t *testing.T) {
	hookEngine := hooks.NewEngine(
		&hooks.Config{Hooks: []hooks.Hook{
			{
				Event:   hooks.EventAfterTool,
				Command: "sh",
				Args:    []string{"-c", "cat"},
			},
		}},
		nil,
	)
	agent := NewAgent(Config{
		Tools:      []Tool{hookMockTool{}},
		HookEngine: hookEngine,
	})

	_, err := agent.executeToolWithResult(context.Background(), "hookmock", `{}`, "call_2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := hookEngine.Store().Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 hook audit entry, got %d", len(entries))
	}
	if entries[0].Event != hooks.EventAfterTool {
		t.Errorf("expected afterTool event, got %v", entries[0].Event)
	}
}

type hookMockTool struct{ BaseTool }

func (hookMockTool) Schema() ToolSchema {
	return ToolSchema{Name: "hookmock", Schema: map[string]any{"type": "object"}}
}

func (hookMockTool) Execute(input string) (string, error) { return "ok", nil }
