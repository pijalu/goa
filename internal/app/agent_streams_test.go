// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/tui"
)

// TestAgentStream_ContentUpdatesExistingBlock verifies that a sequence of
// content deltas for the same agent updates a single chat entry rather than
// creating multiple entries with repeated text.
func TestAgentStream_ContentUpdatesExistingBlock(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	deltas := []string{
		"As the orchestrator, my first step is to understand the scope of the request.",
		" The objective is to provide a clear summary of this project.",
		"\n\nTo proceed, I need more information about the project structure and its goals.",
	}

	for _, d := range deltas {
		a.handleAgentContent(agentID, d, false)
	}

	msgs := a.subs.chat.Messages()
	agentMsgCount := 0
	for _, m := range msgs {
		if m.Type == tui.ConsoleAgentMessage {
			agentMsgCount++
		}
	}
	if agentMsgCount != 1 {
		t.Errorf("expected exactly 1 agent message entry, got %d", agentMsgCount)
	}
}

// TestAgentStream_ThinkingThenContentDoesNotRepeat verifies that switching
// from thinking to content ends the thinking segment and creates a single
// content block that accumulates subsequent deltas.
func TestAgentStream_ThinkingThenContentDoesNotRepeat(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	// Emit some thinking first.
	a.handleAgentThinking(agentID, "The user wants me to summarize the project.", true)
	a.handleAgentThinking(agentID, " I need more context.", true)

	// Then content deltas.
	deltas := []string{
		"As the orchestrator,",
		" my first step is to understand the scope of the request.",
	}
	for _, d := range deltas {
		a.handleAgentContent(agentID, d, false)
	}

	msgs := a.subs.chat.Messages()
	thinkingCount := 0
	agentMsgCount := 0
	for _, m := range msgs {
		switch m.Type {
		case tui.ConsoleThinkingBlock:
			thinkingCount++
		case tui.ConsoleAgentMessage:
			agentMsgCount++
		}
	}
	if thinkingCount != 1 {
		t.Errorf("expected exactly 1 thinking block, got %d", thinkingCount)
	}
	if agentMsgCount != 1 {
		t.Errorf("expected exactly 1 agent message entry after thinking, got %d", agentMsgCount)
	}
}

// TestAgentStream_PartialToolCallUpdatesExistingWidget verifies that streaming
// (IsDelta) tool call updates for the same call_id update the existing widget
// instead of creating duplicate widgets.
func TestAgentStream_PartialToolCallUpdatesExistingWidget(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	callID := "call_123"
	a.handleAgentToolCall(agentID, "write", `{"path":"test.go",`, callID, true)
	a.handleAgentToolCall(agentID, "write", `{"path":"test.go","content":"package`, callID, true)
	a.handleAgentToolCall(agentID, "write", `{"path":"test.go","content":"package main"}`, callID, false)

	msgs := a.subs.chat.Messages()
	toolCount := 0
	for _, m := range msgs {
		if m.Type == tui.ConsoleToolCall {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected exactly 1 tool call widget for streaming updates, got %d", toolCount)
	}
}

// TestAgentStream_FullToolCallThenResultDoesNotDuplicate verifies that a
// final non-delta tool call followed by a result leaves a single tool widget.
func TestAgentStream_FullToolCallThenResultDoesNotDuplicate(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	callID := "call_456"
	a.handleAgentToolCall(agentID, "bash", `{"command":"ls"}`, callID, false)
	a.handleAgentToolResult(agentID, callID, "file.txt", true)

	msgs := a.subs.chat.Messages()
	toolCount := 0
	for _, m := range msgs {
		if m.Type == tui.ConsoleToolCall {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected exactly 1 tool call widget, got %d", toolCount)
	}
}

// TestAgentStream_DeltaToolCallFollowedByFinalCreatesOneWidget verifies that a
// partial tool call stream that transitions to a final call creates exactly
// one widget.
func TestAgentStream_DeltaToolCallFollowedByFinalCreatesOneWidget(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	callID := "call_789"
	a.handleAgentToolCall(agentID, "edit", `{"path":"x.go",`, callID, true)
	a.handleAgentToolCall(agentID, "edit", `{"path":"x.go","operation":"replace"}`, callID, false)
	a.handleAgentToolResult(agentID, callID, "ok", true)

	msgs := a.subs.chat.Messages()
	toolCount := 0
	for _, m := range msgs {
		if m.Type == tui.ConsoleToolCall {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected exactly 1 tool call widget after final transition, got %d", toolCount)
	}
}

// TestAgentStream_DeltaToolCallWithoutCallIDCreatesOneWidget verifies that
// streaming tool calls without a call_id still update a single active widget.
func TestAgentStream_DeltaToolCallWithoutCallIDCreatesOneWidget(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	a.handleAgentToolCall(agentID, "write", `{"path":"a.txt"`, "", true)
	a.handleAgentToolCall(agentID, "write", `{"path":"a.txt","content":"hello"}`, "", true)
	a.handleAgentToolCall(agentID, "write", `{"path":"a.txt","content":"hello world"}`, "", false)

	msgs := a.subs.chat.Messages()
	toolCount := 0
	for _, m := range msgs {
		if m.Type == tui.ConsoleToolCall {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected exactly 1 tool call widget for call_id-less stream, got %d", toolCount)
	}
}

// TestAgentStream_NonDeltaToolCallWithExistingActiveWidgetDoesNotDuplicate
// verifies that if a final tool call arrives for a call_id that already has a
// pending widget from prior deltas, no duplicate widget is created.
func TestAgentStream_NonDeltaToolCallWithExistingWidgetDoesNotDuplicate(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{
		chat:          tui.NewChatViewport(),
		agentStreams:  newAgentStreamRegistry(),
	}

	agentID := "orch-1"
	role := "orchestrator"

	a.beginAgentStream(role, agentID)

	callID := "call_dup"
	a.handleAgentToolCall(agentID, "write", `{"path":"dup.go"`, callID, true)
	// Final non-delta arrival for the same call_id.
	a.handleAgentToolCall(agentID, "write", `{"path":"dup.go","content":"x"}`, callID, false)

	msgs := a.subs.chat.Messages()
	toolCount := 0
	for _, m := range msgs {
		if m.Type == tui.ConsoleToolCall {
			toolCount++
		}
	}
	if toolCount != 1 {
		t.Errorf("expected exactly 1 tool call widget, got %d", toolCount)
	}
}
