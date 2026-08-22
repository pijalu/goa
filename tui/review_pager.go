// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui/annotate"
)

// ReviewPager renders a git diff and lets the user navigate, select a base
// commit, and manage comments.
//
// All text entry — comments AND yes/no confirmations (delete, submit) — is
// performed on the host's main input line via callbacks (OnCommentRequest,
// OnConfirm). The pager deliberately does NOT implement its own line editor:
// doing so previously duplicated the Editor (cursor movement, backspace,
// history) and was the source of multiple bugs the agent could not easily
// diagnose. Keeping the pager a pure renderer+key-router honors SRP and keeps
// all editing in one place.
type ReviewPager struct {
	Session *review.Session
	Diff    string

	// Generic pager core — holds content and anchors (used by PlanPager too).
	pager *annotate.Pager

	// scrollTop is the index of the first visible line in lines.
	scrollTop int
	// cursor is the selected line index within lines.
	cursor int

	// viewport dimensions; set by the host before the first render.
	viewportW int
	viewportH int

	lines []review.DiffLine

	// OnSubmitReview is called when the user confirms submission with 's'.
	// The text passed is Session.MarkdownSummary() — the same content the
	// 'x' export action writes to disk, so submit and export always agree.
	OnSubmitReview func(text string)

	// OnExportReview is called when the user presses 'x'. The host writes the
	// review Markdown (the same content submit sends to the agent) to a file
	// and reports the result. The pager stays open so the user can keep
	// reviewing or submit afterwards. The pager does not perform file I/O
	// directly, keeping it a pure renderer/key-router.
	OnExportReview func()

	// OnClose is called when the user closes the pager.
	OnClose func()

	// OnCommentSaved is called after a comment is added, edited, or deleted.
	// The host can use it to persist the review session.
	OnCommentSaved func()

	// OnCommentRequest is called when the user wants to add or edit a comment.
	// The host should ask for the comment text on the main input line, using
	// the provided title as the input prompt and current as the pre-filled text.
	// When the user submits, call onSubmit; on escape/cancel, discard.
	OnCommentRequest func(title, current string, onSubmit func(string))

	// OnConfirm is called when the user must confirm a destructive or important
	// action (delete a comment, submit the review). The host should present the
	// question on the main input line (typically with a "(y/n)" suffix) and
	// invoke onResult with true for yes and false for no/cancel. Routing this
	// through the main input line — instead of an inline overlay prompt — keeps
	// all prompts in one place and matches the comment-entry UX.
	OnConfirm func(question string, onResult func(yes bool))

	// OnSelectBase is called when the user wants to change the base commit.
	// The host should present the commits to the user and call onSelect with
	// the chosen SHA. An empty selection means the user cancelled.
	OnSelectBase func(commits []review.CommitInfo, onSelect func(string))

	// RequestRender asks the TUI engine to redraw the overlay.
	RequestRender func()

	// RecentCommits is the list of commits shown by the base selector.
	RecentCommits []review.CommitInfo
}

// NewReviewPager creates a pager for the given review session and diff.
func NewReviewPager(session *review.Session, diff string) *ReviewPager {
	p := &ReviewPager{
		Session: session,
		Diff:    diff,
		pager:   annotate.NewPager(),
		lines:   review.ParseDiff(diff),
	}
	p.moveCursorToFirstHunk()
	return p
}

// SetViewport tells the pager the available terminal size so it can render
// full-screen instead of a fixed small window.
func (p *ReviewPager) SetViewport(width, height int) {
	p.viewportW = width
	p.viewportH = height
}

// moveCursorToFirstHunk positions the cursor on the first content line.
// Internal helper; runs on the commandLoop.
func (p *ReviewPager) moveCursorToFirstHunk() {
	for i, l := range p.lines {
		if l.Kind == review.DiffAdded || l.Kind == review.DiffRemoved || l.Kind == review.DiffContext {
			p.cursor = i
			p.scrollTop = max(0, i-2)
			return
		}
	}
}

// visibleHeight returns the available content display height.
func (p *ReviewPager) visibleHeight() int {
	if p.viewportH > 1 {
		return p.viewportH - 1 // reserve one row for title
	}
	// When no viewport is set, render a large buffer so the compositor can
	// clamp to the actual terminal height and still cover the full screen.
	return 200
}

// Render implements Component. It returns the rendered lines.
func (p *ReviewPager) Render(width int) []string {
	return p.renderDiff(width)
}

func (p *ReviewPager) renderDiff(width int) []string {
	var out []string
	title := fmt.Sprintf("Review %s  base:%s  comments:%d", p.Session.ID[:8], p.Session.BaseRef, len(p.Session.Comments))
	out = append(out, ansi.Bold+truncate(title, width)+ansi.BoldReset)

	height := p.visibleHeight()
	if height < 3 {
		height = 3
	}
	p.cursor, p.scrollTop = annotate.EnsureScrollInBounds(p.cursor, p.scrollTop, len(p.lines), height)

	end := p.scrollTop + height
	if end > len(p.lines) {
		end = len(p.lines)
	}

	for i := p.scrollTop; i < end; i++ {
		line := p.lines[i]
		prefix := p.linePrefix(i, width)
		prefixWidth := ansi.Width(prefix)
		hasComment, commentCount := p.lineCommentInfo(line)
		text := p.renderLine(line, width-prefixWidth, hasComment)
		if commentCount > 0 {
			text += " " + ansi.Fg("#d29922") + fmt.Sprintf("[%d comment(s)]", commentCount) + ansi.FgReset
		}
		out = append(out, prefix+text)
	}

	for len(out) < height+1 {
		out = append(out, "")
	}
	return out
}

// linePrefix returns the two-column prefix for a rendered diff line.
// The selected line uses "> "; commented (non-selected) lines replace the
// first space with a green pipe so the diff text does not shift.
func (p *ReviewPager) linePrefix(i, width int) string {
	selected := i == p.cursor
	hasComment := p.lineHasComment(i)
	if selected {
		if hasComment {
			return ansi.Bg("#1e4273") + "> "
		}
		return "> "
	}
	if hasComment {
		return ansi.Bg("#1e4273") + ansi.Fg("#3fb950") + "│" + ansi.FgReset + ansi.Bg("#1e4273") + " "
	}
	return "  "
}

func (p *ReviewPager) lineHasComment(i int) bool {
	if i < 0 || i >= len(p.lines) {
		return false
	}
	_, hasComment, _ := p.anchorCommentInfo(p.lines[i])
	return hasComment
}

func (p *ReviewPager) lineCommentInfo(line review.DiffLine) (bool, int) {
	_, hasComment, count := p.anchorCommentInfo(line)
	return hasComment, count
}

// anchorCommentInfo resolves the line's comment anchor and reports whether
// comments exist at that exact (file, line, side) position. The side-aware
// lookup is what prevents a comment on an added line from leaking onto a
// removed line whose old number collides with the commented new number.
func (p *ReviewPager) anchorCommentInfo(line review.DiffLine) (review.LineAnchor, bool, int) {
	anchor, ok := line.Anchor()
	if !ok || anchor.LineNum <= 0 {
		return anchor, false, 0
	}
	comments := p.Session.CommentsFor(anchor.File, anchor.LineNum, anchor.Side)
	return anchor, len(comments) > 0, len(comments)
}

func (p *ReviewPager) renderLine(line review.DiffLine, width int, hasComment bool) string {
	s := line.Raw
	commentBg := ""
	if hasComment {
		commentBg = ansi.Bg("#1e4273")
	}

	switch line.Kind {
	case review.DiffHeader:
		return commentBg + ansi.Fg("#8b949e") + truncate(s, width) + ansi.FgReset + ansi.Reset
	case review.DiffFileMeta:
		return commentBg + ansi.Bold + ansi.Fg("#58a6ff") + truncate(s, width) + ansi.BoldReset + ansi.FgReset + ansi.Reset
	case review.DiffHunkHeader:
		return commentBg + ansi.Fg("#d29922") + truncate(s, width) + ansi.FgReset + ansi.Reset
	case review.DiffAdded:
		lang := langFromPath(line.File)
		highlighted := tools.HighlightLine(strings.TrimPrefix(s, "+"), lang)
		return commentBg + ansi.Fg("#3fb950") + "+" + ansi.FgReset + truncate(highlighted, width-1) + ansi.Reset
	case review.DiffRemoved:
		lang := langFromPath(line.File)
		highlighted := tools.HighlightLine(strings.TrimPrefix(s, "-"), lang)
		return commentBg + ansi.Fg("#f85149") + "-" + ansi.FgReset + truncate(highlighted, width-1) + ansi.Reset
	default:
		lang := langFromPath(line.File)
		highlighted := tools.HighlightLine(strings.TrimPrefix(s, " "), lang)
		return commentBg + " " + truncate(highlighted, width-1) + ansi.Reset
	}
}

// HandleInput implements Component. It routes only navigation and action keys;
// all text entry is delegated to the host via callbacks.
func (p *ReviewPager) HandleInput(data string) {
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
	case "b":
		p.requestSelectBase()
	case "s":
		p.requestSubmitReview()
	case "x":
		p.requestExportReview()
	case "q", "esc", "ctrl+c":
		p.close()
	}
}

// moveCursor moves the cursor by delta rows and keeps it in view. Runs on
// the commandLoop (sole owner).
func (p *ReviewPager) moveCursor(delta int) {
	p.cursor, p.scrollTop = annotate.MoveCursor(p.cursor, p.scrollTop, delta, len(p.lines), p.visibleHeight())
	p.requestRender()
}

func (p *ReviewPager) close() {
	if p.OnClose != nil {
		p.OnClose()
	}
}

func (p *ReviewPager) requestRender() {
	if p.RequestRender != nil {
		p.RequestRender()
	}
}

// currentLine returns the diff line under the cursor.
func (p *ReviewPager) currentLine() review.DiffLine {
	if p.cursor < 0 || p.cursor >= len(p.lines) {
		return review.DiffLine{}
	}
	return p.lines[p.cursor]
}

// requestAddComment routes through the shared comment lifecycle (D6).
func (p *ReviewPager) requestAddComment() {
	anchor, ok := p.currentLine().Anchor()
	if !ok || anchor.LineNum <= 0 {
		return
	}
	p.newActions().AddCommentAt(anchor)
}

// requestEditComment routes through the shared comment lifecycle (D6).
func (p *ReviewPager) requestEditComment() {
	anchor, ok := p.currentLine().Anchor()
	if !ok {
		return
	}
	p.newActions().EditCommentAt(anchor)
}

// requestDeleteComment routes through the shared comment lifecycle (D6).
func (p *ReviewPager) requestDeleteComment() {
	anchor, ok := p.currentLine().Anchor()
	if !ok {
		return
	}
	p.newActions().DeleteCommentAt(anchor)
}

func (p *ReviewPager) requestSelectBase() {
	commits := p.RecentCommits
	hasOnSelect := p.OnSelectBase != nil
	if len(commits) == 0 || !hasOnSelect {
		return
	}
	p.OnSelectBase(commits, func(sha string) {
		if sha == "" {
			return
		}
		p.changeBase(sha)
	})
}

// requestSubmitReview routes through the shared comment lifecycle (D6).
func (p *ReviewPager) requestSubmitReview() {
	p.newActions().SubmitWithConfirm()
}

// requestExportReview writes the review Markdown to disk via the host. It is
// non-destructive (a new timestamped file) so it needs no confirmation, and
// the pager stays open afterwards.
func (p *ReviewPager) requestExportReview() {
	if p.OnExportReview != nil {
		p.OnExportReview()
	}
}

func (p *ReviewPager) saveComments() {
	if p.OnCommentSaved != nil {
		p.OnCommentSaved()
	}
}

// anchorLabel formats a comment anchor for prompts, marking removed-line
// anchors so the user can tell old-side numbering from new-side numbering.
func anchorLabel(a review.LineAnchor) string {
	label := fmt.Sprintf("%s:%d", a.File, a.LineNum)
	if a.Side == review.SideOld {
		label += " (removed)"
	}
	return label
}

// changeBase recomputes the diff against a new base commit. Runs on the
// commandLoop (sole owner).
func (p *ReviewPager) changeBase(base string) {
	p.Session.BaseRef = base
	diff, err := review.Diff(p.Session.ProjectDir, base)
	if err != nil {
		diff = ""
	}
	p.Diff = diff
	p.cursor = 0
	p.scrollTop = 0
	p.lines = review.ParseDiff(diff)
	p.moveCursorToFirstHunk()
	p.requestRender()
}

// newActions wires the shared comment lifecycle to this pager's current
// callbacks. Built on demand so callbacks assigned after construction are
// always honored.
func (p *ReviewPager) newActions() *reviewActions {
	return &reviewActions{
		session:          p.Session,
		onCommentRequest: p.OnCommentRequest,
		onConfirm:        p.OnConfirm,
		onCommentSaved:   p.saveComments,
		onSubmitReview:   p.OnSubmitReview,
		onClose:          p.OnClose,
	}
}

// Invalidate implements Component.
func (p *ReviewPager) Invalidate() {}

func langFromPath(path string) string {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	return ext
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width-1) + "…" + ansi.Reset
}
