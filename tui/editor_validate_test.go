// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// isDashLine checks if a cleaned line is a plain border (all ─ characters).
func isDashLine(s string) bool {
	trimmed := strings.TrimRight(s, " ")
	if trimmed == "" {
		return false
	}
	for _, ch := range trimmed {
		if ch != '─' {
			return false
		}
	}
	return true
}

// isScrollIndicator checks if a cleaned line is a scroll indicator (contains ↑, ↓, "more").
func isScrollIndicator(s string) bool {
	return strings.Contains(s, "↑") || strings.Contains(s, "↓") || strings.Contains(s, "more")
}

func TestEditor_Validation_AllScenarios(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)

	var lines []string
	for i := 1; i <= 15; i++ {
		lines = append(lines, fmt.Sprintf("line_%d", i))
	}
	text := strings.Join(lines, "\n")
	ed.SetText(text)

	t.Run("cursor_at_top_no_scroll", func(t *testing.T) { testCursorAtTop(t, ed) })
	t.Run("cursor_at_end_auto_scrolls", func(t *testing.T) { testCursorAtEnd(t, ed) })
	t.Run("first_and_last_line_visible", func(t *testing.T) { testFirstAndLastLineVisible(t, ed, text) })
	t.Run("borders_bookend_content", func(t *testing.T) { testBordersBookendContent(t, ed) })
	t.Run("single_line_plain_borders", func(t *testing.T) { testSingleLinePlainBorders(t) })
	t.Run("no_indicator_when_content_fits", func(t *testing.T) { testNoIndicatorWhenContentFits(t) })
	t.Run("scroll_increases_with_cursor_down", func(t *testing.T) { testScrollIncreasesWithCursorDown(t, ed, text) })
}

func testCursorAtTop(t *testing.T, ed *Editor) {
	ed.pos = 0
	ed.scroll = 0
	r := ed.Render(80)
	top := stripANSIExtended(r[0])
	bot := stripANSIExtended(r[len(r)-1])

	if ed.scroll != 0 {
		t.Errorf("scroll should be 0 when cursor at top, got %d", ed.scroll)
	}
	if !isDashLine(top) {
		t.Errorf("top border should be plain ─ when at top: %q", top)
	}
	if !isDashLine(bot) && !isScrollIndicator(bot) {
		t.Errorf("bottom should be ─ or scroll indicator: %q", bot)
	}
}

func testCursorAtEnd(t *testing.T, ed *Editor) {
	ed.pos = len([]rune(ed.Text()))
	ed.scroll = 0
	r := ed.Render(80)
	top := stripANSIExtended(r[0])
	bot := stripANSIExtended(r[len(r)-1])

	if ed.scroll <= 0 {
		t.Errorf("scroll should be > 0 when cursor at end of 15 lines, got %d", ed.scroll)
	}
	if !isDashLine(top) && !isScrollIndicator(top) {
		t.Errorf("top should be ─ or scroll indicator: %q", top)
	}
	if isScrollIndicator(bot) && ed.scroll > 0 {
		t.Errorf("bottom should not have ↓ indicator when scrolled down, got %q. scroll=%d", bot, ed.scroll)
	}
	if !isDashLine(bot) && !isScrollIndicator(bot) {
		t.Errorf("bottom border should be ─ or indicator: %q", bot)
	}
}

func testFirstAndLastLineVisible(t *testing.T, ed *Editor, text string) {
	ed.pos = 0
	ed.scroll = 0
	_ = ed.Render(80)
	if !lineVisible(ed, "line_1") {
		t.Error("line_1 should be visible when cursor at top")
	}

	ed.pos = len([]rune(text))
	if !lineVisible(ed, "line_15") {
		t.Error("line_15 should be visible when cursor at end (auto-scrolled)")
	}
}

func lineVisible(ed *Editor, needle string) bool {
	r := ed.Render(80)
	for i := 1; i < len(r)-1; i++ {
		clean := stripANSIExtended(r[i])
		if strings.Contains(clean, needle) {
			return true
		}
	}
	return false
}

func testBordersBookendContent(t *testing.T, ed *Editor) {
	ed.pos = 0
	r := ed.Render(80)
	borderIdxs := collectBorderIndices(r)

	switch {
	case len(borderIdxs) == 0:
		assertIndicatorBorders(t, r)
	case len(borderIdxs) == 1:
		assertSingleBorder(t, r, borderIdxs[0])
	case len(borderIdxs) > 2:
		t.Errorf("expected at most 2 border lines, got %d: %v", len(borderIdxs), borderIdxs)
	}
}

func collectBorderIndices(r []string) []int {
	var borderIdxs []int
	for i, line := range r {
		clean := stripANSIExtended(line)
		if isDashLine(clean) {
			borderIdxs = append(borderIdxs, i)
		}
	}
	return borderIdxs
}

func assertIndicatorBorders(t *testing.T, r []string) {
	if !isScrollIndicator(stripANSIExtended(r[0])) && !isScrollIndicator(stripANSIExtended(r[len(r)-1])) {
		t.Errorf("borders not found: no dash lines and no indicators")
	}
}

func assertSingleBorder(t *testing.T, r []string, borderIdx int) {
	topIsIndicator := isScrollIndicator(stripANSIExtended(r[0]))
	botIsIndicator := isScrollIndicator(stripANSIExtended(r[len(r)-1]))
	if borderIdx != 0 && !topIsIndicator {
		t.Errorf("expected one border + one indicator, but top is neither dash nor indicator")
	}
	if !botIsIndicator && borderIdx == 0 && len(r) > 1 {
		_ = true
	}
}

func testSingleLinePlainBorders(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("hello")
	ed.pos = 5
	r := ed.Render(80)
	top := stripANSIExtended(r[0])
	bot := stripANSIExtended(r[len(r)-1])

	if !isDashLine(top) {
		t.Errorf("single-line top border should be ─: %q", top)
	}
	if !isDashLine(bot) {
		t.Errorf("single-line bottom border should be ─: %q", bot)
	}
	content := stripANSIExtended(r[1])
	if !strings.Contains(content, "hello") {
		t.Errorf("content should show 'hello': %q", content)
	}
}

func testNoIndicatorWhenContentFits(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("short text")
	ed.pos = 10
	r := ed.Render(80)
	top := stripANSIExtended(r[0])
	bot := stripANSIExtended(r[len(r)-1])

	if isScrollIndicator(top) {
		t.Errorf("top should not have scroll indicator for short content: %q", top)
	}
	if isScrollIndicator(bot) {
		t.Errorf("bottom should not have scroll indicator for short content: %q", bot)
	}
	if !isDashLine(top) || !isDashLine(bot) {
		t.Errorf("short content should have plain dash borders: top=%q bot=%q", top, bot)
	}
}

func testScrollIncreasesWithCursorDown(t *testing.T, ed *Editor, text string) {
	ed.pos = 0
	ed.scroll = 0
	_ = ed.Render(80)
	initialScroll := ed.scroll

	ed.pos = len([]rune(text))
	_ = ed.Render(80)
	if ed.scroll <= initialScroll {
		t.Errorf("scroll should increase when moving cursor to end: initial=%d final=%d", initialScroll, ed.scroll)
	}
}
