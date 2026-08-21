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
// scrollback line-duplication in). The compositor must never emit a
// row that fills the last column.
func TestCompositor_NoFullWidthRowDuringScroll(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)
	wide, over := strings.Repeat("w", 40), strings.Repeat("o", 60)
	var lines []string
	scene := func() *Scene {
		return &Scene{TerminalW: 40, TerminalH: 10, Layers: []Layer{{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: len(lines)}, Content: append([]string(nil), lines...)}}}
	}
	for i := 0; i < 6; i++ {
		lines = append(lines, wide, over, "short")
		comp.Render(scene())
	}
	assertNoFullWidthWrites(t, term.writes)
	t.Log("no full-width rows emitted — wrap-safe")
}

func assertNoFullWidthWrites(t *testing.T, writes []string) {
	for i, write := range writes {
		for _, segment := range strings.Split(write, "\x1b[") {
			assertSafeSegment(t, i, segment)
		}
	}
}

func assertSafeSegment(t *testing.T, index int, segment string) {
	if len(segment) < 3 || !strings.Contains(segment, ";1H") {
		return
	}
	parts := strings.SplitN(segment, "\x1b[2K", 2)
	if len(parts) != 2 {
		return
	}
	content := parts[1]
	if end := strings.Index(content, "\x1b"); end >= 0 {
		content = content[:end]
	}
	if width := visibleWidth(ansi.Strip(content)); width >= 40 {
		t.Fatalf("write[%d] emits full-width row (%d): %q", index, width, content)
	}
}
