// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestStreaming_NoGhosting_FaithfulEmulator is the regression test for the
// streaming "ghosting" artifact (repeated partial copies of a markdown heading
// stacked on screen during streaming).
//
// Root cause: full-width-padded lines trigger the terminal's DEC deferred
// auto-wrap, which desynced the Compositor's RELATIVE cursor moves (CUU/CUD)
// from the real cursor, so updated rows were written at drifting positions and
// old pixels remained. The fix is ABSOLUTE cursor positioning (CUP) for every
// changed line in the Compositor.
//
// This test uses a FAITHFUL terminal emulator (term_emulator_test.go) that
// models per-cell columns and deferred auto-wrap — the coarse screenEmulator
// could not catch this. It doubles as the agent's "what is actually on screen"
// reader (AgentView).
func TestStreaming_NoGhosting_FaithfulEmulator(t *testing.T) {
	term := &fakeTerminal{w: 60, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	chat.AddUserMessage("build it")
	chat.AddAssistantMessage("### 🏗️")
	engine.RenderNow()
	// Stream the heading growing in place (the exact pattern that ghosted).
	for _, s := range []string{
		"### 🏗️ Architecture",
		"### 🏗️ Architecture &",
		"### 🏗️ Architecture & Components",
	} {
		chat.UpdateLastMessage(s, ConsoleAssistantMessage)
		engine.RenderNow()
	}

	emu := newTermEmulator(10, 60)
	for _, w := range term.writes {
		emu.Process(w)
	}

	headingRows := 0
	for r := 0; r < 10; r++ {
		if strings.Contains(strings.TrimSpace(emu.Visible(r)), "🏗️") {
			headingRows++
		}
	}
	if headingRows != 1 {
		t.Errorf("ghosting: %d heading rows visible (want 1); screen:\n%s", headingRows, dumpTerm(emu, 10))
	}
}

// TestStreaming_Growth_NoGhosting verifies the same with a growing message
// (heading + appending paragraphs) in a scrolled viewport.
func TestStreaming_Growth_NoGhosting(t *testing.T) {
	term := &fakeTerminal{w: 60, h: 10}
	engine := NewTUI(term)
	chat := NewChatViewport()
	inp := NewEditor()
	engine.AddChild(chat)
	engine.AddChild(inp)
	engine.SetFocus(inp)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer engine.Stop()

	for i := 0; i < 20; i++ {
		chat.AddSystemMessage("history line")
	}
	chat.AddAssistantMessage("### 🏗️")
	engine.RenderNow()
	steps := []string{
		"### 🏗️ Architecture",
		"### 🏗️ Architecture & Components",
		"### 🏗️ Architecture & Components\n\nThe system has parts.",
		"### 🏗️ Architecture & Components\n\nThe system has parts.\n\nMore detail follows here.",
	}
	for _, s := range steps {
		chat.UpdateLastMessage(s, ConsoleAssistantMessage)
		engine.RenderNow()
	}

	emu := newTermEmulator(10, 60)
	for _, w := range term.writes {
		emu.Process(w)
	}
	headingRows := 0
	for r := 0; r < 10; r++ {
		if strings.Contains(strings.TrimSpace(emu.Visible(r)), "🏗️") {
			headingRows++
		}
	}
	if headingRows != 1 {
		t.Errorf("ghosting: %d heading rows visible (want 1); screen:\n%s", headingRows, dumpTerm(emu, 10))
	}
}

func dumpTerm(e *termEmulator, h int) string {
	var b strings.Builder
	for r := 0; r < h; r++ {
		b.WriteString(strings.TrimRight(e.Visible(r), " ") + "\n")
	}
	return b.String()
}
