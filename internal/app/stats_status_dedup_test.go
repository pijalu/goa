// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"log"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestHandleToolCall_DedupesIdenticalStatusSpam covers the E5 enhancement
// (ENHANCE.md): a streaming tool call emits one EventToolCall per delta, each
// re-entering handleToolCall with the SAME label ("Calling bash..."). The
// status spinner already dedupes internally, but handleToolCall still logged
// a "[status] handleToolCall ... oldText==newText" line per delta — the
// 2026-08-05 export showed 13 identical lines in 0.4s, drowning real events.
// Identical, non-new emissions must be collapsed to one log/status update.
func TestHandleToolCall_DedupesIdenticalStatusSpam(t *testing.T) {
	app := New(testSubsystems())

	var logBuf strings.Builder
	app.subs.logger = agentic.NewLoggerWithStdLogger(log.New(&logBuf, "", 0), agentic.Info)

	// First (final/created) call for this tool call id — must register + log.
	app.handleToolCall(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "bash",
		ToolInput:  `{"command":"ls"}`,
		ToolCallID: "c1",
	})

	// A burst of streaming deltas for the SAME call with the SAME label.
	for i := 0; i < 12; i++ {
		app.handleToolCall(&agentic.OutputEvent{
			Type:       agentic.EventToolCall,
			ToolName:   "bash",
			ToolInput:  `{"command":"ls"}`,
			ToolCallID: "c1",
			IsDelta:    true,
		})
	}

	count := strings.Count(logBuf.String(), "[status] handleToolCall")
	if count > 2 {
		t.Errorf("handleToolCall logged %d status lines for one call + 12 identical deltas, want <= 2 (spam dedupe):\n%s",
			count, logBuf.String())
	}
}

// TestHandleToolCall_LogsDistinctLabels verifies the dedupe does not swallow
// genuine status changes: when the label actually changes, each is logged.
func TestHandleToolCall_LogsDistinctLabels(t *testing.T) {
	app := New(testSubsystems())

	var logBuf strings.Builder
	app.subs.logger = agentic.NewLoggerWithStdLogger(log.New(&logBuf, "", 0), agentic.Info)

	app.handleToolCall(&agentic.OutputEvent{
		Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`, ToolCallID: "c1",
	})
	// A different tool (new call id, different label) must still log.
	app.handleToolCall(&agentic.OutputEvent{
		Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path":"x"}`, ToolCallID: "c2",
	})

	count := strings.Count(logBuf.String(), "[status] handleToolCall")
	if count < 2 {
		t.Errorf("distinct tool calls must each log a status line, got %d:\n%s", count, logBuf.String())
	}
}
