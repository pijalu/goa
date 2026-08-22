// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/review"
)

// newFilePagerFixture writes a source file, loads it, and builds the pager.
type filePagerFixture struct {
	pager   *FileReviewPager
	session *review.Session
	content *review.FileReviewContent
}

func newFilePagerFixture(t *testing.T, name, body string) filePagerFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	session, err := review.NewFileSession(dir, path)
	if err != nil {
		t.Fatalf("NewFileSession: %v", err)
	}
	content, err := review.LoadReviewFile(dir, path)
	if err != nil {
		t.Fatalf("LoadReviewFile: %v", err)
	}
	return filePagerFixture{
		pager:   NewFileReviewPager(session, content),
		session: session,
		content: content,
	}
}

func TestFileReviewPager_TitleAndGutter(t *testing.T) {
	f := newFilePagerFixture(t, "sample.go", "package main\n\nfunc main() {}\n")
	f.pager.SetViewport(80, 24)
	lines := f.pager.Render(80)
	if len(lines) != 24 {
		t.Fatalf("expected 24 rows for a 24-row viewport, got %d", len(lines))
	}
	title := stripANSI(lines[0])
	for _, want := range []string{"Review file ", "sample.go", "3 lines", "comments:0"} {
		if !strings.Contains(title, want) {
			t.Errorf("title %q missing %q", title, want)
		}
	}
	body := stripANSI(strings.Join(lines, "\n"))
	// Cursor starts on line 1; gutter digits follow the two-column prefix.
	if !strings.Contains(body, "> 1 package main") {
		t.Errorf("expected cursor row with line number 1, got:\n%s", body)
	}
	if !strings.Contains(body, "  3 func main() {}") {
		t.Errorf("line 3 row missing, got:\n%s", body)
	}
}

func TestFileReviewPager_GutterRightAligned(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "row %d\n", i)
	}
	f := newFilePagerFixture(t, "many.txt", b.String())
	f.pager.SetViewport(80, 24)
	lines := f.pager.Render(80)

	// Largest line number (12) is two digits wide, so line 1 is padded to
	// " 1": prefix "> " + gutter " 1".
	if got := stripANSI(lines[1]); got != ">  1 row 0" {
		t.Errorf("expected right-aligned gutter '>  1 row 0', got %q", got)
	}
	// Line 12 fills the gutter without padding.
	if got := stripANSI(lines[12]); got != "  12 row 11" {
		t.Errorf("expected full-width gutter '  12 row 11', got %q", got)
	}
}

func TestFileReviewPager_TitleTruncatedFlag(t *testing.T) {
	f := newFilePagerFixture(t, "big.txt", strings.Repeat("x\n", 25))
	f.content.Truncated = true // simulate the loader cap being hit
	title := stripANSI(f.pager.Render(120)[0])
	if !strings.Contains(title, "(truncated)") {
		t.Errorf("truncated content must be flagged in the title, got %q", title)
	}
}

// TestFileReviewPager_CommentAdd covers the add flow: 'c' prompts with a
// file:line title, submitting anchors the comment to the cursor line and
// notifies OnCommentSaved once.
func TestFileReviewPager_CommentAdd(t *testing.T) {
	f := newFilePagerFixture(t, "code.go", "one\ntwo\nthree\n")

	var title string
	var submit func(string)
	f.pager.OnCommentRequest = func(t2, _ string, onSubmit func(string)) {
		title, submit = t2, onSubmit
	}
	var saves int
	f.pager.OnCommentSaved = func() { saves++ }

	// Cursor starts at line 1; move to line 2.
	f.pager.HandleInput("down")
	f.pager.HandleInput("c")
	if !strings.Contains(title, "code.go") || !strings.Contains(title, ":2:") {
		t.Fatalf("add prompt must name file:line, got %q", title)
	}
	submit("what is this?")
	if len(f.session.Comments) != 1 || f.session.Comments[0].LineNum != 2 {
		t.Fatalf("expected comment anchored to line 2, got %+v", f.session.Comments)
	}
	if saves != 1 {
		t.Fatalf("expected save notification after add")
	}
}

// TestFileReviewPager_CommentEditPrefill covers the edit flow: 'e' must
// prefill the prompt with the existing comment text.
func TestFileReviewPager_CommentEditPrefill(t *testing.T) {
	f := newFilePagerFixture(t, "code.go", "one\ntwo\nthree\n")

	var current string
	var submit func(string)
	f.pager.OnCommentRequest = func(_, cur string, onSubmit func(string)) {
		current, submit = cur, onSubmit
	}
	f.session.AddComment(f.content.Path, 1, review.SideNew, "what is this?")

	f.pager.HandleInput("e")
	if current != "what is this?" {
		t.Fatalf("edit must prefill existing comment, got %q", current)
	}
	submit("edited")
	if f.session.Comments[0].Content != "edited" {
		t.Fatalf("edit not applied: %+v", f.session.Comments)
	}
}

// TestFileReviewPager_CommentBadgeAndDelete covers the commented-row render
// (badge exactly once on the row) and the 'd' delete flow gated behind an
// OnConfirm confirmation.
func TestFileReviewPager_CommentBadgeAndDelete(t *testing.T) {
	f := newFilePagerFixture(t, "code.go", "one\ntwo\nthree\n")

	var saves int
	f.pager.OnCommentSaved = func() { saves++ }
	f.pager.OnCommentRequest = func(_ string, _ string, onSubmit func(string)) {
		onSubmit("note")
	}
	f.pager.HandleInput("c") // one comment anchored to line 1

	// Render shows the badge and pipe on the commented row only.
	rendered := stripANSI(strings.Join(f.pager.Render(80), "\n"))
	if !strings.Contains(rendered, "[1 comment(s)]") {
		t.Fatalf("comment badge missing:\n%s", rendered)
	}
	if got := strings.Count(rendered, "[1 comment(s)]"); got != 1 {
		t.Fatalf("badge must appear once, got %d:\n%s", got, rendered)
	}

	// Delete asks for confirmation.
	var confirmQ string
	var confirmCb func(bool)
	f.pager.OnConfirm = func(q string, onResult func(bool)) { confirmQ, confirmCb = q, onResult }
	f.pager.HandleInput("d")
	if !strings.Contains(confirmQ, "Delete comment") {
		t.Fatalf("expected delete confirmation, got %q", confirmQ)
	}
	confirmCb(true)
	// Two notifications total: one when the comment was added, one when the
	// delete persisted.
	if len(f.session.Comments) != 0 || saves != 2 {
		t.Fatalf("delete failed: comments=%+v saves=%d", f.session.Comments, saves)
	}
}

func TestFileReviewPager_SubmitAndClose(t *testing.T) {
	f := newFilePagerFixture(t, "doc.md", "# Title\nbody\n")
	f.session.AddComment(f.content.Path, 2, review.SideNew, "note")

	var submitted string
	var closed bool
	f.pager.OnSubmitReview = func(text string) { submitted = text }
	f.pager.OnClose = func() { closed = true }
	var confirmCb func(bool)
	f.pager.OnConfirm = func(_ string, onResult func(bool)) { confirmCb = onResult }

	f.pager.HandleInput("s")
	if confirmCb == nil {
		t.Fatal("'s' must request confirmation")
	}
	confirmCb(true)
	if submitted == "" {
		t.Fatal("submit callback not invoked")
	}
	if !closed {
		t.Error("submit must close the pager")
	}
	stripped := stripANSI(submitted)
	for _, want := range []string{"# File Review", "doc.md:2", "note"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("summary missing %q:\n%s", want, stripped)
		}
	}
}

func TestFileReviewPager_ExportHandlerInvoked(t *testing.T) {
	f := newFilePagerFixture(t, "x.txt", "a\nb\n")
	exports := 0
	f.pager.OnExportReview = func() { exports++ }
	closes := 0
	f.pager.OnClose = func() { closes++ }
	f.pager.HandleInput("x")
	f.pager.HandleInput("x")
	if exports != 2 || closes != 0 {
		t.Errorf("export must fire without closing, exports=%d closes=%d", exports, closes)
	}
}

func TestFileReviewPager_NavigationClamping(t *testing.T) {
	body := ""
	for i := 0; i < 50; i++ {
		body += fmt.Sprintf("line %d\n", i)
	}
	f := newFilePagerFixture(t, "many.txt", body)
	f.pager.SetViewport(80, 10)

	for i := 0; i < 100; i++ {
		f.pager.HandleInput("down")
	}
	if f.pager.cursor != len(f.content.Lines)-1 {
		t.Errorf("cursor must clamp to last line, got %d", f.pager.cursor)
	}
	for i := 0; i < 200; i++ {
		f.pager.HandleInput("up")
	}
	if f.pager.cursor != 0 {
		t.Errorf("cursor must clamp to first line, got %d", f.pager.cursor)
	}
	// PgDn moves a full viewport.
	f.pager.HandleInput("pgdn")
	if f.pager.cursor != 9 {
		t.Errorf("pgdn should move 9 rows (viewport-1), got %d", f.pager.cursor)
	}
	// Renders stay viewport-sized while scrolled.
	lines := f.pager.Render(80)
	if len(lines) != 10 {
		t.Errorf("expected 10 rendered rows, got %d", len(lines))
	}
}

func TestFileReviewPager_EmptyFileHint(t *testing.T) {
	f := newFilePagerFixture(t, "empty.txt", "")
	lines := f.pager.Render(80)
	body := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(body, "File is empty.") {
		t.Errorf("empty file must render a hint row, got:\n%s", body)
	}
	// Commenting on an empty file is a no-op.
	requested := false
	f.pager.OnCommentRequest = func(string, string, func(string)) { requested = true }
	f.pager.HandleInput("c")
	if requested {
		t.Error("must not offer commenting on an empty file")
	}
}

func TestFileReviewPager_MarkdownHighlightedAsSource(t *testing.T) {
	f := newFilePagerFixture(t, "readme.md", "# Hello\nplain **bold** text\n")
	f.pager.SetViewport(80, 24)
	lines := f.pager.Render(80)
	headingRow := lines[1]
	if !strings.Contains(headingRow, ansi.Bold) || !strings.Contains(headingRow, ansi.Fg(mdHeadingColor)) {
		t.Errorf("markdown heading should be styled in the pager, got %q", headingRow)
	}
	if got := stripANSI(headingRow); !strings.Contains(got, "# Hello") {
		t.Errorf("source must stay 1:1, got %q", got)
	}
	boldRow := lines[2]
	if !strings.Contains(boldRow, ansi.Bold) {
		t.Errorf("inline bold should survive rendering, got %q", boldRow)
	}
}

func TestFileReviewPager_CodeFileHighlighted(t *testing.T) {
	f := newFilePagerFixture(t, "main.go", "package main\nfunc run() {}\n")
	f.pager.SetViewport(80, 24)
	lines := f.pager.Render(80)
	// The go highlighter colors keywords with truecolor sequences; the gutter
	// and plain prefix carry none, so any 38;2 escape on the row comes from
	// tools.HighlightLine (D2).
	if !strings.Contains(lines[1], "\x1b[38;2;") || !strings.Contains(lines[2], "\x1b[38;2;") {
		t.Errorf("expected go syntax colors, got:\n%q\n%q", lines[1], lines[2])
	}
}

func TestFileReviewPager_NoBaseSwitchKey(t *testing.T) {
	f := newFilePagerFixture(t, "a.txt", "a\n")
	// 'b' must do nothing: no base selector callbacks exist to trip.
	f.pager.HandleInput("b")
}

func TestFileReviewPager_CloseKeys(t *testing.T) {
	f := newFilePagerFixture(t, "a.txt", "a\n")
	closes := 0
	f.pager.OnClose = func() { closes++ }
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		f.pager.HandleInput(key)
	}
	if closes != 3 {
		t.Errorf("all close keys must close, got %d", closes)
	}
}

func TestFileReviewPager_RequestRenderOnScroll(t *testing.T) {
	f := newFilePagerFixture(t, "a.txt", "a\nb\nc\n")
	renders := 0
	f.pager.RequestRender = func() { renders++ }
	f.pager.HandleInput("down")
	if renders != 1 {
		t.Errorf("scroll must request a redraw, got %d", renders)
	}
}

func TestFileReviewPager_InvalidateNoop(t *testing.T) {
	f := newFilePagerFixture(t, "a.txt", "a\n")
	f.pager.Invalidate()
}
