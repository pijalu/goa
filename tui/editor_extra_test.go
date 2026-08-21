// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
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
	// Word-wrap: "hello" (line 0), "world" (line 1), trailing spaces wrap onto line 2.
	line, col := visualCursorPos("hello world  ", 13, 5)
	if line != 2 || col != 2 {
		t.Errorf("\"hello world  \" pos=13 w=5: expected (2,2), got (%d,%d)", line, col)
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
