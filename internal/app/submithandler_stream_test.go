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

	// Start a stream: the first content chunk opens the assistant block.
	app.handleAssistantContent(&agentic.OutputEvent{
		Type: agentic.EventContent, State: agentic.StateContent,
		Role: agentic.Assistant, Text: "First part of the answer.",
	})
	app.handleAssistantContent(&agentic.OutputEvent{
		Type: agentic.EventContent, State: agentic.StateContent,
		Role: agentic.Assistant, Text: " More of the stream.",
	})

	// A command with output is echoed mid-stream (e.g. /goal:list filling the
	// screen).
	app.echoCommandResult(&core.RouteResult{Command: &echoableCommand{}}, "/goal:list",
		"## Goals\n\n**1. [active] goal-01**\n\nA very long objective\n")

	// The stream resumes.
	app.handleAssistantContent(&agentic.OutputEvent{
		Type: agentic.EventContent, State: agentic.StateContent,
		Role: agentic.Assistant, Text: "Second part after the command result.",
	})

	msgs := app.subs.chat.Messages()

	// Find the assistant blocks and the command result's system message.
	assistantTexts := []string{}
	commandResultIdx := -1
	for i, m := range msgs {
		switch m.Type {
		case tui.ConsoleAssistantMessage:
			assistantTexts = append(assistantTexts, m.Content)
		case tui.ConsoleSystemMessage:
			if strings.Contains(m.Content, "## Goals") {
				commandResultIdx = i
			}
		}
	}

	// The stream must be split into TWO blocks (before and after the result),
	// never one buried block.
	if len(assistantTexts) != 2 {
		t.Fatalf("expected 2 assistant blocks (before/after command result), got %d: %v", len(assistantTexts), assistantTexts)
	}
	if !strings.Contains(assistantTexts[0], "First part") || !strings.Contains(assistantTexts[0], "More of the stream") {
		t.Errorf("first block missing pre-interrupt text: %q", assistantTexts[0])
	}
	if !strings.Contains(assistantTexts[1], "Second part") {
		t.Errorf("second block missing post-interrupt text: %q", assistantTexts[1])
	}
	if commandResultIdx < 0 {
		t.Fatal("command result system message not found")
	}
	// The command result must sit BETWEEN the two assistant blocks (the stream
	// continues after it, not buried before it).
	firstBlockIdx := -1
	secondBlockIdx := -1
	for i, m := range msgs {
		if m.Type == tui.ConsoleAssistantMessage {
			if firstBlockIdx < 0 {
				firstBlockIdx = i
			} else {
				secondBlockIdx = i
			}
		}
	}
	if !(firstBlockIdx < commandResultIdx && commandResultIdx < secondBlockIdx) {
		t.Errorf("expected command result between the two assistant blocks, got first=%d result=%d second=%d",
			firstBlockIdx, commandResultIdx, secondBlockIdx)
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

func (c *echoableCommand) Name() string                  { return "echoable" }
func (c *echoableCommand) Aliases() []string             { return nil }
func (c *echoableCommand) ShortHelp() string             { return "echoable" }
func (c *echoableCommand) LongHelp() string              { return "echoable" }
func (c *echoableCommand) Run(ctx core.Context, args []string) error { return nil }
