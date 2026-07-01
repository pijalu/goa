// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestPadToWidthStyled_AddsBgToPadding(t *testing.T) {
	s := "hello"
	bg := ansi.Bg("#ff0000")
	result := padToWidthStyled(s, 10, bg)
	vw := visibleWidth(result)
	if vw < 10 {
		t.Errorf("should be at least 10 wide, got %d: %q", vw, result)
	}
	// Should have ANSI reset at end
	if !strings.HasSuffix(result, ansi.Reset) {
		t.Error("should end with ANSI reset")
	}
}

func TestPadToWidthStyled_NoTruncation(t *testing.T) {
	s := "hello world"
	bg := ansi.Bg("#ff0000")
	result := padToWidthStyled(s, 80, bg)
	// Should still contain the original text without truncation
	clean := ansi.Strip(result)
	if !strings.Contains(clean, "hello world") {
		t.Errorf("should contain 'hello world', got %q", clean)
	}
}

func TestChatViewport_ToolSeparator_BetweenConsecutiveTools(t *testing.T) {
	cv := NewChatViewport()

	// Add first tool
	tc1 := cv.AddToolExecution("read", `{"path":"a.txt"}`)
	tc1.SetStatus(ToolSuccess)
	tc1.SetOutput("some content")

	// Add second tool — no separator should be inserted
	tc2 := cv.AddToolExecution("read", `{"path":"b.txt"}`)
	tc2.SetStatus(ToolSuccess)
	tc2.SetOutput("more content")

	if cv.Len() != 2 {
		t.Errorf("expected 2 tool entries, got %d", cv.Len())
	}
}

func TestChatViewport_ToolSeparator_NotAfterMessage(t *testing.T) {
	cv := NewChatViewport()

	// Add a system message first
	cv.AddSystemMessage("this is a message")

	// Add tool — no separator since previous is not a tool
	cv.AddToolExecution("read", `{"path":"a.txt"}`)

	if cv.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", cv.Len())
	}
}

func TestTheme_ToolRunningBg_TokenExists(t *testing.T) {
	// Dark theme
	dark := DarkTheme()
	if _, ok := dark.Colors["tool_running_bg"]; !ok {
		t.Error("DarkTheme missing tool_running_bg")
	}

	// Light theme
	light := LightTheme()
	if _, ok := light.Colors["tool_running_bg"]; !ok {
		t.Error("LightTheme missing tool_running_bg")
	}
}

func TestRequiredColorTokens_IncludesToolRunningBg(t *testing.T) {
	found := false
	for _, token := range RequiredColorTokens {
		if token == "tool_running_bg" {
			found = true
			break
		}
	}
	if !found {
		t.Error("RequiredColorTokens missing tool_running_bg")
	}
}
