// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/review"
)

// TestReviewPager_MainInputAddComment verifies that pressing 'c' invokes
// OnCommentRequest with the correct title, and that the supplied callback
// saves the comment.
func TestReviewPager_MainInputAddComment(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, diff)

	var capturedTitle string
	var capturedCurrent string
	var submitCb func(string)
	pager.OnCommentRequest = func(title, current string, onSubmit func(string)) {
		capturedTitle = title
		capturedCurrent = current
		submitCb = onSubmit
	}

	pager.HandleInput("c")
	if capturedTitle == "" {
		t.Fatal("expected OnCommentRequest title")
	}
	if !strings.Contains(capturedTitle, "main.go") {
		t.Errorf("expected file in title, got %q", capturedTitle)
	}
	if capturedCurrent != "" {
		t.Errorf("add comment current = %q, want empty", capturedCurrent)
	}
	if submitCb == nil {
		t.Fatal("expected submit callback")
	}

	submitCb("nice change")
	if len(s.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(s.Comments))
	}
	if s.Comments[0].Content != "nice change" {
		t.Errorf("comment content = %q, want %q", s.Comments[0].Content, "nice change")
	}
}

// TestReviewPager_MainInputEditComment verifies that pressing 'e' invokes
// OnCommentRequest with the current content pre-filled.
func TestReviewPager_MainInputEditComment(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	s.AddComment("main.go", 2, "original")
	pager := NewReviewPager(s, diff)

	pager.HandleInput("down")

	var capturedCurrent string
	var submitCb func(string)
	pager.OnCommentRequest = func(title, current string, onSubmit func(string)) {
		capturedCurrent = current
		submitCb = onSubmit
	}

	pager.HandleInput("e")
	if capturedCurrent != "original" {
		t.Errorf("edit current = %q, want %q", capturedCurrent, "original")
	}
	if submitCb == nil {
		t.Fatal("expected submit callback")
	}

	submitCb("updated")
	if len(s.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(s.Comments))
	}
	if s.Comments[0].Content != "updated" {
		t.Errorf("comment content = %q, want %q", s.Comments[0].Content, "updated")
	}
}

// TestReviewPager_ConfirmCancel verifies that declining a delete confirm
// (onResult(false)) leaves the comment in place.
func TestReviewPager_ConfirmCancel(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	s.AddComment("main.go", 2, "to delete")
	pager := NewReviewPager(s, diff)

	var captured string
	var resultCb func(yes bool)
	pager.OnConfirm = func(question string, onResult func(yes bool)) {
		captured = question
		resultCb = onResult
	}

	pager.HandleInput("down")
	pager.HandleInput("d")
	if captured == "" {
		t.Fatal("expected OnConfirm for delete")
	}
	if resultCb == nil {
		t.Fatal("expected result callback")
	}

	// Decline (user typed n)
	resultCb(false)
	if len(s.Comments) != 1 {
		t.Errorf("expected comment to remain after 'no', got %d", len(s.Comments))
	}
}

// TestReviewPager_ConfirmDelete verifies that accepting a delete confirm
// (onResult(true)) removes the comment.
func TestReviewPager_ConfirmDelete(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	s.AddComment("main.go", 2, "to delete")
	pager := NewReviewPager(s, diff)

	var resultCb func(yes bool)
	pager.OnConfirm = func(question string, onResult func(yes bool)) {
		resultCb = onResult
	}

	pager.HandleInput("down")
	pager.HandleInput("d")
	resultCb(true)
	if len(s.Comments) != 0 {
		t.Errorf("expected comment deleted, got %d", len(s.Comments))
	}
}

// TestReviewPager_SubmitConfirm verifies that 's' routes through OnConfirm
// and only submits when confirmed.
func TestReviewPager_SubmitConfirm(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, diff)

	var submitted string
	pager.OnSubmitReview = func(text string) { submitted = text }
	var resultCb func(yes bool)
	pager.OnConfirm = func(question string, onResult func(yes bool)) {
		resultCb = onResult
	}

	pager.HandleInput("s")
	if resultCb == nil {
		t.Fatal("expected OnConfirm after pressing 's'")
	}

	// Decline -> not submitted
	resultCb(false)
	if submitted != "" {
		t.Errorf("unexpected submit for decline: %q", submitted)
	}

	// Confirm -> submitted
	pager.HandleInput("s")
	resultCb(true)
	if submitted == "" {
		t.Error("expected review submitted after confirm")
	}
}

// TestReviewPager_NoCallbackNoCrash verifies the pager does not panic when
// the host has not wired OnConfirm (it should simply no-op).
func TestReviewPager_NoCallbackNoCrash(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	s.AddComment("main.go", 2, "x")
	pager := NewReviewPager(s, diff)
	pager.HandleInput("down")
	pager.HandleInput("d") // no OnConfirm wired
	pager.HandleInput("s") // no OnConfirm wired
	if len(s.Comments) != 1 {
		t.Errorf("expected comment untouched, got %d", len(s.Comments))
	}
}
