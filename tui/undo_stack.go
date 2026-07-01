// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// UndoSnapshot represents a single editor state for undo/redo.
type UndoSnapshot struct {
	Text   string
	Cursor int // rune offset
}

// UndoStack manages undo/redo history for the editor.
type UndoStack struct {
	undo []UndoSnapshot
	redo []UndoSnapshot
	max  int
}

// NewUndoStack creates an undo stack with the given max size.
func NewUndoStack(maxSize int) *UndoStack {
	if maxSize < 1 {
		maxSize = 100
	}
	return &UndoStack{max: maxSize}
}

// Push saves the current state for potential undo.
func (us *UndoStack) Push(snap UndoSnapshot) {
	us.undo = append(us.undo, snap)
	if len(us.undo) > us.max {
		us.undo = us.undo[1:]
	}
	// Any new push invalidates the redo stack
	us.redo = nil
}

// Undo returns the previous state. Returns nil if nothing to undo.
func (us *UndoStack) Undo(current UndoSnapshot) *UndoSnapshot {
	if len(us.undo) == 0 {
		return nil
	}
	// Save current state for redo
	us.redo = append(us.redo, current)
	last := us.undo[len(us.undo)-1]
	us.undo = us.undo[:len(us.undo)-1]
	return &last
}

// Redo returns the next state. Returns nil if nothing to redo.
func (us *UndoStack) Redo(current UndoSnapshot) *UndoSnapshot {
	if len(us.redo) == 0 {
		return nil
	}
	us.undo = append(us.undo, current)
	last := us.redo[len(us.redo)-1]
	us.redo = us.redo[:len(us.redo)-1]
	return &last
}

// Clear empties the stack.
func (us *UndoStack) Clear() {
	us.undo = nil
	us.redo = nil
}
