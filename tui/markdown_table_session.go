// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "sync"

// tableWidthPolicy decides the effective width used for markdown table
// geometry (bugs.md B53, Option A). Rows already painted into the native
// terminal scrollback are immutable; a table must therefore compute its
// column widths at a width that stays stable across repaints, so that any
// later re-render at a different terminal width produces byte-identical
// rows instead of mixing geometries inside one table.
type tableWidthPolicy interface {
	// pinnedWidth returns the width table geometry should use (0 = unset,
	// fall back to the renderer's own width).
	pinnedWidth() int
	// observe records a transcript width seen this session.
	observe(width int)
}

// sessionTablePin tracks the smallest transcript content width observed in
// the session. Table geometry is computed at that pinned width: repaints at
// any wider terminal reproduce the rows scrollback already holds. Prose
// reflow is unaffected — only table geometry consults the pin.
type sessionTablePin struct {
	mu   sync.Mutex
	minW int
}

func (s *sessionTablePin) observe(width int) {
	if width <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.minW == 0 || width < s.minW {
		s.minW = width
	}
}

func (s *sessionTablePin) pinnedWidth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.minW
}

func (s *sessionTablePin) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minW = 0
}

// theSessionTablePin is the transcript-wide pin shared by all message
// renderers for the lifetime of the TUI session.
var theSessionTablePin sessionTablePin

// ResetSessionTablePin clears the session table-geometry pin (new session).
// Exported for test isolation.
func ResetSessionTablePin() { theSessionTablePin.reset() }
