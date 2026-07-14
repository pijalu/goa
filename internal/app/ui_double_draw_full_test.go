// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// TestUIScenario_NoDoubleDraw regresses the reported double-draw issue where
// chat content (user message, thinking blocks, tool results, and final answer)
// was rendered twice on the screen. The sequence replays the essential events
// from the exported session.
func TestUIScenario_NoDoubleDraw(t *testing.T) {
	sc := newUIScenario(t, 150, 29)

	msg := "can you try to run a simple hello world python to test the tooling ?"

	sc.engine.ApplySync(func() {
		sc.chat.AddSystemMessage("⟡ No AGENTS.md context file found")
		sc.chat.AddSystemMessage("⟡ 9 skills (1 inline, 8 forced inline · mode: inline)")
		sc.chat.AddSystemMessage("⟡ Connected to OpenCode Zen Go (deepseek-v4-flash).")
		sc.chat.AddSystemMessage("> /model\n/model")
		sc.chat.AddSystemMessage("✓ /model completed successfully")
		sc.chat.AddFlashMessage("⚡ Switched to model: google/gemma-4-e4b")
		sc.chat.AddUserMessage(msg)
	})
	sc.engine.RenderNow()
	sc.film.Capture("after_submit", sc.engine.AgentFrame(), sc.status.Text())

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "The user wants to test the available Python execution tool. I should use the `python` tool with a simple \"Hello, World!\" program."})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "python", ToolInput: `{"code":"print(\"Hello, World!\")"}`, ToolCallID: "c1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult, ToolName: "python", ToolCallID: "c1", Text: "Hello, World!\n"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: "Sending request..."})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "The user asked me to run a simple \"hello world\" python script to test the tooling. I have already executed the following tool call:\n`call:python{code:print(\"Hello, World!\")}`\nThe response confirms that the tool ran successfully and printed \"Hello, World!\".\n\nNow I need to confirm this successful execution to the user."})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Successfully ran a simple Python script using the `python` tool. The output was:\n\n```\nHello, World!\n```\n\nThe tooling appears functional for executing basic Python code. Let me know what you'd like to test next!"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	film := sc.filmstrip()
	t.Logf("filmstrip:\n%s", film.Render())

	checkNotDuplicated := func(label, text string) {
		for i, s := range film.Frames() {
			visible := strings.Join(s.Frame.Visible, "\n")
			stripped := ansi.Strip(visible)
			count := strings.Count(stripped, text)
			if count > 1 {
				t.Errorf("step %d (%s): %q rendered %d times; visible:\n%s", i, s.Label, text, count, stripped)
			}
		}
	}

	checkNotDuplicated("user message", msg)
	checkNotDuplicated("first thinking", "The user wants to test the available Python execution tool")
	checkNotDuplicated("tool command", "✓ >>> print(\"Hello, World!\")")
	checkNotDuplicated("second thinking", "The user asked me to run a simple")
	checkNotDuplicated("final answer", "Successfully ran a simple Python script using the `python` tool")
}
