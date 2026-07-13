// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestCompositor_LargeAppendPreservesAllScrollback is the regression test for
// the "scrollback missing content after long edit/write" bug. emitLargeScroll
// (the path taken when a single frame grows the canvas by more than the
// viewport height) used to write each gap line at the bottom row then emit a
// trailing newline. A trailing newline at the bottom row scrolls the TOP row
// into scrollback — not the just-written line — so gap lines stacked on screen
// and were overwritten by the new viewport, losing them from scrollback.
//
// The fix scrolls BEFORE writing each line, so each gap line is pushed into
// scrollback correctly. This test grows a single base layer by more than a
// screen per frame and asserts every line is recoverable from scrollback or
// the visible screen.
func TestCompositor_LargeAppendPreservesAllScrollback(t *testing.T) {
	term := &fakeTerminal{w: 30, h: 10}
	comp := NewCompositor(term)
	scene := func(lines []string) *Scene {
		return &Scene{
			TerminalW: 30, TerminalH: 10,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 30, H: len(lines)}, Content: lines}},
		}
	}

	var content []string
	content = append(content, "HEADER")
	comp.Render(scene(content)) // frame 0: fits

	// Grow by 25 (> viewport height) per frame for several frames so the
	// emitLargeScroll path is exercised repeatedly.
	for f := 0; f < 8; f++ {
		for i := 0; i < 25; i++ {
			content = append(content, fmt.Sprintf("L%d", len(content)-1))
		}
		comp.Render(scene(content))
	}

	emu := newScreenEmulator(10, 30)
	for _, w := range term.writes {
		emu.Process(w)
	}
	rows := make([]string, 10)
	for r := 0; r < 10; r++ {
		rows[r] = emu.Visible(r)
	}
	total := strings.Join(emu.Scrollback(), "\n") + "\n" + strings.Join(rows, "\n")

	// Every "L<n>" content line must be recoverable.
	missing := 0
	for idx := 1; idx < len(content); idx++ {
		want := fmt.Sprintf("L%d", idx-1)
		if !strings.Contains(total, want) {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("emitLargeScroll lost %d content lines to scrollback (of %d); scrollback=%d lines",
			missing, len(content)-1, len(emu.Scrollback()))
	}
}

// TestCompositor_StreamingWriteWidgetPreservesScrollback covers the realistic
// long-write scenario at the integration level: a write tool widget streams an
// accumulating content body in small per-frame deltas (as real tool-arg
// streaming does). The earlier baseline content and the streamed lines that
// scroll off must remain recoverable in terminal scrollback.
func TestCompositor_StreamingWriteWidgetPreservesScrollback(t *testing.T) {
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

	for i := 0; i < 8; i++ {
		chat.AddSystemMessage(fmt.Sprintf("BASELINE-%d", i))
	}
	engine.RenderNow()

	tc := chat.AddToolExecution("write", ``)
	tc.SetArgsPartial(`{"path":"big.go"}`)
	engine.RenderNow()

	// Stream 300 lines in small (3-line) deltas — the realistic arg-streaming
	// shape, which takes the bare-newline scroll path.
	var acc strings.Builder
	for step := 0; step < 100; step++ {
		for i := 0; i < 3; i++ {
			fmt.Fprintf(&acc, "stream line %d\n", step*3+i)
		}
		tc.SetArgsPartial(fmt.Sprintf(`{"path":"big.go","content":%q}`, acc.String()))
		engine.RenderNow()
	}

	emu := newScreenEmulator(term.h, term.w)
	for _, w := range term.writes {
		emu.Process(w)
	}
	rows := make([]string, term.h)
	for r := 0; r < term.h; r++ {
		rows[r] = emu.Visible(r)
	}
	all := strings.Join(emu.Scrollback(), "\n") + "\n" + strings.Join(rows, "\n")

	// Baseline content must be retained.
	for i := 0; i < 8; i++ {
		if want := fmt.Sprintf("BASELINE-%d", i); !strings.Contains(all, want) {
			t.Errorf("baseline content %q lost from scrollback", want)
		}
	}
	// Streamed lines that scrolled off must be retained (spot-check across the range).
	for _, n := range []int{0, 30, 60, 90, 120} {
		if want := fmt.Sprintf("stream line %d", n); !strings.Contains(all, want) {
			t.Errorf("streamed content %q lost from scrollback", want)
		}
	}
	// And the latest line must be visible.
	if !visibleContains(emu, term.h, "stream line 299") {
		t.Errorf("latest streamed line not visible; screen:\n%s", dumpScreen(emu, term.h))
	}
}

func dumpScreen(emu *screenEmulator, h int) string {
	var b strings.Builder
	for r := 0; r < h; r++ {
		fmt.Fprintf(&b, "row %d: %q\n", r, ansi.Strip(emu.Visible(r)))
	}
	return b.String()
}
