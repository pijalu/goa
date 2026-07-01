// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// ── Undo / Redo ──

func (e *Editor) pushUndo() {
	e.undo.Push(UndoSnapshot{Text: string(e.buf), Cursor: e.pos})
}

func (e *Editor) doUndo() {
	current := UndoSnapshot{Text: string(e.buf), Cursor: e.pos}
	if snap := e.undo.Undo(current); snap != nil {
		e.buf = []rune(snap.Text)
		e.pos = snap.Cursor
	}
}
