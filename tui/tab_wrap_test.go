// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/review"
)

// screenCell tracks a single terminal cell character.
type screenCell struct {
	ch rune
}

// terminalScreen2 is a minimal terminal emulator that expands tabs to 8-column
// tab stops and respects auto-wrap when a character is written in the last
// column.
type terminalScreen2 struct {
	w, h     int
	rows     [][]screenCell
	row, col int
}

func splitInts2(s, sep string) []int {
	parts := strings.Split(s, sep)
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		out = append(out, parseIntDefault2(p, 0))
	}
	return out
}

func parseIntDefault2(s string, def int) int {
	n := 0
	parsed := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		parsed = true
	}
	if !parsed {
		return def
	}
	return n
}

func clamp2(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func newTerminalScreen2(w, h int) *terminalScreen2 {
	s := &terminalScreen2{w: w, h: h}
	s.clear()
	return s
}

func (s *terminalScreen2) clear() {
	s.rows = make([][]screenCell, s.h)
	for i := range s.rows {
		s.rows[i] = make([]screenCell, s.w)
	}
	s.row, s.col = 0, 0
}

func (s *terminalScreen2) setPos(r, c int) {
	s.row = clamp2(r, 0, s.h-1)
	s.col = clamp2(c, 0, s.w-1)
}

func (s *terminalScreen2) moveDown(n int) {
	s.row = clamp2(s.row+n, 0, s.h-1)
}

func (s *terminalScreen2) moveUp(n int) {
	s.row = clamp2(s.row-n, 0, s.h-1)
}

func (s *terminalScreen2) setCol(c int) {
	s.col = clamp2(c, 0, s.w-1)
}

func (s *terminalScreen2) eraseLineFull() {
	for c := range s.rows[s.row] {
		s.rows[s.row][c] = screenCell{}
	}
}

func (s *terminalScreen2) eraseLineFromCursor() {
	for c := s.col; c < s.w; c++ {
		s.rows[s.row][c] = screenCell{}
	}
}

func (s *terminalScreen2) putRune(r rune) {
	if r == '\t' {
		target := ((s.col / 8) + 1) * 8
		for s.col < target && s.col < s.w {
			s.rows[s.row][s.col] = screenCell{ch: ' '}
			s.col++
		}
		return
	}
	if s.col >= s.w {
		// Auto-wrap to next line (typical terminal behaviour when a character
		// is written in the last column).
		s.row++
		if s.row >= s.h {
			s.scrollUp()
		}
		s.col = 0
	}
	s.rows[s.row][s.col] = screenCell{ch: r}
	s.col++
}

func (s *terminalScreen2) scrollUp() {
	s.rows = append(s.rows[1:], make([]screenCell, s.w))
	s.row = s.h - 1
}

func (s *terminalScreen2) writeText(text string) {
	for _, r := range text {
		switch r {
		case '\r':
			s.col = 0
		case '\n':
			s.row++
			if s.row >= s.h {
				s.scrollUp()
			}
			s.col = 0
		default:
			s.putRune(r)
		}
	}
}

func (s *terminalScreen2) process(data string) {
	for len(data) > 0 {
		idx := strings.IndexByte(data, '\x1b')
		if idx == -1 {
			s.writeText(data)
			return
		}
		if idx > 0 {
			s.writeText(data[:idx])
			data = data[idx:]
		}
		seq, rest := s.nextEscape(data)
		s.handleEscape(seq)
		data = rest
	}
}

func (s *terminalScreen2) nextEscape(data string) (seq, rest string) {
	if len(data) < 2 || data[0] != '\x1b' {
		return data[:1], data[1:]
	}
	switch data[1] {
	case '[':
		return s.nextCSI(data)
	case ']':
		return s.nextOSC(data)
	default:
		return data[:2], data[2:]
	}
}

func (s *terminalScreen2) nextCSI(data string) (seq, rest string) {
	for i := 2; i < len(data); i++ {
		b := data[i]
		if b >= 0x40 && b <= 0x7e {
			return data[:i+1], data[i+1:]
		}
	}
	return data, ""
}

func (s *terminalScreen2) nextOSC(data string) (seq, rest string) {
	for i := 2; i < len(data); i++ {
		if data[i] == '\x07' {
			return data[:i+1], data[i+1:]
		}
		if data[i] == '\x1b' && i+1 < len(data) && data[i+1] == '\\' {
			return data[:i+2], data[i+2:]
		}
	}
	return data, ""
}

func (s *terminalScreen2) handleEscape(seq string) {
	if len(seq) < 3 {
		return
	}
	body := seq[2 : len(seq)-1]
	final := seq[len(seq)-1]
	switch final {
	case 'H', 'f':
		s.handleCUP(body)
	case 'A':
		s.moveUp(parseIntDefault2(body, 1))
	case 'B':
		s.moveDown(parseIntDefault2(body, 1))
	case 'G':
		s.setCol(parseIntDefault2(body, 1) - 1)
	case 'J':
		s.handleEraseDisplay(body)
	case 'K':
		s.handleEraseLine(body)
	case 'm', 'h', 'l':
		// ignore styling and mode changes for screen content checks
	}
}

func (s *terminalScreen2) handleCUP(body string) {
	parts := splitInts2(body, ";")
	r, c := 1, 1
	if len(parts) > 0 {
		r = parts[0]
	}
	if len(parts) > 1 {
		c = parts[1]
	}
	s.setPos(r-1, c-1)
}

func (s *terminalScreen2) handleEraseDisplay(body string) {
	if body == "2" || body == "3" {
		s.clear()
		return
	}
	s.eraseLineFromCursor()
	for r := s.row + 1; r < s.h; r++ {
		for c := range s.rows[r] {
			s.rows[r][c] = screenCell{}
		}
	}
}

func (s *terminalScreen2) handleEraseLine(body string) {
	if body == "2" {
		s.eraseLineFull()
	} else {
		s.eraseLineFromCursor()
	}
}

func (s *terminalScreen2) visible() []string {
	out := make([]string, s.h)
	for i, row := range s.rows {
		var b strings.Builder
		for _, cell := range row {
			if cell.ch == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(cell.ch)
			}
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func makeTabDiff() string {
	// Build a context line that the TUI used to think was exactly 80 columns
	// wide but the terminal renders wider because of 8-column tab stops.
	// Prefix: "  " (pager) + " " (diff context marker) = 3 columns.
	// Two tabs bring the terminal cursor to column 19 (3 -> 8 -> 16 -> text).
	// "if !ok {" is 9 columns, so after text the terminal cursor is at column 25.
	// Fill with 55 spaces so the terminal line is 80 columns long.
	raw := " \t\tif !ok {" + strings.Repeat(" ", 55)
	return fmt.Sprintf(`diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main
%s
 func foo() {}
 func bar() {}
 func baz() {}
`, raw)
}

func renderScreenAfterDown(t *testing.T, baseCount int) ([]string, []string) {
	t.Helper()
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < baseCount; i++ {
		cv.AddSystemMessage(fmt.Sprintf("chat line %02d", i))
	}
	engine.AddChild(cv)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, makeTabDiff())
	pager.SetViewport(term.w, 19)
	engine.ShowOverlay(pager, OverlayOptions{Width: 0, Height: 19, BottomOffset: 5, CaptureInput: true})
	engine.RenderNow()

	screen := newTerminalScreen2(term.w, term.h)
	for _, w := range term.Writes() {
		screen.process(w)
	}
	before := screen.visible()

	term.onInput("down")
	engine.RenderNow()

	afterScreen := newTerminalScreen2(term.w, term.h)
	for _, w := range term.Writes() {
		afterScreen.process(w)
	}
	return before, afterScreen.visible()
}

func countOccurrences(rows []string, substr string) int {
	n := 0
	for _, l := range rows {
		if strings.Contains(l, substr) {
			n++
		}
	}
	return n
}

func findSelectedRow(rows []string) int {
	for i, l := range rows {
		if strings.HasPrefix(strings.TrimSpace(l), ">") {
			return i
		}
	}
	return -1
}

func assertSingleRowFor(t *testing.T, rows []string, substr string) {
	t.Helper()
	if got := countOccurrences(rows, substr); got != 1 {
		t.Errorf("%q appears on %d screen rows, want 1", substr, got)
	}
}

func assertSelectionMovedDown(t *testing.T, before, after []string) {
	t.Helper()
	br := findSelectedRow(before)
	ar := findSelectedRow(after)
	if br < 0 || ar < 0 {
		t.Fatalf("selected row missing: before=%d after=%d", br, ar)
	}
	if ar != br+1 {
		t.Errorf("selected row moved %d -> %d, want %d", br, ar, br+1)
	}
}

func TestReviewScroll_TabInducedWrap(t *testing.T) {
	for _, baseCount := range []int{0, 50} {
		t.Run(fmt.Sprintf("base%d", baseCount), func(t *testing.T) {
			before, after := renderScreenAfterDown(t, baseCount)

			assertSingleRowFor(t, after, "if !ok {")
			assertSelectionMovedDown(t, before, after)
		})
	}
}

func TestReviewPager_QuitWorksWithTabDiff(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	cv.AddSystemMessage("chat line")
	engine.AddChild(cv)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, makeTabDiff())
	pager.SetViewport(term.w, 19)
	closed := false
	handle := engine.ShowOverlay(pager, OverlayOptions{Width: 0, Height: 19, BottomOffset: 5, CaptureInput: true})
	pager.OnClose = func() {
		closed = true
		if handle != nil && handle.Hide != nil {
			handle.Hide()
		}
	}
	engine.RenderNow()

	term.onInput("q")
	engine.RenderNow()

	if !closed {
		t.Error("OnClose was not called after pressing 'q'")
	}
	if len(engine.overlayStack) != 0 {
		t.Errorf("overlay still on stack after pressing 'q': %d", len(engine.overlayStack))
	}
}
