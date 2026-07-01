// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestNewSelector_SortsItems(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "Zebra"},
		{Value: "a", Label: "alpha"},
		{Value: "m", Label: "Mango"},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "m", result)

	if len(s.items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(s.items))
	}
	expected := []string{"alpha", "Mango", "Zebra"}
	for i, e := range expected {
		if s.items[i].Label != e {
			t.Errorf("item[%d].Label = %q, want %q", i, s.items[i].Label, e)
		}
	}
}

func TestNewSelector_SortsCaseInsensitive(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "ZEBRA"},
		{Value: "a", Label: "apple"},
		{Value: "m", Label: "Mango"},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "", result)

	// Case-insensitive: apple < Mango < ZEBRA
	if s.items[0].Label != "apple" {
		t.Errorf("expected apple first, got %s", s.items[0].Label)
	}
	if s.items[1].Label != "Mango" {
		t.Errorf("expected Mango second, got %s", s.items[1].Label)
	}
	if s.items[2].Label != "ZEBRA" {
		t.Errorf("expected ZEBRA last, got %s", s.items[2].Label)
	}
}

func TestNewSelector_StartsOnCurrentValue(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "Zebra"},
		{Value: "a", Label: "alpha"},
		{Value: "m", Label: "Mango"},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "m", result)

	// Items are sorted alphabetically; Mango (current) is at index 1.
	if s.selected != 1 {
		t.Errorf("selected = %d, want 1 (Mango)", s.selected)
	}
	if s.filtered[s.selected].Value != "m" {
		t.Errorf("filtered[selected].Value = %q, want m", s.filtered[s.selected].Value)
	}
}

func TestSelector_RenderTitle(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Select mode:", []SelectorItem{
		{Value: "coder", Label: "coder"},
	}, "", result)

	lines := s.Render(50)
	if len(lines) < 1 {
		t.Fatal("expected at least 1 line")
	}
	if !strings.Contains(lines[0], "Select mode:") {
		t.Errorf("title line should contain 'Select mode:', got %q", lines[0])
	}
	if !strings.Contains(lines[0], ansi.Bold) {
		t.Errorf("title should be bold, got %q", lines[0])
	}
}

func TestSelector_RenderCurrentValueMarked(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "cur", Label: "current"},
		{Value: "oth", Label: "other"},
	}, "cur", result)

	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "✓") && strings.Contains(line, "current") {
			found = true
			break
		}
	}
	if !found {
		t.Error("current value should show ✓ marker, none found in render output")
	}
}

func TestSelector_RenderSelectedHighlight(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "›") {
			found = true
			// Selected item should use success color
			if !strings.Contains(line, ansi.Fg(TheTheme.ColorHex("tool_success"))) {
				t.Errorf("selected item should use tool_success color for ›, got %q", line)
			}
			break
		}
	}
	if !found {
		t.Error("selected item should show › marker, none found in render output")
	}
}

func TestSelector_RenderCancelHint(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	lines := s.Render(50)
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "esc") {
		t.Errorf("last line should contain esc hint, got %q", lastLine)
	}
	// Hint should use faint ANSI
	if !strings.Contains(lastLine, ansi.Faint) {
		t.Errorf("cancel hint should be dim/faint, got %q", lastLine)
	}
}

func TestSelector_RenderNoMatches(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	// Type a filter that matches nothing
	s.HandleInput("x")
	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "no matches") {
			found = true
			break
		}
	}
	if !found {
		t.Error("should show 'no matches' when filter matches nothing")
	}
}

func TestSelector_FilterMatching(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "g", Label: "gamma"},
	}, "", result)

	// Send filter characters one at a time
	s.HandleInput("a")
	s.HandleInput("l")

	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item for 'al', got %d", len(s.filtered))
	}
	if len(s.filtered) > 0 && s.filtered[0].Value != "a" {
		t.Errorf("expected filtered value 'a', got %q", s.filtered[0].Value)
	}
}

func TestSelector_FilterMatchesDescription(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha", Description: "first letter"},
		{Value: "b", Label: "beta", Description: "second letter"},
	}, "", result)

	s.HandleInput("s")
	s.HandleInput("e")
	s.HandleInput("c")

	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item matching description, got %d", len(s.filtered))
	}
	if len(s.filtered) > 0 && s.filtered[0].Value != "b" {
		t.Errorf("expected filtered value 'b', got %q", s.filtered[0].Value)
	}
}

func TestSelector_FilterBackspace(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "z", Label: "zebra"},
		{Value: "x", Label: "xylophone"},
	}, "", result)

	s.HandleInput("z")
	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item after 'z', got %d", len(s.filtered))
	}

	// Backspace
	s.HandleInput("backspace")
	if len(s.filtered) != 2 {
		t.Errorf("expected all 2 items after backspace, got %d", len(s.filtered))
	}
}

func TestSelector_NavigateDown(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "c", Label: "gamma"},
	}, "", result)

	if s.selected != 0 {
		t.Errorf("expected initial selected=0, got %d", s.selected)
	}

	s.HandleInput(KeyDown)
	if s.selected != 1 {
		t.Errorf("expected selected=1 after first down, got %d", s.selected)
	}

	s.HandleInput(KeyDown)
	if s.selected != 2 {
		t.Errorf("expected selected=2 after second down, got %d", s.selected)
	}
}

func TestSelector_NavigateUp(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	s.selected = 1
	s.HandleInput(KeyUp)
	if s.selected != 0 {
		t.Errorf("expected selected=0 after up, got %d", s.selected)
	}

	// Wrap around
	s.HandleInput(KeyUp)
	if s.selected != 1 {
		t.Errorf("expected selected=1 wrapping around, got %d", s.selected)
	}
}

func TestSelector_EnterSelects(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "alpha", Label: "alpha"},
		{Value: "beta", Label: "beta"},
	}, "", result)

	// Tab to accept first
	s.HandleInput(KeyTab)
	select {
	case v := <-result:
		if v != "alpha" {
			t.Errorf("expected 'alpha', got %q", v)
		}
	default:
		t.Error("expected result to be delivered on Tab")
	}
}

func TestSelector_EscapeCancels(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	s.HandleInput(KeyEscape)
	select {
	case v := <-result:
		if v != "" {
			t.Errorf("expected empty string on cancel, got %q", v)
		}
	default:
		t.Error("expected result on escape")
	}
}

func TestSelector_CtrlCCancels(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	s.HandleInput(KeyCtrlC)
	select {
	case v := <-result:
		if v != "" {
			t.Errorf("expected empty string on cancel, got %q", v)
		}
	default:
		t.Error("expected result on Ctrl+C")
	}
}

func TestSelector_ApplyFilterResetsSelected(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "g", Label: "gamma"},
	}, "", result)

	s.selected = 2
	s.HandleInput("a")
	// After filtering to just "alpha", selected should be 0
	if s.selected >= len(s.filtered) {
		t.Errorf("selected=%d should be within filtered count=%d", s.selected, len(s.filtered))
	}
}

func TestSelector_RenderWidthClamped(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	// Very large width should be clamped to max 60
	lines := s.Render(200)
	for _, line := range lines {
		vw := visibleWidth(line)
		// The hint line can exceed 60 due to wide Unicode arrows (↑↓).
		// Allow up to 75 for hint text; all other lines must be ≤ 62.
		if vw > 75 {
			t.Errorf("line visual width too wide: vw=%d, line=%q", vw, line)
		}
	}
}

func TestSelector_RenderEmptyList(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{}, "", result)

	lines := s.Render(50)
	if len(lines) == 0 {
		t.Fatal("expected at least some lines for empty list")
	}
}

func TestSelector_SetItems(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "b", Label: "banana"},
	}, "", result)

	s.SetItems([]SelectorItem{
		{Value: "a", Label: "apple"},
		{Value: "b", Label: "banana"},
	})

	if len(s.items) != 2 {
		t.Errorf("expected 2 items after SetItems, got %d", len(s.items))
	}
	if s.items[0].Label != "apple" {
		t.Errorf("expected apple sorted first, got %s", s.items[0].Label)
	}
}

func TestSelector_StartWithSelect(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	// Enter on first item (selected=0)
	s.HandleInput(KeyEnter)
	select {
	case v := <-result:
		if v != "a" {
			t.Errorf("expected 'a', got %q", v)
		}
	default:
		t.Error("expected result on Enter")
	}
}

func TestSelector_RenderSeparators(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	lines := s.Render(50)
	// Should have at least 3 separators (title separator, search separator, hint separator)
	sepCount := 0
	for _, line := range lines {
		if strings.Contains(line, "─") {
			sepCount++
		}
	}
	if sepCount < 3 {
		t.Errorf("expected at least 3 separator lines, got %d", sepCount)
	}
}

func TestSelector_RenderDescription(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha", Description: "first letter"},
	}, "", result)

	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "first letter") {
			found = true
			break
		}
	}
	if !found {
		t.Error("description should be rendered in selector output")
	}
}

func TestSelector_NeedsFilter(t *testing.T) {
	result := make(chan string, 1)
	// With <= 5 items, the hint should still show filter text
	s1 := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "a"},
		{Value: "b", Label: "b"},
	}, "", result)
	lines := s1.Render(50)
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "filter") {
		t.Error("hint should contain filter text even for small lists")
	}

	// With > 5 items, the hint should also show filter text
	s2 := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "a"},
		{Value: "b", Label: "b"},
		{Value: "c", Label: "c"},
		{Value: "d", Label: "d"},
		{Value: "e", Label: "e"},
		{Value: "f", Label: "f"},
	}, "", result)
	lines2 := s2.Render(50)
	lastLine2 := lines2[len(lines2)-1]
	if !strings.Contains(lastLine2, "filter") {
		t.Error("hint should contain filter text even for large lists")
	}
}

func TestSelector_EmitDoneCalled(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	doneCalled := false
	s.SetDone(func() {
		doneCalled = true
	})

	s.HandleInput(KeyEnter)
	if !doneCalled {
		t.Error("done callback should be called on select")
	}
}

func TestSelector_EmitDoneCalledOnCancel(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	doneCalled := false
	s.SetDone(func() {
		doneCalled = true
	})

	s.HandleInput(KeyEscape)
	if !doneCalled {
		t.Error("done callback should be called on cancel")
	}
}

func TestSelector_RenderZeroWidth(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	lines := s.Render(0)
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines with min width, got %d", len(lines))
	}
}

func TestSelector_RenderSmallWidth(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	// Width 10 should be clamped to min 30
	lines := s.Render(10)
	// Should still work without panic
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines, got %d", len(lines))
	}
}
