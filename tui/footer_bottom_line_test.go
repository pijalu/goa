// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestFooterNoEmptyLineAtBottom is the regression test for the
// "1 empty line at bottom" bug.
//
// Root cause: Footer.Render appended renderOrchStatsLines, which returned a
// single blank spacer line when no orchestration stats were active (added in
// commit b9e48a6 to "keep footer height constant, preventing compositor
// height-change full redraws"). That spacer was the permanently-empty bottom
// row. The rationale was invalid: the chat viewport is the layout fill and
// absorbs any chrome-height change, so the total canvas height stays ==
// terminal height and no full redraw ever occurs on a footer height change
// (verified by TestFooterHeightToggle_NoFullRedraw).
//
// Fix: renderOrchStatsLines returns nil when idle, so the idle footer is
// exactly its two chrome lines and the bottom terminal row is real content.
func TestFooterNoEmptyLineAtBottom(t *testing.T) {
	const (
		w = 100
		h = 24
	)
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	header := NewHeader("goa", "0.1.0-dev")
	engine.AddChild(header)

	chat := NewChatViewport()
	engine.AddChild(chat)

	status := NewStatusMsg()
	engine.AddChild(status)

	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	engine.SetFocus(ed)

	footer := NewFooter()
	footer.SetData(FooterData{Workdir: "/test", Mode: "yolo", Profile: "coder", Model: "test-model"})
	engine.AddChild(footer)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	engine.RenderNow()

	// The idle footer must not emit any blank line.
	footerLines := footer.Render(w)
	for i, l := range footerLines {
		if strings.TrimSpace(ansi.Strip(l)) == "" {
			t.Errorf("idle footer line %d is blank (the bug); footer must not emit blank lines: %q", i, l)
		}
	}

	// The composed canvas's last (bottom) row must not be a blank spacer.
	buf := engine.Buffer()
	if len(buf) != h {
		t.Fatalf("canvas height=%d, want terminal height %d (fill must absorb chrome)", len(buf), h)
	}
	last := strings.TrimSpace(ansi.Strip(buf[len(buf)-1]))
	if last == "" {
		t.Errorf("bottom terminal row is empty (the bug): last canvas row is blank; want the footer model line. screen tail:\n%s", tailStripped(buf, 3))
	}
	// And the bottom row should carry footer model content.
	if !strings.Contains(ansi.Strip(buf[len(buf)-1]), "test-model") {
		t.Errorf("bottom row does not show footer model content; screen tail:\n%s", tailStripped(buf, 3))
	}
}

// TestFooterHeightToggle_NoFullRedraw proves the premise of the fix: changing
// the footer height (idle 2-line <-> orchestration N-line) does NOT trigger a
// compositor full screen/scrollback redraw, because the chat viewport fill
// keeps the canvas height == terminal height. This is why removing the idle
// blank spacer is safe.
func TestFooterHeightToggle_NoFullRedraw(t *testing.T) {
	const (
		w = 100
		h = 24
	)
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)

	header := NewHeader("goa", "x")
	engine.AddChild(header)
	chat := NewChatViewport()
	engine.AddChild(chat)
	status := NewStatusMsg()
	engine.AddChild(status)
	ed := NewEditor()
	ed.SetTUI(engine)
	ed.SetFocused(true)
	engine.AddChild(ed)
	engine.SetFocus(ed)
	footer := NewFooter()
	footer.SetData(FooterData{Workdir: "/t", Mode: "yolo", Profile: "coder", Model: "m"})
	engine.AddChild(footer)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	engine.RenderNow()
	if idleH := len(engine.Buffer()); idleH != h {
		t.Fatalf("idle canvas height=%d, want %d", idleH, h)
	}
	term.writes = nil

	// Orchestration ON (footer switches from 2 chrome lines to per-agent lines).
	footer.SetData(FooterData{Workdir: "/t", Mode: "yolo", Profile: "coder", Model: "m",
		OrchestrationStats: "Coder: ↑40 ↓12 - (google) gemma\nReviewer: - (lmstudio) qwen"})
	engine.RenderNow()
	onJoined := strings.Join(term.writes, "")
	term.writes = nil

	// Orchestration OFF (footer back to 2 chrome lines).
	footer.SetData(FooterData{Workdir: "/t", Mode: "yolo", Profile: "coder", Model: "m"})
	engine.RenderNow()
	offJoined := strings.Join(term.writes, "")

	joined := onJoined + offJoined
	if strings.Contains(joined, "\x1b[2J") {
		t.Errorf("footer height toggle emitted a full screen clear (\\x1b[2J)")
	}
	if strings.Contains(joined, "\x1b[3J") {
		t.Errorf("footer height toggle erased scrollback (\\x1b[3J)")
	}
	// Canvas height must remain constant (== terminal height) throughout.
	if finalH := len(engine.Buffer()); finalH != h {
		t.Errorf("canvas height drifted to %d after toggle; want %d", finalH, h)
	}
}

func tailStripped(lines []string, n int) string {
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for i := start; i < len(lines); i++ {
		b.WriteString(ansi.Strip(lines[i]))
		b.WriteByte('\n')
	}
	return b.String()
}
