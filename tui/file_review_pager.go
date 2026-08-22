// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui/annotate"
)

// commentBgColor is the dark-blue background shared with the diff review
// pager for the cursor row and commented rows, so both pagers read as one
// feature (UX spec: "identical visuals").
const commentBgColor = "#1e4273"

// FileReviewPager renders a single file's source and lets the user navigate,
// anchor comments to real lines, submit the review, or export it.
//
// It mirrors ReviewPager structurally (annotate core + reviewActions +
// host-mediated text entry) but anchors every line in one coordinate space:
// LineAnchor{File: Content.Path, LineNum: idx+1, Side: SideNew}.
//
// Like ReviewPager it is a pure renderer/key-router — all text entry
// (comments AND yes/no confirmations) happens on the host's main input line
// via callbacks; there is no inline editor.
type FileReviewPager struct {
	Session *review.Session
	Content *review.FileReviewContent

	// Generic pager core — holds nothing itself; kept for consistency with
	// ReviewPager so future shared helpers have the same shape.
	pager *annotate.Pager

	cursor    int // selected line index within Content.Lines
	scrollTop int // first visible line index

	viewportW int
	viewportH int

	// Host callbacks — same contract/semantics as ReviewPager.
	OnSubmitReview   func(text string)
	OnExportReview   func()
	OnClose          func()
	OnCommentSaved   func()
	OnCommentRequest func(title, current string, onSubmit func(string))
	OnConfirm        func(question string, onResult func(yes bool))
	RequestRender    func()
}

// NewFileReviewPager creates a pager over loaded file content.
func NewFileReviewPager(session *review.Session, content *review.FileReviewContent) *FileReviewPager {
	return &FileReviewPager{
		Session: session,
		Content: content,
		pager:   annotate.NewPager(),
	}
}

// SetViewport tells the pager the available terminal size so it can render
// full-screen instead of a fixed small window.
func (p *FileReviewPager) SetViewport(width, height int) {
	p.viewportW = width
	p.viewportH = height
}

// Invalidate implements Component.
func (p *FileReviewPager) Invalidate() {}

// lineCount returns the number of source lines under review. Nil-safe so a
// misconstructed pager degrades to an empty document instead of panicking.
func (p *FileReviewPager) lineCount() int {
	if p.Content == nil {
		return 0
	}
	return len(p.Content.Lines)
}

// anchorPath is the comment anchor path: the canonical path recorded on the
// content, falling back to the session's FilePath.
func (p *FileReviewPager) anchorPath() string {
	if p.Content != nil && p.Content.Path != "" {
		return p.Content.Path
	}
	return p.Session.FilePath
}

// visibleHeight returns the available body height (title row reserved).
func (p *FileReviewPager) visibleHeight() int {
	if p.viewportH > 1 {
		return p.viewportH - 1
	}
	// No viewport set: render a large buffer so the compositor can clamp to
	// the actual terminal height — same policy as ReviewPager.
	return 200
}

// gutterWidth returns the display width of the right-aligned line-number
// gutter: the decimal width of the largest line number.
func (p *FileReviewPager) gutterWidth() int {
	w := len(fmt.Sprintf("%d", max(p.lineCount(), 1)))
	return max(w, 1)
}

// title renders the bold header per the UX spec:
// "Review file <path>  <N> lines  comments:<M>  [(truncated)]".
func (p *FileReviewPager) title() string {
	t := fmt.Sprintf("Review file %s  %d lines  comments:%d",
		p.anchorPath(), p.lineCount(), len(p.Session.Comments))
	if p.Content != nil && p.Content.Truncated {
		t += "  (truncated)"
	}
	return t
}

// Render implements Component. Pure with respect to document state: the
// Markdown fence state is a local cursor of a fresh mdSourceState walked
// from line 0 on every render, so rendering stays idempotent under
// differential redraws.
func (p *FileReviewPager) Render(width int) []string {
	out := []string{ansi.Bold + truncate(p.title(), width) + ansi.BoldReset}

	height := p.visibleHeight()
	if height < 2 {
		height = 2
	}
	p.cursor, p.scrollTop = annotate.EnsureScrollInBounds(p.cursor, p.scrollTop, p.lineCount(), height)

	gutter := p.gutterWidth()
	contentW := width - 2 - gutter - 1
	md := &mdSourceState{}

	end := min(p.scrollTop+height, p.lineCount())
	for i := p.scrollTop; i < end; i++ {
		out = append(out, p.renderRow(i, gutter, contentW, md))
	}
	if p.lineCount() == 0 {
		out = append(out, ansi.Faint+"File is empty."+ansi.FaintReset)
	}
	for len(out) < height+1 {
		out = append(out, "")
	}
	return out
}

// renderRow renders one body row: prefix(2) + right-aligned gutter + space +
// truncated highlighted content (+ comment badge). Commented rows share the
// diff pager's visuals: green pipe prefix, background block, badge.
func (p *FileReviewPager) renderRow(i, gutterW, contentW int, md *mdSourceState) string {
	lineNum := i + 1
	count := len(p.Session.CommentsFor(p.anchorPath(), lineNum, review.SideNew))
	commented := count > 0

	prefix := "  "
	if i == p.cursor {
		prefix = "> "
		if commented {
			prefix = ansi.Bg(commentBgColor) + "> "
		}
	} else if commented {
		// Green pipe replaces the leading space; no reset keeps the
		// background running into the gutter and content below.
		prefix = ansi.Bg(commentBgColor) + ansi.Fg("#3fb950") + "│" + ansi.FgReset + ansi.Bg(commentBgColor) + " "
	}

	gutter := fmt.Sprintf("%*d", gutterW, lineNum)
	bg := ""
	if commented {
		bg = ansi.Bg(commentBgColor)
	}

	row := prefix + bg + gutter + " " + truncate(p.highlightLine(md, i), contentW) + ansi.Reset
	if commented {
		row += " " + ansi.Fg("#d29922") + fmt.Sprintf("[%d comment(s)]", count) + ansi.FgReset
	}
	return row
}

// highlightLine styles source line idx (0-based). Markdown goes through the
// stateful source highlighter walked in document order; every other
// extension uses the shared per-line highlighter keyed by extension (D2) —
// unknown extensions render sanitized plain text.
func (p *FileReviewPager) highlightLine(md *mdSourceState, idx int) string {
	raw := p.Content.Lines[idx]
	if p.Content.IsMarkdown {
		return md.Highlight(raw)
	}
	return tools.HighlightLine(raw, p.Content.Ext)
}

// HandleInput implements Component. It routes navigation and action keys;
// all text entry is delegated to the host via callbacks. There is no 'b'
// (base switching) — a file has no base commit.
func (p *FileReviewPager) HandleInput(data string) {
	switch data {
	case "up", "k":
		p.moveCursor(-1)
	case "down", "j":
		p.moveCursor(1)
	case "pgup":
		p.moveCursor(-p.visibleHeight())
	case "pgdn":
		p.moveCursor(p.visibleHeight())
	case "c":
		p.requestAddComment()
	case "e":
		p.requestEditComment()
	case "d":
		p.requestDeleteComment()
	case "s":
		p.newActions().SubmitWithConfirm()
	case "x":
		p.requestExportReview()
	case "q", "esc", "ctrl+c":
		p.close()
	}
}

// moveCursor moves the cursor by delta rows and keeps it in view.
func (p *FileReviewPager) moveCursor(delta int) {
	p.cursor, p.scrollTop = annotate.MoveCursor(p.cursor, p.scrollTop, delta, p.lineCount(), p.visibleHeight())
	p.requestRender()
}

// currentAnchor resolves the cursor position to a review anchor. Anchors are
// 1-based over Content.Lines in the file's single coordinate space.
func (p *FileReviewPager) currentAnchor() review.LineAnchor {
	return review.LineAnchor{
		File:    p.anchorPath(),
		LineNum: p.cursor + 1,
		Side:    review.SideNew,
	}
}

// requestAddComment asks for new comment text at the cursor line. It is a
// no-op on an empty file where no real line exists to anchor to.
func (p *FileReviewPager) requestAddComment() {
	if p.lineCount() == 0 {
		return
	}
	p.newActions().AddCommentAt(p.currentAnchor())
}

// requestEditComment edits the first comment at the cursor line.
func (p *FileReviewPager) requestEditComment() {
	if p.lineCount() == 0 {
		return
	}
	p.newActions().EditCommentAt(p.currentAnchor())
}

// requestDeleteComment confirms and removes the first comment at the cursor
// line.
func (p *FileReviewPager) requestDeleteComment() {
	if p.lineCount() == 0 {
		return
	}
	p.newActions().DeleteCommentAt(p.currentAnchor())
}

// requestExportReview hands export to the host (non-destructive, pager stays
// open) — same semantics as the diff pager's 'x'.
func (p *FileReviewPager) requestExportReview() {
	if p.OnExportReview != nil {
		p.OnExportReview()
	}
}

func (p *FileReviewPager) close() {
	if p.OnClose != nil {
		p.OnClose()
	}
}

func (p *FileReviewPager) requestRender() {
	if p.RequestRender != nil {
		p.RequestRender()
	}
}

// newActions wires the shared comment lifecycle (D6) to this pager's current
// callbacks. Built on demand so callbacks assigned after construction are
// always honored.
func (p *FileReviewPager) newActions() *reviewActions {
	return &reviewActions{
		session:          p.Session,
		onCommentRequest: p.OnCommentRequest,
		onConfirm:        p.OnConfirm,
		onCommentSaved:   func() { p.saveComments() },
		onSubmitReview:   p.OnSubmitReview,
		onClose:          p.OnClose,
	}
}

func (p *FileReviewPager) saveComments() {
	if p.OnCommentSaved != nil {
		p.OnCommentSaved()
	}
}
