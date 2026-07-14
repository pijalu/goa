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
	defer engine.Stop()

	msg := "can you try to run a simple hello world python to test the tooling ?"

	chat.AddSystemMessage("⟡ No AGENTS.md context file found")
	chat.AddSystemMessage("⟡ 9 skills (1 inline, 8 forced inline · mode: inline)")
	chat.AddSystemMessage("⟡ Connected to OpenCode Zen Go (deepseek-v4-flash).")
	chat.AddSystemMessage("> /model\n/model")
	chat.AddSystemMessage("✓ /model completed successfully")
	chat.AddFlashMessage("⚡ Switched to model: google/gemma-4-e4b")
	engine.RenderNow()

	chat.AddUserMessage(msg)
	engine.RenderNow()

	status.Show("Thinking...")
	chat.AddThinkingBlock("", true)
	engine.RenderNow()

	chat.UpdateLastMessage("The user wants to test the available Python execution tool. I should use the `python` tool with a simple \"Hello, World!\" program.", ConsoleThinkingBlock)
	engine.RenderNow()

	// Add tool execution widget
	tc := chat.AddToolExecution("python", `{"code":"print(\"Hello, World!\")"}`)
	tc.SetStatus(ToolRunning)
	engine.RenderNow()

	// Complete the tool
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

	// Force scrollback by adding a large follow-up message.
	chat.AddAssistantMessage(strings.Repeat("This is a follow-up paragraph.\n", 50))
	engine.RenderNow()

	emu := NewTermEmulator(29, 150)
	for _, w := range term.writes {
		emu.Process(w)
	}

	// Count occurrences across the visible screen AND the terminal scrollback.
	visibleRows := 0
	for r := 0; r < 29; r++ {
		if strings.Contains(emu.Visible(r), msg) {
			visibleRows++
		}
	}
	scrollbackCount := 0
	for _, line := range emu.Scrollback() {
		if strings.Contains(line, msg) {
			scrollbackCount++
		}
	}
	if visibleRows+scrollbackCount != 1 {
		t.Errorf("user message appears %d times across visible+scrollback (want 1); visibleRows=%d scrollbackCount=%d; screen:\n%s\nscrollback:\n%s",
			visibleRows+scrollbackCount, visibleRows, scrollbackCount, dumpTerm(emu, 29), strings.Join(emu.Scrollback(), "\n"))
	}

	// Also validate the system message for "✓ /model completed successfully" is not duplicated.
	cmdPanel := "✓ /model completed successfully"
	cmdCount := 0
	for r := 0; r < 29; r++ {
		if strings.Contains(emu.Visible(r), cmdPanel) {
			cmdCount++
		}
	}
	for _, line := range emu.Scrollback() {
		if strings.Contains(line, cmdPanel) {
			cmdCount++
		}
	}
	if cmdCount != 1 {
		t.Errorf("system message panel appears %d times across visible+scrollback (want 1); screen:\n%s\nscrollback:\n%s",
			cmdCount, dumpTerm(emu, 29), strings.Join(emu.Scrollback(), "\n"))
	}
}
