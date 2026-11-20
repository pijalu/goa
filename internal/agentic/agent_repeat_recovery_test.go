// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// invalidQuestionTool reproduces the export's native, syntactically valid but
// semantically invalid ask_user_question calls without importing the tools
// package (which would create a test import cycle).
type invalidQuestionTool struct{}

func (invalidQuestionTool) Schema() ToolSchema {
	return ToolSchema{Name: "ask_user_question", Description: "test clarification tool"}
}

func (invalidQuestionTool) Execute(string) (string, error) {
	return "", errors.New("missing_question")
}

func (invalidQuestionTool) IsRetryable(error) bool { return false }

// repeatRoundProvider emits the same invalid call once per ordinary round,
// matching the Kimi export rather than placing every repeat in one batch. A
// NoTools recovery round, or the fifth ordinary round, produces a final answer.
type repeatRoundProvider struct {
	api  provider.Api
	ctxs []provider.Context
}

func (p *repeatRoundProvider) API() provider.Api { return p.api }

func (p *repeatRoundProvider) Stream(_ provider.Model, ctx provider.Context, _ provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.ctxs = append(p.ctxs, ctx)
	round := len(p.ctxs) - 1
	stream := provider.NewAssistantMessageEventStream(8)
	go func() {
		if ctx.NoTools || round >= 5 {
			stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "recovered"})
			stream.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "recovered"}},
				StopReason: provider.StopReasonEndTurn,
			})
			return
		}
		stream.Push(toolCallEvent(fmt.Sprintf("repeat-%d", round), "ask_user_question", `{"questions":[]}`))
		stream.End(&provider.AssistantMessage{StopReason: provider.StopReasonToolCall})
	}()
	return stream, nil
}

func (p *repeatRoundProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.BuildSimpleOptions(model, opts))
}

// TestConsecutiveRepeat_EntersBoundedTextOnlyRecovery pins the 2026-11-20
// export failure: with a limit of two, call three must be the only hard-loop
// placeholder before recovery. Previously calls four and five repeated the
// same hint in ordinary tool-enabled rounds.
func TestConsecutiveRepeat_EntersBoundedTextOnlyRecovery(t *testing.T) {
	p := &repeatRoundProvider{api: provider.Api(fmt.Sprintf("repeat-recovery-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)
	agent := NewAgent(Config{
		Model:                    testModel(p.API()),
		SystemPrompt:             "test",
		Logger:                   NewLogger(Error),
		Tools:                    []Tool{invalidQuestionTool{}},
		MaxToolRepeatConsecutive: 2,
		MaxToolRepeatTotal:       0,
		MaxToolCalls:             0,
	})

	obs := runAgentCollectingEvents(t, agent, "clarify")
	assertRepeatRecoveryEvents(t, obs.Events())
	assertRepeatRecoveryContexts(t, p.ctxs)

	// A genuine new user turn starts with the ordinary tool surface again.
	if _, err := agent.RunAndCollect(context.Background(), "try again"); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if len(p.ctxs) < 5 {
		t.Fatal("second turn did not reach the provider")
	}
	if p.ctxs[4].NoTools {
		t.Error("repeat-recovery latch leaked into the next user turn")
	}
}

func assertRepeatRecoveryEvents(t *testing.T, events []OutputEvent) {
	t.Helper()
	realErrors, hardLoops := countRepeatRecoveryEvents(events)
	if realErrors != 2 {
		t.Errorf("real invalid calls = %d, want 2", realErrors)
	}
	if hardLoops != 1 {
		t.Errorf("hard-loop results = %d, want 1", hardLoops)
	}
}

func countRepeatRecoveryEvents(events []OutputEvent) (realErrors, hardLoops int) {
	for _, event := range events {
		if event.Type != EventToolResult {
			continue
		}
		switch {
		case strings.Contains(event.Text, "Loop guardrail"):
			hardLoops++
		case strings.Contains(event.Text, "missing_question"):
			realErrors++
		}
	}
	return realErrors, hardLoops
}

func assertRepeatRecoveryContexts(t *testing.T, ctxs []provider.Context) {
	t.Helper()
	if len(ctxs) != 4 {
		t.Fatalf("stream rounds = %d, want 3 ordinary rounds + 1 recovery round", len(ctxs))
	}
	for i := 0; i < 3; i++ {
		if ctxs[i].NoTools {
			t.Errorf("ordinary round %d unexpectedly collapsed", i)
		}
	}
	if !ctxs[3].NoTools {
		t.Error("round after hard consecutive-repeat guard must be text-only")
	}
}
