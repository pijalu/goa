// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// AttrFlags is a bitmask of the per-cell SGR text attributes the emulator
// tracks. It is the value-level counterpart of the CSS classes the web UI
// emits for a run (spec §9).
type AttrFlags uint16

const (
	AttrBold AttrFlags = 1 << iota
	AttrItalic
	AttrUnderline
	AttrDim
	AttrInverse
	AttrStrike
	// AttrLink marks a cell written while an OSC-8 hyperlink was open. The
	// target itself lives in CellAttrs.Link — the bit only says "this cell is
	// part of a link", so the wire format can keep the URI in its own field.
	// Value 64 matches the bitmask documented in specs/webui.md §20.
	AttrLink
)

// CellAttrs is the styling of one screen cell: the (ANSI-stripped) grapheme
// cluster it holds plus the attributes in effect when it was written.
// An empty FG/BG means "theme default"; an empty Link means "not a hyperlink".
type CellAttrs struct {
	Text  string
	Flags AttrFlags
	FG    string
	BG    string
	Link  string
}

// Cell returns the attributes of one cell. Out-of-range coordinates yield the
// zero value (empty text, default colours).
func (e *TermEmulator) Cell(row, col int) CellAttrs {
	if row < 0 || row >= e.h || col < 0 || col >= e.w {
		return CellAttrs{}
	}
	return CellAttrs{
		Text:  e.screen[row][col],
		Flags: e.screenFlags[row][col],
		FG:    e.screenFg[row][col],
		BG:    e.screenBg[row][col],
		Link:  e.screenLink[row][col],
	}
}

// Cells returns a copy of one row's cell attributes, or nil for an
// out-of-range row. The caller may retain and mutate the result freely.
func (e *TermEmulator) Cells(row int) []CellAttrs {
	if row < 0 || row >= e.h {
		return nil
	}
	out := make([]CellAttrs, e.w)
	for c := 0; c < e.w; c++ {
		out[c] = CellAttrs{
			Text:  e.screen[row][c],
			Flags: e.screenFlags[row][c],
			FG:    e.screenFg[row][c],
			BG:    e.screenBg[row][c],
			Link:  e.screenLink[row][c],
		}
	}
	return out
}

// CellsText returns a row's plain text (ANSI-stripped), identical to Visible.
func (e *TermEmulator) CellsText(row int) string { return e.Visible(row) }

// Cursor returns the cursor position (0-indexed).
func (e *TermEmulator) Cursor() (row, col int) { return e.row, e.col }

// Size returns the emulator's dimensions (width, height).
func (e *TermEmulator) Size() (w, h int) { return e.w, e.h }

// ScrollbackCells returns the rows that scrolled off the top of the screen,
// with their cell attributes. Copied, so the caller may retain it.
func (e *TermEmulator) ScrollbackCells() [][]CellAttrs {
	if len(e.scrollbackAttrs) == 0 {
		return nil
	}
	out := make([][]CellAttrs, len(e.scrollbackAttrs))
	for i, row := range e.scrollbackAttrs {
		out[i] = append([]CellAttrs(nil), row...)
	}
	return out
}

// Resize changes the emulator geometry to w×h, preserving the content that
// still fits (rows/cols beyond the new bounds are dropped) and clamping the
// cursor and the scroll region. It mirrors what a real terminal does on
// SIGWINCH: the caller repaints after a resize, so no scrollback is fabricated.
func (e *TermEmulator) Resize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if w == e.w && h == e.h {
		return
	}
	oldH := e.h
	e.w, e.h = w, h
	if h > oldH {
		e.growRows(w, oldH, h)
	} else {
		e.shrinkRows(h)
	}
	for r := 0; r < h; r++ {
		e.screen[r] = resizeRow(e.screen[r], w)
		e.screenFg[r] = resizeRow(e.screenFg[r], w)
		e.screenBg[r] = resizeRow(e.screenBg[r], w)
		e.screenLink[r] = resizeRow(e.screenLink[r], w)
		e.screenFlags[r] = resizeFlagsRow(e.screenFlags[r], w)
	}
	e.scrollTop, e.scrollBot = 0, h-1
	if e.tracking {
		e.resizeDirty(h)
	}
	e.markAllDirty()
	e.row = clampInt(e.row, 0, h-1)
	e.col = clampInt(e.col, 0, w-1)
	e.pendingWrap = false
}

// growRows appends blank rows so the screen reaches the new height.
func (e *TermEmulator) growRows(w, oldH, h int) {
	for r := oldH; r < h; r++ {
		e.screen = append(e.screen, make([]string, w))
		e.screenFg = append(e.screenFg, make([]string, w))
		e.screenBg = append(e.screenBg, make([]string, w))
		e.screenFlags = append(e.screenFlags, make([]AttrFlags, w))
		e.screenLink = append(e.screenLink, make([]string, w))
	}
}

// shrinkRows drops the rows that no longer fit.
func (e *TermEmulator) shrinkRows(h int) {
	for r := h; r < len(e.screen); r++ {
		e.screen[r] = nil
		e.screenFg[r] = nil
		e.screenBg[r] = nil
		e.screenFlags[r] = nil
		e.screenLink[r] = nil
	}
	e.screen = e.screen[:h]
	e.screenFg = e.screenFg[:h]
	e.screenBg = e.screenBg[:h]
	e.screenFlags = e.screenFlags[:h]
	e.screenLink = e.screenLink[:h]
}

// resizeDirty keeps the dirty-row mask the same length as the new height.
func (e *TermEmulator) resizeDirty(h int) {
	if len(e.dirty) > h {
		e.dirty = e.dirty[:h]
		return
	}
	for len(e.dirty) < h {
		e.dirty = append(e.dirty, false)
	}
}

// resizeRow grows or truncates a cell-value row to exactly w entries.
func resizeRow(row []string, w int) []string {
	if len(row) > w {
		return row[:w]
	}
	for len(row) < w {
		row = append(row, "")
	}
	return row
}

// resizeFlagsRow is resizeRow for the attribute rows.
func resizeFlagsRow(row []AttrFlags, w int) []AttrFlags {
	if len(row) > w {
		return row[:w]
	}
	for len(row) < w {
		row = append(row, 0)
	}
	return row
}

// Reset returns the emulator to its post-construction state at the current
// geometry: blank screen, empty scrollback, default attributes, home cursor
// and no scroll region.
func (e *TermEmulator) Reset() {
	e.screen = make([][]string, e.h)
	e.screenFg = make([][]string, e.h)
	e.screenBg = make([][]string, e.h)
	e.screenFlags = make([][]AttrFlags, e.h)
	e.screenLink = make([][]string, e.h)
	for i := range e.screen {
		e.screen[i] = make([]string, e.w)
		e.screenFg[i] = make([]string, e.w)
		e.screenBg[i] = make([]string, e.w)
		e.screenFlags[i] = make([]AttrFlags, e.w)
		e.screenLink[i] = make([]string, e.w)
	}
	e.scrollback = nil
	e.scrollbackAttrs = nil
	e.row, e.col, e.pendingWrap = 0, 0, false
	e.oscBuf = ""
	e.curFg, e.curBg, e.curFlags, e.curLink = "", "", 0, ""
	e.scrollTop, e.scrollBot = 0, e.h-1
	e.markAllDirty()
}

// TrackDirty enables (or disables) per-row change tracking. While enabled,
// every row the emulator writes to is remembered until the next DrainDirty,
// which lets a client ship only the rows that actually changed instead of
// diffing the whole screen. Disabling it makes DrainDirty return nil, so
// callers fall back to a full-row diff.
func (e *TermEmulator) TrackDirty(on bool) {
	e.tracking = on
	if on && len(e.dirty) != e.h {
		e.dirty = make([]bool, e.h)
	}
	if !on {
		e.dirty = nil
	}
}

// DrainDirty returns the rows touched since the previous drain (ascending),
// or nil when tracking is off. The marks are cleared.
func (e *TermEmulator) DrainDirty() []int {
	if !e.tracking || len(e.dirty) != e.h {
		return nil
	}
	var rows []int
	for r, d := range e.dirty {
		if d {
			rows = append(rows, r)
		}
	}
	for r := range e.dirty {
		e.dirty[r] = false
	}
	return rows
}

// MarkDirty marks every row as changed — the "something outside the byte
// stream moved (title, cursor mode), repaint fully" signal for callers that
// observe state the emulator does not model itself.
func (e *TermEmulator) MarkDirty() { e.markAllDirty() }

// markDirty records a row as changed. No-op while tracking is off.
func (e *TermEmulator) markDirty(row int) {
	if !e.tracking || row < 0 || row >= len(e.dirty) {
		return
	}
	e.dirty[row] = true
}

// markAllDirty records every row as changed (full repaint).
func (e *TermEmulator) markAllDirty() {
	for r := range e.dirty {
		e.dirty[r] = true
	}
}

// sgrFlagBits maps an SGR token to the flag bit it sets, and sgrFlagClear to
// the bits it resets. Table-driven so trackSGRFlag stays a single switch-free
// lookup (and so the full mapping is readable in one place).
var (
	sgrFlagBits = map[string]AttrFlags{
		"1": AttrBold,
		"2": AttrDim,
		"3": AttrItalic,
		"4": AttrUnderline,
		"7": AttrInverse,
		"9": AttrStrike,
	}
	sgrFlagClear = map[string]AttrFlags{
		"21": AttrBold,
		"22": AttrBold | AttrDim,
		"23": AttrItalic,
		"24": AttrUnderline,
		"27": AttrInverse,
		"29": AttrStrike,
	}
)

// trackSGRFlag applies the text-attribute subset of one SGR token (codes[i])
// to curFlags. applySGR drives it from inside its own single pass over the
// token list, so extended-colour sub-parameters (38/48/58;2;r;g;b and
// 38/48/58;5;n) never reach it — a colour channel of "2" must not be
// mistaken for SGR dim.
func (e *TermEmulator) trackSGRFlag(codes []string, i int) {
	code := codes[i]
	if code == "" || code == "0" {
		e.curFlags = 0
		return
	}
	if bit, ok := sgrFlagBits[code]; ok {
		e.curFlags |= bit
		return
	}
	if bits, ok := sgrFlagClear[code]; ok {
		e.curFlags &^= bits
	}
}

// String renders the flag set as a stable, human-readable name ("default" for
// no attributes) — used by tests and by the plain-text screen mirror.
func (f AttrFlags) String() string {
	if f == 0 {
		return "default"
	}
	var parts []string
	for _, bit := range []struct {
		f AttrFlags
		n string
	}{
		{AttrBold, "bold"},
		{AttrItalic, "italic"},
		{AttrUnderline, "underline"},
		{AttrDim, "dim"},
		{AttrInverse, "inverse"},
		{AttrStrike, "strike"},
		{AttrLink, "link"},
	} {
		if f&bit.f != 0 {
			parts = append(parts, bit.n)
		}
	}
	return strings.Join(parts, "+")
}

// maxOSCBuffer caps how many bytes of an unterminated OSC the emulator holds
// between writes. A real OSC 8 URI is far shorter; anything longer is junk, and
// buffering it forever would grow without bound.
const maxOSCBuffer = 4096

// cellFlags is the attribute set a freshly written cell carries: the current
// SGR flags plus the link bit while an OSC-8 hyperlink is open. A closing
// OSC-8 (empty URI) clears curLink, so the cells written after it are plain.
func (e *TermEmulator) cellFlags() AttrFlags {
	if e.curLink != "" {
		return e.curFlags | AttrLink
	}
	return e.curFlags
}

// oscPayload splits a leading "\x1b]" sequence into its body (the bytes after
// the introducer, terminator excluded) and the total bytes consumed. Both OSC
// terminators are accepted: BEL (\a, what most software emits) and ST
// (ESC \). ok is false when no terminator is present yet — the caller must wait
// for more bytes rather than swallowing the rest of the stream.
func oscPayload(s string) (body string, consumed int, ok bool) {
	if idx := strings.IndexByte(s, '\a'); idx >= 0 {
		return s[2:idx], idx + 1, true
	}
	if idx := strings.Index(s, "\x1b\\"); idx >= 0 {
		return s[2:idx], idx + 2, true
	}
	return "", 0, false
}

// applyOSC applies one OSC sequence's effect. Only OSC 8 (hyperlinks) is
// modelled: "8;params;URI" opens a link, "8;;\a" (or ST) closes it. Every
// other code is consumed and ignored, exactly as before — an unknown OSC must
// never leak its payload into the screen as printable text.
func (e *TermEmulator) applyOSC(body string) {
	code, rest, _ := strings.Cut(body, ";")
	if code != "8" {
		return
	}
	// The parameter list may itself be semicolon-separated; the URI is the
	// remainder after the first field of rest.
	_, uri, _ := strings.Cut(rest, ";")
	e.curLink = uri
}
