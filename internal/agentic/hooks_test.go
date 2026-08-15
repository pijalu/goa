// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
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

// TestExecuteToolWithResult_CCBeforeToolVetoContextReachesModel is the P17
// acceptance at the agent level: a Claude Code dialect beforeTool hook denies
// the call with additionalContext, and the veto error (which the tool-result
// renderer surfaces to the model as "Error: ...") carries that context.
func TestExecuteToolWithResult_CCBeforeToolVetoContextReachesModel(t *testing.T) {
	cmd := `printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"fixture denies bash","additionalContext":"cc-context-for-model"}}'; exit 2`
	hookEngine := hooks.NewEngine(
		&hooks.Config{Hooks: []hooks.Hook{
			{
				Event:          hooks.EventBeforeTool,
				Command:        cmd,
				Dialect:        hooks.DialectClaudeCode,
				Matcher:        "hookmock",
				TimeoutSeconds: 5,
			},
		}},
		nil,
	)
	agent := NewAgent(Config{
		Tools:      []Tool{hookMockTool{}},
		HookEngine: hookEngine,
		ProjectDir: t.TempDir(),
	})

	_, err := agent.executeToolWithResult(context.Background(), "hookmock", `{}`, "call_1")
	if err == nil {
		t.Fatal("expected the CC beforeTool hook to veto the tool call")
	}
	if !strings.Contains(err.Error(), "cc-context-for-model") {
		t.Errorf("veto error must carry additionalContext so the model sees it: %v", err)
	}
	if !strings.Contains(err.Error(), "fixture denies bash") {
		t.Errorf("veto error must carry the deny reason: %v", err)
	}
	// The model sees the veto through the tool-result renderer: resolve the
	// result content and assert the context is present in the rendered message.
	rendered := agent.resolveToolResultContent(provider.ContentBlock{
		Type: provider.ContentBlockToolCall, ToolName: "hookmock", ToolCallID: "call_1",
	}, map[string]ToolCallResult{"call_1": {Name: "hookmock", Err: err}})
	if !strings.Contains(rendered, "cc-context-for-model") {
		t.Errorf("model-visible tool result must carry additionalContext, got: %q", rendered)
	}
	if !strings.Contains(rendered, "fixture denies bash") {
		t.Errorf("model-visible tool result must carry the deny reason, got: %q", rendered)
	}
}

type hookMockTool struct{ BaseTool }

func (hookMockTool) Schema() ToolSchema {
	return ToolSchema{Name: "hookmock", Schema: map[string]any{"type": "object"}}
}

func (hookMockTool) Execute(input string) (string, error) { return "ok", nil }
