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
	"strconv"
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

	cursorVisible bool
	title         string

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
		emu:           emu,
		cols:          cols,
		rows:          rows,
		prev:          blankSnapshot(cols, rows),
		cursorVisible: true,
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
	g.emu.Process(s)
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

// Cursor returns the cursor position and visibility.
func (g *CellGrid) Cursor() Cursor {
	g.mu.RLock()
	defer g.mu.RUnlock()
	r, c := g.emu.Cursor()
	return Cursor{Row: r, Col: c, Visible: g.cursorVisible}
}

// SetCursorVisible toggles the caret carried by every frame.
func (g *CellGrid) SetCursorVisible(v bool) {
	g.mu.Lock()
	changed := g.cursorVisible != v
	g.cursorVisible = v
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
// A grid whose scrollback was cleared (Clear, or CSI 3) re-bases the counter on
// the next call, so the transcript simply restarts instead of losing rows.
func (g *CellGrid) TakeScrollback() []RowPatch {
	rows := g.Scrollback()
	if g.sentScrollback > len(rows) {
		g.sentScrollback = len(rows)
	}
	fresh := rows[g.sentScrollback:]
	g.sentScrollback = len(rows)
	if len(fresh) == 0 {
		return nil
	}
	cols, _ := g.Size()
	out := make([]RowPatch, 0, len(fresh))
	for i, cells := range fresh {
		if len(cells) != cols {
			cells = fitCells(cells, cols)
		}
		normalizeCells(cells)
		out = append(out, RowPatch{Row: g.sentScrollback - len(fresh) + i, Runs: RowRuns(cells)})
	}
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
	cols, rows := g.Size()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prev = g.snapshotLocked(cols, rows)
	out := make([]RowPatch, 0, rows)
	for r := 0; r < rows; r++ {
		out = append(out, RowPatch{Row: r, Runs: RowRuns(g.prev[r])})
	}
	return out
}

// Patches returns the patches for the rows that changed since the previous
// Patches call, then re-baselines. Unchanged rows are omitted entirely, which
// is what keeps a streaming frame to a few kilobytes.
func (g *CellGrid) Patches() []RowPatch {
	cols, rows := g.Size()
	changed := g.takeDirtyRows(rows)
	g.mu.Lock()
	defer g.mu.Unlock()
	cur := g.snapshotLocked(cols, rows)
	d := NewCellDiff(g.prev, cur)
	patches := d.Patches(changed)
	g.prev = cur
	return patches
}

// takeDirtyRows merges the rows recorded by Process with anything the
// emulator marked since (resize, MarkDirty) and clears both.
func (g *CellGrid) takeDirtyRows(rows int) []int {
	g.mu.Lock()
	fresh := g.emu.DrainDirty()
	changed := append(g.pending, fresh...)
	g.pending = nil
	g.mu.Unlock()
	if len(changed) == 0 {
		// Nothing claims to have changed (e.g. a diff of a grid whose baseline
		// was never shipped): fall back to comparing every row rather than
		// silently shipping nothing.
		return allRows(rows)
	}
	return normalizeRows(changed, rows)
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

// snapshotLocked copies the emulator's rows into a fresh snapshot buffer.
func (g *CellGrid) snapshotLocked(cols, rows int) [][]tui.CellAttrs {
	snap := make([][]tui.CellAttrs, rows)
	for r := 0; r < rows; r++ {
		cells := g.emu.Cells(r)
		if len(cells) != cols {
			cells = fitCells(cells, cols)
		}
		normalizeCells(cells)
		snap[r] = cells
	}
	return snap
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
func sgrToCSS(sgr string) string {
	if sgr == "" {
		return ""
	}
	parts := strings.Split(sgr, ";")
	if len(parts) < 2 {
		return ""
	}
	switch parts[1] {
	case "2":
		if len(parts) < 5 {
			return ""
		}
		r, g, b := atoiOr(parts[2], -1), atoiOr(parts[3], -1), atoiOr(parts[4], -1)
		if r < 0 || g < 0 || b < 0 {
			return ""
		}
		return rgbHex(r, g, b)
	case "5":
		if len(parts) < 3 {
			return ""
		}
		n := atoiOr(parts[2], -1)
		if n < 0 {
			return ""
		}
		return Palette256(n)
	}
	return ""
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
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
