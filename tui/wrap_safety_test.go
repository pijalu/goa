package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestCompositor_NoFullWidthRowDuringScroll is the regression guard for the
// auto-wrap off-by-one: with auto-wrap ON (a terminal ignoring DECAWM-off),
// any row whose visible width reaches the full terminal width leaves the
// terminal in a pending-wrap state, so the next line-feed wraps onto an
// extra row and every subsequent compositor row index shifts by one (the
// scrollback line-duplication in bugs.md). The compositor must never emit a
// row that fills the last column.
func TestCompositor_NoFullWidthRowDuringScroll(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)

	// Chat content with lines that are EXACTLY terminal width (the wrap
	// trigger) and longer, mixed with short lines, streamed past the screen.
	wide := strings.Repeat("w", 40)  // exactly width
	over := strings.Repeat("o", 60)   // over width (must be truncated)
	var lines []string
	scene := func() *Scene {
		return &Scene{
			TerminalW: 40, TerminalH: 10,
			Layers: []Layer{
				{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: len(lines)}, Content: append([]string(nil), lines...)},
			},
		}
	}
	for i := 0; i < 6; i++ {
		lines = append(lines, wide, over, "short")
		comp.Render(scene())
	}

	// Scan every emitted write: no row written via CUP may have visible
	// width >= terminal width (filling the last column = pending wrap).
	for wi, wr := range term.writes {
		for _, seg := range strings.Split(wr, "\x1b[") {
			if len(seg) < 3 {
				continue
			}
			// Only inspect "N;1H<content>" row-writes (content after \x1b[2K).
			if !strings.Contains(seg, ";1H") {
				continue
			}
			parts := strings.SplitN(seg, "\x1b[2K", 2)
			if len(parts) != 2 {
				continue
			}
			content := parts[1]
			// stop at the next CSI (shouldn't happen post-split, but be safe)
			if idx := strings.Index(content, "\x1b"); idx >= 0 {
				content = content[:idx]
			}
			if vw := visibleWidth(ansi.Strip(content)); vw >= 40 {
				t.Fatalf("write[%d] emits a full-width row (visibleWidth=%d >= 40) — auto-wrap off-by-one risk:\n%q", wi, vw, content)
			}
		}
	}
	t.Log("no full-width rows emitted — wrap-safe")
}
