// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// ── Kill / Yank ──

func (e *Editor) killWordBack() {
	oldPos := e.pos
	e.wordLeft()
	if e.pos < oldPos {
		e.killRing.Push(string(e.buf[e.pos:oldPos]), false)
		e.buf = append(e.buf[:e.pos], e.buf[oldPos:]...)
		e.dirty = len(e.buf) > 0
		e.pushUndo()
	}
}

func (e *Editor) killWordForward() {
	oldPos := e.pos
	e.wordRight()
	if e.pos > oldPos {
		e.killRing.Push(string(e.buf[oldPos:e.pos]), false)
		e.buf = append(e.buf[:oldPos], e.buf[e.pos:]...)
		e.pos = oldPos
		e.dirty = len(e.buf) > 0
		e.pushUndo()
	}
}

func (e *Editor) killToStart() {
	if e.pos > 0 {
		e.killRing.Push(string(e.buf[:e.pos]), false)
		e.buf = e.buf[e.pos:]
		e.pos = 0
		e.dirty = len(e.buf) > 0
		e.pushUndo()
	}
}

func (e *Editor) killToEnd() {
	if e.pos < len(e.buf) {
		e.killRing.Push(string(e.buf[e.pos:]), false)
		e.buf = e.buf[:e.pos]
		e.dirty = len(e.buf) > 0
		e.pushUndo()
	}
}

func (e *Editor) yank() {
	text := e.killRing.Yank()
	if text != "" {
		e.pushUndo()
		e.insertString(text)
	}
}

func (e *Editor) yankPop() {
	text := e.killRing.YankPop()
	if text != "" {
		e.pushUndo()
		e.insertString(text)
	}
}
