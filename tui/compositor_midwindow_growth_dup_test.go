// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_MidWindowToolGrowth_NoBoundaryDuplicate is the regression test
// for the history-scroll duplicate: a tool widget growing INSIDE the visible
// window (not the last entry — messages sit below it) inserts rows mid-window,
// shifting every row below it down. repaintWindow's unchangedRow skip maps
// screen rows to the previous canvas by the pure-scroll delta, which is
// unsound once an internal insertion/deletion moves content: the line-feed
// scroll advanced the OLD layout, so unchangedRow wrongly skipped rows that
// were actually stale — leaving the same row twice on screen and dropping
// others around the history↔screen boundary.
//
// The fix routes such a frame to a full window repaint (drawWindow) via the
// windowContentShifted guard; scrollback is unaffected (the shift is below the
// scroll-off region) so no scrollback reset is needed. This test grows a
// mid-window widget one line per frame across the boundary and asserts, via a
// faithful terminal replay, that every transcript row appears exactly once
// across scrollback+screen (no duplicate within either, none lost, and none
// on both sides of the boundary).
func midWindowBuild(head, tool, tail []string, w, h, chromeH int) *Scene {
	content := make([]string, 0, len(head)+len(tool)+len(tail)+chromeH)
	content = append(content, head...)
	content = append(content, tool...)
	content = append(content, tail...)
	for i := 0; i < chromeH; i++ {
		content = append(content, "CHROME")
	}
	return &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH,
		Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(content)}, Content: content}}}
}

func midWindowCounts(emu *TermEmulator, want string, h int) (sb, sc int) {
	for _, line := range emu.Scrollback() {
		if strings.TrimSpace(line) == want {
			sb++
		}
	}
	for row := 0; row < h; row++ {
		if strings.TrimSpace(emu.Visible(row)) == want {
			sc++
		}
	}
	return sb, sc
}

func midWindowDump(emu *TermEmulator, h int) string {
	var b strings.Builder
	b.WriteString("\n=== scrollback ===\n")
	for i, line := range emu.Scrollback() {
		fmt.Fprintf(&b, "sb[%d]: %s\n", i, line)
	}
	b.WriteString("=== screen ===\n")
	for row := 0; row < h; row++ {
		fmt.Fprintf(&b, "sc[%d]: %s\n", row, emu.Visible(row))
	}
	return b.String()
}

func assertMidWindowRow(t *testing.T, emu *TermEmulator, want string, h int) {
	t.Helper()
	sb, sc := midWindowCounts(emu, want, h)
	switch {
	case sb > 1:
		t.Errorf("%q duplicated WITHIN scrollback (%d)%s", want, sb, midWindowDump(emu, h))
	case sc > 1:
		t.Errorf("%q duplicated WITHIN screen (%d)%s", want, sc, midWindowDump(emu, h))
	case sb+sc == 0:
		t.Errorf("%q LOST%s", want, midWindowDump(emu, h))
	case sb >= 1 && sc >= 1:
		t.Errorf("%q on BOTH sides of the history↔screen boundary (sb=%d sc=%d)%s", want, sb, sc, midWindowDump(emu, h))
	}
}

func TestCompositor_MidWindowToolGrowth_NoBoundaryDuplicate(t *testing.T) {
	const w, h, chromeH = 46, 11, 2 // transcript window = 9 rows
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	var head []string
	for i := 0; i < 8; i++ {
		head = append(head, fmt.Sprintf("head-%02d", i))
	}
	tool := []string{"TOOL-HEADER", "TOOL-LINE-A"}
	var tail []string
	for i := 0; i < 6; i++ {
		tail = append(tail, fmt.Sprintf("tail-%02d", i))
	}

	comp.Render(midWindowBuild(head, tool, tail, w, h, chromeH))

	// Grow the tool by one body line per frame (insert before its trailing
	// row), advancing the watermark one row per frame so head rows scroll off
	// one at a time — the tool's insertion shifts the tail rows each frame.
	for step := 0; step < 12; step++ {
		ins := len(tool)
		tool = append(tool, "")
		copy(tool[ins:], tool[ins-1:])
		tool[ins-1] = fmt.Sprintf("TOOL-LINE-G%02d", step)
		comp.Render(midWindowBuild(head, tool, tail, w, h, chromeH))
	}

	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(string(wr))
	}

	for i := range head {
		assertMidWindowRow(t, emu, fmt.Sprintf("head-%02d", i), h)
	}
	for _, l := range tool {
		if strings.TrimSpace(l) != "" {
			assertMidWindowRow(t, emu, l, h)
		}
	}
	for i := range tail {
		assertMidWindowRow(t, emu, fmt.Sprintf("tail-%02d", i), h)
	}
}

// TestCompositor_MidWindowToolGrowth_GuardFires proves the real chat-viewport
// path (tool widget growing with messages below it) reaches the vulnerable
// mid-window-shift code: the guard must route those frames to a full window
// repaint (drawWindow), observable as a rising fullRedrawCount.
func TestCompositor_MidWindowToolGrowth_GuardFires(t *testing.T) {
	term := &fakeTerminal{w: 56, h: 14}
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

	for i := 0; i < 10; i++ {
		chat.AddSystemMessage(fmt.Sprintf("base-%02d", i))
	}
	tc := chat.AddToolExecution("bash", `{"command":"MIDTOOL"}`)
	tc.SetArgsPartial(`{"command":"MIDTOOL"}`)
	tc.SetStatus(ToolRunning)
	tc.SetOutput("out-0")
	for i := 0; i < 6; i++ {
		chat.AddSystemMessage(fmt.Sprintf("below-%02d", i))
	}
	engine.RenderNow()

	before := engine.compositor.FullRedrawCount()
	for step := 1; step <= 6; step++ {
		var b strings.Builder
		for j := 0; j <= step; j++ {
			fmt.Fprintf(&b, "out-%d\n", j)
		}
		tc.SetOutput(strings.TrimRight(b.String(), "\n"))
		engine.RenderNow()
	}
	if got := engine.compositor.FullRedrawCount() - before; got == 0 {
		t.Errorf("mid-window tool growth never routed to a full window repaint; " +
			"the unchangedRow diff path ran on an internally-shifted window")
	}
}
