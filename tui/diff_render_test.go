// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// stripANSI removes ANSI escape sequences for easier assertions.
func stripANSI(s string) string { return ansi.Strip(s) }

// TestTUI_DiffRender_ToolPendingToSuccess verifies that when a component grows
// from a pending tool call to a success result, the old pending header line is
// overwritten rather than left on screen.
func TestTUI_DiffRender_ToolPendingToSuccess(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	cv.AddSystemMessage("hello")
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	// Start() does the first full render with hello; clear writes to capture diffs.
	term.writes = nil

	// Add pending tool execution.
	tc := cv.AddToolExecution("bash", `{"command":"ls -F"}`)
	tc.SetStatus(ToolPending)
	engine.RenderNow()

	pendingWrites := stripANSI(strings.Join(term.writes, ""))
	term.writes = nil

	if !strings.Contains(pendingWrites, "◉ $ ls -F") {
		t.Fatalf("pending render should contain '◉ $ ls -F', got:\n%s", pendingWrites)
	}

	// Transition to success with output.
	tc.SetOutput(".goa/\nDuration: 0.05s\n")
	tc.SetStatus(ToolSuccess)
	tc.SetDuration("0.0s")
	engine.RenderNow()

	successWrites := stripANSI(strings.Join(term.writes, ""))

	// The final screen should not still show the pending icon.
	if strings.Contains(successWrites, "◉ $ ls -F") {
		t.Errorf("success diff render still contains pending header '◉ $ ls -F'; old line was not overwritten:\n%s", successWrites)
	}
	if !strings.Contains(successWrites, "✓ $ ls -F") {
		t.Errorf("success diff render should contain '✓ $ ls -F', got:\n%s", successWrites)
	}
}

// TestTUI_DiffRender_ToolPendingToSuccess_WithPrecedingContent tests the same
// transition when enough preceding content exists that the tool block is
// somewhere in the middle of the buffer, exercising cursor positioning.
// TestTUI_DiffRender_ToolPendingToSuccess_NearViewportBottom forces the
// transition to scroll the terminal (moveTargetRow > prevViewportBottom) so
// the scroll-positioning path in writeDiffOutput is exercised.
func TestTUI_DiffRender_ToolPendingToSuccess_NearViewportBottom(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 5}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < 15; i++ {
		cv.AddSystemMessage("preceding line")
	}
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	term.writes = nil

	tc := cv.AddToolExecution("bash", `{"command":"ls -F"}`)
	tc.SetStatus(ToolPending)
	engine.RenderNow()
	pendingWrites := stripANSI(strings.Join(term.writes, ""))
	term.writes = nil

	if !strings.Contains(pendingWrites, "◉ $ ls -F") {
		t.Fatalf("pending render should contain '◉ $ ls -F', got:\n%s", pendingWrites)
	}

	// Output long enough that the success block pushes beyond the 5-row viewport.
	var outputLines []string
	for i := 1; i <= 10; i++ {
		outputLines = append(outputLines, "file-line")
	}
	out := strings.Join(outputLines, "\n") + "\nDuration: 0.05s\n"
	tc.SetOutput(out)
	tc.SetStatus(ToolSuccess)
	tc.SetDuration("0.0s")
	engine.RenderNow()

	successWrites := stripANSI(strings.Join(term.writes, ""))
	if strings.Contains(successWrites, "◉ $ ls -F") {
		t.Errorf("success diff render still contains pending header after scroll:\n%s", successWrites)
	}
	if !strings.Contains(successWrites, "✓ $ ls -F") {
		t.Errorf("success diff render should contain '✓ $ ls -F', got:\n%s", successWrites)
	}
}

func TestTUI_DiffRender_ToolPendingToSuccess_WithPrecedingContent(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	engine := NewTUI(term)
	cv := NewChatViewport()
	for i := 0; i < 5; i++ {
		cv.AddSystemMessage("preceding line")
	}
	engine.AddChild(cv)

	if err := engine.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer engine.Stop()

	term.writes = nil

	tc := cv.AddToolExecution("bash", `{"command":"ls -F"}`)
	tc.SetStatus(ToolPending)
	engine.RenderNow()
	pendingWrites := stripANSI(strings.Join(term.writes, ""))
	term.writes = nil

	if !strings.Contains(pendingWrites, "◉ $ ls -F") {
		t.Fatalf("pending render should contain '◉ $ ls -F', got:\n%s", pendingWrites)
	}

	tc.SetOutput(".goa/\nDuration: 0.05s\n")
	tc.SetStatus(ToolSuccess)
	tc.SetDuration("0.0s")
	engine.RenderNow()

	successWrites := stripANSI(strings.Join(term.writes, ""))
	if strings.Contains(successWrites, "◉ $ ls -F") {
		t.Errorf("success diff render still contains pending header:\n%s", successWrites)
	}
	if !strings.Contains(successWrites, "✓ $ ls -F") {
		t.Errorf("success diff render should contain '✓ $ ls -F', got:\n%s", successWrites)
	}
}
