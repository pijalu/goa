// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/rivo/uniseg"
)

// TermEmulator is a faithful, per-cell terminal emulator for verifying the
// Compositor's output. Unlike the coarse screenEmulator, it tracks the cursor
// column per character (grapheme-width-aware), models DEC-style DEFERRED
// auto-wrap (the cursor enters a pending-wrap state after filling the last
// column, which is the exact mechanism that desyncs relative-cursor
// differential renderers), and honors scrollback. This is the tool that lets
// tests catch the streaming "ghosting" class of bugs — and it doubles as the
// agent's "what is actually on the screen" reader.
type TermEmulator struct {
	w, h        int
	screen      [][]string // [row][col] cell text (ANSI-stripped for assertion)
	scrollback  []string
	row, col    int
	pendingWrap bool // DEC deferred wrap: last cell filled, next char wraps
	// scrollTop/scrollBot model the DECSTBM scroll region (0-indexed,
	// inclusive). \n scrolls only within [scrollTop, scrollBot]; rows outside
	// the region never move, which is how pinned chrome is emulated. Defaults
	// to the full screen.
	scrollTop, scrollBot int
}

func NewTermEmulator(h, w int) *TermEmulator {
	e := &TermEmulator{w: w, h: h, scrollTop: 0, scrollBot: h - 1}
	e.screen = make([][]string, h)
	for i := range e.screen {
		e.screen[i] = make([]string, w)
	}
	return e
}

// Process replays a byte stream of compositor output.
func (e *TermEmulator) Process(s string) {
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\r':
			e.col = 0
			e.pendingWrap = false
			i++
		case c == '\n':
			e.lineFeed()
			i++
		case c == '\x1b':
			n := e.parseEscape(s[i:])
			if n == 0 {
				i++
			} else {
				i += n
			}
		default:
			if c >= 0x20 {
				// Consume one grapheme cluster starting here (best-effort: by rune).
				i = e.writePrintable(s, i)
				continue
			}
			i++
		}
	}
}

// writePrintable writes one grapheme cluster (cluster-aware width) and advances
// the cursor with deferred-wrap semantics. Returns bytes consumed.
func (e *TermEmulator) writePrintable(s string, i int) int {
	rest := s[i:]
	// Extract one grapheme cluster via uniseg.
	gr := uniseg.NewGraphemes(rest)
	if !gr.Next() {
		return i + 1
	}
	cl := gr.Str()
	consumed := len(cl)
	visible := ansi.Strip(cl)
	cw := ansi.Width(visible)
	if cw == 0 {
		return i + consumed
	}
	// Deferred wrap: if pending and we're about to write, wrap first.
	if e.pendingWrap {
		e.lineFeed()
		e.col = 0
		e.pendingWrap = false
	}
	for j := 0; j < cw && e.col < e.w; j++ {
		if e.row >= 0 && e.row < e.h {
			ch := " "
			if j == 0 {
				ch = visible
			}
			e.screen[e.row][e.col] = ch
		}
		e.col++
	}
	if e.col >= e.w {
		e.col = e.w - 1
		e.pendingWrap = true
	}
	return i + consumed
}

func (e *TermEmulator) lineFeed() {
	if e.row < e.scrollBot {
		e.row++
		return
	}
	if e.row == e.scrollBot {
		// Scroll within the region: the region's top row goes to scrollback,
		// rows shift up inside [scrollTop, scrollBot], and a blank row opens at
		// the region bottom. Rows outside the region are untouched.
		var top strings.Builder
		for _, cell := range e.screen[e.scrollTop] {
			top.WriteString(cell)
		}
		e.scrollback = append(e.scrollback, top.String())
		copy(e.screen[e.scrollTop:e.scrollBot], e.screen[e.scrollTop+1:e.scrollBot+1])
		e.screen[e.scrollBot] = make([]string, e.w)
		return
	}
	// Cursor below the region (pinned chrome): plain advance, clamped.
	if e.row < e.h-1 {
		e.row++
	}
}

func (e *TermEmulator) parseEscape(s string) int {
	if strings.HasPrefix(s, "\x1b]") {
		// OSC (e.g. hyperlink \x1b]8;;...\x07): consume until BEL.
		if idx := strings.Index(s, "\x07"); idx >= 0 {
			return idx + 1
		}
		return 0
	}
	if !strings.HasPrefix(s, "\x1b[") {
		return 0
	}
	// CSI: read params until final byte 0x40-0x7E.
	j := 2
	for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
		j++
	}
	if j >= len(s) {
		return 0
	}
	params := s[2:j]
	e.applyCSI(params, s[j])
	return j + 1
}

// applyCSI applies one CSI escape's effect.
func (e *TermEmulator) applyCSI(params string, final byte) {
	switch final {
	case 'H', 'f':
		var row, col int
		fmtSscan(params, &row, &col)
		e.row = clampInt(row-1, 0, e.h-1)
		e.col = clampInt(col-1, 0, e.w-1)
		e.pendingWrap = false
	case 'A':
		e.row = clampInt(e.row-paramInt(params, 1), 0, e.h-1)
		e.pendingWrap = false
	case 'B':
		e.row = clampInt(e.row+paramInt(params, 1), 0, e.h-1)
		e.pendingWrap = false
	case 'C':
		e.col = clampInt(e.col+paramInt(params, 1), 0, e.w-1)
		e.pendingWrap = false
	case 'G':
		e.col = clampInt(paramInt(params, 1)-1, 0, e.w-1)
		e.pendingWrap = false
	case 'J':
		e.eraseDisplay(params)
	case 'K':
		e.eraseLine(params)
	case 'r':
		// DECSTBM: set scroll region ("\x1b[top;bot r" 1-indexed; "\x1b[r" =
		// full screen). Homes the cursor per DEC spec.
		top, bot := 1, e.h
		if params != "" {
			parts := strings.SplitN(params, ";", 2)
			top = paramInt(parts[0], 1)
			if len(parts) > 1 {
				bot = paramInt(parts[1], e.h)
			}
		}
		e.scrollTop = clampInt(top-1, 0, e.h-1)
		e.scrollBot = clampInt(bot-1, 0, e.h-1)
		if e.scrollBot < e.scrollTop {
			e.scrollBot = e.scrollTop
		}
		e.row, e.col, e.pendingWrap = 0, 0, false
	}
}

func (e *TermEmulator) eraseDisplay(params string) {
	switch params {
	case "2", "3":
		for r := range e.screen {
			for c := range e.screen[r] {
				e.screen[r][c] = ""
			}
		}
		if params == "3" {
			e.scrollback = nil
		}
	case "0", "":
		for c := e.col; c < e.w; c++ {
			e.screen[e.row][c] = ""
		}
		for r := e.row + 1; r < e.h; r++ {
			for c := range e.screen[r] {
				e.screen[r][c] = ""
			}
		}
	}
}

func (e *TermEmulator) eraseLine(params string) {
	if params != "" && params != "2" && params != "0" {
		return
	}
	for c := range e.screen[e.row] {
		e.screen[e.row][c] = ""
	}
	e.pendingWrap = false
}

// Visible returns the ANSI-stripped text of a screen row.
func (e *TermEmulator) Visible(row int) string {
	if row < 0 || row >= e.h {
		return ""
	}
	var b strings.Builder
	for _, cell := range e.screen[row] {
		b.WriteString(cell)
	}
	return b.String()
}

func (e *TermEmulator) Scrollback() []string { return e.scrollback }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func paramInt(params string, defaultVal int) int {
	if params == "" {
		return defaultVal
	}
	// Use only the first parameter if multiple are present (e.g. "0;1").
	p := params
	if idx := strings.Index(p, ";"); idx >= 0 {
		p = p[:idx]
	}
	if idx := strings.Index(p, ":"); idx >= 0 {
		p = p[:idx]
	}
	var n int
	if _, err := fmt.Sscanf(p, "%d", &n); err != nil {
		return defaultVal
	}
	return n
}

func fmtSscan(params string, row, col *int) {
	parts := strings.SplitN(params, ";", 2)
	*row = paramInt(parts[0], 1)
	if len(parts) > 1 {
		*col = paramInt(parts[1], 1)
	} else {
		*col = 1
	}
}
