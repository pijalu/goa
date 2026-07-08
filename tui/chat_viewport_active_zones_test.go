// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// lastLineContaining returns the index of the last line containing substr, or -1.
func lastLineContaining(lines []string, substr string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], substr) {
			return i
		}
	}
	return -1
}

func stripAllLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

// TestChatViewport_ToolsStayInChronologicalOrder verifies the FIFO layout
// from bugs.md: a tool widget appears in the order it was created. While it is
// running it sits below earlier entries and above later entries; once
// finalized, later entries continue to render below it.
func TestChatViewport_ToolsStayInChronologicalOrder(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("please run ls")
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	tc.SetStatus(ToolRunning)

	// While running, the tool is below the user message (active at the bottom).
	lines := stripAllLines(cv.Render(80))
	userIdx := lastLineContaining(lines, "please run ls")
	toolIdx := lastLineContaining(lines, "$ ls")
	if userIdx < 0 || toolIdx < 0 {
		t.Fatalf("user/tool not found:\n%s", strings.Join(lines, "\n"))
	}
	if toolIdx <= userIdx {
		t.Errorf("running tool (%d) should be below user (%d):\n%s", toolIdx, userIdx, strings.Join(lines, "\n"))
	}

	// Finalize the tool; a subsequent assistant message appears at the bottom,
	// completed tool scrolled up above it.
	tc.SetStatus(ToolSuccess)
	cv.AddAssistantMessage("all done now")
	lines = stripAllLines(cv.Render(80))
	toolIdx = lastLineContaining(lines, "$ ls")
	assistantIdx := lastLineContaining(lines, "all done")
	if assistantIdx < 0 {
		t.Fatalf("assistant not found:\n%s", strings.Join(lines, "\n"))
	}
	if assistantIdx <= toolIdx {
		t.Errorf("assistant (%d) should be below completed tool (%d):\n%s", assistantIdx, toolIdx, strings.Join(lines, "\n"))
	}
}

// TestChatViewport_RunningToolStaysInChronologicalOrder exercises the FIFO
// layout: a running tool that is NOT the last-appended entry stays in its
// chronological position; later entries render below it.
func TestChatViewport_RunningToolStaysInChronologicalOrder(t *testing.T) {
	cv := NewChatViewport()
	// Running tool (active), then an assistant message that gets finalized
	// (inactive) when a non-streaming entry is appended afterwards.
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	tc.SetStatus(ToolRunning)
	cv.AddAssistantMessage("interim results")
	cv.AddToolResult("tool output") // finalizes the assistant (streamingIdx=-1)

	lines := stripAllLines(cv.Render(80))
	toolIdx := lastLineContaining(lines, "$ ls")
	assistantIdx := lastLineContaining(lines, "interim results")
	if toolIdx < 0 {
		t.Fatalf("running tool not found:\n%s", strings.Join(lines, "\n"))
	}
	if assistantIdx < 0 {
		t.Fatalf("assistant not found:\n%s", strings.Join(lines, "\n"))
	}
	if toolIdx >= assistantIdx {
		t.Errorf("running tool (%d) should be above inactive assistant (%d):\n%s", toolIdx, assistantIdx, strings.Join(lines, "\n"))
	}
}

// TestChatViewport_FIFOFastPathPreserved is the same smoke test as
// StreamingFastPathPreserved but named to clarify that removing the active-zone
// sort did not regress the streaming fast path.
func TestChatViewport_StreamingFastPathPreserved(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hi")
	cv.AddAssistantMessage("")
	for _, chunk := range []string{"Hello", "Hello, world", "Hello, world!"} {
		cv.UpdateLastMessage(chunk, ConsoleAssistantMessage)
	}
	lines := stripAllLines(cv.Render(80))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Hello, world!") {
		t.Fatalf("final streaming chunk lost:\n%s", joined)
	}
	// The streaming content must be the last non-empty line (trailing spacer
	// blank lines from withSpacers are expected after it).
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastNonEmpty = strings.TrimSpace(lines[i])
			break
		}
	}
	if !strings.Contains(lastNonEmpty, "Hello, world!") {
		t.Errorf("streaming content should be the last non-empty line; got %q:\n%s", lastNonEmpty, joined)
	}
}
