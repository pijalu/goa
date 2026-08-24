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

// TestAgent_AssistantRepeat_ConfigurableLimit verifies that the runaway-loop
// repeat limit (execution.runaway_loop_max_repeats → Config.RunawayLoopMaxRepeats)
// controls how many identical stalled responses are tolerated before the
// session stops: every non-terminal repeat injects one recovery hint, the
// terminal error reports the total consecutive responses (original + repeats),
// and the built-in default of 2 keeps the historical warn-once-then-stop
// behavior.
func TestAgent_AssistantRepeat_ConfigurableLimit(t *testing.T) {
	tests := []struct {
		name       string
		maxRepeats int // 0 = built-in default (2)
		wantRuns   int // Run calls until the runaway error fires
		wantWarns  int // recovery hints present in history once stopped
	}{
		{"default is two repeats", 0, 3, 1},
		{"explicit two repeats", 2, 3, 1},
		{"lenient four repeats", 4, 5, 3},
		{"strict single repeat", 1, 2, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := newRepeatLimitAgent(t, tt.maxRepeats)

			runs, err := runRepeatTurns(agent, tt.wantRuns)
			if err == nil {
				t.Fatalf("no runaway error after %d runs (limit %d)", runs, tt.maxRepeats)
			}
			assertRunawayStop(t, err, runs, tt.wantRuns)

			if warns := countStallWarnings(agent); warns != tt.wantWarns {
				t.Errorf("expected %d stall warnings in history, got %d", tt.wantWarns, warns)
			}
		})
	}
}

// newRepeatLimitAgent builds an agent wired to a constant-text provider with
// the given runaway-loop repeat limit (0 = built-in default) and drains its
// output channel.
func newRepeatLimitAgent(t *testing.T, maxRepeats int) *Agent {
	t.Helper()
	p := &repeatTextProvider{
		api:     provider.Api(fmt.Sprintf("test-repeat-limit-%d", testProviderCounter.Add(1))),
		content: "I am stuck.",
	}
	provider.RegisterApiProvider(p)

	cfg := Config{
		Model:        testModel(p.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	}
	cfg.RunawayLoopMaxRepeats = maxRepeats
	agent := NewAgent(cfg)

	go func() {
		for range agent.Output {
		}
	}()
	return agent
}

// runRepeatTurns calls Run up to maxRuns times; it stops early and returns
// the first error. The returned count is how many turns ran.
func runRepeatTurns(agent *Agent, maxRuns int) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var err error
	runs := 0
	for runs < maxRuns {
		runs++
		if err = agent.Run(ctx, "continue"); err != nil {
			break
		}
	}
	return runs, err
}

// assertRunawayStop checks the guardrail stopped after wantRuns identical
// responses and that the error names both the guardrail and the total
// consecutive-response count (original + repeats).
func assertRunawayStop(t *testing.T, err error, runs, wantRuns int) {
	t.Helper()
	if !strings.Contains(err.Error(), "runaway loop detected") {
		t.Errorf("expected runaway loop error, got %v", err)
	}
	if runs != wantRuns {
		t.Errorf("stopped after %d runs, want %d", runs, wantRuns)
	}
	// The terminal message counts the original response plus the repeats.
	if want := fmt.Sprintf("%d consecutive times", wantRuns); !strings.Contains(err.Error(), want) {
		t.Errorf("error should report %q, got %v", want, err)
	}
}

// countStallWarnings counts recovery hints ("Progress has stalled") injected
// into the conversation history by the runaway-loop guardrail.
func countStallWarnings(agent *Agent) int {
	var warns int
	for _, m := range copyAgentHistory(agent) {
		if m.Role == System && strings.Contains(m.Content, "Progress has stalled") {
			warns++
		}
	}
	return warns
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
