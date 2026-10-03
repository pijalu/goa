// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"

	"github.com/pijalu/goa/tui"
)

// CellDiff compares two grid snapshots row by row. It is a pure value object:
// NewCellDiff never touches the emulator, so it is trivially testable and can
// be reused for any two snapshots (previous frame vs current grid, or a golden
// file vs a live grid).
type CellDiff struct {
	prev [][]tui.CellAttrs
	cur  [][]tui.CellAttrs
}

// NewCellDiff pairs a previous snapshot with the current one. Either side may
// be nil (treated as all-blank).
func NewCellDiff(prev, cur [][]tui.CellAttrs) *CellDiff {
	return &CellDiff{prev: prev, cur: cur}
}

// Patches returns one RowPatch per candidate row whose cells differ. Rows not
// listed in candidates are skipped without being compared — the caller passes
// the emulator's dirty rows, which keeps the work proportional to what actually
// changed.
func (d *CellDiff) Patches(candidates []int) []RowPatch {
	var out []RowPatch
	for _, r := range candidates {
		if !d.RowChanged(r) {
			continue
		}
		out = append(out, RowPatch{Row: r, Runs: RowRuns(d.cur[r])})
	}
	return out
}

// RowChanged reports whether the row differs from the previous snapshot
// (different length, or any cell differing).
func (d *CellDiff) RowChanged(r int) bool {
	if r < 0 || r >= len(d.cur) {
		return false
	}
	cur := d.cur[r]
	if r >= len(d.prev) {
		return true
	}
	prev := d.prev[r]
	if len(prev) != len(cur) {
		return true
	}
	for c := range cur {
		if cur[c] != prev[c] {
			return true
		}
	}
	return false
}

// RowRuns collapses one row of cells into maximal runs of identical styling.
// This is where the payload stays small: a 120-column row of uniformly styled
// text becomes one run, while only the genuinely varying tail splits.
func RowRuns(cells []tui.CellAttrs) []Run {
	if len(cells) == 0 {
		return nil
	}
	var runs []Run
	cur := Run{Text: cellText(cells[0]), Flags: cells[0].Flags,
		FG: sgrToCSS(cells[0].FG), BG: sgrToCSS(cells[0].BG), Link: cells[0].Link}
	for _, c := range cells[1:] {
		fg, bg := sgrToCSS(c.FG), sgrToCSS(c.BG)
		if fg == cur.FG && bg == cur.BG && c.Flags == cur.Flags && c.Link == cur.Link {
			cur.Text += cellText(c)
			continue
		}
		runs = append(runs, cur)
		cur = Run{Text: cellText(c), Flags: c.Flags, FG: fg, BG: bg, Link: c.Link}
	}
	return append(runs, cur)
}

// IsLink reports whether the run is part of an OSC-8 hyperlink. The flag bit is
// authoritative (it is what the emulator stamps on the cell); the URI is the
// payload. A run can therefore never claim a link it has no target for.
func (r Run) IsLink() bool { return r.Link != "" && r.Flags&tui.AttrLink != 0 }

// cellText normalises a cell's text: the emulator stores untouched (blank)
// cells as "", which must render as one space so columns line up.
func cellText(c tui.CellAttrs) string {
	if c.Text == "" {
		return " "
	}
	return c.Text
}

// RunsText is the inverse of RowRuns — the plain text of a run list. Used by
// the no-JS page and the /text mirror, and by tests asserting cell parity.
func RunsText(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}
