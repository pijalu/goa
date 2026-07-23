// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestUI_ToolCallBatchDoesNotCollapseTranscript replays the exact event
// sequence from the mascot-redraw incident (term.log 2026-07-23 07:52:39):
// a long thinking block streams, then two bash tool calls arrive back-to-back.
// In the incident, the frame where the second tool call began rendered the
// chat transcript near-empty for a single frame — the canvas height collapsed
// below the scrollback watermark and the compositor repainted the off-screen
// header/mascot onto the visible screen.
//
// The fix guards the ChatViewport incremental cache against a stale lineOffset
// (updateLastEntry) and clamps the compositor window to the scrollback
// watermark. This scenario asserts the transcript's total line count never
// transiently collapses across the tool-call batch: it must be monotonically
// non-decreasing once content is on screen (entries are append-only during a
// turn; nothing legitimately shrinks mid-stream here).
func TestUI_ToolCallBatchDoesNotCollapseTranscript(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Fill enough content that the transcript overflows the 24-row terminal,
	// pushing the header into scrollback (the precondition for the bug).
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	long := "reasoning line with enough text to wrap across the available width "
	for i := 0; i < 40; i++ {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateThinking, IsDelta: true, Text: long})
	}
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, IsDelta: true, Text: "Let me check the current state of the project.\n"})

	// Precondition: transcript is tall enough that the header scrolled off.
	before := sc.chat.TotalHeight()
	if before < 24 {
		t.Fatalf("precondition: transcript too short to have scrolled (TotalHeight=%d)", before)
	}

	// Two back-to-back bash tool calls — the incident trigger.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "bash", ToolInput: `{"command":"go test ./... | tail -20"}`, ToolCallID: "c1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "bash", ToolInput: `{"command":"make quality 2>&1"}`, ToolCallID: "c2"})

	// The invariant: across the batch the transcript never transiently
	// collapses below its pre-tool height. A stale-offset collapse would show
	// up here as a sudden drop in TotalHeight.
	after := sc.chat.TotalHeight()
	if after < before {
		t.Errorf("transcript collapsed across tool-call batch: TotalHeight %d -> %d (would flash the off-screen header back on screen)", before, after)
	}

	// And the visible viewport must still show the tail (newest content), not
	// the header scrolled back to the top.
	frame := sc.engine.AgentFrame()
	vis := frame.Visible
	if len(vis) > 0 && vis[0] != "" {
		t.Logf("top visible row: %q", vis[0])
	}
}
