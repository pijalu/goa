// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"

	"github.com/pijalu/goa/internal/review"
)

// reviewActions is the comment-lifecycle behavior shared by ReviewPager and
// FileReviewPager (D6): request add/edit/delete at an anchor, and
// confirm-guarded submit. Both pagers embed the same UX by delegating here,
// so the flows cannot drift apart.
//
// All text entry — comments AND yes/no confirmations — stays on the host's
// main input line via callbacks; this type performs no rendering and owns no
// input state.
type reviewActions struct {
	session          *review.Session
	onCommentRequest func(title, current string, onSubmit func(string))
	onConfirm        func(question string, onResult func(yes bool))
	onCommentSaved   func()
	onSubmitReview   func(text string)
	onClose          func()
}

// AddCommentAt asks for new comment text at anchor. Empty submitted text is a
// no-op, matching the diff pager's behavior.
func (a *reviewActions) AddCommentAt(anchor review.LineAnchor) {
	if a.onCommentRequest == nil || !validAnchor(anchor) {
		return
	}
	prompt := fmt.Sprintf("Add comment on %s:", anchorLabel(anchor))
	a.onCommentRequest(prompt, "", func(text string) {
		if text == "" {
			return
		}
		a.session.AddComment(anchor.File, anchor.LineNum, anchor.Side, text)
		a.saved()
	})
}

// EditCommentAt edits the first existing comment at anchor.
func (a *reviewActions) EditCommentAt(anchor review.LineAnchor) {
	if a.onCommentRequest == nil || !validAnchor(anchor) {
		return
	}
	comments := a.session.CommentsFor(anchor.File, anchor.LineNum, anchor.Side)
	if len(comments) == 0 {
		return
	}
	c := comments[0]
	prompt := fmt.Sprintf("Edit comment on %s:", anchorLabel(anchor))
	a.onCommentRequest(prompt, c.Content, func(text string) {
		if text == "" {
			return
		}
		a.session.UpdateComment(c.ID, text)
		a.saved()
	})
}

// DeleteCommentAt confirms and removes the first comment at anchor.
func (a *reviewActions) DeleteCommentAt(anchor review.LineAnchor) {
	if a.onConfirm == nil || !validAnchor(anchor) {
		return
	}
	comments := a.session.CommentsFor(anchor.File, anchor.LineNum, anchor.Side)
	if len(comments) == 0 {
		return
	}
	c := comments[0]
	question := fmt.Sprintf("Delete comment on %s?", anchorLabel(anchor))
	a.onConfirm(question, func(yes bool) {
		if !yes {
			return
		}
		a.session.RemoveComment(c.ID)
		a.saved()
	})
}

// SubmitWithConfirm asks for confirmation, then hands Session.MarkdownSummary()
// to onSubmitReview (the single source of truth shared with export) and closes.
func (a *reviewActions) SubmitWithConfirm() {
	if a.onConfirm == nil {
		return
	}
	a.onConfirm("Submit review to agent?", func(yes bool) {
		if !yes {
			return
		}
		if a.onSubmitReview != nil {
			a.onSubmitReview(a.session.MarkdownSummary())
		}
		if a.onClose != nil {
			a.onClose()
		}
	})
}

// saved reports that session content changed so the host can persist it.
func (a *reviewActions) saved() {
	if a.onCommentSaved != nil {
		a.onCommentSaved()
	}
}

// validAnchor mirrors the diff pager's original guard: anchors resolved from
// non-anchored lines (hunk headers, file metadata) carry LineNum <= 0.
func validAnchor(a review.LineAnchor) bool {
	return a.LineNum > 0
}
