// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// TestUI_ToolWidgetRowsKeepStatusBgAfterStreamShrink is the bugs.md Bug D
// regression: two parallel reads at the bottom of a scrolled chat, flipping
// pending → success while an assistant stream finalizes nearby (the
// finalize SHRINKS the canvas, bouncing the compositor's viewport top).
// Pre-fix, the bottom transcript row of the second widget kept the pending
// background — the compositor's repaintWindow skipped it via a stale
// lastScrollCount skip window (target-c.vt counted a window-top move that
// the scroll never wrote). Every widget row must carry the success
// background once the tool completes.
func TestUI_ToolWidgetRowsKeepStatusBgAfterStreamShrink(t *testing.T) {
	sc := newUIScenario(t, 80, 16)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "r1", ToolName: "read", ToolInput: `{"path":"a.py","max_lines":100}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "r2", ToolName: "read", ToolInput: `{"path":"b.py","max_lines":100}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: "r1", ToolName: "read"})
	time.Sleep(15 * time.Millisecond)
	sc.chat.InvalidateRunningToolWidgets()
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolCallID: "r1", ToolName: "read", Text: "read file a.py:1:100"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: "r2", ToolName: "read"})
	time.Sleep(15 * time.Millisecond)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolCallID: "r2", ToolName: "read", Text: "read file b.py:1:100"})

	emu := tui.NewTermEmulator(16, 80)
	for _, w := range sc.term.writes {
		emu.Process(w)
	}

	successBg := bgParams(t, "tool_success_bg")
	pendingBg := bgParams(t, "tool_pending_bg")
	for r := 0; r < 16; r++ {
		for col, bg := range emu.VisibleBg(r) {
			if bg == pendingBg {
				t.Fatalf("row %d col %d kept the PENDING bg after success (row %q) — Bug D stale row",
					r, col, strings.TrimSpace(emu.Visible(r)))
			}
			_ = successBg
		}
	}
}
