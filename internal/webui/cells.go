// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package webui serves the Goa TUI as a web page. The browser *is* the
// terminal: the very same tui.TUI engine renders into a virtual screen
// (CellGrid over tui.TermEmulator) and the resulting cell grid is shipped to
// attached browsers as row patches. One engine, one screen model, two
// transports — so the web page cannot drift from the terminal UI.
package webui

import (
	"sort"
	"strings"
	"sync"

	"github.com/pijalu/goa/tui"
)

// DefaultCols/DefaultRows are the initial virtual-screen geometry used when
// the browser has not reported its own size yet.
const (
	DefaultCols = 120
	DefaultRows = 40
)

// CellGrid owns the authoritative virtual screen: a tui.TermEmulator plus the
// last snapshot shipped to clients. It is a pure state machine — the bytes the
// TUI engine writes go in, patches (or a full snapshot) come out. All methods
// are safe for concurrent use: the render loop writes, transports read.
//
// The emulator is not internally synchronized, so mu guards it along with the
// grid's own state. That matters because the readers are not all the render
// loop: the WebSocket/SSE transports snapshot inside the sink callback, but the
// no-JS page snapshots from its own HTTP handler goroutine at whatever moment
// the user happens to refresh.
type CellGrid struct {
	emu *tui.TermEmulator
	mu  sync.RWMutex // guards the emulator and everything below it

	cols, rows int

	// prev is the last snapshot handed out by Patches/FullPatches, so the next
	// diff is against exactly what the clients hold.
	prev [][]tui.CellAttrs

	// pending holds the rows Process() found dirty. Patches consumes it: the
	// dirty marks live in the emulator and are drained exactly once, by
	// whoever asks for the delta.
	pending []int

	title string

	// sentScrollback counts the scrollback rows already handed to clients, so
	// each row is shipped exactly once (the emulator's scrollback only grows).
	// It is clamped back when the emulator's buffer is cleared.
	sentScrollback int
}

// NewCellGrid creates a blank cols×rows grid with change tracking on.
func NewCellGrid(cols, rows int) *CellGrid {
	cols, rows = clampSize(cols, rows)
	emu := tui.NewTermEmulator(rows, cols)
	emu.TrackDirty(true)
	return &CellGrid{
		emu:  emu,
		cols: cols,
		rows: rows,
		prev: blankSnapshot(cols, rows),
	}
}

func clampSize(cols, rows int) (int, int) {
	if cols < 1 {
		cols = DefaultCols
	}
	if rows < 1 {
		rows = DefaultRows
	}
	if cols > maxCols {
		cols = maxCols
	}
	if rows > maxRows {
		rows = maxRows
	}
	return cols, rows
}

// maxCols/maxRows cap the virtual screen: a huge browser window must not turn
// into a giant JSON payload (spec §18 "max_cols clamp").
const (
	maxCols = 500
	maxRows = 200
)

// Process feeds compositor output into the screen. It reports whether the grid
// has unshipped changes (true when at least one row was touched or the title /
// cursor state moved).
func (g *CellGrid) Process(s string) bool {
	g.mu.Lock()
	before := g.emu.CursorVisible()
	g.emu.Process(s)
	if g.emu.CursorVisible() != before {
		// Hiding/showing the cursor is a DECTCEM transition the compositor
		// emits as bytes, with no cell change of its own: mark the screen so
		// the visibility reaches clients on a frame even when the text did not
		// move.
		g.emu.MarkDirty()
	}
	dirty := g.emu.DrainDirty()
	g.pending = append(g.pending, dirty...)
	g.mu.Unlock()
	return len(dirty) > 0
}

// MarkState forces the next Patches call to produce at least a state-only
// frame (used after a title or cursor-visibility change).
func (g *CellGrid) MarkState() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.emu.MarkDirty()
}

// Size returns the grid geometry (cols, rows).
func (g *CellGrid) Size() (cols, rows int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cols, g.rows
}

// Cursor returns the cursor position and visibility. Visibility is the
// emulator's DECTCEM mode (the compositor only ever signals the hardware
// cursor through \x1b[?25h / \x1b[?25l), so the grid and the browser agree with
// a real terminal.
func (g *CellGrid) Cursor() Cursor {
	g.mu.RLock()
	defer g.mu.RUnlock()
	r, c := g.emu.Cursor()
	return Cursor{Row: r, Col: c, Visible: g.emu.CursorVisible()}
}

// SetCursorVisible toggles the caret carried by every frame (the Terminal
// interface's HideCursor/ShowCursor path, used before rendering starts).
func (g *CellGrid) SetCursorVisible(v bool) {
	g.mu.Lock()
	changed := g.emu.CursorVisible() != v
	g.emu.SetCursorVisible(v)
	g.mu.Unlock()
	if changed {
		g.MarkState()
	}
}

// Title returns the current window title ("" until SetTitle is called).
func (g *CellGrid) Title() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.title
}

// SetTitle records the window title; it is carried by the next frame.
func (g *CellGrid) SetTitle(t string) {
	g.mu.Lock()
	g.title = t
	g.mu.Unlock()
	g.MarkState()
}

// Resize changes the geometry, keeping the content that still fits and
// re-initialising the diff baseline so the next frame is a full repaint.
func (g *CellGrid) Resize(cols, rows int) {
	cols, rows = clampSize(cols, rows)
	g.mu.Lock()
	if cols == g.cols && rows == g.rows {
		g.mu.Unlock()
		return
	}
	g.cols, g.rows = cols, rows
	g.prev = blankSnapshot(cols, rows)
	g.emu.Resize(cols, rows)
	g.mu.Unlock()
}

// DiscardChanges forgets the rows marked dirty since the last frame without
// producing one. It is what a publish does when no client is attached: the
// screen keeps being tracked (so a later attach can snapshot it), but the
// per-frame diff/run/encode work nobody would receive is skipped.
//
// It deliberately leaves the diff baseline alone. Every path that attaches a
// client or changes geometry publishes a FULL frame first (FullPatches
// re-baselines every row), so a stale baseline can never leak into a delta a
// client receives.
func (g *CellGrid) DiscardChanges() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.emu.DrainDirty()
	g.pending = nil
}

// Clear blanks the screen and scrollback, and re-baselines the diff.
func (g *CellGrid) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.emu.Reset()
	g.prev = blankSnapshot(g.cols, g.rows)
	g.sentScrollback = 0
}

// Cells returns a normalised copy of one screen row's cells (blank cells come
// back as a single space). Exposed for tests and for the no-JS mirror.
func (g *CellGrid) Cells(row int) []tui.CellAttrs {
	g.mu.RLock()
	defer g.mu.RUnlock()
	cells := fitCells(g.emu.Cells(row), g.cols)
	normalizeCells(cells)
	return cells
}

// Scrollback returns the rows that scrolled off the top of the screen with
// their cell attributes (spec §7.4).
func (g *CellGrid) Scrollback() [][]tui.CellAttrs {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.emu.ScrollbackCells()
}

// TakeScrollback returns the scrollback rows that have not been published yet,
// as row patches, and marks them as shipped. Row indices are the position in
// the scrollback transcript (0-based), not screen rows.
//
// The work is proportional to the rows that are NEW, never to how much history
// the session has: the emulator is asked for its retained window and its base
// index, and only the rows past the shipped mark are read — one at a time,
// without copying the buffer. (Copying the whole transcript per frame was
// measured at 12.5 ms and 190 MB per frame once a session had 20 000 rows of
// history; the transcript only ever grows, so the cost grew with the session.)
//
// A grid whose scrollback was cleared (Clear, or CSI 3) re-bases the counter, so
// the transcript simply restarts instead of losing rows. Rows evicted by the
// emulator's retention cap are skipped the same way: the shipped mark is clamped
// forward to the oldest row that still exists.
//
// The whole operation runs under the grid lock: the shipped counter is state
// like any other, and two transports attaching at once (or the no-JS page
// refreshing) must not interleave a read of the buffer with an update of the
// counter — that would ship one row twice and lose another.
func (g *CellGrid) TakeScrollback() []RowPatch {
	g.mu.Lock()
	defer g.mu.Unlock()
	base := g.emu.ScrollbackBase()
	end := base + g.emu.ScrollbackLen()
	if g.sentScrollback < base {
		// History the grid never shipped was evicted by the retention cap.
		g.sentScrollback = base
	}
	if g.sentScrollback > end {
		// The buffer was cleared (Clear, CSI 3): restart from what exists.
		g.sentScrollback = end
	}
	first := g.sentScrollback
	if first >= end {
		return nil
	}
	out := make([]RowPatch, 0, end-first)
	for abs := first; abs < end; abs++ {
		cells := g.emu.ScrollbackRow(abs)
		if len(cells) != g.cols {
			cells = fitCells(cells, g.cols)
		}
		// RowRuns normalises blank cells itself (cellText maps "" to " "), so
		// the emulator's own storage is never written to from here.
		out = append(out, RowPatch{Row: abs, Runs: RowRuns(cells)})
	}
	g.sentScrollback = end
	return out
}

// Text renders the current screen as plain text, one line per row — the
// accessibility / curl mirror of the grid.
func (g *CellGrid) Text() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var b strings.Builder
	for r := 0; r < g.rows; r++ {
		b.WriteString(strings.TrimRight(g.emu.CellsText(r), " "))
		if r < g.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// FullPatches returns every row as a patch and re-baselines the diff. Used
// when a client (re)connects: the grid is authoritative, so there is no replay.
func (g *CellGrid) FullPatches() []RowPatch {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]RowPatch, 0, g.rows)
	for r := 0; r < g.rows; r++ {
		cells := g.rowCellsLocked(r)
		g.prev[r] = cells
		out = append(out, RowPatch{Row: r, Runs: RowRuns(cells)})
	}
	return out
}

// Patches returns the patches for the rows that changed since the previous
// Patches call, then re-baselines those rows. Unchanged rows are omitted
// entirely, which is what keeps a streaming frame to a few kilobytes.
//
// Only the rows the emulator reported dirty are examined and only the ones that
// actually differ are copied into the baseline: the cost of a frame is
// proportional to what moved (one or two rows while streaming), not to the size
// of the screen. Snapshotting the whole grid per frame would copy every cell
// 20-60 times a second for nothing.
func (g *CellGrid) Patches() []RowPatch {
	g.mu.Lock()
	defer g.mu.Unlock()
	changed := g.takeDirtyRowsLocked()
	var out []RowPatch
	for _, r := range changed {
		cells := g.rowCellsLocked(r)
		if !rowDiffers(g.prev[r], cells) {
			continue
		}
		out = append(out, RowPatch{Row: r, Runs: RowRuns(cells)})
		g.prev[r] = cells
	}
	return out
}

// rowCellsLocked reads one screen row as a normalised cell slice.
func (g *CellGrid) rowCellsLocked(row int) []tui.CellAttrs {
	cells := g.emu.Cells(row)
	if len(cells) != g.cols {
		cells = fitCells(cells, g.cols)
	}
	normalizeCells(cells)
	return cells
}

// rowDiffers reports whether a row's cells differ from the baseline the clients
// hold. A missing baseline (a row that never existed, e.g. after a grow) counts
// as changed.
func rowDiffers(prev, cur []tui.CellAttrs) bool {
	if prev == nil || len(prev) != len(cur) {
		return true
	}
	for i := range cur {
		if cur[i] != prev[i] {
			return true
		}
	}
	return false
}

// takeDirtyRowsLocked merges the rows recorded by Process with anything the
// emulator marked since (resize, MarkDirty), clears both, and normalises the
// result.
func (g *CellGrid) takeDirtyRowsLocked() []int {
	fresh := g.emu.DrainDirty()
	changed := append(g.pending, fresh...)
	g.pending = nil
	if len(changed) == 0 {
		// Nothing claims to have changed (e.g. a diff of a grid whose baseline
		// was never shipped): fall back to comparing every row rather than
		// silently shipping nothing.
		return allRows(g.rows)
	}
	return normalizeRows(changed, g.rows)
}

// normalizeRows drops duplicates and out-of-range rows, and sorts ascending.
func normalizeRows(rows []int, max int) []int {
	seen := make(map[int]bool, len(rows))
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		if r < 0 || r >= max || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	sort.Ints(out)
	return out
}

// normalizeCells makes untouched cells ("") comparable with the blank
// baseline (" "): without it every row would look changed on the first diff.
func normalizeCells(cells []tui.CellAttrs) {
	for i := range cells {
		if cells[i].Text == "" {
			cells[i].Text = " "
		}
	}
}

func fitCells(cells []tui.CellAttrs, cols int) []tui.CellAttrs {
	out := make([]tui.CellAttrs, cols)
	copy(out, cells)
	for i := len(cells); i < cols; i++ {
		out[i].Text = " "
	}
	return out
}

func blankSnapshot(cols, rows int) [][]tui.CellAttrs {
	snap := make([][]tui.CellAttrs, rows)
	for r := range snap {
		row := make([]tui.CellAttrs, cols)
		for c := range row {
			row[c].Text = " "
		}
		snap[r] = row
	}
	return snap
}

func allRows(rows int) []int {
	out := make([]int, rows)
	for i := range out {
		out[i] = i
	}
	return out
}

// sgrToCSS converts an emulator SGR colour spec ("38;2;r;g;b", "48;5;n") to a
// CSS colour. "" (the theme default) stays ""; anything unrecognised becomes ""
// so an unknown spec degrades to the default colour instead of injecting junk
// into the stylesheet.
//
// It parses with Cut and a hand-rolled integer scan rather than Split/Atoi:
// every cell of every changed row goes through here, and a Split per cell would
// allocate a slice per cell for a three-field string.
func sgrToCSS(sgr string) string {
	if sgr == "" {
		return ""
	}
	// Drop the colour channel (38 foreground, 48 background, 58 underline):
	// the channel says where the colour applies, not which colour it is.
	_, fields, ok := strings.Cut(sgr, ";")
	if !ok {
		return ""
	}
	kind, fields, ok := strings.Cut(fields, ";")
	if !ok {
		return ""
	}
	switch kind {
	case "2":
		r, rest, ok := cutColorPart(fields)
		if !ok {
			return ""
		}
		g, rest, ok := cutColorPart(rest)
		if !ok {
			return ""
		}
		b, _, ok := cutColorPart(rest)
		if !ok {
			return ""
		}
		return rgbHex(r, g, b)
	case "5":
		n, _, ok := cutColorPart(fields)
		if !ok {
			return ""
		}
		return Palette256(n)
	}
	return ""
}

// cutColorPart splits off the next ";"-delimited field and parses it as a
// non-negative integer. ok is false for an empty or non-numeric field, which is
// what makes an unrecognised spec degrade to the theme default.
func cutColorPart(s string) (value int, rest string, ok bool) {
	field, rest, _ := strings.Cut(s, ";")
	if field == "" {
		return 0, rest, false
	}
	value = 0
	for i := 0; i < len(field); i++ {
		c := field[i]
		if c < '0' || c > '9' {
			return 0, rest, false
		}
		value = value*10 + int(c-'0')
		if value > 255*255 {
			return 0, rest, false // absurd component: not a colour we can render
		}
	}
	return value, rest, true
}

func rgbHex(r, g, b int) string {
	return "#" + hex2(r) + hex2(g) + hex2(b)
}

func hex2(v int) string {
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	const digits = "0123456789abcdef"
	return string([]byte{digits[v>>4], digits[v&0x0f]})
}
