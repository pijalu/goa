// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tui"
)

// TestReviewCommand_OverlayWiring verifies that the /review command wires the
// review overlay callbacks correctly and that closing the pager invokes OnClose.
func TestReviewCommand_OverlayWiring(t *testing.T) {
	subs := testSubsystems()
	app := New(subs)

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	app.subs.tuiEngine = engine
	app.subs.chat = chat
	app.subs.inputEditor = inp

	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	session := &review.Session{ID: "abc12345", BaseRef: "HEAD^1", ProjectDir: subs.projectDir}
	pager := tui.NewReviewPager(session, diff)
	pager.RecentCommits = nil

	var handle *tui.OverlayHandle
	geo := reviewOverlayGeometryFor(24)
	pager.SetViewport(80, geo.height)
	app.wireReviewPagerCallbacks(pager, &handle, geo)

	closed := false
	pager.OnClose = func() {
		closed = true
		if handle != nil && handle.Hide != nil {
			handle.Hide()
		}
	}

	handle = engine.ShowOverlay(pager, tui.OverlayOptions{Width: 0, Height: geo.height, BottomOffset: geo.bottomOffset, CaptureInput: true})
	engine.RenderNow()

	pager.HandleInput("q")
	engine.RenderNow()

	if !closed {
		t.Error("expected OnClose called after pressing 'q'")
	}
}

// TestReviewCommand_SubmitWiring verifies that confirming submission invokes
// OnSubmitReview with a Markdown summary.
func TestReviewCommand_SubmitWiring(t *testing.T) {
	subs := testSubsystems()
	app := New(subs)

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	chat := tui.NewChatViewport()
	inp := tui.NewEditor()
	app.subs.tuiEngine = engine
	app.subs.chat = chat
	app.subs.inputEditor = inp

	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	session := &review.Session{ID: "abc12345", BaseRef: "HEAD^1", ProjectDir: subs.projectDir}
	pager := tui.NewReviewPager(session, diff)
	pager.RecentCommits = nil

	var submitted string
	pager.OnSubmitReview = func(text string) {
		submitted = text
	}

	var handle *tui.OverlayHandle
	geo := reviewOverlayGeometryFor(24)
	pager.SetViewport(80, geo.height)
	app.wireReviewPagerCallbacks(pager, &handle, geo)
	handle = engine.ShowOverlay(pager, tui.OverlayOptions{Width: 0, Height: geo.height, BottomOffset: geo.bottomOffset, CaptureInput: true})
	engine.RenderNow()

	pager.HandleInput("s")
	// 's' routes through OnConfirm -> main input line. Simulate the user
	// confirming by typing "y" on the main input line.
	if !app.handlePendingMainInput("y") {
		t.Fatal("expected pending main-input request after pressing 's'")
	}
	engine.RenderNow()

	if submitted == "" {
		t.Fatal("expected review submitted")
	}
	if !strings.Contains(submitted, "# Code Review") {
		t.Errorf("expected Markdown summary, got: %s", submitted)
	}
}
