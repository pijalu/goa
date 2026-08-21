// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestAgent_AssistantRepeat_WarnsThenStops(t *testing.T) {
	p := &repeatTextProvider{
		api:     provider.Api(fmt.Sprintf("test-assistant-repeat-%d", testProviderCounter.Add(1))),
		content: "I am stuck.",
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)
	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First run: assistant responds "I am stuck."
	if err := agent.Run(ctx, "help"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Second run: identical response should inject a warning hint.
	if err := agent.Run(ctx, "continue"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	// Third run: identical response again should stop the session.
	err := agent.Run(ctx, "continue")
	if err == nil {
		t.Fatal("expected runaway loop error, got nil")
	}
	if !strings.Contains(err.Error(), "runaway loop detected") {
		t.Errorf("expected runaway loop error, got %v", err)
	}

	history := copyAgentHistory(agent)
	var warnings int
	for _, m := range history {
		if m.Role == System && strings.Contains(m.Content, "Progress has stalled") {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("expected exactly 1 stall warning in history, got %d", warnings)
	}
}

// TestAgent_EmptyAssistantRepeat_Stops verifies that consecutive empty
// assistant responses (no content, no tool calls) are detected as a stall and
// stop the session before the context explodes.
func TestAgent_EmptyAssistantRepeat_Stops(t *testing.T) {
	p := &repeatTextProvider{
		api:     provider.Api(fmt.Sprintf("test-empty-repeat-%d", testProviderCounter.Add(1))),
		content: "",
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agent.Run(ctx, "help"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if err := agent.Run(ctx, "continue"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	err := agent.Run(ctx, "continue")
	if err == nil {
		t.Fatal("expected runaway loop error for empty repeats, got nil")
	}
	if !strings.Contains(err.Error(), "runaway loop detected") {
		t.Errorf("expected runaway loop error, got %v", err)
	}
}

// TestAgent_ToolResultTooLarge_TruncatesWithNotice verifies that an oversized
// successful tool result is truncated with a clear notice so the LLM can adapt
// and the turn can finish without an opaque error.
func TestAgent_UndoLastAssistantMessage_KeepsPreviousTurn(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	agent.history = []Message{
		{Type: Content, Role: System, Content: "test"},
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
		{Type: Content, Role: User, Content: "second question"},
	}

	agent.undoLastAssistantMessage()

	history := agent.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected history length 4, got %d", len(history))
	}
	if history[2].Content != "first answer" {
		t.Errorf("expected previous assistant message to be preserved, got %q", history[2].Content)
	}
}

func TestAgent_UndoLastAssistantMessage_RemovesCurrentTurnAssistant(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	agent.history = []Message{
		{Type: Content, Role: System, Content: "test"},
		{Type: Content, Role: User, Content: "first question"},
		{Type: Content, Role: Assistant, Content: "first answer"},
		{Type: Content, Role: User, Content: "second question"},
		{Type: Content, Role: Assistant, Content: "partial second answer"},
	}

	agent.undoLastAssistantMessage()

	history := agent.GetHistory()
	if len(history) != 4 {
		t.Fatalf("expected history length 4, got %d", len(history))
	}
	if history[len(history)-1].Role != User {
		t.Errorf("expected last message to be user after undo, got %v", history[len(history)-1].Role)
	}
}
