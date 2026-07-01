// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"

	"github.com/pijalu/goa/internal/review"
)

// TestReviewPager_SelectBaseCallback verifies that pressing 'b' invokes
// OnSelectBase with the recent commits and that selecting a SHA updates the
// session base and re-parses the diff.
func TestReviewPager_SelectBaseCallback(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1", ProjectDir: "/tmp"}
	pager := NewReviewPager(s, diff)
	pager.RecentCommits = []review.CommitInfo{
		{SHA: "1111111111111111111111111111111111111111", Subject: "first commit"},
		{SHA: "2222222222222222222222222222222222222222", Subject: "second commit"},
	}

	var captured []review.CommitInfo
	var selectCb func(string)
	pager.OnSelectBase = func(commits []review.CommitInfo, onSelect func(string)) {
		captured = commits
		selectCb = onSelect
	}

	pager.HandleInput("b")

	if len(captured) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(captured))
	}
	if selectCb == nil {
		t.Fatal("expected select callback")
	}

	// The callback should accept a SHA and update the base. Since we cannot
	// run git in this test, we just verify the callback is wired.
	selectCb("2222222222222222222222222222222222222222")
	if s.BaseRef != "2222222222222222222222222222222222222222" {
		t.Errorf("base ref = %q, want selected SHA", s.BaseRef)
	}
}

// TestReviewPager_SelectBaseNoCommits verifies that pressing 'b' does nothing
// when there are no recent commits.
func TestReviewPager_SelectBaseNoCommits(t *testing.T) {
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, "")
	pager.RecentCommits = nil

	called := false
	pager.OnSelectBase = func(commits []review.CommitInfo, onSelect func(string)) {
		called = true
	}

	pager.HandleInput("b")
	if called {
		t.Error("OnSelectBase should not be called with no commits")
	}
}
