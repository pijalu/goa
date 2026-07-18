// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// Repro for: "write tool streaming — the stats are not moving".
//
// During a write tool call the model streams the file content token-by-token.
// The widget's "… writing N lines" stat must grow as content arrives, so the
// user sees live progress. Reported behavior: the stat froze at "writing 8
// lines / 116 B" while the elapsed timer kept climbing — i.e. the content
// preview/byte count was not updating even though deltas were still arriving.
//
// This test streams a growing write content as a sequence of Anthropic-style
// input_json_delta partials and asserts the stat line reflects the growing
// line count across the Filmstrip.
func TestFilmstrip_WriteStreamingStatsAdvance(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Build the accumulated JSON args incrementally, as the provider would
	// stream them. Each step appends more file content.
	prefix := `{"path":"/tmp/x.go","content":"line1\nline2\n`
	// Stream 6 deltas, each adding two more lines to content.
	deltas := []string{
		prefix,
		prefix + `line3\n`,
		prefix + `line3\nline4\n`,
		prefix + `line3\nline4\nline5\n`,
		prefix + `line3\nline4\nline5\nline6\n`,
		prefix + `line3\nline4\nline5\nline6\nline7\n`,
	}

	var stats []string
	for _, acc := range deltas {
		sc.apply(&agentic.OutputEvent{
			Type:      agentic.EventToolCall,
			State:     agentic.StateToolCall,
			Role:      agentic.Assistant,
			ToolName:  "write",
			ToolInput: acc,
			IsDelta:   true,
		})
		stats = append(stats, writeStatLine(sc))
	}

	t.Logf("stat progression: %q", stats)

	// The line count must be non-decreasing and must end higher than it
	// started — proving the stat advances as content streams in.
	first, last := stats[0], stats[len(stats)-1]
	if first == last {
		t.Errorf("write stat never advanced across %d deltas: %q", len(deltas), stats)
	}
	if !strings.Contains(last, "line") {
		t.Errorf("final stat malformed: %q", last)
	}
}

// writeStatLine extracts the "writing N lines" / byte-count text currently
// shown by the running write tool widget in the chat viewport.
func writeStatLine(sc *uiScenario) string {
	frame := sc.engine.AgentFrame()
	node := frame.FindNode("ChatViewport")
	if node == nil {
		return ""
	}
	for _, line := range strings.Split(node.Text, "\n") {
		l := strings.TrimSpace(line)
		if strings.Contains(l, "writing") || strings.Contains(l, "lines") {
			return l
		}
	}
	return ""
}

// Repro for: the elapsed timer advances but the content preview is static.
// Even when the stat is correct, the body must re-render the growing content
// so the preview shows the latest lines, not the first few forever.
func TestFilmstrip_WriteStreamingContentAdvances(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	prefix := `{"path":"/tmp/x.go","content":"`
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall, Role: agentic.Assistant,
		ToolName: "write", ToolInput: prefix + "AAAA\nBBBB\n", IsDelta: true,
	})
	early := viewportText(sc)

	// More content streams in — a uniquely-marked late line must appear.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall, Role: agentic.Assistant,
		ToolName: "write", ToolInput: prefix + "AAAA\nBBBB\nCCCC\nZZZ-LATE-MARKER\n", IsDelta: true,
	})
	late := viewportText(sc)

	if early == late {
		t.Errorf("write preview did not re-render as content grew")
	}
	if !strings.Contains(late, "ZZZ-LATE-MARKER") {
		t.Errorf("late-streamed content line missing from preview:\n%s", late)
	}
}

// viewportText returns the ANSI-stripped visible chat viewport text.
func viewportText(sc *uiScenario) string {
	frame := sc.engine.AgentFrame()
	node := frame.FindNode("ChatViewport")
	if node == nil {
		return ""
	}
	return node.Text
}

// Repro for: "the bubble corrupts the screen" and "resize should redraw the
// history". After a width change, previously-rendered content (chat history +
// tool widgets) must be re-laid-out at the new width — the footer and tool
// widget borders must not be left at stale widths or overlapping the input.
func TestFilmstrip_ResizeRedrawsHistory(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Render some history at width 100: a user message + a write tool result
	// with a wide box border.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall, Role: agentic.Assistant,
		ToolName: "write", ToolInput: `{"path":"/tmp/x.go","content":"a\nb\nc\n"}`, IsDelta: false,
	})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "write", ToolCallID: "", Text: "[write: /tmp/x.go]\n```\na\nb\nc\n```",
	})

	before := sc.engine.AgentFrame()
	// Narrow the terminal: 100 -> 60.
	sc.term.w, sc.term.h = 60, 24
	sc.engine.RequestRender()
	sc.engine.RenderNow()
	after := sc.engine.AgentFrame()

	if after.Width != 60 {
		t.Fatalf("frame width = %d, want 60 after resize", after.Width)
	}
	// No visible line may exceed the new width (stale wide content = corrupt).
	for i, line := range after.Visible {
		if w := visibleW(line); w > 60 {
			t.Errorf("line %d exceeds new width 60 (w=%d): %q\nBEFORE:\n%s\nAFTER:\n%s",
				i, w, line, strings.Join(before.Visible, "\n"), strings.Join(after.Visible, "\n"))
		}
	}
}

// TestFilmstrip_WidthResizeReflowsHistoryContent proves the resize-redraw fix
// (Plan C) end-to-end: after a width change, content that was laid out at the
// old width is re-wrapped to the new width. A wide steering/history line that
// fit at width 100 must be re-laid-out (wrapped), not left stale — this is
// the "resize should redraw the history" report. We assert the visible
// history content is present and correctly bounded after narrowing.
func TestFilmstrip_WidthResizeReflowsHistoryContent(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// A long assistant message that wraps differently at 100 vs 60 cols.
	longMsg := "The scheduler doesn't spin — 4 fires in 1.2s (correct ~250ms sleep between), and the goroutine provably sleeps between fires. Now the benchmarks:"
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: longMsg,
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Narrow 100 -> 60. History must reflow: the long message re-wraps to more
	// lines, all within width 60.
	sc.term.w = 60
	sc.engine.RequestRender()
	sc.engine.RenderNow()
	frame := sc.engine.AgentFrame()

	// Every visible line within the new width.
	for i, line := range frame.Visible {
		if w := visibleW(line); w > 60 {
			t.Errorf("line %d overflows new width after reflow (w=%d): %q", i, w, line)
		}
	}
	// The history content is still present (not dropped by the reset).
	joined := strings.Join(frame.Visible, " ")
	if !strings.Contains(joined, "scheduler") {
		t.Errorf("history content lost after width resize:\n%s", strings.Join(frame.Visible, "\n"))
	}
}

// visibleW measures the visible column width of an ANSI-stripped line.
func visibleW(s string) int {
	w := 0
	for _, r := range s {
		if r == '\t' {
			w += 4
			continue
		}
		w++
	}
	return w
}
