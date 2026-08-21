// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
)

// TestNewAgentManager_WithModeFields verifies the new constructor accepts mode state.
func TestAgentManager_TurnHistory_Empty(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	history := am.TurnHistory()
	if len(history) != 0 {
		t.Errorf("TurnHistory should start empty, got %d records", len(history))
	}

	last := am.LastTurn()
	if last != nil {
		t.Errorf("LastTurn should be nil when no turns, got %+v", last)
	}
}

// panicRunner is an agentRunner that panics, used to verify that a crash in
// the agent turn is reported to the UI instead of silently stopping the agent.
type panicRunner struct{}

func (p *panicRunner) Run(ctx context.Context, input string) error {
	panic("simulated agent panic")
}

func (p *panicRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	panic("simulated agent panic")
}

// cancelRunner is an agentRunner that always returns context.Canceled. It is
// used to exercise the error path of executeRunner without a real provider.
type cancelRunner struct{}

func (c *cancelRunner) Run(ctx context.Context, input string) error {
	return context.Canceled
}

func (c *cancelRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	return context.Canceled
}

func TestAgentManager_TurnPanic_EmitsEndEvent(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetForwardInternalEvents(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		am.runAgentTurn(ctx, cancel, 1, &panicRunner{}, "hello", nil)
	}()

	select {
	case ev := <-am.events:
		if ev.Type != agentic.EventEnd {
			t.Fatalf("expected EventEnd after panic, got %v", ev.Type)
		}
		if ev.Text == "" {
			t.Fatal("expected EventEnd to carry error text after panic")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no EventEnd emitted after agent panic")
	}

	<-done
	if am.IsRunning() {
		t.Fatal("expected agent manager to stop running after panic")
	}
}

func TestAgentManager_ExecuteRunner_DoesNotBlockInternalChannelInTUIMode(t *testing.T) {
	cfg := &config.Config{}
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, nil, tuiEvents, "")
	// TUI mode: forwardInternalEvents is false and am.events is not consumed.

	// Drain the TUI-bound channel so the only remaining potential block is
	// the internal am.events channel.
	stopDrain := make(chan struct{})
	go func() {
		for {
			select {
			case <-tuiEvents.Agent:
			case <-stopDrain:
				return
			}
		}
	}()
	defer close(stopDrain)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled so the runner returns immediately

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 150; i++ {
			am.executeRunner(ctx, &cancelRunner{}, "hello", nil)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("executeRunner blocked on the undrained internal events channel")
	}

	// No internal events should have been queued in TUI mode.
	select {
	case ev := <-am.events:
		t.Fatalf("unexpected internal event sent in TUI mode: %v", ev)
	default:
	}
}

func TestAgentManager_TurnHistory_ToolCallCapture(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	for _, tc := range toolCallPairs() {
		am.OnEvent(tc.call)
		am.OnEvent(tc.result)
	}

	if len(am.TurnHistory()) != 0 {
		t.Errorf("TurnHistory should be empty before EventEnd")
	}

	am.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	history := am.TurnHistory()
	if len(history) != 1 {
		t.Fatalf("TurnHistory should have 1 record after EventEnd, got %d", len(history))
	}

	turn := history[0]
	if turn.Number != 1 {
		t.Errorf("Turn number = %d, want 1", turn.Number)
	}

	assertTurnToolCalls(t, turn, toolCallPairs())
}

type toolCallPair struct {
	call   agentic.OutputEvent
	result agentic.OutputEvent
}

func toolCallPairs() []toolCallPair {
	return []toolCallPair{
		{
			call:   agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path": "main.go"}`, ToolCallID: "call1"},
			result: agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "read", ToolResult: "file contents", ToolCallID: "call1"},
		},
		{
			call:   agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "edit", ToolInput: `{"path": "main.go", "old_string": "foo", "new_string": "bar"}`, ToolCallID: "call2"},
			result: agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "edit", ToolResult: "file updated", ToolCallID: "call2"},
		},
	}
}

func assertTurnToolCalls(t *testing.T, turn TurnRecord, pairs []toolCallPair) {
	t.Helper()
	if len(turn.ToolCalls) != len(pairs) {
		t.Fatalf("Turn should have %d tool calls, got %d", len(pairs), len(turn.ToolCalls))
	}
	if len(turn.ToolResults) != len(pairs) {
		t.Fatalf("Turn should have %d tool results, got %d", len(pairs), len(turn.ToolResults))
	}
	for i, p := range pairs {
		if turn.ToolCalls[i].Name != p.call.ToolName {
			t.Errorf("ToolCalls[%d].Name = %q, want %q", i, turn.ToolCalls[i].Name, p.call.ToolName)
		}
		if turn.ToolCalls[i].CallID != p.call.ToolCallID {
			t.Errorf("ToolCalls[%d].CallID = %q, want %q", i, turn.ToolCalls[i].CallID, p.call.ToolCallID)
		}
		if turn.ToolResults[i].Name != p.result.ToolName {
			t.Errorf("ToolResults[%d].Name = %q, want %q", i, turn.ToolResults[i].Name, p.result.ToolName)
		}
		if turn.ToolResults[i].CallID != p.result.ToolCallID {
			t.Errorf("ToolResults[%d].CallID = %q, want %q", i, turn.ToolResults[i].CallID, p.result.ToolCallID)
		}
		if turn.ToolResults[i].Result != p.result.ToolResult {
			t.Errorf("ToolResults[%d].Result = %q, want %q", i, turn.ToolResults[i].Result, p.result.ToolResult)
		}
	}
}

func TestAgentManager_TurnHistory_Timing(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	// Send EventEnd without an active session should still create a turn record
	am.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	history := am.TurnHistory()
	if len(history) != 1 {
		t.Fatalf("TurnHistory should have 1 record, got %d", len(history))
	}

	turn := history[0]
	if turn.Timing.Total <= 0 {
		t.Errorf("Expected positive timing total, got %f", turn.Timing.Total)
	}
	// Without an active agent, RequestJSON and ResponseJSON should be empty
	if turn.RequestJSON != "" {
		t.Errorf("RequestJSON should be empty without agent, got: %s", turn.RequestJSON)
	}
	if turn.ResponseJSON != "" {
		t.Errorf("ResponseJSON should be empty without agent, got: %s", turn.ResponseJSON)
	}
}

func TestAgentManager_TurnHistory_MultipleTurns(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	// Simulate three turns
	for i := 0; i < 3; i++ {
		am.OnEvent(agentic.OutputEvent{
			Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command": "echo turn"}`, ToolCallID: fmt.Sprintf("call%d", i),
		})
		am.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})
	}

	history := am.TurnHistory()
	if len(history) != 3 {
		t.Fatalf("TurnHistory should have 3 records, got %d", len(history))
	}

	for i, turn := range history {
		if turn.Number != i+1 {
			t.Errorf("Turn %d: number = %d, want %d", i, turn.Number, i+1)
		}
		// Each turn should have tool calls from that turn only
		if len(turn.ToolCalls) != 1 {
			t.Errorf("Turn %d: expected 1 tool call, got %d", i, len(turn.ToolCalls))
		}
		if turn.ToolCalls[0].CallID != fmt.Sprintf("call%d", i) {
			t.Errorf("Turn %d: ToolCallID = %q, want %q", i, turn.ToolCalls[0].CallID, fmt.Sprintf("call%d", i))
		}
	}

	// LastTurn should return the most recent
	last := am.LastTurn()
	if last == nil {
		t.Fatal("LastTurn should not be nil")
	}
	if last.Number != 3 {
		t.Errorf("LastTurn number = %d, want 3", last.Number)
	}
}

func TestNewAgentManager_WithModeFields(t *testing.T) {
	cfg := &config.Config{}
	ss := NewSessionStore("")
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)

	am := NewAgentManager(cfg, ss, ld, sessionState, tuiEvents, "")
	if am == nil {
		t.Fatal("NewAgentManager returned nil")
	}

	// Verify mode methods work
	current := am.CurrentMode()
	if current.Major != internal.MajorCoder {
		t.Errorf("CurrentMode().Major = %q, want %q", current.Major, internal.MajorCoder)
	}
	if current.Autonomy != internal.AutonomyYolo {
		t.Errorf("CurrentMode().Autonomy = %q, want %q", current.Autonomy, internal.AutonomyYolo)
	}
}

func TestAgentManager_SetMode(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// SetMode should update the mode
	am.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})
	current := am.CurrentMode()
	if current.Major != internal.MajorPlanner {
		t.Errorf("CurrentMode().Major = %q, want %q", current.Major, internal.MajorPlanner)
	}
}

func TestAgentManager_PushPopMode(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// Push a new mode
	prev := am.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "trigger: *.go")
	if prev.Major != internal.MajorCoder {
		t.Errorf("PushMode returned prev.Major = %q, want %q", prev.Major, internal.MajorCoder)
	}
	if am.CurrentMode().Major != internal.MajorPlanner {
		t.Errorf("Current after push = %q, want %q", am.CurrentMode().Major, internal.MajorPlanner)
	}

	// Pop restores
	restored := am.PopMode()
	if restored.Major != internal.MajorCoder {
		t.Errorf("PopMode restored = %q, want %q", restored.Major, internal.MajorCoder)
	}
	if am.CurrentMode().Major != internal.MajorCoder {
		t.Errorf("Current after pop = %q, want %q", am.CurrentMode().Major, internal.MajorCoder)
	}
}

func TestAgentManager_SetMinorMode_CompanionDefaultsToAgentDriven(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	am.SetForegroundOrchestrator(orch)

	if err := am.SetMinorMode("companion", true); err != nil {
		t.Fatalf("SetMinorMode companion on: %v", err)
	}
	if !am.AgentDrivenEnabled() {
		t.Error("AgentDrivenEnabled should be true when companion mode is enabled")
	}
	// Default companion mode is agent-driven, not framework-driven.
	if orch.Mode() != multiagent.WorkflowAgentDriven {
		t.Errorf("orchestrator mode = %v, want WorkflowAgentDriven", orch.Mode())
	}

	if err := am.SetMinorMode("companion", false); err != nil {
		t.Fatalf("SetMinorMode companion off: %v", err)
	}
	if am.AgentDrivenEnabled() {
		t.Error("AgentDrivenEnabled should be false when companion mode is disabled")
	}
}

func TestAgentManager_EmitEvent_DeliversToTUI(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	am.EmitEvent("hello flash")

	select {
	case received := <-tuiEvents.Chat:
		if received.Flash == nil || received.Flash.Text != "hello flash" {
			t.Fatalf("expected flash event, got %+v", received)
		}
	default:
		t.Fatal("expected message on chat channel, got nothing")
	}
}

func TestAgentManager_EmitEvent_DoesNotBlock(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(1, 1, 1, 1) // small buffer
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// Fill the buffer
	tuiEvents.Chat <- event.ChatEvent{Flash: &event.Flash{Text: "dummy"}}

	// Emit should not block (drops if full)
	am.EmitEvent("hello flash")
	// Test passed if we get here without deadlock
}

func TestAgentManager_OnEvent_ForwardsToTUI(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// OnEvent with an OutputEvent should forward to the agent channel
	am.OnEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Text: "Hello",
	})

	select {
	case received := <-tuiEvents.Agent:
		if received.Event.Text != "Hello" {
			t.Errorf("Text = %q, want %q", received.Event.Text, "Hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected message on agent channel, got nothing")
	}
}

func TestAgentManager_OnEvent_DoesNotBlockInternalChannel(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// Drain the TUI-bound channel so the only potential block is the internal
	// am.events channel, which is not consumed in TUI mode.
	stopDrain := make(chan struct{})
	go func() {
		for {
			select {
			case <-tuiEvents.Agent:
			case <-stopDrain:
				return
			}
		}
	}()
	defer close(stopDrain)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 150; i++ {
			am.OnEvent(agentic.OutputEvent{
				Type: agentic.EventContent,
				Text: fmt.Sprintf("chunk %d", i),
			})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnEvent blocked on the undrained internal events channel")
	}
}

func TestAgentManager_OnEvent_DoesNotDropTUIEvents(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(5, 5, 5, 5) // small buffer
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	const total = 50
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		received := 0
		for received < total {
			select {
			case <-tuiEvents.Agent:
				received++
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	for i := 0; i < total; i++ {
		am.OnEvent(agentic.OutputEvent{
			Type: agentic.EventContent,
			Text: fmt.Sprintf("chunk %d", i),
		})
	}

	select {
	case <-drainDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("not all TUI events were delivered")
	}
}

func TestAgentManager_CurrentMode(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	current := am.CurrentMode()
	if current.Major != internal.MajorPlanner {
		t.Errorf("CurrentMode().Major = %q, want %q", current.Major, internal.MajorPlanner)
	}
	if current.Autonomy != internal.AutonomyReview {
		t.Errorf("CurrentMode().Autonomy = %q, want %q", current.Autonomy, internal.AutonomyReview)
	}
}

func TestAgentManager_PreviousMode(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	if am.PreviousMode() != nil {
		t.Errorf("PreviousMode before push should be nil")
	}

	am.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "test")
	prev := am.PreviousMode()
	if prev == nil {
		t.Fatal("PreviousMode after push should not be nil")
	}
	if prev.Major != internal.MajorCoder {
		t.Errorf("PreviousMode().Major = %q, want %q", prev.Major, internal.MajorCoder)
	}
}

func TestAgentManager_Source(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	if am.Source() != "" {
		t.Errorf("Source before push = %q, want empty", am.Source())
	}

	am.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "skill: planner")
	if am.Source() != "skill: planner" {
		t.Errorf("Source = %q, want %q", am.Source(), "skill: planner")
	}
}

func TestAgentManager_SetMode_EmitsEvent(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyYolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	am.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})

	select {
	case received := <-tuiEvents.Footer:
		if received.ModeChange == nil || received.ModeChange.NewMode.Major != internal.MajorPlanner {
			t.Fatalf("expected mode change event, got %+v", received)
		}
		if received.ModeChange.Source != "user" {
			t.Errorf("Source = %q, want 'user'", received.ModeChange.Source)
		}
	default:
		t.Fatal("expected footer event, got nothing")
	}
}

func TestAgentManager_PushMode_EmitsEvent(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	am.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "skill: planner")

	select {
	case received := <-tuiEvents.Footer:
		if received.ModeChange == nil || received.ModeChange.NewMode.Major != internal.MajorPlanner {
			t.Fatalf("expected mode change event, got %+v", received)
		}
		if received.ModeChange.Source != "skill: planner" {
			t.Errorf("Source = %q, want 'skill: planner'", received.ModeChange.Source)
		}
	default:
		t.Fatal("expected footer event, got nothing")
	}
}

// TestAgentManager_StartSession_ForwardsConfig verifies StartSession creates
// an active agent when provided a valid config.
func TestAgentManager_StartSession_ForwardsConfig(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{
			Mode:               internal.ExecutionYolo,
			WorktreeMode:       internal.WorktreeAlways,
			MaxToolRepeatTotal: 7,
		},
		Skills: config.SkillsConfig{ExecutionMode: config.AgenticSkillModeInline},
		ContextCompression: config.ContextCompressionConfig{
			Enabled:             ccBoolPtr(true),
			MaxTokens:           4096,
			ThresholdPercent:    75,
			OnContextError:      true,
			Strategy:            config.AgenticCompressionToolElision,
			PreserveRecentTurns: 3,
		},
	}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	opts := agenticprovider.StreamOptions{MaxTokens: 256}

	events, err := am.StartSession(mdl, opts, "You are a test assistant.", nil, cfg)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if events == nil {
		t.Fatal("StartSession returned nil event channel")
	}
	if am.CurrentAgent() == nil {
		t.Fatal("CurrentAgent should be set after StartSession")
	}
}

// TestAgentManager_StartSession_SetsSessionID verifies that StartSession
// forwards the session store's session ID to the agent's stream options for
// provider-side prompt cache affinity.
func TestAgentManager_StartSession_SetsSessionID(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
	}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	sessionStore := NewSessionStore(t.TempDir())
	am := NewAgentManager(cfg, sessionStore, nil, sessionState, tuiEvents, "")

	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	opts := agenticprovider.StreamOptions{MaxTokens: 256}

	if _, err := am.StartSession(mdl, opts, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	agent := am.CurrentAgent()
	if agent == nil {
		t.Fatal("CurrentAgent should be set after StartSession")
	}
	if agent.StreamOptions().SessionID == "" {
		t.Error("expected SessionID to be set from session store")
	}
}

// TestAgentManager_StartSession_AlreadyActive verifies a second session errors.
func TestAgentManager_StartSession_AlreadyActive(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
	}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	mdl := agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions}
	opts := agenticprovider.StreamOptions{}

	if _, err := am.StartSession(mdl, opts, "sys", nil, cfg); err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}
	if _, err := am.StartSession(mdl, opts, "sys", nil, cfg); err == nil {
		t.Error("second StartSession should fail when session already active")
	}
}

// TestAgentManager_ConcurrentModeAccess verifies thread safety.
func TestAgentManager_LogEvent_TracesEvents(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	var buf bytes.Buffer
	stdLogger := log.New(&buf, "", 0)
	logger := agentic.NewLoggerWithStdLogger(stdLogger, agentic.Debug)
	am.SetLogger(logger)

	am.OnEvent(agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		ToolName:  "read",
		ToolInput: `{"path":"README.md"}`,
	})

	output := buf.String()
	if !strings.Contains(output, "tool_call") {
		t.Errorf("expected event type in log, got: %s", output)
	}
	if !strings.Contains(output, "read") {
		t.Errorf("expected tool name in log, got: %s", output)
	}
}

func TestAgentManager_LogEvent_SkipsWhenNotDebug(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	var buf bytes.Buffer
	stdLogger := log.New(&buf, "", 0)
	logger := agentic.NewLoggerWithStdLogger(stdLogger, agentic.Info)
	am.SetLogger(logger)

	am.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "hello"})

	if buf.Len() != 0 {
		t.Errorf("expected no debug trace at Info level, got: %s", buf.String())
	}
}
