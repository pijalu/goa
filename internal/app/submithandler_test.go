// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tui"
)

type testRecordableCommand struct{}

func (c *testRecordableCommand) Name() string      { return "testcmd" }
func (c *testRecordableCommand) Aliases() []string { return nil }
func (c *testRecordableCommand) ShortHelp() string { return "test command" }
func (c *testRecordableCommand) LongHelp() string  { return "test command" }
func (c *testRecordableCommand) Run(ctx core.Context, args []string) error {
	return nil
}

type testInternalCommand2 struct{}

func (c *testInternalCommand2) Name() string                              { return "internalcmd" }
func (c *testInternalCommand2) Aliases() []string                         { return nil }
func (c *testInternalCommand2) ShortHelp() string                         { return "internal command" }
func (c *testInternalCommand2) LongHelp() string                          { return "internal command" }
func (c *testInternalCommand2) Run(ctx core.Context, args []string) error { return nil }
func (c *testInternalCommand2) IsInternal() bool                          { return true }

// testPlaceholderCommand observes the status placeholder while Run executes.
type testPlaceholderCommand struct {
	status       *tui.StatusMsg
	visibleInRun bool
	textInRun    string
}

func (c *testPlaceholderCommand) Name() string      { return "slowcmd" }
func (c *testPlaceholderCommand) Aliases() []string { return nil }
func (c *testPlaceholderCommand) ShortHelp() string { return "slow command" }
func (c *testPlaceholderCommand) LongHelp() string  { return "slow command" }
func (c *testPlaceholderCommand) Run(ctx core.Context, args []string) error {
	c.visibleInRun = c.status.IsVisible()
	c.textInRun = c.status.Text()
	return nil
}

// TestHandleSlashCommand_ShowsExecutingPlaceholder is the regression test for
// Session: slow commands need an executing placeholder: the status
// line must show "executing /cmd ..." while the command runs and be cleared
// once the result is delivered.
func TestHandleSlashCommand_ShowsExecutingPlaceholder(t *testing.T) {
	registry := core.NewCommandRegistry()
	status := tui.NewStatusMsg()
	cmd := &testPlaceholderCommand{status: status}
	if err := registry.Register(cmd); err != nil {
		t.Fatal(err)
	}

	a := &App{
		subs: &subsystems{
			cfg:       &config.Config{},
			chat:      tui.NewChatViewport(),
			cmdRouter: core.NewCommandRouter(registry, core.NewDocEngine(registry)),
			footer:    tui.NewFooter(),
			statusMsg: status,
			tuiEngine: tui.NewTUI(&testTerminal{w: 80, h: 24}),
		},
	}

	a.handleSlashCommand("/slowcmd")

	if !cmd.visibleInRun {
		t.Fatal("expected status placeholder to be visible while the command runs")
	}
	if cmd.textInRun != "executing /slowcmd ..." {
		t.Fatalf("unexpected placeholder text: %q", cmd.textInRun)
	}
	if status.IsVisible() {
		t.Fatalf("expected placeholder cleared after execution, still shows %q", status.Text())
	}
}

func TestHandleSlashCommand_RecordsNonInternalCommandInSessionStore(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	store.StartSession()

	registry := core.NewCommandRegistry()
	if err := registry.Register(&testRecordableCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&testInternalCommand2{}); err != nil {
		t.Fatal(err)
	}

	chat := tui.NewChatViewport()
	a := &App{
		subs: &subsystems{
			cfg:          &config.Config{},
			chat:         chat,
			cmdRouter:    core.NewCommandRouter(registry, core.NewDocEngine(registry)),
			sessionStore: store,
			footer:       tui.NewFooter(),
			tuiEngine:    tui.NewTUI(&testTerminal{w: 80, h: 24}),
		},
	}

	a.handleSlashCommand("/testcmd")

	store.Close()
	info, err := store.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(info) != 1 || info[0].EventCount != 1 {
		t.Fatalf("expected 1 session with 1 event, got %d sessions: %+v", len(info), info)
	}
}

func TestHandleSlashCommand_DoesNotRecordInternalCommandInSessionStore(t *testing.T) {
	dir := t.TempDir()
	store := core.NewSessionStore(dir)
	store.StartSession()

	registry := core.NewCommandRegistry()
	if err := registry.Register(&testInternalCommand2{}); err != nil {
		t.Fatal(err)
	}

	chat := tui.NewChatViewport()
	a := &App{
		subs: &subsystems{
			cfg:          &config.Config{},
			chat:         chat,
			cmdRouter:    core.NewCommandRouter(registry, core.NewDocEngine(registry)),
			sessionStore: store,
			footer:       tui.NewFooter(),
			tuiEngine:    tui.NewTUI(&testTerminal{w: 80, h: 24}),
		},
	}

	a.handleSlashCommand("/internalcmd")

	store.Close()
	info, err := store.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(info) == 1 && info[0].EventCount != 0 {
		t.Fatalf("expected internal command not to be recorded, got eventCount=%d", info[0].EventCount)
	}
}

// writePNG writes a real 1×1 PNG into dir and returns its path, so the
// attachment tests exercise the real sniffing rule rather than a stub.
func writePNG(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSplitUserInput_AttachesExistingImages(t *testing.T) {
	dir := t.TempDir()
	a := writePNG(t, dir, "a.png")
	b := writePNG(t, dir, "b.png")

	msg, images, unreadable := splitUserInput("compare " + a + " and " + b)

	if msg != "compare and" {
		t.Errorf("message = %q, want %q", msg, "compare and")
	}
	if !reflect.DeepEqual(images, []string{a, b}) {
		t.Errorf("images = %v, want %v", images, []string{a, b})
	}
	if len(unreadable) != 0 {
		t.Errorf("unreadable = %v, want none", unreadable)
	}
}

func TestSplitUserInput_PreservesNewlines(t *testing.T) {
	dir := t.TempDir()
	a := writePNG(t, dir, "a.png")

	msg, images, _ := splitUserInput("first line\nsecond line " + a + "\nthird line")

	want := "first line\nsecond line\nthird line"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	if !reflect.DeepEqual(images, []string{a}) {
		t.Errorf("images = %v, want [%s]", images, a)
	}
}

// TestSplitUserInput_KeepsProseWithMissingImage is the regression test for the
// prose-destroying heuristic: a file that does not exist is not an attachment,
// so the words stay in the message and the token is reported instead of silently
// dropped.
func TestSplitUserInput_KeepsProseWithMissingImage(t *testing.T) {
	msg, images, unreadable := splitUserInput("check the logo at /nope/missing.png please")

	if msg != "check the logo at /nope/missing.png please" {
		t.Errorf("message = %q, want the original prose", msg)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want none", images)
	}
	if !reflect.DeepEqual(unreadable, []string{"/nope/missing.png"}) {
		t.Errorf("unreadable = %v, want [/nope/missing.png]", unreadable)
	}
}

// TestSplitUserInput_KeepsURLs keeps a URL that ends in an image suffix out of
// the attachment set (it is not a local file).
func TestSplitUserInput_KeepsURLs(t *testing.T) {
	text := "see https://example.com/pic.jpg for context"
	msg, images, unreadable := splitUserInput(text)

	if msg != text {
		t.Errorf("message = %q, want %q", msg, text)
	}
	if len(images) != 0 || len(unreadable) != 0 {
		t.Errorf("images=%v unreadable=%v, want none", images, unreadable)
	}
}

// TestSplitUserInput_NonImageFileIsNotAttached guards the sniffing rule: bytes
// decide, not the extension.
func TestSplitUserInput_NonImageFileIsNotAttached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}

	msg, images, unreadable := splitUserInput("read " + path)

	if msg != "read "+path {
		t.Errorf("message = %q, want the path kept", msg)
	}
	if len(images) != 0 {
		t.Errorf("images = %v, want none", images)
	}
	if !reflect.DeepEqual(unreadable, []string{path}) {
		t.Errorf("unreadable = %v, want [%s]", unreadable, path)
	}
}

// TestSplitUserInput_DedupesRepeatedImage keeps a double mention from attaching
// (and paying for) the same image twice.
func TestSplitUserInput_DedupesRepeatedImage(t *testing.T) {
	dir := t.TempDir()
	a := writePNG(t, dir, "a.png")

	_, images, _ := splitUserInput(a + " " + a)

	if !reflect.DeepEqual(images, []string{a}) {
		t.Errorf("images = %v, want [%s]", images, a)
	}
}

func TestHandlePendingMainInput_AcceptsSlashPrefixedText(t *testing.T) {
	var received string
	a := &App{pendingInput: &inputRequest{
		prompt:   "objective",
		onSubmit: func(s string) { received = s },
	}}

	if !a.handlePendingMainInput("/src/main.go fix the bug") {
		t.Fatal("expected handlePendingMainInput to consume the input")
	}
	if received != "/src/main.go fix the bug" {
		t.Errorf("received = %q, want the slash-prefixed objective", received)
	}
	if a.pendingInput != nil {
		t.Error("pendingInput should be cleared after handling")
	}
}

func TestHandlePendingMainInput_NoPending(t *testing.T) {
	a := &App{}
	if a.handlePendingMainInput("anything") {
		t.Error("expected false when no pending request")
	}
}
