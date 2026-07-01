// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// stripPanelBox removes ANSI escapes and goa-panel box-drawing characters so
// tests can assert on the inner text content regardless of the border styling.
func stripPanelBox(s string) string {
	s = stripANSIExtended(s)
	for _, c := range []string{"│", "╭", "╮", "╰", "╯", "─"} {
		s = strings.ReplaceAll(s, c, "")
	}
	return strings.TrimSpace(s)
}

func TestSystemMessage_Preformatted_PreservesNewlines(t *testing.T) {
	text := "  /commands  List commands\n  /help     Show help\n  /memory   Manage memory"
	msg := newSystemMessage(text)
	if !msg.preformatted {
		t.Error("command list with indented '/' should be detected as preformatted")
	}

	lines := msg.Render(80)
	// Should have at least 4 lines (3 content + 1 empty trailing)
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	// Each boxed line should contain one command (strip border + ANSI first)
	found := 0
	for _, line := range lines {
		clean := stripPanelBox(line)
		if strings.HasPrefix(clean, "/commands") {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected '/commands' on exactly 1 line, found %d", found)
	}
}

func TestSystemMessage_Markdown_StillRendersNormally(t *testing.T) {
	text := "# Heading\n\nThis is a paragraph with **bold** text."
	msg := newSystemMessage(text)
	if msg.preformatted {
		t.Error("markdown should not be detected as preformatted")
	}

	lines := msg.Render(80)
	if len(lines) < 2 {
		t.Fatal("expected at least 2 lines (heading + trailing empty)")
	}
}

func TestSystemMessage_SingleLine_NotPreformatted(t *testing.T) {
	msg := newSystemMessage("Just a single line of text.")
	if msg.preformatted {
		t.Error("single line should not be preformatted")
	}
}

func TestSystemMessage_ExplicitPreformatted(t *testing.T) {
	msg := newSystemMessagePreformatted("line1\nline2\nline3")
	if !msg.preformatted {
		t.Error("explicit preformatted should have preformatted=true")
	}

	lines := msg.Render(80)
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}
	clean0 := stripPanelBox(lines[1])
	if !strings.Contains(clean0, "line1") {
		t.Errorf("expected 'line1', got %q", clean0)
	}
}

func TestSystemMessage_Empty(t *testing.T) {
	msg := newSystemMessage("")
	lines := msg.Render(80)
	if lines != nil {
		t.Errorf("expected nil for empty text, got %d lines", len(lines))
	}
}

func TestIsPreformatted_IndentedCommand(t *testing.T) {
	if !isPreformatted("  /help  show help\n  /quit  exit") {
		t.Error("indented commands should be preformatted")
	}
}

func TestIsPreformatted_Markdown(t *testing.T) {
	if isPreformatted("# Heading\n\n- list item\n\n```\ncode\n```") {
		t.Error("markdown should not be preformatted")
	}
}

func TestIsPreformatted_SingleLine(t *testing.T) {
	if isPreformatted("just one line") {
		t.Error("single line should not be preformatted")
	}
}

func TestIsPreformatted_WideLines(t *testing.T) {
	longLine := "this is a very long line that exceeds sixty characters and should be detected as wide enough for preformatting"
	if !isPreformatted(longLine + "\n" + longLine) {
		t.Error("lines over 60 chars should be detected as preformatted")
	}
}

func TestIsPreformatted_HelpCommand(t *testing.T) {
	// /help outputs markdown — must NOT be preformatted
	helpText := `# Goa Commands

- **/help** — show help
- **/mode** — set mode
- **/quit** — exit`
	if isPreformatted(helpText) {
		t.Error("/help outputs markdown, should not be preformatted")
	}
}

func TestChatViewport_AddSystemMessagePreformatted(t *testing.T) {
	cv := NewChatViewport()
	cv.AddSystemMessagePreformatted("header\n  /cmd1  desc1\n  /cmd2  desc2")

	lines := cv.Render(80)
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 rendered lines, got %d", len(lines))
	}
	hasCmd1 := false
	for _, line := range lines {
		if strings.Contains(line, "/cmd1") {
			hasCmd1 = true
			break
		}
	}
	if !hasCmd1 {
		t.Error("expected /cmd1 in rendered output")
	}
}
