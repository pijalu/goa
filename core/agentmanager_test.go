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
	"github.com/pijalu/goa/prompts"
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
	default:
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
			Enabled:             true,
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

func TestAgentManager_ConcurrentModeAccess(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(100, 100, 100, 100)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// Concurrent reads and writes should not race (verified by -race flag)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			am.SetMode(internal.ModeState{Major: internal.MajorCoder})
			am.PushMode(internal.ModeState{Major: internal.MajorPlanner}, "test")
			am.PopMode()
		}
		done <- struct{}{}
	}()

	go func() {
		for i := 0; i < 50; i++ {
			_ = am.CurrentMode()
			_ = am.PreviousMode()
			_ = am.Source()
		}
		done <- struct{}{}
	}()

	<-done
	<-done
}

// blockingProvider is a test provider that blocks until its Stream context is
// canceled, then returns the context error. This lets us verify that
// AgentManager.Interrupt cancels the active turn.
type blockingProvider struct {
	api     agenticprovider.Api
	started chan struct{}
}

func (p *blockingProvider) API() agenticprovider.Api { return p.api }

func (p *blockingProvider) Stream(model agenticprovider.Model, ctx agenticprovider.Context, opts agenticprovider.StreamOptions) (*agenticprovider.AssistantMessageEventStream, error) {
	if p.started != nil {
		close(p.started)
	}
	goCtx := ctx.GoContext()
	<-goCtx.Done()
	return nil, goCtx.Err()
}

func (p *blockingProvider) StreamSimple(model agenticprovider.Model, ctx agenticprovider.Context, opts agenticprovider.SimpleStreamOptions) (*agenticprovider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, agenticprovider.BuildSimpleOptions(model, opts))
}

func waitForCondition(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not satisfied within timeout")
}

func TestAgentManager_InjectCompanionReview_UpdatesSystemPrompt(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	base := "You are a helpful assistant."
	mdl := agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, base, nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := am.InjectCompanionReview(true); err != nil {
		t.Fatalf("InjectCompanionReview(true): %v", err)
	}
	if !strings.Contains(am.SystemPrompt(), "Companion review is enabled") {
		t.Errorf("system prompt missing enabled text: %q", am.SystemPrompt())
	}

	if err := am.InjectCompanionReview(false); err != nil {
		t.Fatalf("InjectCompanionReview(false): %v", err)
	}
	if strings.Contains(am.SystemPrompt(), "Companion review is enabled") {
		t.Errorf("system prompt should not contain enabled text after disable: %q", am.SystemPrompt())
	}
	if !strings.Contains(am.SystemPrompt(), "Companion review is disabled") {
		t.Errorf("system prompt missing disabled text: %q", am.SystemPrompt())
	}
}

func TestAgentManager_InjectCompanionReview_ReplacesHistoryMessages(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	base := "You are a helpful assistant."
	mdl := agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, base, nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := am.InjectCompanionReview(i%2 == 0); err != nil {
			t.Fatalf("InjectCompanionReview(%v): %v", i%2 == 0, err)
		}
	}

	history := am.CurrentAgent().GetHistory()
	var companionMsgs int
	for _, m := range history {
		if m.Role == agentic.System && strings.HasPrefix(m.Content, "Companion review is") {
			companionMsgs++
		}
	}
	if companionMsgs != 1 {
		t.Errorf("expected exactly 1 companion review system message, got %d", companionMsgs)
	}
}

func TestAgentManager_RefreshContextWindow_OnFirstAssistantDelta(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	agent := agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{
			ID:            "test",
			Api:           agenticprovider.ApiOpenAICompletions,
			Provider:      agenticprovider.ProviderLMStudio,
			ContextWindow: 262144,
		},
	})
	am.SetActiveAgentForTest(agent)

	refreshed := make(chan int, 1)
	am.SetContextWindowRefresher(func() int {
		refreshed <- 32768
		return 32768
	})

	// A bare state-change (start of generation) must NOT trigger the refresh —
	// the model may still be loading and would report max_context_length.
	am.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	select {
	case <-refreshed:
		t.Fatal("refresher fired on state change; should only fire on first assistant delta")
	case <-time.After(100 * time.Millisecond):
	}

	// The first assistant content delta is the reliable "model loaded" signal.
	am.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "Hello"})

	select {
	case n := <-refreshed:
		if n != 32768 {
			t.Errorf("refresher returned %d, want 32768", n)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("context window refresher was not called after first assistant delta")
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for agent.Model().ContextWindow != 32768 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if agent.Model().ContextWindow != 32768 {
		t.Errorf("agent ContextWindow = %d, want 32768", agent.Model().ContextWindow)
	}

	// A second delta must not re-trigger (one-shot).
	am.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: " world"})
	select {
	case <-refreshed:
		t.Fatal("refresher fired a second time; should be one-shot")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAgentManager_Interrupt_CancelsRunningTurn(t *testing.T) {
	api := agenticprovider.Api("test-blocking-" + t.Name())
	prov := &blockingProvider{api: api, started: make(chan struct{})}
	agenticprovider.RegisterApiProvider(prov)

	cfg := &config.Config{}
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{Major: internal.MajorCoder}), tuiEvents, "")

	mdl := agenticprovider.Model{
		ID:         "block",
		Api:        api,
		Provider:   agenticprovider.ProviderLMStudio,
		InputTypes: []string{"text"},
	}
	opts := agenticprovider.StreamOptions{}
	am.SetForwardInternalEvents(true)
	if _, err := am.StartSession(mdl, opts, "system prompt", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := am.SendUserInput("hello"); err != nil {
		t.Fatalf("SendUserInput: %v", err)
	}

	// Wait until the provider's Stream method is actually running.
	select {
	case <-prov.started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider Stream did not start")
	}

	waitForCondition(t, func() bool {
		am.mu.Lock()
		defer am.mu.Unlock()
		return am.running
	}, 100*time.Millisecond)

	if err := am.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	// The canceled turn should report an error event within 100 ms.
	deadline := time.After(100 * time.Millisecond)
	var sawEnd bool
	for !sawEnd {
		select {
		case ev := <-am.Events():
			if ev.Type == agentic.EventEnd {
				sawEnd = true
				if ev.Text != "" {
					t.Fatalf("expected empty Text for user cancellation, got %q", ev.Text)
				}
				if ev.Metadata["cancelled"] != "true" {
					t.Fatalf("expected cancelled metadata for user cancellation, got %v", ev.Metadata)
				}
			}
		case <-deadline:
			t.Fatal("turn did not terminate within 100 ms of Interrupt")
		}
	}

	waitForCondition(t, func() bool {
		am.mu.Lock()
		defer am.mu.Unlock()
		return !am.running && am.cancel == nil
	}, 100*time.Millisecond)
}

func TestAgentManager_SetModel_UpdatesContextCompression(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	mdl1 := agenticprovider.Model{
		ID:            "model-1",
		Api:           agenticprovider.ApiOpenAICompletions,
		Provider:      agenticprovider.ProviderLMStudio,
		ContextWindow: 131072,
	}
	if _, err := am.StartSession(mdl1, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if cs := am.CurrentAgent().ContextStats(); cs.MaxTokens != 131072 {
		t.Fatalf("initial MaxTokens = %d, want 131072", cs.MaxTokens)
	}

	mdl2 := agenticprovider.Model{
		ID:            "model-2",
		Api:           agenticprovider.ApiOpenAICompletions,
		Provider:      agenticprovider.ProviderLMStudio,
		ContextWindow: 32768,
	}
	am.SetModel(mdl2)

	if cs := am.CurrentAgent().ContextStats(); cs.MaxTokens != 32768 {
		t.Errorf("after SetModel MaxTokens = %d, want 32768", cs.MaxTokens)
	}
}

// TestAgentManager_BuildCompressionConfig_AutoFromModelWindow verifies that
// when the user does not configure context compression, AgentManager still
// derives a compression config from the model's advertised context window.
func TestAgentManager_BuildCompressionConfig_AutoFromModelWindow(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{TokenCritical: 80},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, 32768)
	if cc.MaxTokens != 32768 {
		t.Errorf("MaxTokens = %d, want 32768", cc.MaxTokens)
	}
	if cc.ThresholdPercent != 80 {
		t.Errorf("ThresholdPercent = %d, want 80", cc.ThresholdPercent)
	}
	if cc.Strategy != agentic.CompressionToolElision {
		t.Errorf("Strategy = %q, want tool_elision", cc.Strategy)
	}
}

// TestAgentManager_BuildCompressionConfig_ExplicitWins verifies that explicit
// context_compression settings override the model-window fallback.
func TestAgentManager_BuildCompressionConfig_ExplicitWins(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{TokenCritical: 80},
		ContextCompression: config.ContextCompressionConfig{
			MaxTokens:        4096,
			ThresholdPercent: 50,
			Strategy:         config.AgenticCompressionSelective,
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, 32768)
	if cc.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", cc.MaxTokens)
	}
	if cc.ThresholdPercent != 50 {
		t.Errorf("ThresholdPercent = %d, want 50", cc.ThresholdPercent)
	}
	if cc.Strategy != agentic.CompressionSelective {
		t.Errorf("Strategy = %q, want selective", cc.Strategy)
	}
}

// TestAgentManager_HandleLoopWarningCriticalInterrupts guards STUB-2: the
// TestAgentManager_ThinkingLoopInterrupts verifies that a thinking/reasoning
// loop cancels the in-flight turn, mirroring the tool-loop interrupt path. This
// reproduces the failure captured in bugs.md where the assistant repeated the
// same reasoning paragraph many times with no loop protection firing.
func TestAgentManager_ThinkingLoopInterrupts(t *testing.T) {
	cfg := &config.Config{}
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	am := NewAgentManager(cfg, nil, ld, NewSessionState(internal.ModeState{}), nil, "")

	ctx, cancel := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel
	am.running = true
	am.mu.Unlock()

	line := "I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime."
	// Default interrupt threshold is 6 identical significant lines.
	for i := 0; i < 6; i++ {
		lvl := ld.RecordThinkingDelta(line + "\n")
		am.handleThinkingLoopWarning(lvl)
	}

	if ctx.Err() == nil {
		t.Fatal("thinking loop did not cancel the in-flight turn context")
	}

	// Sanity: a non-looping turn must not interrupt.
	ctx2, cancel2 := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel2
	am.running = true
	am.mu.Unlock()
	am.handleThinkingLoopWarning(LoopOK)
	if ctx2.Err() != nil {
		t.Error("LoopOK unexpectedly cancelled the turn")
	}
	cancel2()
}

// LoopCritical branch previously only flashed "will be paused" without pausing.
// It must now actually cancel the in-flight turn.
func TestAgentManager_HandleLoopWarningCriticalInterrupts(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), nil, "")

	ctx, cancel := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel
	am.running = true
	am.mu.Unlock()

	am.handleLoopWarning(LoopCritical)

	if ctx.Err() == nil {
		t.Fatal("LoopCritical did not cancel the in-flight turn context")
	}
	am.mu.Lock()
	cancelled := am.cancel == nil
	am.mu.Unlock()
	if !cancelled {
		t.Error("LoopCritical left am.cancel set after interrupting")
	}

	// Sanity: LoopOK must not interrupt.
	ctx2, cancel2 := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel2
	am.running = true
	am.mu.Unlock()
	am.handleLoopWarning(LoopOK)
	if ctx2.Err() != nil {
		t.Error("LoopOK unexpectedly cancelled the turn")
	}
	cancel2()
}

func TestAgentManager_SetMode_InjectsPromptBody(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	// Wire a ModeRegistry so injectModePrompt has mode bodies to inject.
	reg := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	am.SetModeRegistry(reg)

	// Start a session so an active agent exists.
	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	opts := agenticprovider.StreamOptions{MaxTokens: 256}
	_, err := am.StartSession(mdl, opts, "You are a test assistant.", nil, cfg)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	// Switch to planner mode while a session is active: the prompt is queued,
	// not injected immediately.
	am.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})

	agent := am.CurrentAgent()
	if agent == nil {
		t.Fatal("no active agent after SetMode")
	}

	historyBefore := agent.GetHistory()
	foundBefore := false
	for _, msg := range historyBefore {
		if msg.Role == agentic.System && strings.Contains(msg.Content, "planner agent") {
			foundBefore = true
			break
		}
	}
	if foundBefore {
		t.Error("planner mode body was injected immediately; expected deferred injection")
	}

	// Simulate the start of the next turn: pending prompt is applied.
	am.applyPendingMajorMode()

	historyAfter := agent.GetHistory()
	foundAfter := false
	for _, msg := range historyAfter {
		if msg.Role == agentic.System && strings.Contains(msg.Content, "planner agent") {
			foundAfter = true
			break
		}
	}
	if !foundAfter {
		t.Error("planner mode body not found in agent history after applying pending mode")
	}
}

func TestAgentManager_SetMode_WithoutSession_DoesNotQueuePrompt(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	reg := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	am.SetModeRegistry(reg)

	am.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})

	am.mu.Lock()
	pending := am.pendingMajor
	am.mu.Unlock()
	if pending != nil {
		t.Error("expected no pending major when no active session")
	}
}

func TestAgentManager_SetMode_EmitsFlashEvent(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	reg := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	am.SetModeRegistry(reg)

	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	opts := agenticprovider.StreamOptions{MaxTokens: 256}
	if _, err := am.StartSession(mdl, opts, "You are a test assistant.", nil, cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	am.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})

	select {
	case ev := <-tuiEvents.Chat:
		if ev.Flash == nil {
			t.Fatalf("expected Flash chat event, got %+v", ev)
		}
		want := "Mode: planner"
		if ev.Flash.Text != want {
			t.Errorf("Flash.Text = %q, want %q", ev.Flash.Text, want)
		}
	default:
		t.Error("expected Flash chat event to be emitted")
	}
}

func TestAgentManager_SetMode_AutonomyOnlyEmitsAutonomyFlash(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	reg := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	am.SetModeRegistry(reg)

	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	opts := agenticprovider.StreamOptions{MaxTokens: 256}
	if _, err := am.StartSession(mdl, opts, "You are a test assistant.", nil, cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	am.SetMode(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomyReview})

	select {
	case ev := <-tuiEvents.Chat:
		if ev.Flash == nil {
			t.Fatalf("expected Flash chat event, got %+v", ev)
		}
		want := "Autonomy: review"
		if ev.Flash.Text != want {
			t.Errorf("Flash.Text = %q, want %q", ev.Flash.Text, want)
		}
	default:
		t.Error("expected Flash chat event to be emitted")
	}
}

func TestAgentManager_SetMode_NoFlashWithoutSession(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	reg := NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	am.SetModeRegistry(reg)

	am.SetMode(internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyReview})

	select {
	case ev := <-tuiEvents.Chat:
		t.Fatalf("expected no chat event without active session, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestAgentManager_SetThinkingLevel_DeferredUntilNextTurn(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
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
	if _, err := am.StartSession(mdl, opts, "You are a test assistant.", nil, cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	if err := am.SetThinkingLevel("high"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}

	// Before applying, the active agent still has the session-start effort.
	before := am.activeAgent.ReasoningEffort()

	am.applyPendingThinkingLevel()

	after := am.activeAgent.ReasoningEffort()
	if after != agentic.ReasoningEffort("high") {
		t.Errorf("reasoning effort after apply = %q, want high", after)
	}
	if before == after {
		t.Errorf("reasoning effort did not change: before=%q after=%q", before, after)
	}
}

func TestAgentManager_SetThinkingLevel_WithoutSession_DoesNotQueue(t *testing.T) {
	cfg := &config.Config{}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	if err := am.SetThinkingLevel("high"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}

	am.mu.Lock()
	pending := am.pendingThinkingLevel
	am.mu.Unlock()
	if pending != nil {
		t.Error("expected no pending thinking level when no active session")
	}
}

// TestAgentManager_SetDisableToolBudget verifies the session-level toggle.
func TestAgentManager_SetDisableToolBudget(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	// Default: budget is enabled.
	am.mu.Lock()
	enabled := am.disableToolBudget
	am.mu.Unlock()
	if enabled {
		t.Error("disableToolBudget should default to false")
	}

	// Disable budget.
	am.SetDisableToolBudget(true)
	am.mu.Lock()
	if !am.disableToolBudget {
		t.Error("disableToolBudget should be true after SetDisableToolBudget(true)")
	}
	am.mu.Unlock()

	// Re-enable budget.
	am.SetDisableToolBudget(false)
	am.mu.Lock()
	if am.disableToolBudget {
		t.Error("disableToolBudget should be false after SetDisableToolBudget(false)")
	}
	am.mu.Unlock()
}
