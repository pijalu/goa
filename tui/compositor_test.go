// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_RendersBaseLayersInOrder feeds the Compositor a Scene of
// stacked base layers and verifies the composed canvas + emulated screen show
// them in order. This tests the protocol owner in isolation, independent of
// real components.
func TestCompositor_RendersBaseLayersInOrder(t *testing.T) {
	term := &fakeTerminal{w: 20, h: 10}
	comp := NewCompositor(term)
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "a", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 20, H: 1}, Content: []string{"AAA"}},
			{Name: "b", Kind: LayerBase, Rect: Rect{X: 0, Y: 1, W: 20, H: 1}, Content: []string{"BBB"}},
		},
	}
	comp.Render(scene)

	emu := newScreenEmulator(10, 20)
	for _, w := range term.writes {
		emu.Process(w)
	}
	if !visibleContains(emu, 10, "AAA") || !visibleContains(emu, 10, "BBB") {
		t.Errorf("base layers not rendered in order:\n%s", dumpEmu(emu, 10))
	}
}

// TestCompositor_OverlayCompositesOnTop verifies an overlay layer overwrites
// the base canvas at its viewport-relative rect (centered).
func TestCompositor_OverlayCompositesOnTop(t *testing.T) {
	term := &fakeTerminal{w: 20, h: 10}
	comp := NewCompositor(term)
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "base", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 20, H: 1}, Content: []string{"base-line"}},
			{Name: "ov", Kind: LayerOverlay, Z: 5,
				Rect:    Rect{X: 0, Y: 0, W: 20, H: 1}, // viewport row 0 (top)
				Content: []string{"OVERLAY-TOP"}},
		},
	}
	comp.Render(scene)
	emu := newScreenEmulator(10, 20)
	for _, w := range term.writes {
		emu.Process(w)
	}
	top := ansiClean(emu.Visible(0))
	if !strings.Contains(top, "OVERLAY-TOP") {
		t.Errorf("overlay did not composite on top; row0=%q", top)
	}
}

// TestCompositor_CursorPositionsHardwareCursor verifies the explicit
// Scene.Cursor drives the hardware cursor column (no CURSOR_MARKER scanning
// in the compositor).
func TestCompositor_CursorPositionsHardwareCursor(t *testing.T) {
	term := &fakeTerminal{w: 20, h: 10}
	comp := NewCompositor(term)
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "base", Kind: LayerBase, Rect: Rect{X: 0, Y: 9, W: 20, H: 1}, Content: []string{"hello"}},
		},
		Cursor: &CursorPos{Row: 9, Col: 5},
	}
	comp.Render(scene)
	emu := newScreenEmulator(10, 20)
	for _, w := range term.writes {
		emu.Process(w)
	}
	if emu.col != 5 {
		t.Errorf("hardware cursor col = %d, want 5", emu.col)
	}
}

// TestScene_AgentFrame_NoANSI verifies the AgentView strips ANSI and reports
// the visible viewport + structured layers for AI tooling.
func TestScene_AgentFrame_NoANSI(t *testing.T) {
	scene := &Scene{
		TerminalW: 10, TerminalH: 3,
		Layers: []Layer{
			{Name: "x", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 10, H: 2},
				Content: []string{"\x1b[31mred\x1b[0m", "plain"}},
		},
	}
	frame := scene.AgentFrame(3)
	if len(frame.Layers) != 1 || frame.Layers[0].Name != "x" {
		t.Fatalf("agent layer missing: %+v", frame.Layers)
	}
	if frame.Layers[0].Lines[0] != "red" {
		t.Errorf("ANSI not stripped: %q", frame.Layers[0].Lines[0])
	}
	// Visible viewport: canvas is 2 rows tall (one layer, H=2).
	if len(frame.Visible) != 2 || frame.Visible[0] != "red" {
		t.Errorf("visible viewport wrong: %+v", frame.Visible)
	}
	for _, v := range frame.Visible {
		if strings.Contains(v, "\x1b") {
			t.Errorf("visible contains ANSI: %q", v)
		}
	}
}

// TestTUI_AgentFrame_MatchesScreen verifies the TUI's AgentFrame agrees with
// what the Compositor actually paints — agent and terminal see the same thing.
func TestTUI_AgentFrame_MatchesScreen(t *testing.T) {
	term := &fakeTerminal{w: 30, h: 8}
	engine := NewTUI(term)
	chat := NewChatViewport()
	chat.AddSystemMessage("hello-agent")
	engine.AddChild(chat)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()
	engine.RenderNow()

	frame := engine.AgentFrame()
	emu := newScreenEmulator(8, 30)
	for _, w := range term.writes {
		emu.Process(w)
	}
	// The agent visible viewport must contain the same text as the emulated
	// visible screen.
	found := false
	for _, v := range frame.Visible {
		if strings.Contains(v, "hello-agent") {
			found = true
		}
	}
	if !found {
		t.Errorf("agent frame missing 'hello-agent': %+v", frame.Visible)
	}
	if !visibleContains(emu, 8, "hello-agent") {
		t.Errorf("emulated screen missing 'hello-agent'")
	}
}

func dumpEmu(emu *screenEmulator, h int) string {
	var b strings.Builder
	for r := 0; r < h; r++ {
		b.WriteString(emu.Visible(r) + "\n")
	}
	return b.String()
}
