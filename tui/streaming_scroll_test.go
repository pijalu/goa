// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// screenEmulator is a minimal terminal emulator for verifying TUI output.
// It understands the small subset of escape sequences used by the TUI:
// synchronized output boundaries, CUP, cursor up/down, clear-line, clear-screen,
// and carriage-return/newline scrolling. It keeps a scrollback of lines that
// were pushed off the top of the screen.
type screenEmulator struct {
	h, w       int
	screen     []string
	scrollback []string
	row, col   int
	pending    strings.Builder
	// scrollTop/scrollBot model the DECSTBM scroll region (0-indexed,
	// inclusive): \n scrolls only within the region, so pinned chrome below it
	// never moves. Defaults to the full screen.
	scrollTop, scrollBot int
}

func newScreenEmulator(h, w int) *screenEmulator {
	return &screenEmulator{
		h:         h,
		w:         w,
		screen:    make([]string, h),
		row:       0,
		col:       0,
		scrollTop: 0,
		scrollBot: h - 1,
	}
}

func (e *screenEmulator) Process(s string) {
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '\r':
			e.flush()
			e.col = 0
			i++
		case '\n':
			e.flush()
			e.newLine()
			i++
		case '\x1b':
			n := e.parseEscape(s[i:])
			if n == 0 {
				// Unknown/unparseable escape: skip the ESC to avoid infinite loop.
				i++
			} else {
				i += n
			}
		default:
			if c >= 0x20 {
				e.pending.WriteByte(c)
			}
			i++
		}
	}
	e.flush()
}

func (e *screenEmulator) flush() {
	if e.pending.Len() == 0 {
		return
	}
	if e.row >= 0 && e.row < e.h {
		text := ansi.Strip(e.pending.String())
		if e.col == 0 {
			e.screen[e.row] = text
		} else {
			e.screen[e.row] += text
		}
	}
	e.pending.Reset()
}

func (e *screenEmulator) newLine() {
	if e.row < e.scrollBot {
		e.row++
		return
	}
	if e.row == e.scrollBot {
		// Scroll within the region: the region's top line moves to scrollback,
		// everything inside the region shifts up, and a blank row opens at the
		// region bottom. Rows outside the region are untouched.
		e.scrollback = append(e.scrollback, e.screen[e.scrollTop])
		copy(e.screen[e.scrollTop:e.scrollBot], e.screen[e.scrollTop+1:e.scrollBot+1])
		e.screen[e.scrollBot] = ""
		return
	}
	// Cursor below the region (pinned chrome): plain advance, clamped.
	if e.row < e.h-1 {
		e.row++
	}
}

// parseEscape consumes a single escape sequence and returns the number of
// bytes processed, or 0 if it could not be parsed.
func (e *screenEmulator) parseEscape(s string) int {
	if !strings.HasPrefix(s, "\x1b[") {
		if strings.HasPrefix(s, "\x1b]8;") {
			// OSC 8 hyperlink: read until BEL.
			if idx := strings.Index(s, "\x07"); idx >= 0 {
				return idx + 1
			}
		}
		return 0
	}
	// CSI: read until a byte in 0x40-0x7E.
	for i := 2; i < len(s); i++ {
		final := s[i]
		if final >= 0x40 && final <= 0x7E {
			e.handleCSI(s[2:i], final)
			return i + 1
		}
	}
	return 0
}

func (e *screenEmulator) handleCSI(params string, final byte) {
	switch final {
	case 'H', 'f':
		// Text written just before a cursor move belongs at the CURRENT row;
		// commit it before repositioning, or it would land on the destination row
		// (the "multiple lines stacked on one row" false positive).
		e.flush()
		row, col := 1, 1
		fmt.Sscanf(params, "%d;%d", &row, &col)
		e.row = max(0, row-1)
		e.col = max(0, col-1)
	case 'A':
		e.flush()
		e.row = max(0, e.row-paramInt(params, 1))
	case 'B':
		e.flush()
		e.row = min(e.h-1, e.row+paramInt(params, 1))
	case 'G':
		e.flush()
		e.col = max(0, paramInt(params, 1)-1)
	case 'J':
		e.flush()
		e.eraseDisplay(params)
	case 'K':
		e.flush()
		e.eraseLine(params)
	case 'r':
		// DECSTBM: set scroll region ("\x1b[top;bot r" 1-indexed; "\x1b[r" =
		// full screen). Homes the cursor per DEC spec.
		e.flush()
		top, bot := 1, e.h
		if params != "" {
			parts := strings.SplitN(params, ";", 2)
			top = paramInt(parts[0], 1)
			if len(parts) > 1 {
				bot = paramInt(parts[1], e.h)
			}
		}
		e.scrollTop = max(0, top-1)
		e.scrollBot = min(e.h-1, bot-1)
		if e.scrollBot < e.scrollTop {
			e.scrollBot = e.scrollTop
		}
		e.row, e.col = 0, 0
	}
}

func (e *screenEmulator) eraseDisplay(params string) {
	if params != "2" && params != "3" {
		return
	}
	for i := range e.screen {
		e.screen[i] = ""
	}
	if params == "3" {
		e.scrollback = nil
	}
}

func (e *screenEmulator) eraseLine(params string) {
	if params != "" && params != "2" {
		return
	}
	if e.row >= 0 && e.row < e.h {
		e.screen[e.row] = ""
	}
}

func (e *screenEmulator) Visible(row int) string {
	if row < 0 || row >= e.h {
		return ""
	}
	return e.screen[row]
}

func (e *screenEmulator) Scrollback() []string { return e.scrollback }

// TestChatLargeAppendScrollsVirtually is a regression test for the TUI not
// scrolling during streaming. It appends a large block to an already-scrolled
// chat and verifies, via a minimal terminal emulator, that the new content
// ends up at the bottom of the visible screen and the old top lines are
// pushed into scrollback — without a full screen erase.
func visibleContains(emu *screenEmulator, h int, text string) bool {
	for r := 0; r < h; r++ {
		if strings.Contains(emu.Visible(r), text) {
			return true
		}
	}
	return false
}

func logScreen(t *testing.T, emu *screenEmulator, h int) {
	for r := 0; r < h; r++ {
		t.Logf("row %d: %q", r, emu.Visible(r))
	}
}

func assertNoFullEraseAfterInitial(t *testing.T, frames []string) {
	t.Helper()
	if len(frames) < 2 {
		return
	}
	afterInitial := strings.Join(frames[1:], "")
	if strings.Contains(afterInitial, "\x1b[2J") || strings.Contains(afterInitial, "\x1b[3J") {
		t.Errorf("large append should scroll, not use full screen/scrollback erase")
	}
}

func TestChatLargeAppendScrollsVirtually(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Fill the chat so the viewport is already scrolled.
	for i := 0; i < 20; i++ {
		chat.AddSystemMessage(fmt.Sprintf("baseline %d", i))
	}
	engine.RenderNow()

	// Append a large preformatted block that pushes the viewport up by many lines.
	var big strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&big, "tool output line %d\n", i)
	}
	chat.AddSystemMessagePreformatted(big.String())
	engine.RenderNow()

	frames := collectFrames(term)
	if len(frames) < 2 {
		t.Fatal("expected at least initial + append frames")
	}

	// Replay all terminal writes through the emulator. The first frame is the
	// initial full clear+draw; the subsequent frames exercise differential
	// scrolling.
	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emu.Process(w)
	}

	if !visibleContains(emu, term.h, "tool output line 19") {
		t.Errorf("latest tool output line not visible on emulated screen")
		logScreen(t, emu, term.h)
	}
	if len(emu.Scrollback()) == 0 {
		t.Errorf("expected scrollback to grow after large append, got %d lines", len(emu.Scrollback()))
	}
	assertNoFullEraseAfterInitial(t, frames)
}

// TestChatLargeAppend_PopulatesScrollbackWithAllLines reproduces bug #4: when a
// single append is larger than the viewport, the lines that scroll past the top
// must still be present in terminal scrollback so the user can scroll back to
// them. Previously the differential renderer emitted bare newlines to scroll
// but never wrote the text of the skipped middle lines, leaving blanks in
// scrollback.
func TestChatLargeAppend_PopulatesScrollbackWithAllLines(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Small initial content so the first frame is a normal (screen-filling)
	// render that establishes prevLines without scrollback.
	chat.AddSystemMessage("start")
	engine.RenderNow()

	// One huge append — far taller than the viewport — in a single render.
	var big strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&big, "huge line %d\n", i)
	}
	chat.AddSystemMessagePreformatted(big.String())
	engine.RenderNow()

	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emu.Process(w)
	}

	// The latest line must be visible...
	if !visibleContains(emu, term.h, "huge line 59") {
		t.Errorf("latest line not visible on screen")
		logScreen(t, emu, term.h)
	}
	// ...and an early line that scrolled off must be recoverable from scrollback.
	all := strings.Join(emu.Scrollback(), "\n")
	if !strings.Contains(all, "huge line 5") {
		t.Errorf("early line missing from scrollback (gap not populated); scrollback lines=%d", len(emu.Scrollback()))
		t.Logf("scrollback:\n%s", all)
	}
}

// TestChatFirstScroll_PopulatesScrollbackWithScrolledOffLines reproduces the
// first-message scrollback bug: when the first scroll of a session is smaller
// than the viewport height, bare newlines would push blank rows into
// scrollback because the previous viewport was not yet full. The fix writes
// every scrolled-off row directly on the first scroll.
func TestChatFirstScroll_PopulatesScrollbackWithScrolledOffLines(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Small initial content so the viewport is at the top and not yet full.
	chat.AddSystemMessage("start")
	engine.RenderNow()

	// First message append: just large enough to exceed the viewport by a few
	// lines. This triggers the small-scroll path in emitViewportScroll.
	var msg strings.Builder
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&msg, "first line %d\n", i)
	}
	chat.AddSystemMessagePreformatted(msg.String())
	engine.RenderNow()

	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emu.Process(w)
	}

	// The latest line must be visible...
	if !visibleContains(emu, term.h, "first line 14") {
		t.Errorf("latest line not visible on screen")
		logScreen(t, emu, term.h)
	}
	// ...and an early line from the scrolled-off region must be in scrollback.
	all := strings.Join(emu.Scrollback(), "\n")
	if !strings.Contains(all, "first line 1") {
		t.Errorf("early line missing from scrollback on first small scroll; scrollback lines=%d", len(emu.Scrollback()))
		t.Logf("scrollback:\n%s", all)
	}
	if !strings.Contains(all, "start") {
		t.Errorf("initial content missing from scrollback on first small scroll")
	}
}
