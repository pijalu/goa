// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package annotate provides a document-agnostic full-screen pager with
// line-anchored comments. It is used by ReviewPager (code-review diffs) and
// PlanPager (structured plans).
package annotate

// Pager is a generic full-screen pager that manages content lines, viewport
// navigation, and callbacks for host-mediated text entry. It does no I/O and
// knows nothing about diffs or plans — those live in the calling code.
//
// Viewport state (cursor, scrollTop) is managed by the calling code. Pager
// provides pure helpers for clamping, rendering, and navigation.
type Pager struct {
	// Rendered content lines. The host sets these before each render cycle.
	Content []string

	// Anchors maps rendered line indices (0-based) to anchor identifiers.
	// In ReviewPager these are "file:line", in PlanPager these are item IDs.
	Anchors []Anchor

	// OnCommentRequest is called when the user adds or edits a comment.
	OnCommentRequest func(title, current string, onSubmit func(string))
	// OnConfirm is called when the user must confirm an action (y/n).
	OnConfirm func(question string, onResult func(yes bool))
	// OnClose is called when the pager is closed.
	OnClose func()
	// RequestRender asks the host to redraw the overlay.
	RequestRender func()
}

// Anchor maps a rendered line to an identifier.
type Anchor struct {
	Line int    `json:"line"` // 0-based line index in Content
	ID   string `json:"id"`   // anchor identifier
}

// NewPager creates a new pager.
func NewPager() *Pager {
	return &Pager{}
}

// ClampCursor clamps the cursor to valid range within content length.
func ClampCursor(cursor, contentLen int) int {
	if contentLen == 0 {
		return 0
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= contentLen {
		return contentLen - 1
	}
	return cursor
}

// EnsureScrollInBounds clamps cursor and scrollTop to valid ranges.
// Returns (newCursor, newScrollTop).
func EnsureScrollInBounds(cursor, scrollTop, contentLen, visibleHeight int) (int, int) {
	cursor = ClampCursor(cursor, contentLen)
	if contentLen == 0 {
		return 0, 0
	}
	if scrollTop > cursor {
		scrollTop = cursor
	}
	height := visibleHeight
	if cursor >= scrollTop+height {
		scrollTop = cursor - height + 1
	}
	if scrollTop < 0 {
		scrollTop = 0
	}
	if scrollTop > contentLen-height {
		scrollTop = contentLen - height
	}
	if scrollTop < 0 {
		scrollTop = 0
	}
	return cursor, scrollTop
}

// MoveCursor moves the cursor by delta, clamps it, and updates scrollTop.
// Returns (newCursor, newScrollTop).
func MoveCursor(cursor, scrollTop, delta, contentLen, visibleHeight int) (int, int) {
	cursor += delta
	return EnsureScrollInBounds(cursor, scrollTop, contentLen, visibleHeight)
}

// AnchorAtLine returns the anchor at the given line, or nil.
func AnchorAtLine(anchors []Anchor, line int) *Anchor {
	for i := range anchors {
		if anchors[i].Line == line {
			return &anchors[i]
		}
	}
	return nil
}

// RenderContent returns a slice of content lines visible in the viewport.
func RenderContent(content []string, scrollTop, height int) []string {
	if scrollTop < 0 {
		scrollTop = 0
	}
	end := scrollTop + height
	if end > len(content) {
		end = len(content)
	}
	return content[scrollTop:end]
}
