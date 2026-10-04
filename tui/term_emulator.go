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

// Scrollback retention. The transcript is a bounded buffer, not an archive: it
// only has to hold the rows a consumer has not read yet, because every consumer
// ships each row exactly once. Two thousand rows is far more than any consumer
// can fall behind by, and it caps the per-session cost at ~19 MB for a 120-column
// screen (a scrolled row is stored per-cell, ~79 bytes/cell) instead of letting
// it grow with the session — which is what a long-running server cannot afford.
const (
	MaxScrollbackRows = 2000
	// scrollbackTrimBatch is how many rows are evicted at once once the cap is
	// passed. Trimming row by row would copy the whole retained buffer per
	// scrolled row (O(cap) per row); trimming a batch makes it O(cap) per batch,
	// which is a few copies per row.
	scrollbackTrimBatch = 256
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
	screen      [][]string    // [row][col] cell text (ANSI-stripped for assertion)
	screenBg    [][]string    // [row][col] cell background SGR ("" = default)
	screenFg    [][]string    // [row][col] cell foreground SGR ("" = default)
	screenFlags [][]AttrFlags // [row][col] per-cell text attributes (term_cells.go)
	screenLink  [][]string    // [row][col] OSC-8 hyperlink target ("" = none)
	scrollback  []string
	// scrollbackAttrs mirrors scrollback with per-cell attributes so a scrolled
	// row keeps its styling (phase 0 of the web UI cell pipeline).
	scrollbackAttrs [][]CellAttrs
	// scrollbackBase is the ABSOLUTE index of scrollback[0]: how many rows have
	// been dropped off the front of the bounded transcript. Consumers that ship
	// each row exactly once (the web UI) count in absolute indices, so they can
	// tell "rows I have not sent yet" from "rows that are gone" after eviction.
	scrollbackBase int
	curFlags       AttrFlags // current SGR text attributes
	curLink        string    // current OSC-8 hyperlink target ("" = none)
	row, col       int
	curBg          string // current SGR background params (e.g. "48;2;42;50;41")
	curFg          string // current SGR foreground params (e.g. "38;2;139;148;158")
	pendingWrap    bool   // DEC deferred wrap: last cell filled, next char wraps
	// oscBuf holds the tail of an OSC sequence whose terminator has not arrived
	// yet. Without it a hyperlink split across two writes (title, chunked
	// stream, a diffed repaint) would leak "8;;https://…" onto the screen as
	// printable text.
	oscBuf string
	// cursorVisible models DECTCEM (\x1b[?25h / \x1b[?25l): the terminal's
	// cursor-show mode. The compositor conveys the hardware cursor's visibility
	// ONLY as those bytes, so a renderer that places its own caret (the web UI)
	// reads the mode here. Defaults to true, like a real terminal after reset.
	cursorVisible bool
	// scrollTop/scrollBot model the DECSTBM scroll region (0-indexed,
	// inclusive). \n scrolls only within [scrollTop, scrollBot]; rows outside
	// the region never move, which is how pinned chrome is emulated. Defaults
	// to the full screen.
	scrollTop, scrollBot int
	// dirty marks the rows touched since the last DrainDirty. It is only
	// maintained while TrackDirty(true) is set (the web UI grid uses it to ship
	// just the changed rows); tests and one-shot replays leave it off and pay
	// no bookkeeping.
	dirty    []bool
	tracking bool
}

func NewTermEmulator(h, w int) *TermEmulator {
	e := &TermEmulator{w: w, h: h, scrollTop: 0, scrollBot: h - 1, cursorVisible: true}
	e.screen = make([][]string, h)
	e.screenBg = make([][]string, h)
	e.screenFg = make([][]string, h)
	e.screenFlags = make([][]AttrFlags, h)
	e.screenLink = make([][]string, h)
	e.dirty = make([]bool, h)
	for i := range e.screen {
		e.screen[i] = make([]string, w)
		e.screenBg[i] = make([]string, w)
		e.screenFg[i] = make([]string, w)
		e.screenFlags[i] = make([]AttrFlags, w)
		e.screenLink[i] = make([]string, w)
	}
	return e
}

// Process replays a byte stream of compositor output.
func (e *TermEmulator) Process(s string) {
	// Resume an OSC left unterminated by a previous write before parsing.
	if e.oscBuf != "" {
		s = "\x1b]" + e.oscBuf + s
		e.oscBuf = ""
	}
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
			switch {
			case n > 0:
				i += n
			case strings.HasPrefix(s[i:], "\x1b]") && len(s)-i <= maxOSCBuffer:
				// Unterminated OSC: hold the payload until the terminator
				// arrives instead of printing it as text.
				e.oscBuf = s[i+2:]
				return
			default:
				i++
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
			e.screenBg[e.row][e.col] = e.curBg
			e.screenFg[e.row][e.col] = e.curFg
			e.screenFlags[e.row][e.col] = e.cellFlags()
			e.screenLink[e.row][e.col] = e.curLink
		}
		e.markDirty(e.row)
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
		e.scrollbackAttrs = append(e.scrollbackAttrs, e.Cells(e.scrollTop))
		e.trimScrollback()
		copy(e.screen[e.scrollTop:e.scrollBot], e.screen[e.scrollTop+1:e.scrollBot+1])
		copy(e.screenBg[e.scrollTop:e.scrollBot], e.screenBg[e.scrollTop+1:e.scrollBot+1])
		// The foreground (and flags) must move with the row: leaving them
		// behind made a scrolled row lose its text colour.
		copy(e.screenFg[e.scrollTop:e.scrollBot], e.screenFg[e.scrollTop+1:e.scrollBot+1])
		copy(e.screenFlags[e.scrollTop:e.scrollBot], e.screenFlags[e.scrollTop+1:e.scrollBot+1])
		copy(e.screenLink[e.scrollTop:e.scrollBot], e.screenLink[e.scrollTop+1:e.scrollBot+1])
		e.screen[e.scrollBot] = make([]string, e.w)
		e.screenBg[e.scrollBot] = make([]string, e.w)
		e.screenFg[e.scrollBot] = make([]string, e.w)
		e.screenFlags[e.scrollBot] = make([]AttrFlags, e.w)
		e.screenLink[e.scrollBot] = make([]string, e.w)
		for r := e.scrollTop; r <= e.scrollBot; r++ {
			e.markDirty(r)
		}
		return
	}
	// Cursor below the region (pinned chrome): plain advance, clamped.
	if e.row < e.h-1 {
		e.row++
	}
}

func (e *TermEmulator) parseEscape(s string) int {
	if strings.HasPrefix(s, "\x1b]") {
		// OSC: \x1b]<code>;<params/text> BEL-or-ST. The payload may contain
		// semicolons (OSC 8 hyperlinks: id;URI), so it is handled as one
		// string and terminated by BEL (\a) or ST (\x1b\\).
		body, n, ok := oscPayload(s)
		if !ok {
			return 0 // unterminated: wait for more bytes
		}
		e.applyOSC(body)
		return n
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
		e.applyCursorPosition(params)
	case 'A':
		e.moveCursor(-paramInt(params, 1), 0)
	case 'B':
		e.moveCursor(paramInt(params, 1), 0)
	case 'C':
		e.moveCursor(0, paramInt(params, 1))
	case 'G':
		e.moveCursor(0, paramInt(params, 1)-1-e.col)
	case 'J':
		e.eraseDisplay(params)
	case 'K':
		e.eraseLine(params)
	case 'm':
		e.applySGR(params)
	case 'r':
		e.applyScrollRegion(params)
	case 'h', 'l':
		e.applyPrivateMode(params, final == 'h')
	}
}

// applyPrivateMode applies a DEC private mode set ('h') or reset ('l'). Only
// DECTCEM (?25, cursor show/hide) is modelled: it is the one mode the
// compositor uses to signal hardware-cursor visibility, and a client that
// draws its own caret needs it. Every other private mode (?2026 synchronized
// output, ?1049 alt screen, …) is deliberately ignored — what a real terminal
// does with a mode it does not implement, and what keeps an unknown sequence
// from silently changing behaviour here.
func (e *TermEmulator) applyPrivateMode(params string, set bool) {
	if params == "?25" {
		e.cursorVisible = set
	}
}

func (e *TermEmulator) applyCursorPosition(params string) {
	var row, col int
	fmtSscan(params, &row, &col)
	e.row, e.col = clampInt(row-1, 0, e.h-1), clampInt(col-1, 0, e.w-1)
	e.pendingWrap = false
}

func (e *TermEmulator) moveCursor(rowDelta, colDelta int) {
	e.row = clampInt(e.row+rowDelta, 0, e.h-1)
	e.col = clampInt(e.col+colDelta, 0, e.w-1)
	e.pendingWrap = false
}

func (e *TermEmulator) applyScrollRegion(params string) {
	top, bot := 1, e.h
	if params != "" {
		parts := strings.SplitN(params, ";", 2)
		top, bot = paramInt(parts[0], 1), e.h
		if len(parts) > 1 {
			bot = paramInt(parts[1], e.h)
		}
	}
	e.scrollTop, e.scrollBot = clampInt(top-1, 0, e.h-1), clampInt(bot-1, 0, e.h-1)
	if e.scrollBot < e.scrollTop {
		e.scrollBot = e.scrollTop
	}
	e.row, e.col, e.pendingWrap = 0, 0, false
}

// applySGR tracks the current background color from SGR sequences. Only the
// background is modeled (0/49 reset, 48;2;r;g;b truecolor, 48;5;n palette);
// all other attributes are ignored.
func (e *TermEmulator) applySGR(params string) {
	codes := strings.Split(params, ";")
	for i := 0; i < len(codes); i++ {
		e.trackSGRFlag(codes, i)
		switch codes[i] {
		case "", "0", "49":
			e.curBg = ""
			if codes[i] != "49" {
				e.curFg = "" // 0/empty reset both; 49 clears background only
			}
		case "39":
			e.curFg = "" // default foreground
		case "48":
			if n := extendedColorLen(codes, i); n > 0 {
				e.curBg = strings.Join(codes[i:i+n], ";")
				i += n - 1
			}
		case "38":
			if n := extendedColorLen(codes, i); n > 0 {
				e.curFg = strings.Join(codes[i:i+n], ";")
				i += n - 1
			}
		case "58":
			// Underline-color specs are not modeled here, but their
			// sub-parameters MUST be consumed so they are never misread as
			// standalone attribute codes below (e.g. a "0" red channel would
			// otherwise look like an SGR 0 reset and clear curBg/curFg).
			if n := extendedColorLen(codes, i); n > 0 {
				i += n - 1
			}
		}
	}
}

// extendedColorLen reports the token count of an extended color spec starting
// at codes[i] — "38"/"48"/"58" followed by "2;r;g;b" (5 tokens) or "5;n"
// (3 tokens) — or 0 if malformed/truncated.
func extendedColorLen(codes []string, i int) int {
	if i+1 >= len(codes) {
		return 0
	}
	switch codes[i+1] {
	case "2":
		if i+4 < len(codes) {
			return 5
		}
	case "5":
		if i+2 < len(codes) {
			return 3
		}
	}
	return 0
}

func (e *TermEmulator) eraseDisplay(params string) {
	e.markAllDirty()
	switch params {
	case "2", "3":
		for r := range e.screen {
			for c := range e.screen[r] {
				e.screen[r][c] = ""
				e.screenBg[r][c] = ""
				e.screenFg[r][c] = ""
				e.screenFlags[r][c] = 0
				e.screenLink[r][c] = ""
			}
		}
		if params == "3" {
			e.scrollback = nil
			e.scrollbackAttrs = nil
			e.scrollbackBase = 0
		}
	case "0", "":
		for c := e.col; c < e.w; c++ {
			e.screen[e.row][c] = ""
			e.screenBg[e.row][c] = ""
			e.screenFg[e.row][c] = ""
			e.screenFlags[e.row][c] = 0
			e.screenLink[e.row][c] = ""
		}
		for r := e.row + 1; r < e.h; r++ {
			for c := range e.screen[r] {
				e.screen[r][c] = ""
				e.screenBg[r][c] = ""
				e.screenFg[r][c] = ""
				e.screenFlags[r][c] = 0
				e.screenLink[r][c] = ""
			}
		}
	}
}

func (e *TermEmulator) eraseLine(params string) {
	if params != "" && params != "2" && params != "0" {
		return
	}
	e.markDirty(e.row)
	for c := range e.screen[e.row] {
		e.screen[e.row][c] = ""
		e.screenBg[e.row][c] = ""
		e.screenFg[e.row][c] = ""
		e.screenFlags[e.row][c] = 0
		e.screenLink[e.row][c] = ""
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

// VisibleBg returns the per-cell background SGR params of a screen row
// ("" = default background), for asserting background-color continuity.
func (e *TermEmulator) VisibleBg(row int) []string {
	if row < 0 || row >= e.h {
		return nil
	}
	out := make([]string, e.w)
	copy(out, e.screenBg[row])
	return out
}

// VisibleFg returns the per-cell foreground SGR params of a screen row
// ("" = default foreground), for asserting text-color continuity (the
// grey-vs-white class of streaming regressions).
func (e *TermEmulator) VisibleFg(row int) []string {
	if row < 0 || row >= e.h {
		return nil
	}
	out := make([]string, e.w)
	copy(out, e.screenFg[row])
	return out
}

// RowFg returns the dominant non-empty foreground of a screen row, or "" when
// every cell uses the default foreground.
func (e *TermEmulator) RowFg(row int) string {
	cells := e.VisibleFg(row)
	for _, fg := range cells {
		if fg != "" {
			return fg
		}
	}
	return ""
}

func (e *TermEmulator) Scrollback() []string { return e.scrollback }

// ScrollbackBase is the absolute index of the oldest retained transcript row:
// the number of rows evicted off the front of the bounded buffer. A consumer
// that counts the rows it has shipped in absolute indices compares them against
// this to notice that history it never read is gone.
func (e *TermEmulator) ScrollbackBase() int { return e.scrollbackBase }

// ScrollbackLen is the number of transcript rows currently retained.
func (e *TermEmulator) ScrollbackLen() int { return len(e.scrollbackAttrs) }

// ScrollbackRow returns one retained transcript row by ABSOLUTE index, without
// copying it. It returns nil when the index is no longer retained (evicted, or
// never written). The caller must not modify the result and must not retain it
// past the lock that made the call safe — it aliases the emulator's own storage.
// Use ScrollbackCells when a private copy is needed.
func (e *TermEmulator) ScrollbackRow(abs int) []CellAttrs {
	i := abs - e.scrollbackBase
	if i < 0 || i >= len(e.scrollbackAttrs) {
		return nil
	}
	return e.scrollbackAttrs[i]
}

// trimScrollback enforces MaxScrollbackRows by dropping a batch off the front
// once the cap is passed. Both stores are trimmed together — they are two views
// of the same rows, and letting them diverge would make Scrollback() and
// ScrollbackCells() disagree about what history exists.
func (e *TermEmulator) trimScrollback() {
	if len(e.scrollback) <= MaxScrollbackRows {
		return
	}
	keep := MaxScrollbackRows - scrollbackTrimBatch
	if keep < 0 {
		keep = 0
	}
	drop := len(e.scrollback) - keep
	e.scrollback = dropFront(e.scrollback, drop)
	e.scrollbackAttrs = dropFront(e.scrollbackAttrs, drop)
	e.scrollbackBase += drop
}

// dropFront removes the first n elements of s in place, clearing the vacated
// slots so the dropped rows (and their cell strings) become collectable. The
// backing array is reused, so the buffer's memory stays bounded.
func dropFront[T any](s []T, n int) []T {
	if n <= 0 {
		return s
	}
	if n >= len(s) {
		return s[:0]
	}
	copy(s, s[n:])
	var zero T
	for i := len(s) - n; i < len(s); i++ {
		s[i] = zero
	}
	return s[:len(s)-n]
}

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
