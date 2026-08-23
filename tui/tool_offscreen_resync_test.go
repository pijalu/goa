// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// scrollbackWipes counts the scrollback wipe sequences (\x1b[3J) across the
// terminal's writes — each one proves a full reset re-emitted the transcript.
func scrollbackWipes(writes []string) int {
	n := 0
	for _, w := range writes {
		n += strings.Count(w, "\x1b[3J")
	}
	return n
}

// TestCompositor_RequestScrollbackResync pins the compositor half of the
// off-screen tool fix (bugs.md §2): RequestScrollbackResync makes the NEXT
// frame take the full-reset path (one scrollback wipe + re-emit), exactly
// once — never a wipe per frame afterwards.
func TestCompositor_RequestScrollbackResync(t *testing.T) {
	term := &fakeTerminal{w: 30, h: 10}
	comp := NewCompositor(term)
	scene := func(n int) *Scene {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("row-%d", i)
		}
		return &Scene{
			TerminalW: 30, TerminalH: 10,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 30, H: n}, Content: lines}},
		}
	}

	comp.Render(scene(40)) // first frame (InitialClear wipe not counted: separate write)
	comp.Render(scene(40)) // steady diff — no wipe expected
	if got := scrollbackWipes(term.Writes()); got != 0 {
		t.Fatalf("steady frames must not wipe scrollback, got %d wipes", got)
	}

	comp.RequestScrollbackResync()
	comp.Render(scene(40)) // settled (MutationGen unchanged) → one full reset
	wipes := scrollbackWipes(term.Writes())
	if wipes != 1 {
		t.Fatalf("resync request must trigger exactly one scrollback wipe, got %d", wipes)
	}

	comp.Render(scene(40)) // flag cleared by the reset — no further wipes
	if got := scrollbackWipes(term.Writes()); got != 1 {
		t.Fatalf("resync must fire once per request, got %d wipes after follow-up frame", got)
	}
}

// TestChatViewport_OffscreenResyncBoundaries pins the viewport half: a tool
// widget whose rows are committed to scrollback requests the one-time resync
// when it changes while off-screen — once for the running episode, once more
// (re-armed) at completion — and never for on-screen widgets.
func TestChatViewport_OffscreenResyncBoundaries(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	resyncs := 0
	chat.SetScrollbackResyncRequest(func() { resyncs++ })

	for i := 0; i < 6; i++ {
		chat.AddSystemMessage(fmt.Sprintf("baseline-%d", i))
	}
	engine.RenderNow()

	tc := chat.AddToolExecution("bash", `{"command":"go test ./..."}`)
	tc.SetArgsComplete()
	tc.SetStatus(ToolRunning)
	engine.RenderNow()

	// Push the widget fully off-screen with later content.
	for i := 0; i < 12; i++ {
		chat.AddSystemMessage(fmt.Sprintf("below-%d", i))
	}
	engine.RenderNow()

	if resyncs != 0 {
		t.Fatalf("on-screen→off-screen scroll alone must not resync (no widget state change), got %d", resyncs)
	}
	if !chat.IsScrolledOff(tc) {
		t.Fatal("fixture broken: tool widget must be fully scrolled off here")
	}

	// Running boundary: streamed progress while off-screen → ONE resync.
	tc.SetOutput("progress line 1")
	if resyncs != 1 {
		t.Fatalf("off-screen running update must request one resync, got %d", resyncs)
	}
	tc.SetOutput("progress line 2")
	tc.SetOutput("progress line 3")
	if resyncs != 1 {
		t.Fatalf("resync must be once per episode (no per-tick storm), got %d", resyncs)
	}

	// Completion boundary: re-armed — the final status must rewrite the
	// stale running rows with the true final duration.
	tc.startTime = time.Now().Add(-5 * time.Second) // deterministic "Took 5.0s"
	tc.SetStatus(ToolSuccess)
	if resyncs != 2 {
		t.Fatalf("terminal transition while off-screen must resync again, got %d", resyncs)
	}
	tc.SetStatus(ToolError) // terminal→terminal: no new episode
	if resyncs != 2 {
		t.Fatalf("terminal→terminal must not resync again, got %d", resyncs)
	}
}

// TestChatViewport_OnscreenToolNeverResyncs is the negative control: a widget
// still inside the repaintable window never asks for a scrollback resync.
func TestChatViewport_OnscreenToolNeverResyncs(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	chat := NewChatViewport()
	engine.AddChild(chat)
	engine.AddChild(NewEditor())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	resyncs := 0
	chat.SetScrollbackResyncRequest(func() { resyncs++ })

	tc := chat.AddToolExecution("bash", `{"command":"echo hi"}`)
	tc.SetArgsComplete()
	tc.SetStatus(ToolRunning)
	engine.RenderNow()
	if chat.IsScrolledOff(tc) {
		t.Fatal("fixture broken: widget must be on-screen")
	}

	tc.SetOutput("hi")
	tc.startTime = time.Now().Add(-2 * time.Second)
	tc.SetStatus(ToolSuccess)
	engine.RenderNow()
	if resyncs != 0 {
		t.Fatalf("on-screen widget must never request a scrollback resync, got %d", resyncs)
	}
}

// TestOffscreenToolCompletionRewritesScrollback is the end-to-end pin of the
// reported bug: a tool that completes after its widget scrolled into terminal
// scrollback must leave the TRUE final duration ("Took …") recoverable from
// scrollback — not the frozen intermediate "elapsed" it had when it left the
// screen.
func TestOffscreenToolCompletionRewritesScrollback(t *testing.T) {
	term := &fakeTerminal{w: 100, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	engine.AddChild(chat)
	engine.AddChild(NewEditor())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	for i := 0; i < 5; i++ {
		chat.AddSystemMessage(fmt.Sprintf("context-%d", i))
	}
	engine.RenderNow()

	tc := chat.AddToolExecution("bash", `{"command":"go test ./..."}`)
	tc.SetArgsComplete()
	tc.startTime = time.Now().Add(-30 * time.Second) // long run: elapsed ~30s
	tc.SetStatus(ToolRunning)
	engine.RenderNow()

	for i := 0; i < 12; i++ { // widget leaves the screen mid-run
		chat.AddSystemMessage(fmt.Sprintf("streamed-%d", i))
	}
	engine.RenderNow()
	if !chat.IsScrolledOff(tc) {
		t.Fatal("fixture broken: running widget must be fully off-screen")
	}

	// Long run continues off-screen, then completes with the true duration.
	tc.startTime = time.Now().Add(-42 * time.Second)
	tc.SetOutput("ok  pkg 1.2s")
	tc.SetStatus(ToolSuccess)
	// Frame 1 takes the deferred path (the widget grew above the window); the
	// settled frame that follows performs the actual full reset — in production
	// the render loop supplies it continuously.
	engine.RenderNow()
	engine.RenderNow()

	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.Writes() {
		emu.Process(w)
	}
	history := strings.Join(emu.Scrollback(), "\n") + "\n" + dumpScreen(emu, term.h)
	if !strings.Contains(history, "Took 42.0s") {
		t.Errorf("final scrollback must carry the true final duration, got:\n%s", history)
	}
	if !strings.Contains(history, "go test ./...") {
		t.Errorf("completed tool block must remain recoverable from scrollback:\n%s", history)
	}
}
