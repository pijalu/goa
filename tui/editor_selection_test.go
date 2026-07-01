// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func TestRenderAutoComp_SelectedItem_UsesArrowAndBold(t *testing.T) {
	e := NewEditor()
	e.compState = CompState{
		Phase: PhaseCommand,
		Items: []Completion{
			{Value: "/mode", Display: "/mode", Description: "Set agent mode"},
			{Value: "/memory", Display: "/memory", Description: "Manage memory"},
			{Value: "/help", Display: "/help", Description: "Show help"},
		},
		Idx: 0,
	}

	lines := e.renderAutoComp(50)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	// First line is the separator, skip it
	selectedFound := false
	for _, line := range lines {
		if strings.Contains(line, "›") {
			selectedFound = true
			if !strings.Contains(line, "\x1b[1m") {
				t.Error("selected line should contain bold ANSI code (\\x1b[1m), got:", line)
			}
			if strings.Contains(line, "\x1b[48") {
				t.Error("selected line should NOT contain background ANSI code (\\x1b[48), got:", line)
			}
		}
	}
	if !selectedFound {
		t.Error("expected '›' arrow marker in renderAutoComp output")
	}
}

func TestRenderAutoComp_NoBlueBackground(t *testing.T) {
	e := NewEditor()
	e.compState = CompState{
		Phase: PhaseCommand,
		Items: []Completion{
			{Value: "/mode", Display: "/mode", Description: "Set agent mode"},
		},
		Idx: 0,
	}

	lines := e.renderAutoComp(50)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	// Check that no line in the output uses ANSI background color escape
	for i, line := range lines {
		if strings.Contains(line, "\x1b[48") {
			t.Errorf("line %d should not contain background color: %q", i, line)
		}
	}
}

func TestRenderAutoComp_NonSelectedNoArrow(t *testing.T) {
	e := NewEditor()
	e.compState = CompState{
		Phase: PhaseCommand,
		Items: []Completion{
			{Value: "/mode", Display: "/mode", Description: "Set agent mode"},
			{Value: "/memory", Display: "/memory", Description: "Manage memory"},
			{Value: "/help", Display: "/help", Description: "Show help"},
		},
		Idx: 1, // second item selected
	}

	lines := e.renderAutoComp(50)

	// First line is separator, verify arrow count (exactly 1 arrow)
	arrowCount := 0
	arrowLines := 0
	for _, line := range lines {
		if strings.Contains(line, "›") {
			arrowCount++
			arrowLines++
		}
	}
	if arrowCount != 1 {
		t.Errorf("expected exactly 1 arrow, got %d", arrowCount)
	}
	if arrowLines != 1 {
		t.Errorf("expected exactly 1 line with arrow, got %d", arrowLines)
	}
}

func TestRenderAutoComp_DescriptionShownOnSelected(t *testing.T) {
	e := NewEditor()
	e.compState = CompState{
		Phase: PhaseCommand,
		Items: []Completion{
			{Value: "/mode", Display: "/mode", Description: "Set agent mode"},
			{Value: "/memory", Display: "/memory", Description: "Manage memory"},
		},
		Idx: 0,
	}

	lines := e.renderAutoComp(50)

	found := false
	for _, line := range lines {
		if strings.Contains(line, "›") && strings.Contains(line, "Set agent mode") {
			found = true
			break
		}
	}
	if !found {
		t.Error("selected item with description should show description text")
	}
}

func TestRenderAutoComp_MoreIndicator(t *testing.T) {
	e := NewEditor()
	items := make([]Completion, 20)
	for i := 0; i < 20; i++ {
		items[i] = Completion{
			Value:       "/cmd",
			Display:     "/cmd",
			Description: "",
		}
	}
	e.compState = CompState{
		Phase: PhaseCommand,
		Items: items,
		Idx:   0,
	}

	lines := e.renderAutoComp(50)

	foundMore := false
	for _, line := range lines {
		if strings.Contains(line, "more") {
			foundMore = true
			break
		}
	}
	if !foundMore {
		t.Error("expected more indicator when items exceed maxShow")
	}
}
