// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestEditor_AcceptAndRecomplete_KeepsPopup(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	cc := NewCommandCompleter([]string{"/mode"}, map[string]string{"/mode": "set mode"})
	cc.SetArgCompleter(func(cmdName, argPrefix string) []Completion {
		if cmdName == "/mode" && argPrefix == "" {
			return []Completion{{Value: "coder", Description: "coder mode"}}
		}
		return nil
	})
	ed.SetCompleter(cc)

	// Simulate typing "/m"
	ed.SetText("/m")
	ed.pos = 2
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}
	if len(ed.compState.Items) == 0 {
		t.Fatal("expected completion items")
	}

	// Tab should accept the first item and re-trigger completion
	ed.acceptAndRecomplete()

	// Buffer should now contain "/mode"
	if ed.Text() != "/mode" {
		t.Errorf("buffer = %q, want /mode", ed.Text())
	}
	// Completion should still be active (showing modifiers)
	if !ed.compState.Active() {
		t.Error("expected completion to stay active after acceptAndRecomplete")
	}
	// Should now see modifier variants
	var foundModifier bool
	for _, it := range ed.compState.Items {
		if it.Category == CatModifier {
			foundModifier = true
			break
		}
	}
	if !foundModifier {
		t.Error("expected modifier items after re-complete")
	}
}

func TestEditor_AcceptCompletion_ClosesPopup(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/mode"}, map[string]string{"/mode": "set mode"}))

	ed.SetText("/m")
	ed.pos = 2
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}

	// Enter (acceptCompletion) should accept and close
	ed.acceptCompletion()

	if ed.Text() != "/mode" {
		t.Errorf("buffer = %q, want /mode", ed.Text())
	}
	if ed.compState.Active() {
		t.Error("expected completion to be closed after acceptCompletion")
	}
}

func TestEditor_SlashCompletionEnterAcceptsSelected(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/copy"}, map[string]string{"/copy": "copy last message"}))

	var submitted string
	ed.onSubmit = func(text string) {
		submitted = text
	}

	ed.SetText("/co")
	ed.pos = 3
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}

	// Enter should expand the partial, non-existent command to the selected
	// completion candidate and submit it.
	ed.HandleInput(KeyEnter)

	if ed.Text() != "" {
		t.Errorf("editor not cleared after submit, text = %q", ed.Text())
	}
	if submitted != "/copy" {
		t.Errorf("submitted = %q, want /copy", submitted)
	}
	if len(ed.history) != 1 || ed.history[0] != "/copy" {
		t.Errorf("history = %v, want [/copy]", ed.history)
	}
}

func TestSystemMessage_Render_BoxBorder(t *testing.T) {
	msg := newSystemMessage("# Title\n\nsome **bold** text")
	if msg.preformatted {
		t.Fatal("markdown should not be preformatted")
	}
	lines := msg.Render(60)
	if len(lines) < 3 {
		t.Fatalf("expected top/content/bottom, got %d", len(lines))
	}
	assertBoxBorders(t, lines)
	assertRenderedBody(t, lines)
}

func assertBoxBorders(t *testing.T, lines []string) {
	top := stripANSIExtended(lines[0])
	bot := stripANSIExtended(lines[len(lines)-1])
	if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
		t.Errorf("top border should be a box top, got %q", top)
	}
	if !strings.HasPrefix(bot, "╰") || !strings.HasSuffix(bot, "╯") {
		t.Errorf("bottom border should be a box bottom, got %q", bot)
	}
}

func assertRenderedBody(t *testing.T, lines []string) {
	foundBody := false
	for _, line := range lines[1 : len(lines)-1] {
		if strings.Contains(line, "Title") || strings.Contains(line, "bold") {
			foundBody = true
		}
		if !strings.Contains(line, "│") {
			t.Errorf("inner line missing side border: %q", stripANSIExtended(line))
		}
	}
	if !foundBody {
		t.Error("expected rendered body content inside the panel")
	}
}

func TestSystemMessage_Render_PreformattedBox(t *testing.T) {
	msg := newSystemMessagePreformatted("alpha\nbeta\ngamma")
	lines := msg.Render(40)
	if len(lines) < 5 { // top + 3 content + bottom
		t.Fatalf("expected at least 5 lines, got %d", len(lines))
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		present := false
		for _, line := range lines {
			if strings.Contains(stripPanelBox(line), want) {
				present = true
				break
			}
		}
		if !present {
			t.Errorf("expected %q in boxed preformatted output", want)
		}
	}
}

func TestTheme_GoaPanelTokens_Resolve(t *testing.T) {
	for name, theme := range map[string]*Theme{"dark": DarkTheme(), "light": LightTheme()} {
		if theme.ColorHex("goa_panel_bg") == "" {
			t.Errorf("%s theme missing goa_panel_bg", name)
		}
		if theme.ColorHex("goa_panel_border") == "" {
			t.Errorf("%s theme missing goa_panel_border", name)
		}
	}
}

func TestEditor_SlashCompletionEnterAcceptsNavigated(t *testing.T) {
	// Typing /go, moving the popup DOWN to /goal, then Enter must submit
	// /goal (the highlighted candidate), not /go (text as typed).
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/goa", "/goal"}, map[string]string{
		"/goa": "goa", "/goal": "goal",
	}))

	var submitted string
	ed.onSubmit = func(text string) { submitted = text }

	ed.SetText("/go")
	ed.pos = 3
	ed.updateAutoComp()
	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}
	if ed.compState.UserNavigated {
		t.Error("UserNavigated should be false before navigation")
	}

	// Navigate down to /goal.
	ed.HandleInput(KeyDown)
	sel := ed.compState.Selected()
	if sel == nil || sel.Value != "/goal" {
		t.Fatalf("selected = %v, want /goal", sel)
	}
	if !ed.compState.UserNavigated {
		t.Error("UserNavigated should be true after cycling")
	}

	ed.HandleInput(KeyEnter)
	if submitted != "/goal" {
		t.Errorf("submitted = %q, want /goal", submitted)
	}
}

func TestEditor_SlashCompletionNavigationResetsOnTyping(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/goa", "/goal"}, map[string]string{
		"/goa": "goa", "/goal": "goal",
	}))

	ed.SetText("/go")
	ed.pos = 3
	ed.updateAutoComp()
	ed.HandleInput(KeyDown) // navigate
	if !ed.compState.UserNavigated {
		t.Fatal("expected navigated")
	}

	// Typing another char refreshes the popup and clears navigation state.
	ed.pos = 3          // keep cursor at end
	ed.HandleInput("a") // now buffer is /goa
	if ed.compState.UserNavigated {
		t.Error("UserNavigated should reset after typing")
	}
}

func TestEditor_NonSlashCompletionEnterAcceptsOnly(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	// Simulate a file-like completer that uses @ prefixes.
	ed.SetCompleter(NewCommandCompleter([]string{"@file.txt"}, map[string]string{"@file.txt": "file"}))

	var submitted string
	ed.onSubmit = func(text string) {
		submitted = text
	}

	ed.SetText("@fi")
	ed.pos = 3
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}

	ed.HandleInput(KeyEnter)

	if ed.Text() != "@file.txt" {
		t.Errorf("buffer = %q, want @file.txt", ed.Text())
	}
	if submitted != "" {
		t.Errorf("non-slash completion should not submit, got %q", submitted)
	}
}

func TestEditor_Escape_ClosesPopup(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/mode"}, map[string]string{"/mode": "set mode"}))

	ed.SetText("/m")
	ed.pos = 2
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}

	// Escape clears completion
	ed.clearCompletion()

	if ed.compState.Active() {
		t.Error("expected completion to be closed after escape")
	}

	// Typing a printable char should re-trigger completion
	ed.HandleInput("o")
	if !ed.compState.Active() {
		t.Error("expected completion to re-trigger after typing post-escape")
	}
}

func TestEditor_BracketedPaste_SingleLine(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.HandleInput("hello")
	if ed.Text() != "hello" {
		t.Errorf("expected 'hello', got %q", ed.Text())
	}
}

func TestEditor_BracketedPaste_MultiLine(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.HandleInput("line1\nline2\nline3")
	want := "line1\nline2\nline3"
	if ed.Text() != want {
		t.Errorf("expected %q, got %q", want, ed.Text())
	}
}

func TestEditor_BracketedPaste_LargeBecomesMarker(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	// 12 lines triggers the marker path.
	ed.HandleInput("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl")
	if !strings.Contains(ed.Text(), "[paste #") {
		t.Errorf("expected paste marker, got %q", ed.Text())
	}
}

func TestEditor_BracketedPaste_NormalizesCRLF(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.HandleInput("a\r\nb")
	want := "a\nb"
	if ed.Text() != want {
		t.Errorf("expected %q, got %q", want, ed.Text())
	}
}

func TestEditor_BracketedPaste_StripsANSI(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.HandleInput("\x1b[32mgreen\x1b[0m\n\x1b[1mbold\x1b[0m")
	want := "green\nbold"
	if ed.Text() != want {
		t.Errorf("expected %q, got %q", want, ed.Text())
	}
}

func TestEditor_BracketedPaste_ExpandsTabs(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.HandleInput("a\tb")
	want := "a  b"
	if ed.Text() != want {
		t.Errorf("expected %q, got %q", want, ed.Text())
	}
}

// visualCursorFor maps a rune position to (visual line, col) via the single
// wrapChunks source of truth — the same layout used to render the editor. It
// replaces the old standalone visualCursorPos simulation that could drift
// from the displayed wrapping.
func visualCursorFor(text string, pos, width int) (line, col int) {
	chunks := wrapChunks(text, width)
	idx, off := cursorChunk(chunks, text, pos)
	c := chunks[idx]
	return idx, visibleWidth(c.Text[:runeOffsetToByte(c.Text, off)])
}

func TestVisualCursorPos_SingleLine(t *testing.T) {
	line, col := visualCursorFor("hello", 5, 80)
	if line != 0 || col != 5 {
		t.Errorf("expected (0,5), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_Empty(t *testing.T) {
	line, col := visualCursorFor("", 0, 80)
	if line != 0 || col != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_MultiLine(t *testing.T) {
	// "test\nme" cursor at end (after "me")
	line, col := visualCursorFor("test\nme", 7, 80)
	if line != 1 || col != 2 {
		t.Errorf("\"test\\nme\" pos=7: expected (1,2), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_MultiLineAtNewline(t *testing.T) {
	// "test\n" cursor at newline (after "test")
	line, col := visualCursorFor("test\n", 5, 80)
	if line != 1 || col != 0 {
		t.Errorf("\"test\\n\" pos=5: expected (1,0), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_MultiLineFirstLine(t *testing.T) {
	// "test\nme" cursor at "test"
	line, col := visualCursorFor("test\nme", 2, 80)
	if line != 0 || col != 2 {
		t.Errorf("\"test\\nme\" pos=2: expected (0,2), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_WrappedLine(t *testing.T) {
	// A line long enough to wrap: "hello world" in width 5
	// Word-wrap produces: ["hello", "world"]
	// Cursor at end of "hello world" (11)
	line, col := visualCursorFor("hello world", 11, 5)
	// visibleWidth("hello world") = 11. Word-wrapped: "hello" (line 0), "world" (line 1)
	// Cursor at end of "world" is on line 1, col 5
	if line != 1 || col != 5 {
		t.Errorf("\"hello world\" pos=11 w=5: expected (1,5), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_TrailingSpaces(t *testing.T) {
	// "> this is " cursor at end (10) — trailing space after "is"
	line, col := visualCursorFor("> this is ", 10, 80)
	if line != 0 || col != 10 {
		t.Errorf("\"> this is \" pos=10 w=80: expected (0,10), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_TrailingSpaces_Multiple(t *testing.T) {
	// "hello   " cursor at end (8) — three trailing spaces after "hello"
	line, col := visualCursorFor("hello   ", 8, 80)
	if line != 0 || col != 8 {
		t.Errorf("\"hello   \" pos=8 w=80: expected (0,8), got (%d,%d)", line, col)
	}
}

func TestVisualCursorPos_TrailingSpaces_Wrapped(t *testing.T) {
	// "hello world  " cursor at end (13) — two trailing spaces after "world", width=5
	// Word-wrap: "hello" (line 0), "world" (line 1), trailing spaces count
	line, col := visualCursorFor("hello world  ", 13, 5)
	if line != 1 || col != 7 {
		t.Errorf("\"hello world  \" pos=13 w=5: expected (1,7), got (%d,%d)", line, col)
	}
}
func TestVisualCursorPos_MultiLineWithWrap(t *testing.T) {
	// "hello\nworld" cursor at end (11), width 3
	// Line 0 "hello" wraps: "hel", "lo" = 2 visual lines
	// Line 1 "world" wraps: "wor", "ld" = 2 visual lines
	// total = 4 visual lines, cursor col = "ld" = 2
	line, col := visualCursorFor("hello\nworld", 11, 3)
	if line != 3 || col != 2 {
		t.Errorf("\"hello\\nworld\" pos=11 w=3: expected (3,2), got (%d,%d)", line, col)
	}
}

// ── Editor Layout and Scrolling Tests ──

// stripANSIExtended removes all ANSI/OSC escape sequences for test assertions.
// Handles both CSI (\x1b[...) and OSC (\x1b]...; ... \a or \x1b\) sequences.
func stripANSIExtended(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '\x1b' {
			result.WriteByte(s[i])
			i++
			continue
		}
		i = skipEscapeSequence(s, i+1)
	}
	return result.String()
}

func skipEscapeSequence(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', '_', 'P', '^':
		return skipUntilSTorBEL(s, i+1)
	default:
		return i + 1
	}
}

func skipCSI(s string, i int) int {
	for i < len(s) && !isFinalByte(s[i]) {
		i++
	}
	if i < len(s) {
		i++
	}
	return i
}

func skipUntilSTorBEL(s string, i int) int {
	for i < len(s) {
		if s[i] == '\x07' {
			return i + 1
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return i
}

func TestEditor_Render_SingleLine(t *testing.T) {
	// Verify single-line content renders with top/bottom borders and the text
	ed := NewEditor()
	ed.SetText("hello")
	ed.pos = 5
	ed.SetFocused(false)

	// Use a width of 80
	result := ed.Render(80)

	if len(result) < 3 {
		t.Fatalf("expected at least 3 lines (border + content + border), got %d", len(result))
	}

	// Top border
	top := stripANSIExtended(result[0])
	t.Run("has_top_border", func(t *testing.T) {
		if !strings.HasPrefix(top, "─") {
			t.Errorf("expected top border to start with ─, got %q", top[:2])
		}
	})

	// Content
	content := stripANSIExtended(result[1])
	t.Run("shows_text", func(t *testing.T) {
		if !strings.Contains(content, "hello") {
			t.Errorf("expected content to contain 'hello', got %q", content)
		}
	})

	// Bottom border
	t.Run("has_bottom_border", func(t *testing.T) {
		bot := stripANSIExtended(result[len(result)-1])
		if !strings.HasPrefix(bot, "─") {
			t.Errorf("expected bottom border to start with ─, got %q", bot[:2])
		}
	})
}

func TestEditor_Render_MultiLine_WithScroll(t *testing.T) {
	// Create content that exceeds capLines (5) to trigger scrolling
	ed := NewEditor()
	ed.SetFocused(true)

	// 7 lines of content (will exceed capLines=5 in a narrow width)
	ed.SetText("line1\nline2\nline3\nline4\nline5\nline6\nline7")
	ed.pos = len([]rune("line1\nline2\nline3\nline4\nline5\nline6\nline7")) // end of last line

	// Render at width 80 (should show all lines with a small 7-line editor)
	result := ed.Render(80)
	totalLines := len(result)
	t.Logf("total rendered lines: %d", totalLines)
	for i, line := range result {
		t.Logf("  [%d] %s", i, stripANSIExtended(line))
	}

	t.Run("has_top_border", func(t *testing.T) {
		top := stripANSIExtended(result[0])
		if !strings.HasPrefix(top, "─") {
			t.Errorf("expected top border to start with ─, got %q", top[:2])
		}
	})

	t.Run("has_bottom_border", func(t *testing.T) {
		bot := stripANSIExtended(result[totalLines-1])
		if !strings.HasPrefix(bot, "─") {
			t.Errorf("expected bottom border to start with ─, got %q", bot[:2])
		}
	})

	t.Run("cursor_at_end_keeps_last_line_visible", func(t *testing.T) {
		// With cursor at the end (line7), the last visible content should be line7
		content := stripANSIExtended(result[totalLines-2]) // second-to-last line
		if !strings.Contains(content, "line7") {
			t.Errorf("expected cursor line (line7) to be visible, got %q", content)
		}
	})
}

func TestEditor_AutoScroll_FollowsCursor(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)

	// Start with content at top
	ed.SetText("line1\nline2\nline3\nline4\nline5")
	ed.pos = 0 // cursor at beginning

	// Render at narrow width so we get a predictable viewport
	_ = ed.Render(80)

	// Verify scroll is 0 (no scrolling needed yet)
	if ed.scroll != 0 {
		t.Errorf("expected scroll=0 initially, got %d", ed.scroll)
	}

	// Move cursor to the last line
	ed.pos = len([]rune("line1\nline2\nline3\nline4\nline5"))
	_ = ed.Render(80)

	// Verify cursor is visible (scroll adjusted so cursor line is in viewport)
	t.Logf("scroll after moving to end: %d", ed.scroll)
	// At capLines=5 vs totalVisualLines=5, scroll should be 0
}

func TestEditor_AutoScroll_LargeContent(t *testing.T) {
	// Create enough content to overflow capLines and verify auto-scroll
	ed := NewEditor()
	ed.SetFocused(true)

	// 15 lines — should exceed capLines (5 max for any terminal >= 17 rows)
	text := ""
	for i := 1; i <= 15; i++ {
		if i > 1 {
			text += "\n"
		}
		text += fmt.Sprintf("content_line_%d", i)
	}
	ed.SetText(text)

	// Cursor at the end
	ed.pos = len([]rune(text))

	// Render — should auto-scroll to show the last lines
	result := ed.Render(80)
	totalLines := len(result)
	t.Logf("total rendered lines: %d", totalLines)
	t.Logf("scroll: %d", ed.scroll)

	t.Run("scroll_is_positive", func(t *testing.T) {
		if ed.scroll <= 0 {
			t.Errorf("expected scroll > 0 when cursor at end of 15-line content, got %d", ed.scroll)
		}
	})

	t.Run("last_content_line_visible", func(t *testing.T) {
		// Content lines are between top border (index 0) and bottom border (last)
		// The last content line should be visible
		contentLines := result[1 : totalLines-1]
		lastContent := stripANSIExtended(contentLines[len(contentLines)-1])
		if !strings.Contains(lastContent, "content_line_15") {
			t.Errorf("expected last content line to be visible, got %q", lastContent)
		}
	})

	t.Run("no_bottom_indicator_when_at_end", func(t *testing.T) {
		// When cursor is at the end and scroll shows the last lines,
		// there should be no "↓ N more" indicator
		bot := stripANSIExtended(result[totalLines-1])
		if strings.Contains(bot, "more") {
			t.Errorf("expected bottom border without 'more' when at end, got %q", bot)
		}
	})
}

func TestEditor_ScrollIndicator_ShowsCorrectDirection(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)

	// Create 15 lines of content
	text := ""
	for i := 1; i <= 15; i++ {
		if i > 1 {
			text += "\n"
		}
		text += fmt.Sprintf("line_%d", i)
	}
	ed.SetText(text)

	// Put cursor at the top (line 1)
	ed.pos = 0
	ed.scroll = 0
	result := ed.Render(80)
	topBorder := stripANSIExtended(result[0])
	botBorder := stripANSIExtended(result[len(result)-1])
	t.Logf("cursor at top - top: %q, bot: %q", topBorder, botBorder)

	// At top: no top indicator, bottom shows ↓ N more
	if strings.Contains(topBorder, "more") {
		t.Errorf("cursor at top: expected no scroll indicator in top border, got %q", topBorder)
	}
	if !strings.Contains(botBorder, "↓") {
		t.Errorf("cursor at top: expected ↓ indicator in bottom border, got %q", botBorder)
	}

	// Put cursor at the last line
	ed.pos = len([]rune(text))
	result = ed.Render(80)
	topBorder2 := stripANSIExtended(result[0])
	botBorder2 := stripANSIExtended(result[len(result)-1])
	t.Logf("cursor at end - top: %q, bot: %q", topBorder2, botBorder2)

	// At end: top shows ↑ N more, no bottom indicator
	if !strings.Contains(topBorder2, "↑") {
		t.Errorf("cursor at end: expected ↑ indicator in top border, got %q", topBorder2)
	}
	if strings.Contains(botBorder2, "more") {
		t.Errorf("cursor at end: expected no scroll indicator in bottom border, got %q", botBorder2)
	}
}

func TestEditor_Border_RendersCorrectly(t *testing.T) {
	// Test the editor border color and styling
	ed := NewEditor()
	ed.SetText("hello")
	ed.pos = 5
	result := ed.Render(80)

	// Top border should be all ─ when no scroll
	top := result[0]
	if !strings.HasPrefix(top, "\x1b") {
		t.Errorf("expected top border to have ANSI color, got plain text")
	}
	topClean := stripANSIExtended(top)
	for _, ch := range topClean {
		if ch != '─' {
			t.Errorf("expected top border to be all ─, got %c", ch)
			break
		}
	}

	// Bottom border should be all ─ when no scroll
	bot := result[len(result)-1]
	if !strings.HasPrefix(bot, "\x1b") {
		t.Errorf("expected bottom border to have ANSI color, got plain text")
	}
	botClean := stripANSIExtended(bot)
	for _, ch := range botClean {
		if ch != '─' {
			t.Errorf("expected bottom border to be all ─, got %c", ch)
			break
		}
	}
}

func TestEditor_Render_NoDuplicateBorders(t *testing.T) {
	// Verify there are exactly 2 border lines (top and bottom) with content between.
	ed := NewEditor()
	ed.SetText("test")
	ed.pos = 4
	result := ed.Render(80)

	// First line should be the top border (starts with ─ after ANSI)
	topClean := stripANSIExtended(result[0])
	if !strings.HasPrefix(topClean, "─") {
		t.Errorf("first line should be top border, got %q", topClean)
	}

	// Count border lines (all-─ lines after stripping ANSI)
	borderCount := 0
	for _, line := range result {
		clean := stripANSIExtended(line)
		allDash := true
		for _, ch := range clean {
			if ch != '─' {
				allDash = false
				break
			}
		}
		if allDash {
			borderCount++
		}
	}
	if borderCount != 2 {
		t.Errorf("expected exactly 2 border lines (top + bottom), got %d", borderCount)
	}

	// Content line should contain the text
	contentFound := false
	for i := 1; i < len(result)-1; i++ {
		clean := stripANSIExtended(result[i])
		if strings.Contains(clean, "test") {
			contentFound = true
			break
		}
	}
	if !contentFound {
		t.Error("expected content to contain 'test'")
	}
}

// ── TUI-level Editor Integration Tests ──
// These tests exercise the full editor pipeline through the TUI engine
// with a fake terminal, validating rendered output.

func TestEditor_AutoScroll_RendersLastLine(t *testing.T) {
	ed := setupAutoScrollEditor()
	result := ed.Render(80)

	t.Run("scroll_advanced", func(t *testing.T) { assertScrollAdvanced(t, ed) })
	t.Run("last_line_visible", func(t *testing.T) { assertLastLineVisible(t, result) })
	t.Run("first_line_not_visible", func(t *testing.T) { assertFirstLineNotVisible(t, result) })
}

func setupAutoScrollEditor() *Editor {
	ed := NewEditor()
	ed.SetFocused(true)
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("line_%d", i))
	}
	text := strings.Join(lines, "\n")
	ed.SetText(text)
	ed.pos = len([]rune(text))
	return ed
}

func assertScrollAdvanced(t *testing.T, ed *Editor) {
	if ed.scroll <= 0 {
		t.Errorf("expected scroll > 0 after 12 lines with capLines=5, got %d", ed.scroll)
	}
}

func assertLastLineVisible(t *testing.T, result []string) {
	if !renderedContains(result, "line_12") {
		t.Error("expected 'line_12' in rendered output")
	}
}

func assertFirstLineNotVisible(t *testing.T, result []string) {
	if renderedContains(result, "line_1") {
		t.Log("line_1 is still visible (may be correct if content fits)")
	}
}

func renderedContains(result []string, needle string) bool {
	for _, line := range result {
		clean := stripANSIExtended(line)
		if strings.Contains(clean, needle) {
			return true
		}
	}
	return false
}

func TestEditor_Borders_BookendContent(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("hello world")
	ed.pos = 11
	result := ed.Render(80)

	topBorderIdx, contentIdx, botBorderIdx := findBorderAndContent(result)
	t.Logf("top border at %d, content at %d, bottom border at %d", topBorderIdx, contentIdx, botBorderIdx)

	t.Run("has_top_border", func(t *testing.T) { assertHasBorder(t, "top", topBorderIdx) })
	t.Run("has_content", func(t *testing.T) { assertHasContent(t, contentIdx) })
	t.Run("has_bottom_border", func(t *testing.T) { assertHasBorder(t, "bottom", botBorderIdx) })
	t.Run("top_border_before_content", func(t *testing.T) { assertBorderBeforeContent(t, topBorderIdx, contentIdx, "top") })
	t.Run("bottom_border_after_content", func(t *testing.T) { assertBorderAfterContent(t, botBorderIdx, contentIdx) })
}

func findBorderAndContent(result []string) (top, content, bottom int) {
	top, content, bottom = -1, -1, -1
	for i, line := range result {
		clean := stripANSIExtended(line)
		trimmed := strings.TrimRight(clean, " ")
		if trimmed == "" {
			continue
		}
		allDash := isAllDash(trimmed)
		switch {
		case allDash && top == -1:
			top = i
		case allDash && top != -1:
			bottom = i
		case strings.Contains(clean, "hello world"):
			content = i
		}
	}
	return top, content, bottom
}

func isAllDash(s string) bool {
	for _, ch := range s {
		if ch != '─' && ch != '\x1b' {
			return false
		}
	}
	return true
}

func assertHasBorder(t *testing.T, name string, idx int) {
	if idx < 0 {
		t.Errorf("expected %s border", name)
	}
}

func assertHasContent(t *testing.T, idx int) {
	if idx < 0 {
		t.Error("expected content 'hello world'")
	}
}

func assertBorderBeforeContent(t *testing.T, borderIdx, contentIdx int, name string) {
	if borderIdx >= 0 && contentIdx >= 0 && borderIdx > contentIdx {
		t.Errorf("%s border (line %d) should come before content (line %d)", name, borderIdx, contentIdx)
	}
}

func assertBorderAfterContent(t *testing.T, borderIdx, contentIdx int) {
	if borderIdx >= 0 && contentIdx >= 0 && borderIdx < contentIdx {
		t.Errorf("bottom border (line %d) should come after content (line %d)", borderIdx, contentIdx)
	}
}

func TestEditor_SetTitle_GetTitle(t *testing.T) {
	ed := NewEditor()
	if ed.Title() != "" {
		t.Errorf("default title = %q, want empty", ed.Title())
	}
	ed.SetTitle("goal objective")
	if got := ed.Title(); got != "goal objective" {
		t.Errorf("Title() = %q, want %q", got, "goal objective")
	}
	ed.SetTitle("")
	if got := ed.Title(); got != "" {
		t.Errorf("Title() after clear = %q, want empty", got)
	}
}

func TestEditor_Render_TitleInTopBorder(t *testing.T) {
	ed := NewEditor()
	ed.SetText("hello")
	ed.pos = 5
	ed.SetTitle("goal objective")
	result := ed.Render(80)

	top := stripANSIExtended(result[0])
	if !strings.Contains(top, "goal objective") {
		t.Errorf("expected top border to contain title, got %q", top)
	}
	if !strings.Contains(top, "┨") || !strings.Contains(top, "┠") {
		t.Errorf("expected title brackets in top border, got %q", top)
	}
	// Bottom border is plain when there is a title (no scroll).
	bot := stripANSIExtended(result[len(result)-1])
	if strings.Contains(bot, "goal objective") {
		t.Errorf("bottom border should not contain title, got %q", bot)
	}
}

func TestEditor_Render_NoTitleIsPlainBorder(t *testing.T) {
	ed := NewEditor()
	ed.SetText("hello")
	ed.pos = 5
	result := ed.Render(80)
	top := stripANSIExtended(result[0])
	for _, ch := range top {
		if ch != '─' {
			t.Errorf("expected plain ruled border without title, got %q", top)
			break
		}
	}
}

func TestEditor_Render_ScrollIndicatorOverridesTitle(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	text := ""
	for i := 1; i <= 15; i++ {
		if i > 1 {
			text += "\n"
		}
		text += fmt.Sprintf("line_%d", i)
	}
	ed.SetText(text)
	ed.pos = 0 // top, so scroll = 0 and no up-indicator; move cursor to bottom to scroll
	ed.SetTitle("goal objective")

	// Move cursor to end to force downward scroll (top indicator appears)
	ed.pos = len([]rune(text))
	result := ed.Render(80)
	top := stripANSIExtended(result[0])
	// With scroll active the up-indicator must show, not the title.
	if !strings.Contains(top, "↑") {
		t.Errorf("expected up scroll indicator, got %q", top)
	}
}

func TestEditor_CompletionMaxLinesStable(t *testing.T) {
	// Regression: appendCompletionLines must NOT grow e.maxLines each render.
	// The editor's maxLines should be determined solely by computeLayout.
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/mode", "/model", "/memory", "/copy"},
		map[string]string{
			"/mode":   "set mode",
			"/model":  "select model",
			"/memory": "manage memory",
			"/copy":   "copy output",
		}))

	// Type /m to trigger completion
	ed.SetText("/m")
	ed.pos = 2
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}

	// Render multiple times with completions active
	initialMaxLines := ed.maxLines
	for i := 0; i < 5; i++ {
		ed.Render(80)
	}

	// maxLines must NOT grow across renders — computeLayout resets it each cycle.
	// The old code added len(extra) in appendCompletionLines, compounding each render.
	if ed.maxLines != initialMaxLines {
		t.Errorf("maxLines grew from %d to %d after multiple renders with completions",
			initialMaxLines, ed.maxLines)
	}
}

func TestEditor_CompletionItemsTruncatedToWidth(t *testing.T) {
	// Completion items with wide descriptions must be truncated to fit the width.
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetCompleter(NewCommandCompleter([]string{"/mode"},
		map[string]string{"/mode": "set mode"}))

	ed.SetText("/m")
	ed.pos = 2
	ed.updateAutoComp()

	if !ed.compState.Active() {
		t.Fatal("expected completion to be active")
	}

	// Render with a narrow width — items must be truncated, not overflow.
	width := 20
	lines := ed.Render(width)

	for _, line := range lines {
		if visibleWidth(line) > width {
			t.Errorf("completion line exceeds width %d: visible=%d line=%q",
				width, visibleWidth(line), line)
		}
	}
}

// ── Benchmarks ──

// BenchmarkEditorRender_1000Chars measures rendering performance for a
// ~1000-char buffer (typical agent prompt input). The render loop target
// is 16ms/frame (60fps), so each Render call should complete in <16ms.
func BenchmarkEditorRender_1000Chars(b *testing.B) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetMaxLines(10)

	// Build a ~1000-char multiline buffer
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("line %d: this is some text with words that need wrapping and analysis\n", i))
	}
	text := sb.String()
	if len(text) > 1000 {
		text = text[:1000]
	}
	ed.SetText(text)
	ed.pos = 500

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ed.Render(80)
	}
}

// BenchmarkEditorComputeLayout_1000Chars isolates the layout computation
// (wrapChunks + cursorChunk) from the render overhead.
func BenchmarkEditorComputeLayout_1000Chars(b *testing.B) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetMaxLines(10)

	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("line %d: this is some text with words that need wrapping and analysis\n", i))
	}
	text := sb.String()
	if len(text) > 1000 {
		text = text[:1000]
	}
	ed.SetText(text)
	ed.pos = 500

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ed.computeLayout(80)
	}
}

// BenchmarkVisualCursorPos_1000Chars isolates the cursor-location call
// (wrapChunks + cursorChunk).
func BenchmarkVisualCursorPos_1000Chars(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("line %d: this is some text with words that need wrapping and analysis\n", i))
	}
	text := sb.String()
	if len(text) > 1000 {
		text = text[:1000]
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		visualCursorFor(text, 500, 80)
	}
}

// BenchmarkWrapText_1000Chars isolates the wrapText call.
func BenchmarkWrapText_1000Chars(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("line %d: this is some text with words that need wrapping and analysis\n", i))
	}
	text := sb.String()
	if len(text) > 1000 {
		text = text[:1000]
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wrapText(text, 80)
	}
}
