// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// TestEchoCommandResult_MidStreamStartsNewBlockAfterResult is the regression
// test for the /goal:list-during-streaming CPU storm: a screen-filling
// command result echoed mid-stream must NOT leave the streaming block buried
// under it. Previously the stream kept growing that buried block, and every
// chunk forced the compositor's mid-transcript scrollback-reset path — CPU
// >100% until a new block finally started after the result.
//
// Fix: echoCommandResult finalizes the in-progress stream block BEFORE the
// result lands, so the result is appended after a complete block and the next
// stream chunk starts a fresh block AFTER the result (bottom append — the
// fast O(viewport) path).
func TestEchoCommandResult_MidStreamStartsNewBlockAfterResult(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	feedAssistant(app, "First part of the answer.", " More of the stream.")
	app.echoCommandResult(&core.RouteResult{Command: &echoableCommand{}}, "/goal:list", "## Goals\n\n**1. [active] goal-01**\n\nA very long objective\n")
	feedAssistant(app, "Second part after the command result.")
	assertStreamBlocks(t, app.subs.chat.Messages())
}

func feedAssistant(app *App, texts ...string) {
	for _, text := range texts {
		app.handleAssistantContent(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: text})
	}
}

func assertStreamBlocks(t *testing.T, msgs []*tui.ChatMessage) {
	assistant := assistantMessages(msgs)
	result, first, second := streamMessageIndices(msgs)
	if len(assistant) != 2 {
		t.Fatalf("expected 2 assistant blocks, got %d: %v", len(assistant), assistant)
	}
	assertAssistantText(t, assistant)
	if result < 0 {
		t.Fatal("command result system message not found")
	}
	if !(first < result && result < second) {
		t.Errorf("result ordering first=%d result=%d second=%d", first, result, second)
	}
}

func assistantMessages(msgs []*tui.ChatMessage) []string {
	var out []string
	for _, msg := range msgs {
		if msg.Type == tui.ConsoleAssistantMessage {
			out = append(out, msg.Content)
		}
	}
	return out
}

func streamMessageIndices(msgs []*tui.ChatMessage) (result, first, second int) {
	result, first, second = -1, -1, -1
	for i, msg := range msgs {
		if msg.Type == tui.ConsoleSystemMessage && strings.Contains(msg.Content, "## Goals") {
			result = i
		}
		if msg.Type == tui.ConsoleAssistantMessage {
			if first < 0 {
				first = i
			} else {
				second = i
			}
		}
	}
	return
}

func assertAssistantText(t *testing.T, assistant []string) {
	if !strings.Contains(assistant[0], "First part") || !strings.Contains(assistant[0], "More of the stream") {
		t.Errorf("first block = %q", assistant[0])
	}
	if !strings.Contains(assistant[1], "Second part") {
		t.Errorf("second block = %q", assistant[1])
	}
}

// TestEchoCommandResult_NoActiveStreamIsNoOp verifies the fix does not disturb
// the common case: echoing a command result when no stream is active leaves
// the conversation unchanged except for the result itself.
func TestEchoCommandResult_NoActiveStreamIsNoOp(t *testing.T) {
	app := New(testSubsystems())
	app.subs.tuiEngine = tui.NewTUI(tui.NewProcessTerminal())
	app.echoCommandResult(&core.RouteResult{Command: &echoableCommand{}}, "/status", "idle")
	msgs := app.subs.chat.Messages()
	if len(msgs) != 2 { // "> /status" + "idle"
		t.Fatalf("expected 2 messages, got %d: %v", len(msgs), msgs)
	}
}

// echoableCommand is a non-internal command so echoCommandResult echoes it.
type echoableCommand struct{}

func (c *echoableCommand) Name() string                              { return "echoable" }
func (c *echoableCommand) Aliases() []string                         { return nil }
func (c *echoableCommand) ShortHelp() string                         { return "echoable" }
func (c *echoableCommand) LongHelp() string                          { return "echoable" }
func (c *echoableCommand) Run(ctx core.Context, args []string) error { return nil }
