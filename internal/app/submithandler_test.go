// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
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

func (c *testInternalCommand2) Name() string      { return "internalcmd" }
func (c *testInternalCommand2) Aliases() []string { return nil }
func (c *testInternalCommand2) ShortHelp() string { return "internal command" }
func (c *testInternalCommand2) LongHelp() string  { return "internal command" }
func (c *testInternalCommand2) Run(ctx core.Context, args []string) error { return nil }
func (c *testInternalCommand2) IsInternal() bool                            { return true }

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
// bugs.md "Session: slow commands need an executing placeholder": the status
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

func TestExtractImagePaths(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "text with image path",
			text: "describe this file /tmp/screenshot.png please",
			want: []string{"/tmp/screenshot.png"},
		},
		{
			name: "multiple image paths",
			text: "/tmp/a.png /tmp/b.jpg /tmp/c.webp",
			want: []string{"/tmp/a.png", "/tmp/b.jpg", "/tmp/c.webp"},
		},
		{
			name: "no images",
			text: "just some regular text",
			want: nil,
		},
		{
			name: "case insensitive",
			text: "/tmp/photo.JPEG",
			want: []string{"/tmp/photo.JPEG"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractImagePaths(tc.text)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("extractImagePaths(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestStripImagePaths(t *testing.T) {
	text := "describe this file /tmp/screenshot.png please"
	got := stripImagePaths(text)
	want := "describe this file please"
	if got != want {
		t.Errorf("stripImagePaths(%q) = %q, want %q", text, got, want)
	}
}

func TestStripImagePaths_PreservesNewlines(t *testing.T) {
	text := "line one\nline two /tmp/img.png\nline three"
	got := stripImagePaths(text)
	want := "line one\nline two\nline three"
	if got != want {
		t.Errorf("stripImagePaths(%q) = %q, want %q", text, got, want)
	}
}

func TestSplitUserInput(t *testing.T) {
	text := "compare /tmp/a.png and /tmp/b.png"
	msg, images := splitUserInput(text)
	if msg != "compare and" {
		t.Errorf("message = %q, want %q", msg, "compare and")
	}
	want := []string{"/tmp/a.png", "/tmp/b.png"}
	if !reflect.DeepEqual(images, want) {
		t.Errorf("images = %v, want %v", images, want)
	}
}

func TestSplitUserInput_PreservesNewlines(t *testing.T) {
	text := "first line\nsecond line /tmp/a.png\nthird line"
	msg, images := splitUserInput(text)
	want := "first line\nsecond line\nthird line"
	if msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	wantImages := []string{"/tmp/a.png"}
	if !reflect.DeepEqual(images, wantImages) {
		t.Errorf("images = %v, want %v", images, wantImages)
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
