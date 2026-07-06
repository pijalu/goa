// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestChatViewportAgentStream_TwoIndependentAgents is a RED test that asserts
// two agent-scoped streams can update independently in the same chat viewport.
func TestChatViewportAgentStream_TwoIndependentAgents(t *testing.T) {
	cv := NewChatViewport()

	cv.AddAgentThinkingBlock("coder", "planning", true)
	cv.UpdateAgentThinking("coder", "planning the design")
	cv.AddAgentContent("coder", "hello")
	cv.UpdateAgentContent("coder", "hello world")

	cv.AddAgentThinkingBlock("reviewer", "checking", true)
	cv.UpdateAgentThinking("reviewer", "checking the code")
	cv.AddAgentContent("reviewer", "lgtm")
	cv.UpdateAgentContent("reviewer", "lgtm!")

	rendered := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(rendered, "coder thinking") {
		t.Errorf("expected coder thinking block, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "reviewer thinking") {
		t.Errorf("expected reviewer thinking block, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "planning the design") {
		t.Errorf("expected accumulated coder thinking, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "checking the code") {
		t.Errorf("expected accumulated reviewer thinking, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "hello world") {
		t.Errorf("expected accumulated coder content, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "lgtm!") {
		t.Errorf("expected accumulated reviewer content, got:\n%s", rendered)
	}
	if strings.Count(rendered, "[coder]") < 1 {
		t.Errorf("expected coder label on content block, got:\n%s", rendered)
	}
	if strings.Count(rendered, "[reviewer]") < 1 {
		t.Errorf("expected reviewer label on content block, got:\n%s", rendered)
	}
}

// TestChatViewportAgentStream_ToolExecutionIsAgentLabelled asserts that
// agent-scoped tool calls render with the agent label and stay independent.
func TestChatViewportAgentStream_ToolExecutionIsAgentLabelled(t *testing.T) {
	cv := NewChatViewport()
	cv.AddAgentToolExecution("coder", "bash", `{"command":"ls"}`)
	cv.AddAgentToolExecution("reviewer", "read", `{"path":"x.txt"}`)

	rendered := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(rendered, "ls") {
		t.Errorf("expected coder bash tool widget, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "x.txt") {
		t.Errorf("expected reviewer read tool widget, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[coder]") {
		t.Errorf("expected coder label on tool widget, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[reviewer]") {
		t.Errorf("expected reviewer label on tool widget, got:\n%s", rendered)
	}
}
