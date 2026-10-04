// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// The VirtualTerminal must satisfy the same interface the TUI engine renders
// to — that is the whole premise of the web UI.
func TestVirtualTerminal_ImplementsTerminal(t *testing.T) {
	var _ tui.Terminal = NewVirtualTerminal(80, 24)
}

// A browser-reachable session must never claim the process terminal: no raw
// mode, no screen ownership, no writes to stdout.
func TestVirtualTerminal_NoRawModeClaim(t *testing.T) {
	if tui.OwnsScreen() {
		t.Fatal("precondition: something already owns the screen")
	}
	vt := NewVirtualTerminal(80, 24)
	restore, err := vt.SetRaw()
	if err != nil {
		t.Fatalf("SetRaw: %v", err)
	}
	if restore == nil {
		t.Fatal("SetRaw returned a nil restore func")
	}
	restore()
	if tui.OwnsScreen() {
		t.Error("virtual terminal claimed screen ownership")
	}
}

// Input delivered by a transport must reach the engine's input callback, and
// a panicking handler must not take the transport down.
func TestVirtualTerminal_InputReachesEngine(t *testing.T) {
	vt := NewVirtualTerminal(80, 24)
	var got []string
	vt.Start(func(s string) { got = append(got, s) }, func() {})

	vt.Input("ls -la\r")
	vt.Input("")
	vt.Input("\x03")

	if len(got) != 2 || got[0] != "ls -la\r" || got[1] != "\x03" {
		t.Fatalf("input not delivered verbatim: %q", got)
	}
}

func TestVirtualTerminal_InputSurvivesPanickingHandler(t *testing.T) {
	vt := NewVirtualTerminal(80, 24)
	vt.Start(func(string) { panic("handler exploded") }, nil)
	vt.Input("x") // must not propagate
}

// The screen the engine writes must come back out as cells, and a second,
// unchanged write must not publish a frame.
func TestVirtualTerminal_WriteUpdatesGrid(t *testing.T) {
	vt := NewVirtualTerminal(20, 3)
	rec := &recordSink{}
	vt.SetSink(rec)

	vt.WriteString("\x1b[1;1Hgoa\x1b[0m")
	if len(rec.frames) != 1 {
		t.Fatalf("want 1 frame, got %d", len(rec.frames))
	}
	f := rec.frames[0]
	if len(f.Patches) != 1 {
		t.Fatalf("want 1 patched row, got %d", len(f.Patches))
	}
	if got := RunsText(f.Patches[0].Runs); !strings.HasPrefix(got, "goa") {
		t.Errorf("row text = %q", got)
	}

	vt.WriteString("\x1b[0m") // no cell change
	if len(rec.frames) != 1 {
		t.Errorf("unchanged write published a frame: %d total", len(rec.frames))
	}
}

// The compositor signals the hardware cursor ONLY through DECTCEM bytes, so the
// cursor the frames carry must follow them. Before this was modelled the caret
// was permanently hidden — the "missing cursor in the input" symptom.
func TestVirtualTerminal_CursorVisibilityFollowsDECTCEM(t *testing.T) {
	vt := NewVirtualTerminal(20, 3)
	rec := &recordSink{}
	vt.SetSink(rec)

	// A real terminal starts with the cursor shown; the engine then hides it at
	// startup (tui.Start) before rendering.
	vt.HideCursor()
	vt.WriteString("input>")
	if got := rec.frames[len(rec.frames)-1].Cursor.Visible; got {
		t.Fatalf("cursor visible after HideCursor: %v", got)
	}

	// A render that wants the cursor emits \x1b[?25h in the same buffer.
	vt.WriteString("\x1b[?25h")
	if got := rec.frames[len(rec.frames)-1].Cursor.Visible; !got {
		t.Error("cursor still hidden after \\x1b[?25h")
	}

	// Hiding it again takes effect even when no cell moved.
	vt.WriteString("\x1b[?25l")
	if got := rec.frames[len(rec.frames)-1].Cursor.Visible; got {
		t.Error("cursor still visible after \\x1b[?25l")
	}
}

func TestVirtualTerminal_ResizeFiresCallbackAndFullFrame(t *testing.T) {
	vt := NewVirtualTerminal(20, 3)
	rec := &recordSink{}
	vt.SetSink(rec)
	resized := 0
	vt.Start(nil, func() { resized++ })

	vt.Resize(40, 10)
	if resized != 1 {
		t.Fatalf("resize callback fired %d times, want 1", resized)
	}
	w, h := vt.Size()
	if w != 40 || h != 10 {
		t.Errorf("size = %dx%d, want 40x10", w, h)
	}
	if len(rec.frames) != 1 || !rec.frames[0].Full {
		t.Errorf("resize did not publish a full frame: %+v", rec.frames)
	}

	vt.Resize(40, 10) // no change → no repaint, no callback
	if resized != 1 || len(rec.frames) != 1 {
		t.Errorf("idempotent resize published work: cb=%d frames=%d", resized, len(rec.frames))
	}
}

func TestVirtualTerminal_CursorVisibilityAndTitle(t *testing.T) {
	vt := NewVirtualTerminal(20, 3)
	rec := &recordSink{}
	vt.SetSink(rec)

	if !vt.Grid().Cursor().Visible {
		t.Error("caret should start visible")
	}
	vt.HideCursor()
	if vt.Grid().Cursor().Visible {
		t.Error("HideCursor did not hide the caret")
	}
	vt.ShowCursor()
	if !vt.Grid().Cursor().Visible {
		t.Error("ShowCursor did not show the caret")
	}
	vt.SetTitle("goa — work")
	if got := rec.lastTitle(); got != "goa — work" {
		t.Errorf("title = %q", got)
	}
}

func TestVirtualTerminal_ClearScreen(t *testing.T) {
	vt := NewVirtualTerminal(10, 2)
	rec := &recordSink{}
	vt.SetSink(rec)
	vt.WriteString("\x1b[1;1Hhello")
	vt.ClearScreen()
	if got := strings.TrimSpace(vt.Grid().Text()); got != "\n" && strings.TrimSpace(got) != "" {
		t.Errorf("screen not blank after ClearScreen: %q", got)
	}
}

type recordSink struct {
	frames []*Frame
	titles []string
}

func (r *recordSink) Publish(f *Frame) {
	r.frames = append(r.frames, f)
	if f.Title != "" {
		r.titles = append(r.titles, f.Title)
	}
}

// HasClients always reports true: a recorder stands in for an attached browser,
// and a test that installs one wants every frame built.
func (r *recordSink) HasClients() bool { return true }

func (r *recordSink) lastTitle() string {
	if len(r.titles) == 0 {
		return ""
	}
	return r.titles[len(r.titles)-1]
}
