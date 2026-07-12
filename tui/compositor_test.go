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

// TestCompositor_CursorClampedAtFullWidth verifies that a cursor column equal
// to the terminal width (the end of a completely filled line) is clamped to
// the last column instead of forcing the hardware cursor to wrap to the next
// physical line. This is the "cursor jumps to the next line at end of line"
// failure mode.
func TestCompositor_CursorClampedAtFullWidth(t *testing.T) {
	term := &fakeTerminal{w: 20, h: 10}
	comp := NewCompositor(term)
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "base", Kind: LayerBase, Rect: Rect{X: 0, Y: 9, W: 20, H: 1},
				Content: []string{"12345678901234567890"}}, // exactly width (20) chars
		},
		Cursor: &CursorPos{Row: 9, Col: 20}, // one past the last visible cell
	}
	comp.Render(scene)

	emu := newScreenEmulator(10, 20)
	for _, w := range term.writes {
		emu.Process(w)
	}
	if emu.col != 19 {
		t.Errorf("hardware cursor col = %d, want 19 (clamped to last column, not wrapping)", emu.col)
	}
}

// TestCompositor_CursorPositionOnFirstFrame verifies that the hardware cursor
// is placed at the correct screen row even on the very first frame, before
// the compositor has recorded a previous frame height.
func TestCompositor_CursorPositionOnFirstFrame(t *testing.T) {
	term := &fakeTerminal{w: 20, h: 10}
	comp := NewCompositor(term)
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "base", Kind: LayerBase, Rect: Rect{X: 0, Y: 5, W: 20, H: 1},
				Content: []string{"hello"}},
		},
		Cursor: &CursorPos{Row: 5, Col: 2},
	}
	comp.Render(scene)

	emu := newTermEmulator(10, 20)
	for _, w := range term.Writes() {
		emu.Process(w)
	}
	if emu.row != 5 {
		t.Errorf("hardware cursor row = %d, want 5 on first frame", emu.row)
	}
	if emu.col != 2 {
		t.Errorf("hardware cursor col = %d, want 2 on first frame", emu.col)
	}
}

// TestExtractCursorMarker_OverlayPreferred verifies that an overlay's cursor
// marker takes precedence over base-layer markers, so input-owning overlays
// (e.g. selectors) place the hardware cursor correctly.
func TestExtractCursorMarker_OverlayPreferred(t *testing.T) {
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 20, H: 8},
				Content: []string{"chat" + CURSOR_MARKER}},
			{Name: "selector", Kind: LayerOverlay, Rect: Rect{X: 0, Y: 2, W: 20, H: 3},
				Content: []string{"title", "───", "search> " + CURSOR_MARKER}},
		},
	}
	extractCursorMarker(scene)
	if scene.Cursor == nil {
		t.Fatal("cursor not extracted")
	}
	if scene.Cursor.Row != 4 {
		t.Errorf("cursor row = %d, want 4 (overlay absolute row)", scene.Cursor.Row)
	}
	if scene.Cursor.Col != 8 {
		t.Errorf("cursor col = %d, want 8 (after 'search> ')", scene.Cursor.Col)
	}
	if strings.Contains(scene.Layers[1].Content[2], CURSOR_MARKER) {
		t.Error("cursor marker should be stripped from overlay content")
	}
}

// TestExtractCursorMarker_FallsBackToBaseLayer verifies that when no overlay
// marker exists, the base layer marker is still used.
func TestExtractCursorMarker_FallsBackToBaseLayer(t *testing.T) {
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 20, H: 8},
				Content: []string{"chat" + CURSOR_MARKER}},
			{Name: "selector", Kind: LayerOverlay, Rect: Rect{X: 0, Y: 2, W: 20, H: 3},
				Content: []string{"title", "───", "search> "}},
		},
	}
	extractCursorMarker(scene)
	if scene.Cursor == nil {
		t.Fatal("cursor not extracted")
	}
	if scene.Cursor.Row != 0 {
		t.Errorf("cursor row = %d, want 0 (base layer)", scene.Cursor.Row)
	}
	if scene.Cursor.Col != 4 {
		t.Errorf("cursor col = %d, want 4 (after 'chat')", scene.Cursor.Col)
	}
}

// TestExtractCursorMarker_ReverseScanPrefersLastLayer verifies that the cursor
// marker extraction scans base layers from back to front. This keeps the cost
// bounded by the input layer instead of scanning the entire chat history, and
// it prefers the focused input layer when earlier layers happen to contain the
// marker sequence.
func TestExtractCursorMarker_ReverseScanPrefersLastLayer(t *testing.T) {
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 20, H: 1},
				Content: []string{"chat" + CURSOR_MARKER}},
			{Name: "input", Kind: LayerBase, Rect: Rect{X: 0, Y: 1, W: 20, H: 1},
				Content: []string{"input" + CURSOR_MARKER}},
		},
	}
	extractCursorMarker(scene)
	if scene.Cursor == nil {
		t.Fatal("cursor not extracted")
	}
	if scene.Cursor.Row != 1 {
		t.Errorf("cursor row = %d, want 1 (last base layer)", scene.Cursor.Row)
	}
	if scene.Cursor.Col != 5 {
		t.Errorf("cursor col = %d, want 5 (after 'input')", scene.Cursor.Col)
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

// TestPlaceLayer_OnlyWritesVisibleSubrange verifies placeLayer is O(visible):
// it writes only the content lines whose absolute Y falls inside the visible
// region and leaves off-screen canvas rows untouched, even when the layer's
// Content is far larger than the viewport (the chat-transcript case).
func TestPlaceLayer_OnlyWritesVisibleSubrange(t *testing.T) {
	const canvasH = 1000
	const rectY = 0
	const vStart = 990 // viewport shows the last 10 rows
	const vEnd = 1000

	content := make([]string, canvasH)
	for i := range content {
		content[i] = "line-" + itoaStr(i)
	}
	canvas := make([]string, canvasH)

	l := Layer{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: rectY, W: 80, H: canvasH}, Content: content}
	placeLayer(canvas, l, 80, vStart, vEnd)

	// Visible rows populated.
	for y := vStart; y < vEnd; y++ {
		if canvas[y] != content[y] {
			t.Errorf("visible row %d = %q, want %q", y, canvas[y], content[y])
		}
	}
	// Off-screen rows untouched (still empty).
	for y := 0; y < vStart; y++ {
		if canvas[y] != "" {
			t.Errorf("off-screen row %d should be empty, got %q", y, canvas[y])
		}
	}
}

// TestPlaceLayer_LayerOffsetBelowViewport verifies a layer whose Rect.Y starts
// above the viewport still maps its tail content into the visible region.
func TestPlaceLayer_LayerOffsetBelowViewport(t *testing.T) {
	const canvasH = 50
	const rectY = 40 // layer occupies rows 40..44
	content := []string{"a", "b", "c", "d", "e"}
	canvas := make([]string, canvasH)
	l := Layer{Name: "x", Kind: LayerBase, Rect: Rect{X: 0, Y: rectY, W: 80, H: 5}, Content: content}
	placeLayer(canvas, l, 80, 42, 50) // visible 42..49
	// rows 42,43,44 visible -> c,d,e ; rows 40,41 off-screen -> untouched
	if canvas[42] != "c" || canvas[43] != "d" || canvas[44] != "e" {
		t.Errorf("visible tail wrong: %q %q %q", canvas[42], canvas[43], canvas[44])
	}
	if canvas[40] != "" || canvas[41] != "" {
		t.Errorf("off-screen rows should be empty: %q %q", canvas[40], canvas[41])
	}
}

// itoaStr avoids pulling in strconv just for one test helper.
func itoaStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestCompositor_ResizePreservesScrollbackAndEmitsViewportOnly verifies the
// resize path (review 2.3): on a size change the compositor must NOT emit
// \x1b[3J (which would wipe scrollback) and must repaint only the visible
// viewport instead of re-emitting the whole transcript.
func TestCompositor_ResizePreservesScrollbackAndEmitsViewportOnly(t *testing.T) {
	term := &fakeTerminal{w: 20, h: 10}
	comp := NewCompositor(term)

	// 30-line transcript on a 10-row terminal.
	content := make([]string, 30)
	for i := range content {
		content[i] = "line " + itoaStr(i)
	}
	scene := &Scene{
		TerminalW: 20, TerminalH: 10,
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 20, H: 30}, Content: content},
		},
	}
	comp.Render(scene) // first frame populates scrollback

	// Resize to 30 cols x 16 rows (both above the clamp thresholds).
	scene.TerminalW = 30
	scene.TerminalH = 16
	beforeWrites := len(term.writes)
	comp.Render(scene) // resize frame
	if len(term.writes) <= beforeWrites {
		t.Fatalf("no resize frame emitted")
	}
	resize := term.writes[len(term.writes)-1]

	if strings.Contains(resize, "\x1b[3J") {
		t.Errorf("resize wiped scrollback (emitted \\x1b[3J): %q", resize)
	}
	if !strings.Contains(resize, "\x1b[2J") {
		t.Errorf("resize did not clear the screen (missing \\x1b[2J)")
	}
	// The resize frame should repaint at most the new viewport height (16) rows,
	// not all 30 transcript lines.
	if crlf := strings.Count(resize, "\r\n"); crlf >= 30 {
		t.Errorf("resize re-emitted the whole transcript (%d \\r\\n, want < 30): %q", crlf, resize)
	}
}

// TestCompositor_InViewportShrinkSkipsFullRedraw verifies the review 2.4
// refinement: a content shrink that never involved scrollback (everything
// fit on screen) must NOT force an O(history) full redraw. The differential
// path clears the stale trailing rows instead. Only scrollback-affecting
// shrinks (maxLinesRendered > height) need a full redraw.
func TestCompositor_InViewportShrinkSkipsFullRedraw(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 12}
	comp := NewCompositor(term)

	makeScene := func(n int) *Scene {
		content := make([]string, n)
		for i := range content {
			content[i] = "row " + itoaStr(i)
		}
		return &Scene{
			TerminalW: 40, TerminalH: 12,
			Layers: []Layer{
				{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: n}, Content: content},
			},
		}
	}
	comp.Render(makeScene(8)) // fits on 12-row screen; no scrollback
	before := comp.FullRedrawCount()
	comp.Render(makeScene(4)) // in-viewport shrink
	after := comp.FullRedrawCount()
	if after != before {
		t.Errorf("in-viewport shrink triggered a full redraw (before=%d after=%d); the differential path should handle it", before, after)
	}
}
