// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestUserMessage_NoDoubleDraw uses the faithful terminal emulator to
// reproduce the reported double-draw where the same user message appeared
// twice in the chat viewport after a model-switch flash and the start of
// agent thinking.
func TestUserMessage_NoDoubleDraw(t *testing.T) {
	term, chat, status, engine := setupDoubleDrawTest(t)
	defer engine.Stop()

	msg := "can you try to run a simple hello world python to test the tooling ?"
	driveModelSwitchScenario(chat, status, term, engine)
	replayAndAssertNoDuplicates(t, term, msg, 29)
}

// setupDoubleDrawTest builds a TUI engine with the chat, status, footer, and
// editor components used by the double-draw regression test.
func setupDoubleDrawTest(t *testing.T) (*fakeTerminal, *ChatViewport, *StatusMsg, *TUI) {
	t.Helper()
	term := &fakeTerminal{w: 150, h: 29}
	engine := NewTUI(term)
	chat := NewChatViewport()
	status := NewStatusMsg()
	footer := NewFooter()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(status)
	engine.AddChild(footer)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return term, chat, status, engine
}

// driveModelSwitchScenario reproduces the sequence reported to trigger the
// double draw: model switch, user message, multiple thinking/tool/answering
// cycles, and a final large assistant message that forces scrollback.
func driveModelSwitchScenario(chat *ChatViewport, status *StatusMsg, term *fakeTerminal, engine *TUI) {
	chat.AddSystemMessage("⟡ No AGENTS.md context file found")
	chat.AddSystemMessage("⟡ 9 skills (1 inline, 8 forced inline · mode: inline)")
	chat.AddSystemMessage("⟡ Connected to OpenCode Zen Go (deepseek-v4-flash).")
	chat.AddSystemMessage("> /model\n/model")
	chat.AddSystemMessage("✓ /model completed successfully")
	chat.AddFlashMessage("⚡ Switched to model: google/gemma-4-e4b")
	engine.RenderNow()

	msg := "can you try to run a simple hello world python to test the tooling ?"
	chat.AddUserMessage(msg)
	engine.RenderNow()

	status.Show("Thinking...")
	chat.AddThinkingBlock("", true)
	engine.RenderNow()

	chat.UpdateLastMessage("The user wants to test the available Python execution tool. I should use the `python` tool with a simple \"Hello, World!\" program.", ConsoleThinkingBlock)
	engine.RenderNow()

	tc := chat.AddToolExecution("python", `{"code":"print(\"Hello, World!\")"}`)
	tc.SetStatus(ToolRunning)
	engine.RenderNow()

	tc.SetOutput("Hello, World!\n")
	tc.SetStatus(ToolSuccess)
	tc.SetPartial(false)
	engine.RenderNow()

	status.Show("Sending request...")
	engine.RenderNow()

	status.Show("Thinking...")
	chat.AddThinkingBlock("", true)
	engine.RenderNow()
	chat.UpdateLastMessage("The user asked me to run a simple \"hello world\" python script to test the tooling. I have already executed the following tool call:\n`call:python{code:print(\"Hello, World!\")}`\nThe response confirms that the tool ran successfully and printed \"Hello, World!\".\n\nNow I need to confirm this successful execution to the user.", ConsoleThinkingBlock)
	engine.RenderNow()

	status.Show("Answering...")
	chat.AddAssistantMessage("Successfully ran a simple Python script using the `python` tool. The output was:\n\n```\nHello, World!\n```\n\nThe tooling appears functional for executing basic Python code. Let me know what you'd like to test next!")
	engine.RenderNow()

	chat.AddAssistantMessage(strings.Repeat("This is a follow-up paragraph.\n", 50))
	engine.RenderNow()
}

// replayAndAssertNoDuplicates replays the emitted bytes through the faithful
// emulator and asserts that the user message and the system message each
// appear exactly once across the visible screen and scrollback.
func replayAndAssertNoDuplicates(t *testing.T, term *fakeTerminal, msg string, h int) {
	t.Helper()
	emu := NewTermEmulator(29, 150)
	for _, w := range term.writes {
		emu.Process(w)
	}

	visibleRows := countVisibleOccurrences(emu, msg, h)
	scrollbackCount := countScrollbackOccurrences(emu, msg)
	if visibleRows+scrollbackCount != 1 {
		t.Errorf("user message appears %d times across visible+scrollback (want 1); visibleRows=%d scrollbackCount=%d; screen:\n%s\nscrollback:\n%s",
			visibleRows+scrollbackCount, visibleRows, scrollbackCount, dumpTerm(emu, h), strings.Join(emu.Scrollback(), "\n"))
	}

	cmdPanel := "✓ /model completed successfully"
	cmdCount := countVisibleOccurrences(emu, cmdPanel, h) + countScrollbackOccurrences(emu, cmdPanel)
	if cmdCount != 1 {
		t.Errorf("system message panel appears %d times across visible+scrollback (want 1); screen:\n%s\nscrollback:\n%s",
			cmdCount, dumpTerm(emu, h), strings.Join(emu.Scrollback(), "\n"))
	}
}

func countVisibleOccurrences(emu *TermEmulator, msg string, h int) int {
	count := 0
	for r := 0; r < h; r++ {
		if strings.Contains(emu.Visible(r), msg) {
			count++
		}
	}
	return count
}

func countScrollbackOccurrences(emu *TermEmulator, msg string) int {
	count := 0
	for _, line := range emu.Scrollback() {
		if strings.Contains(line, msg) {
			count++
		}
	}
	return count
}
