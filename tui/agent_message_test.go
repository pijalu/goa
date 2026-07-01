// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func TestAgentMessage_Render_WithContent(t *testing.T) {
	msg := newAgentMessage("Hello from reviewer", "reviewer")
	lines := msg.Render(60)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	// First line should include the agent prefix
	firstLine := lines[1]
	if !strings.Contains(firstLine, "[reviewer]") {
		t.Errorf("expected agent prefix [reviewer], got %q", firstLine)
	}
}

func TestAgentMessage_Render_EmptyReturnsNil(t *testing.T) {
	msg := newAgentMessage("", "planner")
	lines := msg.Render(60)
	if lines != nil {
		t.Error("expected nil for empty content")
	}
}

func TestAgentMessage_Render_ColorPerAgent(t *testing.T) {
	msg1 := newAgentMessage("content", "reviewer")
	msg2 := newAgentMessage("content", "coder")

	lines1 := msg1.Render(60)
	lines2 := msg2.Render(60)

	if len(lines1) == 0 || len(lines2) == 0 {
		t.Fatal("expected both to render")
	}

	// Different agents may have different prefix colors
	// We just verify both render without crashing
	_ = lines1[0]
	_ = lines2[0]
}

func TestAgentMessage_Render_ContentWrapped(t *testing.T) {
	msg := newAgentMessage("short", "planner")
	lines := msg.Render(20)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}
	if !strings.Contains(lines[1], "short") {
		t.Errorf("expected content in rendered line, got %q", lines[1])
	}
}

func TestAgentMessage_TrailingNewline(t *testing.T) {
	msg := newAgentMessage("test content", "coder")
	lines := msg.Render(40)
	if len(lines) < 3 {
		t.Fatal("expected at least 3 lines (leading + content + trailing)")
	}
	// First line should be empty (leading spacer)
	if lines[0] != "" {
		t.Errorf("expected leading empty line, got %q", lines[0])
	}
	// Last line should be empty (trailing spacer)
	if lines[len(lines)-1] != "" {
		t.Errorf("expected trailing empty line, got %q", lines[len(lines)-1])
	}
}

func TestAddAgentMessage_RendersWithoutCrash(t *testing.T) {
	cv := NewChatViewport()
	cv.AddAgentMessage("reviewer", "test content")

	// Verify the viewport renders without crashing
	lines := cv.Render(50)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}

	// Verify agent prefix appears in output
	found := false
	for _, line := range lines {
		if strings.Contains(line, "[reviewer]") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected [reviewer] prefix in rendered output, got: %q", lines)
	}
}

func TestNewAgentMessage_ReturnsAgentMessage(t *testing.T) {
	comp := NewAgentMessage("test", "planner")
	if comp == nil {
		t.Fatal("NewAgentMessage returned nil")
	}

	// Verify it renders without crashing
	lines := comp.Render(50)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
}

func TestAgentMessage_HandleInput_Noop(t *testing.T) {
	msg := newAgentMessage("content", "reviewer")
	// Should not panic
	msg.HandleInput("anything")
}

func TestAgentMessage_SetText_Updates(t *testing.T) {
	msg := newAgentMessage("old", "coder")
	msg.SetText("new")
	if msg.text != "new" {
		t.Errorf("expected text='new', got %q", msg.text)
	}
}
