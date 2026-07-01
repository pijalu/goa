// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func renderCardPlain(c *ClarifyCard, width int) string {
	lines := c.Render(width)
	for i := range lines {
		lines[i] = ansi.Strip(lines[i])
	}
	return strings.Join(lines, "\n")
}

func TestClarifyCard_RenderContainsAllFields(t *testing.T) {
	c := NewClarifyCard("Project name", "Need to disambiguate", "Which repo?", []string{"goa", "other"})
	out := renderCardPlain(c, 60)
	for _, want := range []string{"Project name", "Need to disambiguate", "Which repo?", "1. goa", "2. other"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestClarifyCard_EmptyOptionalFields(t *testing.T) {
	c := NewClarifyCard("Title", "", "Just answer", nil)
	out := renderCardPlain(c, 40)
	if !strings.Contains(out, "Title") {
		t.Error("title missing")
	}
	if strings.Contains(out, "1.") {
		t.Errorf("options should not render when empty:\n%s", out)
	}
}

func TestClarifyCard_QuestionRequired(t *testing.T) {
	c := NewClarifyCard("T", "S", "  ", nil)
	if c.Question() != "" {
		t.Errorf("question should be trimmed to empty, got %q", c.Question())
	}
	// Renders a box even without content (no panic).
	if lines := c.Render(30); len(lines) < 2 {
		t.Errorf("expected a bordered box, got %d lines", len(lines))
	}
}

func TestChatViewport_AddClarifyCard(t *testing.T) {
	cv := NewChatViewport()
	cv.AddClarifyCard(NewClarifyCard("T", "S", "Q", []string{"a"}))
	// AddComponent uses Type -1; ensure it was appended.
	if n := len(cv.Children()); n == 0 {
		t.Fatal("card not appended as a child")
	}
}

func TestChatViewport_AddClarifyCardNilSafe(t *testing.T) {
	cv := NewChatViewport()
	before := len(cv.Children())
	cv.AddClarifyCard(nil) // must not panic or append
	if len(cv.Children()) != before {
		t.Errorf("nil card should be ignored")
	}
}
