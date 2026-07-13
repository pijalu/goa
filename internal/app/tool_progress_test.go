// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
)

// TestToolProgress_ShowsPartialOutputWhileRunning is the regression test for the
// "nothing happens" syndrome: a long-running tool must stream partial output
// into its widget BEFORE it completes. EventToolProgress updates the running
// widget's output without completing it (status stays Running, elapsed timer
// keeps ticking); the final EventToolResult then resolves it.
func TestToolProgress_ShowsPartialOutputWhileRunning(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	sc := newUIScenario(t, 100, 24)

	// Start a bash tool call (widget created, Running).
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "bash", ToolInput: `{"command":"echo hi"}`, ToolCallID: "c1",
	})

	// Mid-execution: the tool streams partial output.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolProgress, State: agentic.StateToolCall,
		ToolName: "bash", ToolCallID: "c1", Text: "building... 42%",
	})
	visible := strings.Join(sc.engine.AgentFrame().Visible, "\n")
	if !strings.Contains(visible, "building...") {
		t.Errorf("expected partial tool output visible mid-run; visible:\n%s", visible)
	}

	// The widget must still be Running (not completed by the progress event).
	widget := findToolWidget(sc)
	if widget == nil {
		t.Fatalf("no tool widget found after progress")
	}
	if widget.Status() == tui.ToolSuccess || widget.Status() == tui.ToolError {
		t.Errorf("progress event must not complete the tool; status=%v", widget.Status())
	}
	if !widget.IsPartial() {
		t.Errorf("widget must remain partial (running) after a progress event")
	}

	// Final result resolves the widget.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "bash", ToolCallID: "c1", Text: "all done",
	})
	visible = strings.Join(sc.engine.AgentFrame().Visible, "\n")
	if !strings.Contains(visible, "all done") {
		t.Errorf("expected final tool output visible; visible:\n%s", visible)
	}
	if widget.Status() != tui.ToolSuccess {
		t.Errorf("expected tool Success after result, got %v", widget.Status())
	}
	if widget.IsPartial() {
		t.Errorf("widget must not be partial after the final result")
	}
}

// findToolWidget returns the first tool-execution widget in the chat viewport.
func findToolWidget(sc *uiScenario) *tui.ToolExecutionComponent {
	for _, ch := range sc.chat.Children() {
		if tc, ok := ch.(*tui.ToolExecutionComponent); ok {
			return tc
		}
	}
	return nil
}
