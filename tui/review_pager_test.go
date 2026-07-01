// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/review"
)

func TestReviewPager_Render(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1", HeadRef: "def"}
	p := NewReviewPager(s, diff)
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	rendered := stripReviewANSi(strings.Join(lines, "\n"))
	if !strings.Contains(rendered, "func new") {
		t.Errorf("expected diff content in render, got:\n%s", rendered)
	}
}

func TestReviewPager_SetViewport(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)
	p.SetViewport(80, 24)
	lines := p.Render(80)
	if len(lines) != 24 {
		t.Errorf("expected 24 lines for 24-row viewport, got %d", len(lines))
	}
}

func TestReviewPager_FullScreenAfterScroll(t *testing.T) {
	var diff strings.Builder
	diff.WriteString("diff --git a/main.go b/main.go\n")
	diff.WriteString("--- a/main.go\n+++ b/main.go\n")
	diff.WriteString("@@ -1,52 +1,52 @@\n")
	for i := 0; i < 52; i++ {
		fmt.Fprintf(&diff, "-func fn%d() {}\n", i)
		fmt.Fprintf(&diff, "+func fn%d() {}\n", i)
	}
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff.String())
	p.SetViewport(80, 24)

	before := len(p.Render(80))
	if before != 24 {
		t.Fatalf("expected 24 initial lines, got %d", before)
	}
	for i := 0; i < 20; i++ {
		p.HandleInput("down")
	}
	after := len(p.Render(80))
	if after != 24 {
		t.Errorf("expected 24 lines after scroll, got %d", after)
	}
}

func TestReviewPager_MoveCursor(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 line1
-line2
+line2new
 line3
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)
	start := p.cursor
	p.HandleInput("down")
	if p.cursor == start {
		t.Error("expected cursor to move down")
	}
	p.HandleInput("up")
	if p.cursor != start {
		t.Error("expected cursor to return to start")
	}
}

func TestReviewPager_AddComment(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)

	var capturedTitle string
	var submitCb func(string)
	p.OnCommentRequest = func(title, current string, onSubmit func(string)) {
		capturedTitle = title
		submitCb = onSubmit
	}
	p.HandleInput("c")

	if capturedTitle == "" {
		t.Fatal("expected OnCommentRequest title")
	}
	if !strings.Contains(capturedTitle, "main.go") {
		t.Errorf("expected file in title, got %q", capturedTitle)
	}
	if submitCb == nil {
		t.Fatal("expected submit callback")
	}
	submitCb("nice change")

	if len(s.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(s.Comments))
	}
	if s.Comments[0].Content != "nice change" {
		t.Errorf("unexpected comment content: %q", s.Comments[0].Content)
	}
}

func TestReviewPager_AddComment_NotOnHeader(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)

	var requested bool
	p.OnCommentRequest = func(title, current string, onSubmit func(string)) {
		requested = true
	}

	// Cursor starts on the first content line. Move up to the hunk header.
	p.HandleInput("up")
	p.HandleInput("up")
	p.HandleInput("c")

	if requested {
		t.Error("comment request should not fire on a hunk header")
	}
}

func TestReviewPager_CommentIndicatorOnlyOnContentLines(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	s.AddComment("main.go", 2, "question")
	p := NewReviewPager(s, diff)

	rendered := stripReviewANSi(strings.Join(p.Render(80), "\n"))
	if !strings.Contains(rendered, "[1 comment(s)]") {
		t.Fatalf("expected comment indicator on content line, got:\n%s", rendered)
	}
	// The comment attaches to line 2, which appears as both a removed and an
	// added line, so the indicator is shown on both representations.
	if strings.Count(rendered, "[1 comment(s)]") != 2 {
		t.Errorf("comment indicator should appear on removed+added lines (2), got %d:\n%s",
			strings.Count(rendered, "[1 comment(s)]"), rendered)
	}
	// Hunk headers must not show the indicator.
	for _, ln := range p.Render(80) {
		stripped := stripReviewANSi(ln)
		if strings.HasPrefix(stripped, "  @@") && strings.Contains(stripped, "[1 comment(s)]") {
			t.Errorf("hunk header should not carry comment indicator: %q", stripped)
		}
	}
}

func TestReviewPager_SubmitReview(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1 +1 @@
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)

	var submitted string
	var closed bool
	p.OnSubmitReview = func(text string) {
		submitted = text
	}
	p.OnClose = func() {
		closed = true
	}
	var confirmCb func(yes bool)
	p.OnConfirm = func(q string, onResult func(yes bool)) { confirmCb = onResult }
	p.HandleInput("s")
	if confirmCb == nil {
		t.Fatal("expected OnConfirm after pressing 's'")
	}
	confirmCb(true)

	if submitted == "" {
		t.Error("expected review submitted")
	}
	if !closed {
		t.Error("expected pager closed")
	}
}

// TestReviewPager_CommentPipeAlignsWithCursor verifies that the green comment
// pipe occupies the cursor column on commented, non-selected lines, while the
// selected line keeps the '>' cursor and the diff text starts at the same
// column in all cases.
func TestReviewPager_CommentPipeAlignsWithCursor(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	s.AddComment("main.go", 2, "note")
	p := NewReviewPager(s, diff)
	p.SetViewport(80, 12)

	// Cursor starts on the first content line (package main). Move to the
	// commented line (func new).
	p.HandleInput("down")
	p.HandleInput("down")

	rendered := stripReviewANSi(strings.Join(p.Render(80), "\n"))
	// Selected commented line should use ">" and not the green pipe.
	if !strings.Contains(rendered, "> +func new()") {
		t.Errorf("selected line should use '>', got:\n%s", rendered)
	}
	if strings.Contains(rendered, "> │+func new()") {
		t.Errorf("selected line should not show pipe, got:\n%s", rendered)
	}

	// Move cursor away; the commented line should show the pipe in the cursor
	// column and the diff text should remain aligned.
	p.HandleInput("up")
	rendered = stripReviewANSi(strings.Join(p.Render(80), "\n"))
	if !strings.Contains(rendered, "│ +func new()") {
		t.Errorf("commented line should show pipe in cursor column, got:\n%s", rendered)
	}
	// A non-commented non-selected line should have two leading spaces.
	if !strings.Contains(rendered, "  package main") {
		t.Errorf("non-commented line should keep two-space prefix, got:\n%s", rendered)
	}
}

func TestReviewPager_Export(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1 +1 @@
-func old() {}
+func new() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1", ProjectDir: t.TempDir()}
	s.AddComment("main.go", 1, "needs a test")
	p := NewReviewPager(s, diff)

	var exported, submitted string
	var closed bool
	p.OnExportReview = func() {
		// Mirror the host wiring: write the same Markdown submit would send.
		path, err := s.ExportPath(s.ProjectDir)
		if err != nil {
			t.Fatalf("ExportPath: %v", err)
		}
		if err := s.Export(p.Diff, path); err != nil {
			t.Fatalf("Export: %v", err)
		}
		exported = path
	}
	p.OnSubmitReview = func(text string) { submitted = text }
	p.OnClose = func() { closed = true }

	p.HandleInput("x")

	if exported == "" {
		t.Fatal("expected OnExportReview to fire on 'x'")
	}
	// Export must NOT submit to the model or close the pager.
	if submitted != "" {
		t.Error("export must not submit the review to the agent")
	}
	if closed {
		t.Error("export must not close the pager")
	}

	// The file content must equal what submit sends to the model.
	body, err := os.ReadFile(exported)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if want := s.MarkdownSummary(p.Diff); string(body) != want {
		t.Errorf("export content differs from submit content\nwant:\n%s\ngot:\n%s", want, string(body))
	}
}

// TestReviewPager_CtrlCCloses verifies that Ctrl+C closes the review pager.
func TestReviewPager_CtrlCCloses(t *testing.T) {
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, "")
	closed := false
	p.OnClose = func() { closed = true }
	p.HandleInput("ctrl+c")
	if !closed {
		t.Error("expected Ctrl+C to close the pager")
	}
}

func TestReviewPager_TruncateANSI(t *testing.T) {
	// A string whose raw length exceeds the width but whose visible width
	// fits should not be truncated.
	s := ansi.Fg("#3fb950") + "short" + ansi.FgReset
	got := truncate(s, 10)
	if got != s {
		t.Errorf("expected %q, got %q", s, got)
	}

	// A string whose visible width exceeds the width should truncate with an
	// ellipsis while keeping the ANSI reset.
	long := ansi.Fg("#3fb950") + strings.Repeat("x", 20) + ansi.FgReset
	got = truncate(long, 10)
	if ansi.Width(got) != 10 {
		t.Errorf("expected visible width 10, got %d for %q", ansi.Width(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected ellipsis in truncated string, got %q", got)
	}
	if !strings.HasSuffix(got, ansi.Reset) {
		t.Errorf("expected reset sequence at end, got %q", got)
	}
}

// TestReviewPager_NoDuplicateLinesOnScroll verifies that scrolling through
// a diff never produces duplicated or leaked content lines. This catches
// off-by-one errors in visibleHeight/scrollTop calculations.
func TestReviewPager_NoDuplicateLinesOnScroll(t *testing.T) {
	diff := buildTestDiff(30)
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)
	p.SetViewport(80, 12)

	for step := 0; step < 80; step++ {
		lines := p.Render(80)
		checkNoRenderedDuplicates(t, step, lines)
		p.HandleInput("down")
	}
}

// TestReviewPager_NoDuplicateLinesOnScrollUp verifies scrolling up also
// does not produce duplicates.
func TestReviewPager_NoDuplicateLinesOnScrollUp(t *testing.T) {
	diff := buildTestDiff(30)
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)
	p.SetViewport(80, 12)

	for step := 0; step < 40; step++ {
		p.HandleInput("down")
	}
	for step := 0; step < 40; step++ {
		lines := p.Render(80)
		checkNoRenderedDuplicates(t, step, lines)
		p.HandleInput("up")
	}
}

// TestReviewPager_RenderAfterBufferGrowth simulates adding a chat message
// while the review overlay is open, then scrolling. This catches the
// stale-viewportTop bug where buffer growth causes positional corruption.
func TestReviewPager_RenderAfterBufferGrowth(t *testing.T) {
	diff := buildTestDiff(15)
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	p := NewReviewPager(s, diff)
	p.SetViewport(80, 12)

	// Render baseline
	p.Render(80)

	// Scroll a bit to get some cursor movement
	for i := 0; i < 5; i++ {
		p.HandleInput("down")
	}
	lines := p.Render(80)
	checkNoRenderedDuplicates(t, 0, lines)

	// Now scroll down one more and check
	p.HandleInput("down")
	lines = p.Render(80)
	checkNoRenderedDuplicates(t, 1, lines)

	// Scroll up and check
	p.HandleInput("up")
	lines = p.Render(80)
	checkNoRenderedDuplicates(t, 2, lines)
}

func TestReviewPager_OverlayQuit(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 12}
	engine := NewTUI(term)
	cv := NewChatViewport()
	cv.AddSystemMessage("chat line")
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

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
	overlayH := term.h - 5 // leave input(3) + footer(2) visible
	if overlayH < 5 {
		overlayH = term.h
	}
	pager.SetViewport(term.w, overlayH)
	closed := false
	handle := engine.ShowOverlay(pager, OverlayOptions{Width: 0, Height: overlayH, BottomOffset: 5, CaptureInput: true})
	pager.OnClose = func() {
		closed = true
		if handle != nil && handle.Hide != nil {
			handle.Hide()
		}
	}
	engine.RenderNow()

	if len(engine.overlayStack) != 1 {
		t.Fatalf("expected overlay on stack, got %d", len(engine.overlayStack))
	}

	term.onInput("q")
	// Wait for the asynchronous render triggered by closing the overlay to
	// complete before stopping the engine, avoiding a data race with Stop().
	engine.RenderNow()

	if !closed {
		t.Errorf("OnClose was not called after pressing 'q'")
	}
	if len(engine.overlayStack) != 0 {
		t.Errorf("overlay still on stack after pressing 'q': %d", len(engine.overlayStack))
	}
}

func TestReviewPager_RenderAfterScroll(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,20 +1,20 @@
 package main
-func old0() {}
+func new0() {}
-func old1() {}
+func new1() {}
-func old2() {}
+func new2() {}
-func old3() {}
+func new3() {}
-func old4() {}
+func new4() {}
-func old5() {}
+func new5() {}
-func old6() {}
+func new6() {}
-func old7() {}
+func new7() {}
-func old8() {}
+func new8() {}
-func old9() {}
+func new9() {}
`
	s := &review.Session{ID: "abc12345", BaseRef: "HEAD^1"}
	pager := NewReviewPager(s, diff)
	pager.SetViewport(80, 12)

	first := pager.Render(80)
	if len(first) != 12 {
		t.Fatalf("expected 12 lines, got %d", len(first))
	}
	if !strings.Contains(stripReviewANSi(first[0]), "Review") {
		t.Errorf("title missing: %q", first[0])
	}
	for i := 0; i < 5; i++ {
		pager.HandleInput("down")
	}
	second := pager.Render(80)
	if len(second) != 12 {
		t.Errorf("expected 12 lines after scroll, got %d", len(second))
	}

	rendered := stripReviewANSi(strings.Join(second, "\n"))
	if strings.Count(rendered, "Review abc12345") != 1 {
		t.Errorf("title should appear exactly once, got %d:\n%s",
			strings.Count(rendered, "Review abc12345"), rendered)
	}
}

// buildTestDiff creates a unified diff with count removed/added pairs.
func buildTestDiff(count int) string {
	var buf strings.Builder
	buf.WriteString("diff --git a/main.go b/main.go\n")
	buf.WriteString("--- a/main.go\n+++ b/main.go\n")
	fmt.Fprintf(&buf, "@@ -1,%d +1,%d @@\n", count, count)
	for i := 0; i < count; i++ {
		fmt.Fprintf(&buf, "-func old%d() {}\n", i)
		fmt.Fprintf(&buf, "+func new%d() {}\n", i)
	}
	return buf.String()
}

// checkNoRenderedDuplicates checks that the rendered lines contain no
// duplicate content (except blank lines which are expected padding).
func checkNoRenderedDuplicates(t *testing.T, step int, lines []string) {
	t.Helper()
	rendered := stripReviewANSi(strings.Join(lines, "\n"))
	if strings.Count(rendered, "Review abc12345") != 1 {
		t.Fatalf("step %d: title should appear once, got %d\n%s", step, strings.Count(rendered, "Review abc12345"), rendered)
	}
	for i := 1; i < len(lines); i++ {
		prev := stripReviewANSi(lines[i-1])
		cur := stripReviewANSi(lines[i])
		if prev == "" || cur == "" {
			continue
		}
		if prev == cur {
			t.Fatalf("step %d: consecutive duplicate line at %d/%d:\n%s", step, i-1, i, rendered)
		}
	}
	seen := make(map[string]int)
	for i, ln := range lines {
		strip := stripReviewANSi(ln)
		if strip == "" {
			continue
		}
		if prevIdx, ok := seen[strip]; ok {
			t.Fatalf("step %d: line %d duplicates line %d:\n%s\n\nFull output:\n%s", step, i, prevIdx, strip, rendered)
		}
		seen[strip] = i
	}
}

func stripReviewANSi(s string) string {
	return ansi.Strip(s)
}
