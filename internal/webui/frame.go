// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import "github.com/pijalu/goa/tui"

// Cursor is the caret position carried by every frame. Visible mirrors the
// terminal's cursor mode (the engine hides the caret while a selector is up).
type Cursor struct {
	Row     int  `json:"r"`
	Col     int  `json:"c"`
	Visible bool `json:"v"`
}

// Run is a maximal span of consecutive cells that share text styling: the
// unit the browser turns into one <span>. An empty FG/BG means the theme
// default, so the client can omit the inline style entirely.
type Run struct {
	Text  string        `json:"t"`
	Flags tui.AttrFlags `json:"f,omitempty"`
	FG    string        `json:"fg,omitempty"`
	BG    string        `json:"bg,omitempty"`
	// Link is the OSC-8 target of the run ("" = plain text). Flags carries
	// tui.AttrLink whenever Link is set, so a client can test one or the other.
	Link string `json:"link,omitempty"`
}

// RowPatch replaces one screen row: the runs from column 0 to cols-1.
type RowPatch struct {
	Row  int   `json:"row"`
	Runs []Run `json:"runs"`
}

// Frame is an immutable snapshot of the grid's delta. Frames are built once
// and only ever read afterwards, so the hub can fan them out without copying
// or locking per client.
type Frame struct {
	Seq     uint64
	Cols    int
	Rows    int
	Cursor  Cursor
	Patches []RowPatch
	// Scrollback carries the rows that scrolled off the top since the previous
	// frame, in order. The grid never re-sends them, so a client that keeps
	// them in a transcript list ends up with a full scroll history — what a
	// real terminal's scrollback buffer gives you. Empty on most frames.
	Scrollback []RowPatch
	// Title is set only when it changed since the previous frame ("" = keep
	// the client's current title).
	Title string
	// Full marks a frame that carries every row (a fresh or reconnecting
	// client), so the client can reset its model instead of patching.
	Full bool
}

// NewFrame assembles a frame from a grid's current state. patches is the diff
// to ship; title is carried only when non-empty.
func NewFrame(seq uint64, g *CellGrid, patches []RowPatch, title string, full bool) *Frame {
	cols, rows := g.Size()
	return &Frame{
		Seq:     seq,
		Cols:    cols,
		Rows:    rows,
		Cursor:  g.Cursor(),
		Patches: patches,
		Title:   title,
		Full:    full,
	}
}

// PatchedRow returns the patch for one row, if the frame carries it.
func (f *Frame) PatchedRow(row int) (RowPatch, bool) {
	for _, p := range f.Patches {
		if p.Row == row {
			return p, true
		}
	}
	return RowPatch{}, false
}

// Control is a server→client out-of-band message (session rotation, read-only
// notice, shutdown). It rides the same socket as frames but is a different
// message type so a client can react without touching the grid.
type Control struct {
	Kind    string `json:"t"`
	Session string `json:"session,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Control message kinds (see specs/webui.md §20).
const (
	CtrlSessionRotated = "session_rotated"
	CtrlBye            = "bye"
	CtrlError          = "error"
	CtrlReadOnly       = "read_only"
)
