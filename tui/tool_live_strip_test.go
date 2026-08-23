// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/ansi"
)

// TestToolLiveStrip_RendersOnlyForOffscreenRunningTool covers the pinned live
// line (bugs.md §2): zero rows when every running tool is inside the
// repaintable window, one row with a fresh elapsed when a running tool is
// committed to scrollback, and the row disappears at completion.
func TestToolLiveStrip_RendersOnlyForOffscreenRunningTool(t *testing.T) {
	term := &fakeTerminal{w: 100, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	strip := NewToolLiveStrip(chat)
	engine.AddChild(chat)
	engine.AddChild(strip)
	engine.AddChild(NewEditor())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	for i := 0; i < 5; i++ {
		chat.AddSystemMessage("ctx")
	}
	engine.RenderNow()

	tc := chat.AddToolExecution("bash", `{"command":"go test ./..."}`)
	tc.SetArgsComplete()
	tc.SetStatus(ToolRunning)
	tc.startTime = time.Now().Add(-12 * time.Second) // visible "elapsed 12.xs"
	engine.RenderNow()

	// On-screen running tool: the strip must stay empty (chrome unchanged).
	if rows := strip.Render(100); len(rows) != 0 {
		t.Fatalf("strip must render 0 rows for an on-screen tool, got %d", len(rows))
	}

	// Push the widget into scrollback.
	for i := 0; i < 12; i++ {
		chat.AddSystemMessage("below")
	}
	engine.RenderNow()
	// The watermark publishes after the frame that scrolls the widget off;
	// the strip activates on the next frame (the live ticker supplies it in
	// production).
	engine.RenderNow()

	rows := strip.Render(100)
	if len(rows) != 1 {
		t.Fatalf("strip must render 1 row for an off-screen running tool, got %d", len(rows))
	}
	line := ansiStripForTest(rows[0])
	if !strings.Contains(line, "go test ./...") || !strings.Contains(line, "elapsed") {
		t.Errorf("strip line must carry the call identity and live elapsed, got %q", line)
	}

	// The elapsed is computed at call time: advancing the clock must change it.
	tc.startTime = time.Now().Add(-87 * time.Second)
	advanced := ansiStripForTest(strip.Render(100)[0])
	if !strings.Contains(advanced, "elapsed 87.") {
		t.Errorf("strip must tick (fresh elapsed), got %q", advanced)
	}

	// Completion: no running off-screen tool anymore — strip empties.
	tc.SetStatus(ToolSuccess)
	engine.RenderNow()
	engine.RenderNow()
	if rows := strip.Render(100); len(rows) != 0 {
		t.Fatalf("strip must empty after completion, got %d rows: %v", len(rows), rows)
	}
}

// TestToolLiveStrip_VisibleInChrome verifies end-to-end that while the tool
// runs off-screen the live status is VISIBLE in the pinned chrome band of the
// composed screen — the "status stays current without scrolling up" half of
// the bug.
func TestToolLiveStrip_VisibleInChrome(t *testing.T) {
	term := &fakeTerminal{w: 100, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	engine.AddChild(chat)
	engine.AddChild(NewToolLiveStrip(chat))
	engine.AddChild(NewEditor())
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	for i := 0; i < 5; i++ {
		chat.AddSystemMessage("ctx")
	}
	engine.RenderNow()

	tc := chat.AddToolExecution("bash", `{"command":"sleep 900 && echo done"}`)
	tc.SetArgsComplete()
	tc.SetStatus(ToolRunning) // resets the execution clock — backdate after
	tc.startTime = time.Now().Add(-9 * time.Second)
	engine.RenderNow()
	for i := 0; i < 12; i++ {
		chat.AddSystemMessage("below")
	}
	engine.RenderNow()
	engine.RenderNow() // watermark landed → strip active
	if !chat.IsScrolledOff(tc) {
		t.Fatal("fixture broken: widget must be fully off-screen")
	}

	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.Writes() {
		emu.Process(w)
	}
	if !visibleContains(emu, term.h, "elapsed 9.") {
		t.Errorf("live tool status must be visible in the chrome band; screen:\n%s", dumpScreen(emu, term.h))
	}
	if !visibleContains(emu, term.h, "sleep 900") {
		t.Errorf("live tool identity must be visible in the chrome band; screen:\n%s", dumpScreen(emu, term.h))
	}
}

// ansiStripForTest strips SGR sequences from a rendered row for assertions.
func ansiStripForTest(s string) string {
	return strings.TrimSpace(ansi.Strip(s))
}
