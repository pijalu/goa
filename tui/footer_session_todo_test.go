// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestFooter_SessionTodoCountPreservedAndGoalTakesPrecedence(t *testing.T) {
	f := NewFooter()
	f.SetData(FooterData{Profile: "coding-posture", Mode: "yolo"})
	f.SetSessionTodoCount(4)

	// A routine stats rebuild has no Todo knowledge and must not erase the
	// session count written by SetSessionTodoCount.
	f.SetData(FooterData{Stats: "↑1k"})
	footer := ansi.Strip(strings.Join(f.Render(100), "\n"))
	if !strings.Contains(footer, "⬩⬩⬩+1") {
		t.Fatalf("session Todo markers lost across stats rebuild:\n%s", footer)
	}

	// The active goal's linked list replaces, rather than adds to, the hidden
	// session list. Clearing the goal reveals the unchanged session count.
	f.SetGoalStatus("active", 1, 1)
	footer = ansi.Strip(strings.Join(f.Render(100), "\n"))
	if !strings.Contains(footer, "◈") || !strings.Contains(footer, "⬩") || strings.Contains(footer, "+1") {
		t.Fatalf("active goal must render only its own Todo count:\n%s", footer)
	}
	f.SetGoalStatus("", 0, 0)
	footer = ansi.Strip(strings.Join(f.Render(100), "\n"))
	if strings.Contains(footer, "◈") || !strings.Contains(footer, "⬩⬩⬩+1") {
		t.Fatalf("clearing goal must restore session Todo markers:\n%s", footer)
	}

	f.SetSessionTodoCount(0)
	footer = ansi.Strip(strings.Join(f.Render(100), "\n"))
	if strings.Contains(footer, "⬩") {
		t.Fatalf("zero session Todos must clear markers:\n%s", footer)
	}
}
