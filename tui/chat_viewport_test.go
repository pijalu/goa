// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// viewAt returns the View of the i-th entry via the Model's snapshot ordering.
// Tests assert through the public Model API, not internal fields.
func viewAt(cv *ChatViewport, i int) Component {
	idx := 0
	var found Component
	cv.ForEach(func(e MessageEntry) {
		if idx == i {
			found = e.View
		}
		idx++
	})
	return found
}

func TestUpdateLastMessage_Companion(t *testing.T) {
	cv := NewChatViewport()
	cv.AddSystemMessage("system msg")
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: ""})

	cv.UpdateLastMessage("companion chunk 1", ConsoleCompanionMessage)

	last := cv.LastView([]ConsoleItemType{ConsoleCompanionMessage})
	g, ok := last.(*gutteredComponent)
	if !ok {
		t.Fatalf("expected *gutteredComponent, got %T", last)
	}
	if !renderContains(g.Render(40), "companion chunk 1") {
		t.Errorf("expected 'companion chunk 1', got %v", g.Render(40))
	}
}

func TestUpdateLastMessage_Companion_MultipleChunks(t *testing.T) {
	cv := NewChatViewport()
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: ""})
	cv.UpdateLastMessage("hello", ConsoleCompanionMessage)
	cv.UpdateLastMessage("hello world", ConsoleCompanionMessage)

	g := cv.LastView([]ConsoleItemType{ConsoleCompanionMessage}).(*gutteredComponent)
	if !renderContains(g.Render(40), "hello world") {
		t.Errorf("expected 'hello world', got %v", g.Render(40))
	}
}

func TestUpdateLastMessage_Companion_IgnoresOtherTypes(t *testing.T) {
	cv := NewChatViewport()
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: "initial"})
	cv.AddSystemMessage("system msg")

	cv.UpdateLastMessage("assistant text", ConsoleAssistantMessage)

	g := viewAt(cv, 0).(*gutteredComponent)
	if !renderContains(g.Render(40), "initial") {
		t.Errorf("companion should keep 'initial', got %v", g.Render(40))
	}
}

func TestAddMessage_Companion_WrapsWithGutter(t *testing.T) {
	cv := NewChatViewport()
	cv.AddMessage(&ChatMessage{Type: ConsoleCompanionMessage, Content: "review note"})
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*gutteredComponent); !ok {
		t.Fatalf("expected *gutteredComponent, got %T", viewAt(cv, 0))
	}
}

func TestAddThinkingBlock_CreatesThinkingComponent(t *testing.T) {
	cv := NewChatViewport()
	cv.AddThinkingBlock("", true)
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*thinkingBlock); !ok {
		t.Fatalf("expected *thinkingBlock, got %T", viewAt(cv, 0))
	}
}

func TestUpdateLastMessage_ThinkingBlock(t *testing.T) {
	cv := NewChatViewport()
	cv.AddThinkingBlock("", true)
	cv.UpdateLastMessage("thinking chunk", ConsoleThinkingBlock)

	tb := cv.LastView([]ConsoleItemType{ConsoleThinkingBlock}).(*thinkingBlock)
	if !renderContains(tb.Render(40), "thinking chunk") {
		t.Errorf("expected 'thinking chunk', got %v", tb.Render(40))
	}
}

func TestAddToolExecution_NoSeparatorBeforeFirst(t *testing.T) {
	cv := NewChatViewport()
	cv.AddToolExecution("read", `{"path":"a.txt"}`)
	if cv.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", cv.Len())
	}
}

func TestAddToolExecution_ConsecutiveTools(t *testing.T) {
	cv := NewChatViewport()
	cv.AddToolExecution("read", `{"path":"a.txt"}`)
	cv.AddToolExecution("read", `{"path":"b.txt"}`)
	if cv.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", cv.Len())
	}
}

func TestAddBashExecution_UsesUnifiedToolComponent(t *testing.T) {
	cv := NewChatViewport()
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	if tc == nil || tc.toolName != "bash" {
		t.Fatalf("expected bash tool execution, got %+v", tc)
	}
}

func TestRemoveLastMessageOfType_RemovesMatchingType(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	cv.AddAssistantMessage("partial assistant")

	if !cv.RemoveLastMessageOfType(ConsoleAssistantMessage) {
		t.Fatal("expected removal to return true")
	}
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry after removal, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*userMessage); !ok {
		t.Errorf("expected remaining userMessage, got %T", viewAt(cv, 0))
	}
}

func TestRemoveLastMessageOfType_KeepsNonMatchingType(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	if cv.RemoveLastMessageOfType(ConsoleAssistantMessage, ConsoleThinkingBlock) {
		t.Fatal("expected false for non-matching type")
	}
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
}

func TestRemoveLastMessageOfType_RemovesThinkingBlock(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	cv.AddThinkingBlock("partial thinking", true)
	if !cv.RemoveLastMessageOfType(ConsoleThinkingBlock) {
		t.Fatal("expected removal to return true")
	}
	if cv.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cv.Len())
	}
}

func TestUpdateLastMessage_ThinkingToAssistantTransition(t *testing.T) {
	cv := NewChatViewport()
	cv.AddThinkingBlock("", true)
	cv.UpdateLastMessage("thinking", ConsoleThinkingBlock)
	cv.AddAssistantMessage("")
	cv.UpdateLastMessage("assistant text", ConsoleAssistantMessage)

	if cv.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", cv.Len())
	}
	if _, ok := viewAt(cv, 0).(*thinkingBlock); !ok {
		t.Errorf("expected first thinkingBlock, got %T", viewAt(cv, 0))
	}
	if _, ok := viewAt(cv, 1).(*assistantMessage); !ok {
		t.Errorf("expected second assistantMessage, got %T", viewAt(cv, 1))
	}
}

// TestChatViewport_SnapshotExposesStructuredData verifies the Model exposes a
// pure-data snapshot for AI agent tooling (no View references, no ANSI).
func TestChatViewport_SnapshotExposesStructuredData(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hi")
	cv.AddAssistantMessage("hello")
	snap := cv.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2, got %d", len(snap))
	}
	if snap[0].Type != ConsoleUserMessage || snap[0].Text != "hi" {
		t.Errorf("entry 0 wrong: %+v", snap[0])
	}
	if snap[1].Type != ConsoleAssistantMessage || snap[1].Text != "hello" {
		t.Errorf("entry 1 wrong: %+v", snap[1])
	}
	if snap[1].ID <= snap[0].ID {
		t.Errorf("IDs must be monotonic: %d then %d", snap[0].ID, snap[1].ID)
	}
}

func TestChatViewport_InvalidateRunningToolWidgets(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("user msg")
	tc := cv.AddToolExecution("bash", `{"command":"ls"}`)
	tc.SetStatus(ToolRunning)

	cv.Render(80)
	genBefore := cv.generation
	cacheBefore := cv.renderCache.lines

	currentSpinnerFrame = "1"
	cv.InvalidateRunningToolWidgets()
	cv.Render(80) // patch happens on the render goroutine
	rendered1 := tc.Render(80)
	if len(rendered1) < 2 {
		t.Fatalf("tool widget rendered fewer than 2 lines: %v", rendered1)
	}

	currentSpinnerFrame = "2"
	cv.InvalidateRunningToolWidgets()
	cv.Render(80) // patch happens on the render goroutine
	rendered2 := tc.Render(80)
	if len(rendered2) < 2 {
		t.Fatalf("tool widget rendered fewer than 2 lines: %v", rendered2)
	}
	if rendered1[1] == rendered2[1] {
		t.Errorf("running tool widget header did not change after spinner tick: %q", rendered1[1])
	}
	if cv.generation != genBefore {
		t.Errorf("InvalidateRunningToolWidgets should not increment generation; got %d, want %d", cv.generation, genBefore)
	}
	if &cv.renderCache.lines[0] != &cacheBefore[0] {
		t.Errorf("InvalidateRunningToolWidgets should patch the frame cache in place, not reallocate")
	}

	// Non-running tools should not be invalidated again.
	tc.SetStatus(ToolSuccess)
	genBefore = cv.generation
	cv.InvalidateRunningToolWidgets()
	cv.Render(80)
	if cv.generation != genBefore {
		t.Errorf("non-running tools should not be invalidated")
	}
}

func TestChatViewport_SteeringPending_StaysAtBottom(t *testing.T) {
	cv := NewChatViewport()
	cv.AddUserMessage("hello")
	cv.AddSteeringPending("fix this")
	if cv.pendingSteering < 0 {
		t.Fatal("expected pending steering entry")
	}
	cv.AddAssistantMessage("working...")
	if cv.pendingSteering < 0 {
		t.Fatal("pending steering index should remain after adding message")
	}
	if cv.entries[cv.pendingSteering].Data.Type != ConsoleSteeringPending {
		t.Errorf("last entry should be steering pending, got %d", cv.entries[cv.pendingSteering].Data.Type)
	}
	if cv.entries[cv.pendingSteering].Data.Text != "fix this" {
		t.Errorf("pending text wrong: %q", cv.entries[cv.pendingSteering].Data.Text)
	}
	cv.ClearSteeringPending()
	if cv.pendingSteering >= 0 {
		t.Error("pending steering should be cleared")
	}
}

func renderContains(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}
