// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestChatViewportAgentFilter_IsolatesOneAgent is the RED→GREEN test for R4:
// a per-agent tab filters the chat to that one worker's blocks, restoring the
// per-agent view without duplicating streaming widgets.
func TestChatViewportAgentFilter_IsolatesOneAgent(t *testing.T) {
	cv := NewChatViewport()
	cv.AddAgentContent("coder", "coder says hi")
	cv.AddAgentContent("reviewer", "reviewer says lgtm")
	cv.AddAgentContent("coder", "coder continues")

	// No filter: both visible.
	both := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(both, "coder says hi") || !strings.Contains(both, "reviewer says lgtm") {
		t.Fatalf("unfiltered view missing agents:\n%s", both)
	}

	// Filter to coder: only coder blocks visible.
	cv.SetAgentFilter("coder")
	filtered := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(filtered, "coder says hi") || !strings.Contains(filtered, "coder continues") {
		t.Errorf("coder filter dropped coder blocks:\n%s", filtered)
	}
	if strings.Contains(filtered, "reviewer says lgtm") {
		t.Errorf("coder filter leaked reviewer block:\n%s", filtered)
	}

	// Clear filter: both visible again.
	cv.SetAgentFilter("")
	again := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(again, "reviewer says lgtm") {
		t.Errorf("clearing filter did not restore reviewer block:\n%s", again)
	}
}

// TestChatViewportAgentFilter_IncludesToolWidgets is the RED regression for
// bugs.md: per-agent tabs must show the agent's tool widgets, not only text
// blocks.
func TestChatViewportAgentFilter_IncludesToolWidgets(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddAgentToolExecution("coder", "write", `{"path":"x"}`)
	tc.SetStatus(ToolSuccess)
	cv.AddAgentContent("coder", "Done.")
	cv.AddAgentContent("reviewer", "reviewer note")

	cv.SetAgentFilter("coder")
	rendered := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(rendered, "write") {
		t.Errorf("coder filter should show coder tool widget:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Done.") {
		t.Errorf("coder filter should show coder content:\n%s", rendered)
	}
	if strings.Contains(rendered, "reviewer note") {
		t.Errorf("coder filter should not show reviewer content:\n%s", rendered)
	}
}

// TestChatViewportAgentContentReconcile_ToFullText is the RED→GREEN test for
// R5: after partial streaming, snapping the content widget to the
// authoritative full text repairs any gaps dropped by the live fanout.
func TestChatViewportAgentContentReconcile_ToFullText(t *testing.T) {
	cv := NewChatViewport()
	cv.AddAgentContent("coder", "answer ") // simulates a dropped-chunk gap
	cv.UpdateAgentContent("coder", "answer text") // reconcile to full text

	rendered := ansi.Strip(strings.Join(cv.Render(80), "\n"))
	if !strings.Contains(rendered, "answer text") {
		t.Errorf("reconciled content missing full text:\n%s", rendered)
	}
	if strings.Contains(rendered, "answer ") && !strings.Contains(rendered, "answer text") {
		t.Errorf("content not reconciled to full text:\n%s", rendered)
	}
}
