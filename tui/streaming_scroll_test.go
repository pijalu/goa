// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strconv"
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
}

func newScreenEmulator(h, w int) *screenEmulator {
	return &screenEmulator{
		h:      h,
		w:      w,
		screen: make([]string, h),
		row:    0,
		col:    0,
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
	if e.row < e.h-1 {
		e.row++
		return
	}
	// Scroll: the top line moves to scrollback, everything shifts up, and
	// the cursor stays on the (now blank) bottom row.
	e.scrollback = append(e.scrollback, e.screen[0])
	copy(e.screen, e.screen[1:])
	e.screen[e.h-1] = ""
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
		row, col := 1, 1
		fmt.Sscanf(params, "%d;%d", &row, &col)
		e.row = max(0, row-1)
		e.col = max(0, col-1)
	case 'A':
		e.row = max(0, e.row-paramInt(params, 1))
	case 'B':
		e.row = min(e.h-1, e.row+paramInt(params, 1))
	case 'G':
		e.col = max(0, paramInt(params, 1)-1)
	case 'J':
		e.eraseDisplay(params)
	case 'K':
		e.eraseLine(params)
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

func paramInt(params string, defaultVal int) int {
	if params == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(params)
	if err != nil {
		return defaultVal
	}
	return n
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
