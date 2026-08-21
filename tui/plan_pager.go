// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core/plan"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui/annotate"
)

// PlanPager renders a structured plan for annotation and review. It is a thin
// adapter over annotate.Pager with plan-specific key bindings and comment
// management backed by a plan.Store.
//
// All text entry is delegated to the host via callbacks. Comments are saved
// immediately (event-sourced) so closing the pager never loses work.
type PlanPager struct {
	// Store is the plan store backing this pager.
	Store *plan.Store

	// Generic pager core for content, anchors, and callbacks.
	pager *annotate.Pager

	// Rendered content lines (cached from plan.Render).
	lines []string

	// Viewport state.
	cursor    int
	scrollTop int
	viewportW int
	viewportH int

	// Anchors mapping line → item ID.
	anchors []annotate.Anchor

	// Revision at which the pager opened (for stale detection).
	revision int

	// Host callbacks.
	OnSubmitAnnotations func(text string)
	OnApprovePlan       func()
	OnClose             func()
	OnCommentRequest    func(title, current string, onSubmit func(string))
	OnConfirm           func(question string, onResult func(yes bool))
	RequestRender       func()
}

// NewPlanPager creates a PlanPager for the given store.
func NewPlanPager(store *plan.Store) *PlanPager {
	p := &PlanPager{
		Store: store,
		pager: annotate.NewPager(),
	}
	p.refreshContent()
	return p
}

// refreshContent re-renders the plan content and updates anchors.
func (p *PlanPager) refreshContent() {
	snap, err := p.Store.Snapshot()
	if err != nil {
		return
	}
	md, renderAnchors := plan.Render(snap)
	p.lines = strings.Split(md, "\n")
	p.revision = snap.Revision

	// Convert render anchors to generic annotate anchors (0-based).
	p.anchors = make([]annotate.Anchor, 0, len(renderAnchors))
	// Note: renderAnchors.Line is 1-based; convert to 0-based.
	for _, a := range renderAnchors {
		lineIdx := a.Line - 1
		if lineIdx >= 0 && lineIdx < len(p.lines) {
			p.anchors = append(p.anchors, annotate.Anchor{Line: lineIdx, ID: a.ItemID})
		}
	}
	p.pager.Content = p.lines
	p.pager.Anchors = p.anchors
}

// SetViewport tells the pager the available terminal dimensions.
func (p *PlanPager) SetViewport(width, height int) {
	p.viewportW = width
	p.viewportH = height
}

func (p *PlanPager) visibleHeight() int {
	if p.viewportH > 1 {
		return p.viewportH - 1
	}
	return 200
}

// Render implements Component.
func (p *PlanPager) Render(width int) []string {
	// Refresh content from store (may have changed via store mutations).
	p.refreshContent()

	p.cursor, p.scrollTop = annotate.EnsureScrollInBounds(p.cursor, p.scrollTop, len(p.lines), p.visibleHeight())

	var out []string

	// Title/header line.
	snap, _ := p.Store.Snapshot()
	var title string
	if snap != nil {
		openComments := 0
		for _, c := range snap.Comments {
			if !c.Resolved {
				openComments++
			}
		}
		title = fmt.Sprintf("Plan: %s (rev %d)  comments:%d", snap.Name, snap.Revision, openComments)
	} else {
		title = "Plan"
	}
	out = append(out, ansi.Bold+truncate(title, width)+ansi.BoldReset)

	height := p.visibleHeight()
	if height < 3 {
		height = 3
	}

	end := p.scrollTop + height
	if end > len(p.lines) {
		end = len(p.lines)
	}

	for i := p.scrollTop; i < end; i++ {
		line := p.lines[i]
		prefix := p.linePrefix(i)
		hasComment := p.lineHasComment(i)

		// Check for item anchor marker in the line.
		displayLine := line
		anchor := annotate.AnchorAtLine(p.anchors, i)
		if anchor != nil {
			// Remove the anchor comment from display.
			displayLine = stripAnchorComment(displayLine)
		}

		text := displayLine
		if hasComment {
			text = ansi.Bg("#1e4273") + text + ansi.Reset
		}

		out = append(out, prefix+text)
	}

	// Footer.
	footer := p.renderFooter(width)
	out = append(out, footer)

	for len(out) < height+2 {
		out = append(out, "")
	}
	return out
}

// linePrefix returns the prefix for a rendered line.
func (p *PlanPager) linePrefix(i int) string {
	if i == p.cursor {
		return ansi.Bg("#1e4273") + "> " + ansi.Reset
	}
	return "  "
}

// lineHasComment checks whether the line has comments.
func (p *PlanPager) lineHasComment(i int) bool {
	anchor := annotate.AnchorAtLine(p.anchors, i)
	if anchor == nil {
		return false
	}
	snap, err := p.Store.Snapshot()
	if err != nil {
		return false
	}
	for _, c := range snap.Comments {
		if c.ItemID == anchor.ID && !c.Resolved {
			return true
		}
	}
	return false
}

// renderFooter returns the footer line with key hints.
func (p *PlanPager) renderFooter(width int) string {
	snap, _ := p.Store.Snapshot()
	openComments := 0
	if snap != nil {
		for _, c := range snap.Comments {
			if !c.Resolved {
				openComments++
			}
		}
	}

	left := fmt.Sprintf("rev %d  %d open comment(s)", p.revision, openComments)
	right := "n/p:item  c:comment  s:submit  a:approve  q:close"

	padding := width - ansi.Width(left) - ansi.Width(right)
	if padding < 1 {
		padding = 1
	}

	return ansi.Fg("#8b949e") + left + strings.Repeat(" ", padding) + right + ansi.FgReset
}

// HandleInput implements Component.
func (p *PlanPager) HandleInput(data string) {
	if p.handleNavigation(data) {
		return
	}
	switch data {
	case "c":
		p.requestAddComment()
	case "e":
		p.requestEditComment()
	case "d":
		p.requestDeleteComment()
	case "s":
		p.requestSubmit()
	case "a":
		p.requestApprove()
	case "q", "esc", "ctrl+c":
		p.close()
	}
}

func (p *PlanPager) handleNavigation(data string) bool {
	delta := map[string]int{"up": -1, "k": -1, "down": 1, "j": 1, "pgup": -p.visibleHeight(), "pgdn": p.visibleHeight()}
	if step, ok := delta[data]; ok {
		p.cursor, p.scrollTop = annotate.MoveCursor(p.cursor, p.scrollTop, step, len(p.lines), p.visibleHeight())
		p.requestRender()
		return true
	}
	if data == "n" {
		p.jumpToNextItem(1)
		return true
	}
	if data == "p" {
		p.jumpToNextItem(-1)
		return true
	}
	return false
}

// jumpToNextItem moves the cursor to the next/previous item heading.
func (p *PlanPager) jumpToNextItem(dir int) {
	if len(p.anchors) == 0 {
		return
	}
	best := p.nextAnchorLine(dir)
	if best < 0 {
		return
	}
	p.cursor = best
	p.cursor, p.scrollTop = annotate.EnsureScrollInBounds(p.cursor, p.scrollTop, len(p.lines), p.visibleHeight())
	p.requestRender()
}

func (p *PlanPager) nextAnchorLine(dir int) int {
	if dir > 0 {
		for _, anchor := range p.anchors {
			if anchor.Line > p.cursor {
				return anchor.Line
			}
		}
		return p.anchors[0].Line
	}
	for i := len(p.anchors) - 1; i >= 0; i-- {
		if p.anchors[i].Line < p.cursor {
			return p.anchors[i].Line
		}
	}
	return p.anchors[len(p.anchors)-1].Line
}

// currentAnchor returns the anchor at the cursor, or nil.
func (p *PlanPager) currentAnchor() *annotate.Anchor {
	return annotate.AnchorAtLine(p.anchors, p.cursor)
}

func (p *PlanPager) requestAddComment() {
	anchor := p.currentAnchor()
	itemID := ""
	if anchor != nil {
		itemID = anchor.ID
	}

	if p.OnCommentRequest == nil {
		return
	}

	prompt := "Add comment:"
	if itemID != "" {
		prompt = fmt.Sprintf("Add comment on %s:", itemID)
	}

	p.OnCommentRequest(prompt, "", func(text string) {
		if text == "" {
			return
		}
		p.Store.AddComment(itemID, text)
		p.requestRender()
	})
}

func (p *PlanPager) requestEditComment() {
	anchor := p.currentAnchor()
	itemID := ""
	if anchor != nil {
		itemID = anchor.ID
	}

	// Find the first open comment for this item.
	snap, _ := p.Store.Snapshot()
	if snap == nil {
		return
	}

	var targetComment plan.PlanComment
	found := false
	for _, c := range snap.Comments {
		if c.ItemID == itemID && !c.Resolved {
			targetComment = c
			found = true
			break
		}
	}
	if !found {
		return
	}

	if p.OnCommentRequest == nil {
		return
	}

	p.OnCommentRequest("Edit comment:", targetComment.Content, func(text string) {
		if text == "" {
			return
		}
		p.Store.UpdateComment(targetComment.ID, text)
		p.requestRender()
	})
}

func (p *PlanPager) requestDeleteComment() {
	anchor := p.currentAnchor()
	itemID := ""
	if anchor != nil {
		itemID = anchor.ID
	}

	snap, _ := p.Store.Snapshot()
	if snap == nil {
		return
	}

	var targetComment plan.PlanComment
	found := false
	for _, c := range snap.Comments {
		if c.ItemID == itemID && !c.Resolved {
			targetComment = c
			found = true
			break
		}
	}
	if !found {
		return
	}

	if p.OnConfirm == nil {
		return
	}

	p.OnConfirm("Delete this comment?", func(yes bool) {
		if !yes {
			return
		}
		p.Store.RemoveComment(targetComment.ID)
		p.requestRender()
	})
}

func (p *PlanPager) requestSubmit() {
	if p.OnConfirm == nil {
		return
	}
	p.OnConfirm("Submit annotations to planner?", func(yes bool) {
		if !yes {
			return
		}
		p.submitAnnotations()
	})
}

func (p *PlanPager) submitAnnotations() {
	snap, err := p.Store.Snapshot()
	if err != nil {
		return
	}
	summary := plan.AnnotationsSummary(snap)
	if p.OnSubmitAnnotations != nil {
		p.OnSubmitAnnotations(summary)
	}
}

func (p *PlanPager) requestApprove() {
	// Check for open comments.
	snap, _ := p.Store.Snapshot()
	openComments := 0
	if snap != nil {
		for _, c := range snap.Comments {
			if !c.Resolved {
				openComments++
			}
		}
	}

	confirmMsg := "Approve plan?"
	if openComments > 0 {
		confirmMsg = fmt.Sprintf("Approve plan with %d open comment(s)?", openComments)
	}

	if p.OnConfirm == nil {
		return
	}
	p.OnConfirm(confirmMsg, func(yes bool) {
		if !yes {
			return
		}
		if p.OnApprovePlan != nil {
			p.OnApprovePlan()
		}
	})
}

func (p *PlanPager) close() {
	if p.OnClose != nil {
		p.OnClose()
	}
}

func (p *PlanPager) requestRender() {
	if p.RequestRender != nil {
		p.RequestRender()
	}
}

// Invalidate implements Component.
func (p *PlanPager) Invalidate() {}

// stripAnchorComment removes the "<!-- anchor: item-N -->" trailing comment from a line.
func stripAnchorComment(line string) string {
	idx := strings.LastIndex(line, "<!-- anchor: ")
	if idx < 0 {
		return line
	}
	// Trim trailing whitespace before the comment.
	before := strings.TrimRight(line[:idx], " ")
	return before
}
