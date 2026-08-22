// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/review"
)

// newTestActions builds a reviewActions wired to captured callbacks.
func newTestActions(t *testing.T) (*reviewActions, *review.Session, *actionsCapture) {
	t.Helper()
	s := &review.Session{ID: "abc12345", ProjectDir: t.TempDir()}
	cap := &actionsCapture{}
	a := &reviewActions{
		session:          s,
		onCommentRequest: cap.commentRequest,
		onConfirm:        cap.confirm,
		onCommentSaved:   func() { cap.saved++ },
		onSubmitReview:   func(text string) { cap.submitted = text },
		onClose:          func() { cap.closed = true },
	}
	return a, s, cap
}

// actionsCapture records callback invocations for assertions.
type actionsCapture struct {
	commentTitle    string
	commentCurrent  string
	commentSubmit   func(string)
	confirmQuestion string
	confirmResult   func(bool)
	submitted       string
	saved           int
	closed          bool
}

func (c *actionsCapture) commentRequest(title, current string, onSubmit func(string)) {
	c.commentTitle = title
	c.commentCurrent = current
	c.commentSubmit = onSubmit
}

func (c *actionsCapture) confirm(question string, onResult func(bool)) {
	c.confirmQuestion = question
	c.confirmResult = onResult
}

func TestReviewActions_AddCommentAt(t *testing.T) {
	a, s, cap := newTestActions(t)
	anchor := review.LineAnchor{File: "main.go", LineNum: 7, Side: review.SideNew}

	a.AddCommentAt(anchor)
	if cap.commentTitle == "" || !strings.Contains(cap.commentTitle, "main.go:7") {
		t.Fatalf("expected add prompt naming the anchor, got %q", cap.commentTitle)
	}
	if cap.commentCurrent != "" {
		t.Errorf("add must not prefill text, got %q", cap.commentCurrent)
	}

	cap.commentSubmit("looks wrong") // empty text would be a no-op
	if len(s.Comments) != 1 || s.Comments[0].Content != "looks wrong" {
		t.Fatalf("expected one stored comment, got %+v", s.Comments)
	}
	if cap.saved != 1 {
		t.Errorf("expected OnCommentSaved after save, got %d", cap.saved)
	}
}

func TestReviewActions_AddComment_EmptyTextNoOp(t *testing.T) {
	a, s, cap := newTestActions(t)
	a.AddCommentAt(review.LineAnchor{File: "f.go", LineNum: 1})
	cap.commentSubmit("")
	if len(s.Comments) != 0 {
		t.Errorf("empty comment text must be dropped, got %+v", s.Comments)
	}
	if cap.saved != 0 {
		t.Errorf("no save expected for dropped comment")
	}
}

func TestReviewActions_InvalidAnchorNoOp(t *testing.T) {
	a, _, cap := newTestActions(t)
	// LineNum <= 0 mirrors hunk-header anchors in the diff pager.
	a.AddCommentAt(review.LineAnchor{File: "f.go", LineNum: 0})
	a.EditCommentAt(review.LineAnchor{File: "f.go", LineNum: -1})
	a.DeleteCommentAt(review.LineAnchor{})
	if cap.commentTitle != "" || cap.confirmQuestion != "" {
		t.Error("invalid anchors must not trigger any callback")
	}
}

func TestReviewActions_EditCommentAt(t *testing.T) {
	a, s, cap := newTestActions(t)
	s.AddComment("main.go", 3, review.SideNew, "first draft")

	a.EditCommentAt(review.LineAnchor{File: "main.go", LineNum: 3, Side: review.SideNew})
	if !strings.Contains(cap.commentTitle, "main.go:3") {
		t.Fatalf("expected edit prompt, got %q", cap.commentTitle)
	}
	if cap.commentCurrent != "first draft" {
		t.Errorf("edit must prefill existing content, got %q", cap.commentCurrent)
	}
	cap.commentSubmit("polished")
	if len(s.Comments) != 1 || s.Comments[0].Content != "polished" {
		t.Fatalf("expected updated comment, got %+v", s.Comments)
	}
	if cap.saved != 1 {
		t.Errorf("expected save notification, got %d", cap.saved)
	}
}

func TestReviewActions_EditComment_NoCommentsNoRequest(t *testing.T) {
	a, _, cap := newTestActions(t)
	a.EditCommentAt(review.LineAnchor{File: "f.go", LineNum: 2})
	if cap.commentTitle != "" {
		t.Error("edit with no comment at anchor must be a no-op")
	}
}

func TestReviewActions_DeleteCommentAt(t *testing.T) {
	a, s, cap := newTestActions(t)
	s.AddComment("main.go", 4, review.SideNew, "remove me")

	a.DeleteCommentAt(review.LineAnchor{File: "main.go", LineNum: 4, Side: review.SideNew})
	if !strings.Contains(cap.confirmQuestion, "Delete comment") {
		t.Fatalf("expected delete confirmation, got %q", cap.confirmQuestion)
	}
	cap.confirmResult(false)
	if len(s.Comments) != 1 {
		t.Fatal("declined confirmation must keep the comment")
	}
	cap.confirmResult(true)
	if len(s.Comments) != 0 {
		t.Fatalf("confirmed delete must remove the comment, got %+v", s.Comments)
	}
	if cap.saved != 1 {
		t.Errorf("expected exactly one save notification, got %d", cap.saved)
	}
}

func TestReviewActions_SubmitWithConfirm(t *testing.T) {
	a, s, cap := newTestActions(t)

	a.SubmitWithConfirm()
	if cap.confirmQuestion != "Submit review to agent?" {
		t.Fatalf("unexpected confirm question %q", cap.confirmQuestion)
	}
	cap.confirmResult(true)
	if !strings.Contains(cap.submitted, "Code Review") {
		// Diff-kind session summary header; asserts the real summary flows through.
		t.Errorf("expected session markdown summary, got %q", cap.submitted)
	}
	if !cap.closed {
		t.Error("submit must close the pager")
	}
	_ = s
}

func TestReviewActions_Submit_DeclinedClosesNothing(t *testing.T) {
	a, _, cap := newTestActions(t)
	a.SubmitWithConfirm()
	cap.confirmResult(false)
	if cap.submitted != "" || cap.closed {
		t.Error("declined submit must neither send nor close")
	}
}

func TestReviewActions_NilCallbacksSafe(t *testing.T) {
	s := &review.Session{ID: "x"}
	a := &reviewActions{session: s} // every callback nil
	a.AddCommentAt(review.LineAnchor{File: "f", LineNum: 1})
	a.EditCommentAt(review.LineAnchor{File: "f", LineNum: 1})
	a.DeleteCommentAt(review.LineAnchor{File: "f", LineNum: 1})
	a.SubmitWithConfirm()
}
