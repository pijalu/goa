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

	// scrollTop is the index of the first visible line in lines.
	scrollTop int
	// cursor is the selected line index within lines.
	cursor int

	lines []review.DiffLine

	// OnSubmitReview is called when the user confirms submission with 's'.
	// The text passed is Session.MarkdownSummary(Diff) — the same content the
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

	// viewport dimensions; set by the host before the first render.
	viewportW int
	viewportH int
}

// NewReviewPager creates a pager for the given review session and diff.
func NewReviewPager(session *review.Session, diff string) *ReviewPager {
	p := &ReviewPager{
		Session: session,
		Diff:    diff,
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

// Render implements Component. It takes the read lock and delegates to
// renderLocked; renderLocked and every helper it calls (ensureScrollInBounds,
// currentLine, renderLine, ...) assume the read lock is already held.
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
	p.ensureScrollInBounds()

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
	_, lineNum := commentTarget(p.lines[i])
	if lineNum <= 0 {
		return false
	}
	return len(p.Session.CommentsFor(p.lines[i].File, lineNum)) > 0
}

func (p *ReviewPager) lineCommentInfo(line review.DiffLine) (bool, int) {
	_, lineNum := commentTarget(line)
	if lineNum <= 0 {
		return false, 0
	}
	comments := p.Session.CommentsFor(line.File, lineNum)
	return len(comments) > 0, len(comments)
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

func (p *ReviewPager) visibleHeight() int {
	if p.viewportH > 1 {
		return p.viewportH - 1 // reserve one row for title
	}
	// When no viewport is set, render a large buffer so the compositor can
	// clamp to the actual terminal height and still cover the full screen.
	return 200
}

func (p *ReviewPager) ensureScrollInBounds() {
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.lines) {
		p.cursor = len(p.lines) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.scrollTop > p.cursor {
		p.scrollTop = p.cursor
	}
	height := p.visibleHeight()
	if p.cursor >= p.scrollTop+height {
		p.scrollTop = p.cursor - height + 1
	}
	if p.scrollTop < 0 {
		p.scrollTop = 0
	}
	if p.scrollTop > len(p.lines)-height {
		p.scrollTop = len(p.lines) - height
	}
	if p.scrollTop < 0 {
		p.scrollTop = 0
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

func (p *ReviewPager) close() {
	if p.OnClose != nil {
		p.OnClose()
	}
}

// moveCursor moves the cursor by delta rows and keeps it in view. Runs on
// the commandLoop (sole owner).
func (p *ReviewPager) moveCursor(delta int) {
	p.cursor += delta
	p.ensureScrollInBounds()
	p.requestRender()
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

func (p *ReviewPager) requestAddComment() {
	line := p.currentLine()
	file, lineNum := commentTarget(line)
	if file == "" || lineNum <= 0 {
		return
	}
	if p.OnCommentRequest == nil {
		return
	}
	prompt := fmt.Sprintf("Add comment on %s:%d:", file, lineNum)
	p.OnCommentRequest(prompt, "", func(text string) {
		if text == "" {
			return
		}
		p.Session.AddComment(file, lineNum, text)
		p.saveComments()
	})
}

func (p *ReviewPager) requestEditComment() {
	line := p.currentLine()
	file, lineNum := commentTarget(line)
	comments := p.Session.CommentsFor(file, lineNum)
	if len(comments) == 0 {
		return
	}
	if p.OnCommentRequest == nil {
		return
	}
	c := comments[0]
	prompt := fmt.Sprintf("Edit comment on %s:%d:", file, lineNum)
	p.OnCommentRequest(prompt, c.Content, func(text string) {
		if text == "" {
			return
		}
		p.Session.UpdateComment(c.ID, text)
		p.saveComments()
	})
}

func (p *ReviewPager) requestDeleteComment() {
	line := p.currentLine()
	file, lineNum := commentTarget(line)
	comments := p.Session.CommentsFor(file, lineNum)
	if len(comments) == 0 {
		return
	}
	if p.OnConfirm == nil {
		return
	}
	c := comments[0]
	question := fmt.Sprintf("Delete comment on %s:%d?", file, lineNum)
	p.OnConfirm(question, func(yes bool) {
		if !yes {
			return
		}
		p.Session.RemoveComment(c.ID)
		p.saveComments()
	})
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

func (p *ReviewPager) requestSubmitReview() {
	if p.OnConfirm == nil {
		return
	}
	p.OnConfirm("Submit review to agent?", func(yes bool) {
		if !yes {
			return
		}
		p.submitReview()
	})
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

// commentTarget returns the file and line number to attach a comment to.
// Only actual diff content lines (context, added, removed) can carry comments;
// headers and file meta lines are not valid targets. Removed lines don't exist
// in the new file, so their old line number is used.
func commentTarget(line review.DiffLine) (string, int) {
	if line.File == "" {
		return "", 0
	}
	switch line.Kind {
	case review.DiffAdded, review.DiffContext:
		return line.File, line.NewLineNum
	case review.DiffRemoved:
		return line.File, line.OldLineNum
	default:
		return "", 0
	}
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

func (p *ReviewPager) submitReview() {
	// MarkdownSummary is the single source of truth for the review body; the
	// 'x' export action writes the exact same string to a file.
	if p.OnSubmitReview != nil {
		p.OnSubmitReview(p.Session.MarkdownSummary(p.Diff))
	}
	if p.OnClose != nil {
		p.OnClose()
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
