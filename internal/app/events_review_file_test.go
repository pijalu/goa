// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/review"
	"github.com/pijalu/goa/tui"
)

// newFileReviewHarness builds an App with a live TUI engine and an input
// editor over a throwaway project directory — exactly the host surface
// showFileReviewPager touches. Tests stay single-goroutine (no RunLoops), so
// Apply/ApplySync run inline.
func newFileReviewHarness(t *testing.T) (*App, *tui.TUI) {
	t.Helper()
	subs := testSubsystems()
	subs.projectDir = t.TempDir()
	app := New(subs)

	term := &testTerminal{w: 80, h: 24}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start failed: %v", err)
	}
	t.Cleanup(engine.Stop)

	app.subs.tuiEngine = engine
	app.subs.inputEditor = tui.NewEditor()
	return app, engine
}

// newFileReviewPager creates <projectDir>/main.go and a pager over it with
// three source lines to navigate and comment on.
func newFileReviewPager(t *testing.T, projectDir string) *tui.FileReviewPager {
	t.Helper()
	mainGo := filepath.Join(projectDir, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	session := &review.Session{
		ID:         "abc12345",
		ProjectDir: projectDir,
		Kind:       review.KindFile,
		FilePath:   "main.go",
	}
	content, err := review.LoadReviewFile(projectDir, "main.go")
	if err != nil {
		t.Fatalf("LoadReviewFile: %v", err)
	}
	return tui.NewFileReviewPager(session, content)
}

// dispatchShowFileReviewPager routes the event through the real dispatcher so
// both the events_control.go case and the handler are exercised.
func dispatchShowFileReviewPager(app *App, pager any) {
	app.handleChatEvent(event.ChatEvent{
		ShowFileReviewPager: &event.ShowFileReviewPager{Pager: pager},
	})
}

// TestShowFileReviewPager_EventWiring verifies the event wires every pager
// callback, shows the overlay, sets the file-review help title, and routes
// comment entry through the main input line into the session.
func TestShowFileReviewPager_EventWiring(t *testing.T) {
	app, engine := newFileReviewHarness(t)
	pager := newFileReviewPager(t, app.subs.projectDir)

	dispatchShowFileReviewPager(app, pager)

	if vt := engine.VisibleText(); !strings.Contains(vt, "Review file main.go") {
		t.Fatalf("overlay not visible after event; screen:\n%s", vt)
	}
	if got := app.subs.inputEditor.Title(); got != fileReviewHelpTitle {
		t.Errorf("input title = %q, want %q", got, fileReviewHelpTitle)
	}
	if pager.RequestRender == nil || pager.OnClose == nil ||
		pager.OnCommentRequest == nil || pager.OnConfirm == nil || pager.OnExportReview == nil {
		t.Fatal("expected the event to wire all pager callbacks")
	}

	// 'c' opens comment entry on the main input line; submitting the text
	// anchors the comment at line 1 and restores the help title.
	pager.HandleInput("c")
	if !app.handlePendingMainInput("check this line") {
		t.Fatal("expected pending main-input request after pressing 'c'")
	}
	comments := pager.Session.CommentsFor("main.go", 1, review.SideNew)
	if len(comments) != 1 || comments[0].Content != "check this line" {
		t.Errorf("comments at line 1 = %+v, want one %q", comments, "check this line")
	}
	if got := app.subs.inputEditor.Title(); got != fileReviewHelpTitle {
		t.Errorf("input title after comment = %q, want restored %q", got, fileReviewHelpTitle)
	}
}

// TestShowFileReviewPager_SubmitCloses verifies 's' asks for confirmation on
// the main input line, hands the Markdown summary to OnSubmitReview, and
// closes the overlay (hide + title reset).
func TestShowFileReviewPager_SubmitCloses(t *testing.T) {
	app, engine := newFileReviewHarness(t)
	pager := newFileReviewPager(t, app.subs.projectDir)

	var submitted string
	pager.OnSubmitReview = func(text string) { submitted = text }

	dispatchShowFileReviewPager(app, pager)

	pager.HandleInput("s")
	if !app.handlePendingMainInput("y") {
		t.Fatal("expected pending confirmation after pressing 's'")
	}

	if !strings.Contains(submitted, "# File Review") {
		t.Errorf("submitted summary missing header, got: %s", submitted)
	}
	if vt := engine.VisibleText(); strings.Contains(vt, "Review file main.go") {
		t.Errorf("overlay still visible after submit:\n%s", vt)
	}
	if got := app.subs.inputEditor.Title(); got != "" {
		t.Errorf("input title after submit = %q, want empty", got)
	}
}

// TestShowFileReviewPager_Export verifies 'x' writes the file-variant export
// (review_file_*.md) without closing the pager, and surfaces the result on
// the input separator.
func TestShowFileReviewPager_Export(t *testing.T) {
	app, engine := newFileReviewHarness(t)
	pager := newFileReviewPager(t, app.subs.projectDir)

	dispatchShowFileReviewPager(app, pager)

	pager.HandleInput("x")

	// Note: ExportPath sanitizes the base name ('.' → '-'), hence main-go.
	matches, err := filepath.Glob(filepath.Join(app.subs.projectDir, "review_file_*.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("exported files = %v, want exactly one", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), "# File Review") {
		t.Errorf("export content missing summary header, got: %s", data)
	}
	want := "Exported review to " + filepath.Base(matches[0])
	if got := app.subs.inputEditor.Title(); got != want {
		t.Errorf("input title = %q, want %q", got, want)
	}
	// Export is non-destructive: the pager stays open.
	if vt := engine.VisibleText(); !strings.Contains(vt, "Review file main.go") {
		t.Errorf("overlay hidden after export; screen:\n%s", vt)
	}
}

// TestShowFileReviewPager_CloseHides verifies 'q' hides the overlay and
// resets the input title.
func TestShowFileReviewPager_CloseHides(t *testing.T) {
	app, engine := newFileReviewHarness(t)
	pager := newFileReviewPager(t, app.subs.projectDir)

	dispatchShowFileReviewPager(app, pager)

	pager.HandleInput("q")

	if vt := engine.VisibleText(); strings.Contains(vt, "Review file main.go") {
		t.Errorf("overlay still visible after close:\n%s", vt)
	}
	if got := app.subs.inputEditor.Title(); got != "" {
		t.Errorf("input title after close = %q, want empty", got)
	}
}

// TestShowFileReviewPager_Guards verifies malformed payloads are ignored:
// wrong concrete type, nil Pager, and an absent payload must not open an
// overlay or panic.
func TestShowFileReviewPager_Guards(t *testing.T) {
	app, engine := newFileReviewHarness(t)

	dispatchShowFileReviewPager(app, "not a pager")
	dispatchShowFileReviewPager(app, nil)
	app.handleChatEvent(event.ChatEvent{})

	if vt := engine.VisibleText(); strings.Contains(vt, "Review file") {
		t.Errorf("overlay opened for malformed payload:\n%s", vt)
	}
	if got := app.subs.inputEditor.Title(); got != "" {
		t.Errorf("input title changed by malformed payload: %q", got)
	}
}
