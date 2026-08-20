// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/prompts"
)

// TestNewAgentManager_WithModeFields verifies the new constructor accepts mode state.

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

// TestAgentManager_ThinkingLoopSetsStopReason verifies the user-facing
// regression from the invalid-stop export: when the thinking-loop detector
// cancels a turn, the manager must record a loop stop reason so the UI shows
// a clear explanation instead of a bare "context canceled".
func TestAgentManager_ThinkingLoopSetsStopReason(t *testing.T) {
	cfg := &config.Config{}
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	am := NewAgentManager(cfg, nil, ld, NewSessionState(internal.ModeState{}), nil, "")

	ctx, cancel := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel
	am.running = true
	am.mu.Unlock()

	am.handleThinkingLoopWarning(LoopInterrupt)

	if ctx.Err() == nil {
		t.Fatal("LoopInterrupt did not cancel the in-flight turn context")
	}
	am.mu.Lock()
	reason := am.loopStopReason
	am.mu.Unlock()
	if reason == "" {
		t.Fatal("thinking-loop interrupt left loopStopReason empty; UI would show a bare 'context canceled'")
	}
	if !strings.Contains(reason, "thinking loop") {
		t.Errorf("stop reason %q does not explain the thinking loop", reason)
	}
}

// TestAgentManager_ThinkingLoopDoesNotLatchAcrossTurns reproduces the exact
// production failure: the detector interrupts turn N (cancelling its context,
// so no EventEnd fires), then the user's resumed turn N+1 streams a single
// "The" delta. Before the fix the latched counter re-triggered LoopInterrupt
// and killed the resumed turn immediately. After the fix the accumulator is
// reset on interrupt, so the fresh turn is unaffected.
func TestAgentManager_ThinkingLoopDoesNotLatchAcrossTurns(t *testing.T) {
	cfg := &config.Config{}
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	am := NewAgentManager(cfg, nil, ld, NewSessionState(internal.ModeState{}), nil, "")

	// Turn N: stream the repeated session line until the detector interrupts.
	ctxN, cancelN := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancelN
	am.running = true
	am.mu.Unlock()

	line := "I can see the main.ts files are very similar. The pbl version has additional imports from SDK runtime."
	for i := 0; i < 6; i++ {
		lvl := ld.RecordThinkingDelta(line + "\n")
		am.handleThinkingLoopWarning(lvl)
	}
	if ctxN.Err() == nil {
		t.Fatal("turn N was not cancelled by the thinking-loop detector")
	}

	// Turn N+1 (resume): first delta is the single token seen in the export.
	ctxN1, cancelN1 := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancelN1
	am.running = true
	am.mu.Unlock()

	lvl := ld.RecordThinkingDelta("The")
	am.handleThinkingLoopWarning(lvl)
	if ctxN1.Err() != nil {
		t.Fatal("resumed turn was cancelled on its first thinking delta — latched loop detector regression")
	}
	cancelN1()
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

// TestAgentManager_LoopStopReason_EmitsClearEventEnd verifies that when the
// loop detector interrupts a turn, the EventEnd produced by executeRunner
// contains a clear loop-stop message and is not marked as a user-initiated
// cancellation (which would produce "Generation stopped by user.").
func TestAgentManager_LoopStopReason_EmitsClearEventEnd(t *testing.T) {
	cfg := &config.Config{}
	bus := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, NewSessionState(internal.ModeState{}), bus, "")

	ctx, cancel := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel
	am.running = true
	am.mu.Unlock()

	am.handleLoopWarning(LoopInterrupt)

	runner := &canceledRunner{}
	am.executeRunner(ctx, runner, "hello", nil)

	var got event.AgentEvent
	select {
	case got = <-bus.Agent:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for EventEnd on the agent bus")
	}

	if got.Event.Type != agentic.EventEnd {
		t.Fatalf("expected EventEnd, got %v", got.Event.Type)
	}
	if got.Event.Metadata != nil && got.Event.Metadata["cancelled"] == "true" {
		t.Errorf("EventEnd should not be marked as user-cancelled when the loop detector stopped the turn; metadata=%v", got.Event.Metadata)
	}
	if !strings.Contains(got.Event.Text, "loop") {
		t.Errorf("EventEnd text should contain a clear loop-stop reason, got %q", got.Event.Text)
	}

	am.Close()
}

type canceledRunner struct{}

func (r *canceledRunner) Run(ctx context.Context, input string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (r *canceledRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestAgentManager_ToolCallStreamingDeltasDoNotLoop(t *testing.T) {
	cfg := &config.Config{}
	ld := NewLoopDetector(DefaultLoopDetectorConfig())
	am := NewAgentManager(cfg, nil, ld, NewSessionState(internal.ModeState{}), nil, "")

	ctx, cancel := context.WithCancel(context.Background())
	am.mu.Lock()
	am.cancel = cancel
	am.running = true
	am.mu.Unlock()

	input := `{"command":"cd ~/dev/goa && git status --short"}`

	// Simulate a single streamed tool call: the provider emits many deltas,
	// each carrying the accumulated arguments so far. Before the fix this
	// looked like the same call repeated many times and tripped the loop
	// detector after the configured threshold (10 by default).
	for i := 0; i < 15; i++ {
		am.OnEvent(agentic.OutputEvent{
			Type:       agentic.EventToolCall,
			State:      agentic.StateToolCall,
			Role:       agentic.Assistant,
			ToolName:   "bash",
			ToolInput:  input,
			ToolCallID: "call_01_same",
			IsDelta:    true,
		})
	}
	if ctx.Err() != nil {
		t.Fatal("streaming tool-call deltas falsely triggered the tool-call loop detector")
	}

	// The final completed event must not itself be treated as a repeat,
	// because the delta phase was not counted.
	am.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		State:      agentic.StateToolCall,
		Role:       agentic.Assistant,
		ToolName:   "bash",
		ToolInput:  input,
		ToolCallID: "call_01_same",
		IsDelta:    false,
	})
	if ctx.Err() != nil {
		t.Fatal("single completed tool call triggered a false loop")
	}

	// Sanity: genuinely repeated completed calls still trigger the detector.
	for i := 0; i < 8; i++ {
		am.OnEvent(agentic.OutputEvent{
			Type:       agentic.EventToolCall,
			State:      agentic.StateToolCall,
			Role:       agentic.Assistant,
			ToolName:   "bash",
			ToolInput:  input,
			ToolCallID: fmt.Sprintf("call_repeat_%d", i),
			IsDelta:    false,
		})
	}
	if ctx.Err() != nil {
		t.Fatal("loop detector fired before the configured interrupt threshold")
	}
	am.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		State:      agentic.StateToolCall,
		Role:       agentic.Assistant,
		ToolName:   "bash",
		ToolInput:  input,
		ToolCallID: "call_repeat_9",
		IsDelta:    false,
	})
	if ctx.Err() == nil {
		t.Fatal("expected real repeated completed calls to trigger the loop detector")
	}
	cancel()
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

// recordingConfigSaver implements config.ConfigSaver for tests: it records
// home/project providers+models save calls and can inject an error.
type recordingConfigSaver struct {
	homeSaves    int
	projectSaves int
	err          error
}

func (f *recordingConfigSaver) Save(cfg *config.Config) error { return f.err }
func (f *recordingConfigSaver) SaveProjectConfig(cfg *config.Config) error {
	return f.err
}
func (f *recordingConfigSaver) SaveHomeProvidersAndModels(cfg *config.Config) error {
	f.homeSaves++
	return f.err
}
func (f *recordingConfigSaver) SaveProjectProvidersAndModels(cfg *config.Config) error {
	f.projectSaves++
	return f.err
}
func (f *recordingConfigSaver) SaveHomeField(path []string, value any) error    { return f.err }
func (f *recordingConfigSaver) SaveProjectField(path []string, value any) error { return f.err }
func (f *recordingConfigSaver) SaveProjectFieldValue(path []string, value any) error {
	return f.err
}
func (f *recordingConfigSaver) SaveHomeFieldValue(path []string, value any) error {
	return f.err
}
func (f *recordingConfigSaver) SaveLocalFieldValue(path []string, value any) error {
	return f.err
}
func (f *recordingConfigSaver) DeleteProjectField(path []string) error { return f.err }
func (f *recordingConfigSaver) DeleteHomeField(path []string) error    { return f.err }
func (f *recordingConfigSaver) Reload() (*config.Config, error)        { return nil, f.err }

// thinkingLevelTestConfig returns a config with two configured models so
// per-model thinking-level behavior can be exercised.
func thinkingLevelTestConfig() *config.Config {
	return &config.Config{
		ActiveModel: "deepseek",
		Models: []config.ModelConfig{
			{ID: "deepseek", ProviderID: "p1", Model: "deepseek-chat"},
			{ID: "kimi", ProviderID: "p1", Model: "kimi-k2"},
		},
	}
}

// TestAgentManager_SetThinkingLevel_SavesAtModelLevel verifies that changing
// the thinking level records it on the active model's config entry (and
// persists it) without touching other models: changing deepseek must not
// change kimi.
func TestAgentManager_SetThinkingLevel_SavesAtModelLevel(t *testing.T) {
	cfg := thinkingLevelTestConfig()
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	am := NewAgentManager(cfg, nil, nil, sessionState, event.MakeBus(10, 10, 10, 10), "")
	saver := &recordingConfigSaver{}
	am.SetConfigSaver(saver)

	if err := am.SetThinkingLevel("high"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}

	if got := cfg.GetModelByID("deepseek").ThinkingLevel; got != "high" {
		t.Errorf("deepseek thinking level = %q, want high", got)
	}
	if got := cfg.GetModelByID("kimi").ThinkingLevel; got != "" {
		t.Errorf("kimi thinking level = %q, want untouched empty", got)
	}
	if saver.homeSaves != 1 {
		t.Errorf("home saves = %d, want 1", saver.homeSaves)
	}
	if saver.projectSaves != 0 {
		t.Errorf("project saves = %d, want 0 (auto_save_model off)", saver.projectSaves)
	}

	// Resolving the level for kimi must not see deepseek's saved level.
	cfg.ActiveModel = "kimi"
	if got := cfg.GetThinkingLevel("main_agent"); got == "high" {
		t.Errorf("kimi resolved deepseek's level: got %q", got)
	}
}

// TestAgentManager_SetThinkingLevel_PersistenceSuppressed verifies team UI
// bug RC-5 fix: while a team governs the session model (guard set via
// SetModelPersistenceSuppressed), changing the thinking level applies at
// session level but writes NOTHING to home/project config — the team's model
// must never leak into the user's saved configuration.
func TestAgentManager_SetThinkingLevel_PersistenceSuppressed(t *testing.T) {
	cfg := thinkingLevelTestConfig()
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	am := NewAgentManager(cfg, nil, nil, sessionState, event.MakeBus(10, 10, 10, 10), "")
	saver := &recordingConfigSaver{}
	am.SetConfigSaver(saver)

	am.SetModelPersistenceSuppressed(true)
	if !am.ModelPersistenceSuppressed() {
		t.Fatal("ModelPersistenceSuppressed = false after SetModelPersistenceSuppressed(true)")
	}

	if err := am.SetThinkingLevel("high"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	if got := am.GetThinkingLevel(); got != "high" {
		t.Errorf("session level = %q, want high (session-level change must still apply)", got)
	}
	if saver.homeSaves != 0 || saver.projectSaves != 0 {
		t.Errorf("saves while suppressed: home=%d project=%d, want 0/0", saver.homeSaves, saver.projectSaves)
	}
	if got := cfg.GetModelByID("deepseek").ThinkingLevel; got != "" {
		t.Errorf("model config entry mutated while suppressed: thinking level = %q, want empty", got)
	}

	// Releasing the guard re-enables persistence.
	am.SetModelPersistenceSuppressed(false)
	if err := am.SetThinkingLevel("low"); err != nil {
		t.Fatalf("SetThinkingLevel after release: %v", err)
	}
	if saver.homeSaves != 1 {
		t.Errorf("home saves after release = %d, want 1", saver.homeSaves)
	}
}

// TestAgentManager_SetThinkingLevel_SaveError verifies a persistence failure
// is surfaced to the caller.
func TestAgentManager_SetThinkingLevel_SaveError(t *testing.T) {
	cfg := thinkingLevelTestConfig()
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetConfigSaver(&recordingConfigSaver{err: fmt.Errorf("disk full")})

	if err := am.SetThinkingLevel("high"); err == nil {
		t.Fatal("expected error when saving model thinking level fails")
	}
}

// TestAgentManager_SetThinkingLevel_UnconfiguredModel verifies changing the
// level with a model that is not in the config's model list stays session-only.
func TestAgentManager_SetThinkingLevel_UnconfiguredModel(t *testing.T) {
	cfg := &config.Config{ActiveModel: "custom-remote"}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	saver := &recordingConfigSaver{}
	am.SetConfigSaver(saver)

	if err := am.SetThinkingLevel("low"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	if got := am.GetThinkingLevel(); got != "low" {
		t.Errorf("session level = %q, want low", got)
	}
	if saver.homeSaves != 0 {
		t.Errorf("home saves = %d, want 0 for unconfigured model", saver.homeSaves)
	}
}

// TestAgentManager_SetThinkingLevel_AutoSaveModel verifies the project config
// is also updated when execution.auto_save_model is enabled.
func TestAgentManager_SetThinkingLevel_AutoSaveModel(t *testing.T) {
	cfg := thinkingLevelTestConfig()
	cfg.Execution.AutoSaveModel = true
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	saver := &recordingConfigSaver{}
	am.SetConfigSaver(saver)

	if err := am.SetThinkingLevel("low"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	if saver.projectSaves != 1 {
		t.Errorf("project saves = %d, want 1 (auto_save_model on)", saver.projectSaves)
	}
}

// TestAgentManager_RestoreThinkingLevel_DoesNotSaveToModel verifies the
// startup restore path updates the session level but never writes the model
// config entry or disk.
func TestAgentManager_RestoreThinkingLevel_DoesNotSaveToModel(t *testing.T) {
	cfg := thinkingLevelTestConfig()
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	saver := &recordingConfigSaver{}
	am.SetConfigSaver(saver)

	if err := am.RestoreThinkingLevel("xhigh"); err != nil {
		t.Fatalf("RestoreThinkingLevel: %v", err)
	}
	if got := am.GetThinkingLevel(); got != "xhigh" {
		t.Errorf("session level = %q, want xhigh", got)
	}
	if got := cfg.GetModelByID("deepseek").ThinkingLevel; got != "" {
		t.Errorf("restore wrote model config entry: %q", got)
	}
	if saver.homeSaves != 0 || saver.projectSaves != 0 {
		t.Errorf("restore persisted: home=%d project=%d, want 0", saver.homeSaves, saver.projectSaves)
	}
}

// TestAgentManager_SetModel_RestoresModelThinkingLevel verifies switching
// models restores each model's own saved thinking level: a level set while on
// deepseek must not follow the session onto kimi, and switching back to
// deepseek must bring its saved level back.
func TestAgentManager_SetModel_RestoresModelThinkingLevel(t *testing.T) {
	cfg := thinkingLevelTestConfig()
	cfg.GetModelByID("kimi").ThinkingLevel = "low"
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	newTestModel := func(id string) agenticprovider.Model {
		return agenticprovider.Model{
			ID:         id,
			Api:        agenticprovider.ApiOpenAICompletions,
			Provider:   agenticprovider.ProviderLMStudio,
			BaseURL:    "http://localhost:1234/v1/chat/completions",
			InputTypes: []string{"text"},
		}
	}
	opts := agenticprovider.StreamOptions{MaxTokens: 256}
	if _, err := am.StartSession(newTestModel("deepseek-chat"), opts, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Change deepseek's level; it is saved on the deepseek config entry.
	if err := am.SetThinkingLevel("high"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	am.applyPendingThinkingLevel()

	// Switch to kimi (callers update cfg.ActiveModel before SetModel).
	cfg.ActiveModel = "kimi"
	am.SetModel(newTestModel("kimi-k2"))
	if got := am.GetThinkingLevel(); got != "low" {
		t.Errorf("after switch to kimi: session level = %q, want kimi's saved low", got)
	}
	am.applyPendingThinkingLevel()
	if got := am.activeAgent.ReasoningEffort(); got != agentic.ReasoningEffort("low") {
		t.Errorf("agent effort after kimi switch = %q, want low", got)
	}

	// Switch back to deepseek: its saved level must be restored.
	cfg.ActiveModel = "deepseek"
	am.SetModel(newTestModel("deepseek-chat"))
	if got := am.GetThinkingLevel(); got != "high" {
		t.Errorf("after switch back to deepseek: session level = %q, want saved high", got)
	}
	am.applyPendingThinkingLevel()
	if got := am.activeAgent.ReasoningEffort(); got != agentic.ReasoningEffort("high") {
		t.Errorf("agent effort after deepseek switch = %q, want high", got)
	}

	// The model switch itself must not write config entries.
	if got := cfg.GetModelByID("kimi").ThinkingLevel; got != "low" {
		t.Errorf("kimi entry = %q, want low (unchanged by switches)", got)
	}
}

// TestAgentManager_SetDisableToolBudget verifies the session-level toggle.
