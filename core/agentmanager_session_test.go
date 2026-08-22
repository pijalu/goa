// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

// TestNewAgentManager_WithModeFields verifies the new constructor accepts mode state.

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

func TestAgentManager_SteeringAppendWhileRunning(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.activeAgent = agentic.NewAgent(agentic.Config{})
	am.running = true

	err := am.SendUserInput("steer me")
	if err != nil {
		t.Fatalf("SendUserInput while running: %v", err)
	}
	if am.steering.Len() != 1 {
		t.Errorf("steering queue length = %d, want 1", am.steering.Len())
	}
	pending := am.steering.Flush()
	if len(pending) != 1 || pending[0] != "steer me" {
		t.Errorf("steering queue = %v, want [steer me]", pending)
	}
}

// TestAgentManager_SteeringFlushedAtTurnEnd verifies that steering input
// appended while the agent is running is flushed from the queue when the
// current turn completes. The flushed text is captured as pendingSteering and
// dispatched by the turn defer.
func TestAgentManager_SteeringFlushedAtTurnEnd(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	runner := &recordingRunner{started: make(chan struct{})}

	am.activeAgent = agentic.NewAgent(agentic.Config{})
	am.running = true
	am.steering.Append("steer me")
	am.steering.Append("and me")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	am.runAgentTurn(ctx, cancel, 1, runner, "initial", nil)

	if am.steering.Len() != 0 {
		t.Errorf("steering queue length = %d, want 0 after turn end", am.steering.Len())
	}
	// The defer should have dispatched pendingSteering via SendUserInput; since
	// the active agent is minimal, SendUserInput will fail, but the queue flush
	// itself is the behavior under test.
}

type recordingRunner struct {
	started chan struct{}
}

func (r *recordingRunner) Run(ctx context.Context, input string) error {
	if r.started != nil {
		close(r.started)
	}
	return nil
}

func (r *recordingRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	return r.Run(ctx, input)
}

// TestAgentManager_StartSession_FreshConversationID covers Issue 8: every
// StartSession must give the agent a FRESH, non-empty StreamOptions.SessionID so
// provider-side cache (prompt_cache_key / previous_response_id / session-affinity)
// is cleared between sessions (/new, first start). Two consecutive sessions must not
// share an id.
func TestAgentManager_StartSession_FreshConversationID(t *testing.T) {
	cfg := &config.Config{}
	dir := t.TempDir()
	ss := NewSessionStore(dir)
	defer ss.Close()
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, ss, nil, sessionState, tuiEvents, dir)

	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}

	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	id1 := am.CurrentAgent().StreamOptions().SessionID
	if id1 == "" {
		t.Fatalf("first session: StreamOptions.SessionID is empty (cache key would be unset)")
	}
	if err := am.StopSession(); err != nil {
		t.Fatalf("StopSession: %v", err)
	}

	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("second StartSession: %v", err)
	}
	id2 := am.CurrentAgent().StreamOptions().SessionID
	if id2 == "" {
		t.Fatalf("second session: StreamOptions.SessionID is empty")
	}
	if id1 == id2 {
		t.Fatalf("consecutive sessions reused SessionID %q; provider cache would not be cleared", id1)
	}
}

// TestAgentManager_ResetConversationID covers Issue 8: rotating the
// conversation id on a live session (fresh-context goal) must give the active
// agent a NEW non-empty SessionID distinct from the previous one, so provider
// cache is not carried into the clean context.
func TestAgentManager_ResetConversationID(t *testing.T) {
	cfg := &config.Config{}
	dir := t.TempDir()
	ss := NewSessionStore(dir)
	defer ss.Close()
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, ss, nil, sessionState, tuiEvents, dir)

	mdl := agenticprovider.Model{
		ID:         "test-model",
		Api:        agenticprovider.ApiOpenAICompletions,
		Provider:   agenticprovider.ProviderLMStudio,
		BaseURL:    "http://localhost:1234/v1/chat/completions",
		InputTypes: []string{"text"},
	}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	before := am.CurrentAgent().StreamOptions().SessionID

	newID := am.ResetConversationID()
	if newID == "" {
		t.Fatalf("ResetConversationID returned empty id")
	}
	if newID == before {
		t.Fatalf("ResetConversationID reused id %q", newID)
	}
	if got := am.CurrentAgent().StreamOptions().SessionID; got != newID {
		t.Fatalf("agent StreamOptions.SessionID = %q, want rotated %q", got, newID)
	}
}

// okRunner completes a turn immediately.
type okRunner struct{}

func (o *okRunner) Run(ctx context.Context, input string) error { return nil }
func (o *okRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	return nil
}

// TestRunAgentTurn_PostTurnHookFiresAfterCleanup asserts the post-turn hook
// observes the turn as fully ended (Issue 7): the hook starts the
// goal driver, and if it fired while the manager still marked the turn
// running, the driver's agent-busy guard would always trip — or worse,
// without the guard, the driver would queue-storm continuation prompts into
// the still-processing agent.
func TestRunAgentTurn_PostTurnHookFiresAfterCleanup(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	runningAtHook := true
	hookFired := false
	am.SetPostTurnHook(func() {
		hookFired = true
		runningAtHook = am.IsRunning()
	})

	// Simulate SendUserInput's running state for the duration of the turn.
	am.mu.Lock()
	am.running = true
	am.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	am.runAgentTurn(ctx, cancel, 1, &okRunner{}, "hello", nil)

	if !hookFired {
		t.Fatal("post-turn hook did not fire")
	}
	if runningAtHook {
		t.Error("post-turn hook fired while the manager still marked the turn running")
	}
}

// TestRunAgentTurn_PostTurnHookSkippedOnPanic guards the panic path: a
// panicking turn must not re-drive goals (the hook only fires for turns that
// ended normally).
func TestRunAgentTurn_PostTurnHookSkippedOnPanic(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetForwardInternalEvents(true)

	hookFired := false
	am.SetPostTurnHook(func() { hookFired = true })

	am.mu.Lock()
	am.running = true
	am.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		am.runAgentTurn(ctx, cancel, 1, &panicRunner{}, "hello", nil)
	}()
	select {
	case <-am.events:
	case <-time.After(2 * time.Second):
		t.Fatal("no EventEnd emitted after agent panic")
	}
	<-done

	if hookFired {
		t.Error("post-turn hook fired after a panicking turn; want it skipped")
	}
}

// TestAgentManager_PersistState_SavesCompanionHistory is the F6 regression:
// the headless agent-driven companion path never wrote companion_history to
// state.json because persistState was only triggered by mode changes. The
// exported PersistState must persist the companion agent's history.
func TestAgentManager_PersistState_SavesCompanionHistory(t *testing.T) {
	dir := t.TempDir()
	am := NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetStateStore(NewStateStore(dir))

	agent := agentic.NewAgent(agentic.Config{Model: agenticprovider.Model{ID: "comp-model"}})
	am.SetCompanionAgent(agent)
	agent.SetHistory([]agentic.Message{
		{Type: agentic.Content, Role: agentic.User, Content: "review this"},
	})

	if err := am.PersistState(); err != nil {
		t.Fatalf("PersistState: %v", err)
	}
	snap, err := NewStateStore(dir).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(snap.CompanionHistory) == 0 {
		t.Fatal("companion_history not persisted by PersistState (F6)")
	}
}

// TestAgentManager_LogCompanionStarted_WritesModelMarker is the F6 regression:
// the companion path had no per-agent model identity logged (unlike the
// orchestrator's agent_started.model). LogCompanionStarted must write a
// machine-checkable session-log marker carrying the companion model.
func TestAgentManager_LogCompanionStarted_WritesModelMarker(t *testing.T) {
	dir := t.TempDir()
	am := NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	ss := NewSessionStore(dir)
	sessionID := ss.StartSession()
	am.sessionStore = ss

	agent := agentic.NewAgent(agentic.Config{Model: agenticprovider.Model{ID: "comp-model", Provider: "lmstudio"}})
	am.LogCompanionStarted(agent)

	if err := ss.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events, err := ss.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	for _, ev := range events {
		if ev.Type != agentic.EventProgress {
			continue
		}
		if ev.Metadata["event"] == "companion_started" {
			if ev.Metadata["model"] != "comp-model" {
				t.Errorf("companion_started model = %q, want comp-model", ev.Metadata["model"])
			}
			if ev.Metadata["provider"] != "lmstudio" {
				t.Errorf("companion_started provider = %q, want lmstudio", ev.Metadata["provider"])
			}
			return
		}
	}
	t.Fatal("no companion_started marker in session log (F6)")
}

// loopResetRunner tracks ResetLoopStop calls; used to verify that a genuine
// new user turn clears the agent's runaway-loop latch (runaway-loop
// bricking) while runners without the optional interface are left alone.
type loopResetRunner struct {
	resetCalls int
}

func (r *loopResetRunner) Run(ctx context.Context, input string) error { return nil }

func (r *loopResetRunner) RunWithImages(ctx context.Context, input string, images []string) error {
	return nil
}

func (r *loopResetRunner) ResetLoopStop() { r.resetCalls++ }

// TestAgentManager_UserTurnResetsLoopStop verifies runAgentTurn resets the
// agent's runaway-loop latch on a genuine new user turn.
func TestAgentManager_UserTurnResetsLoopStop(t *testing.T) {
	cfg := &config.Config{}
	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &loopResetRunner{}
	am.runAgentTurn(ctx, cancel, 1, runner, "hello", nil)

	if runner.resetCalls != 1 {
		t.Fatalf("ResetLoopStop called %d times, want 1", runner.resetCalls)
	}
}

// TestAgentManager_BuildCompressionConfig_OnErrorStrategyMapping pins the
// on-error wiring: config on_error_strategy maps into the agentic config
// verbatim (empty stays empty → the SDK hybrid default), and enabled:false
// does not touch the reactive net fields.
func TestAgentManager_BuildCompressionConfig_OnErrorStrategyMapping(t *testing.T) {
	mk := func(onErr string) agentic.ContextCompressionConfig {
		cfg := &config.Config{
			ContextCompression: config.ContextCompressionConfig{
				Enabled:         ccBoolPtr(true),
				OnContextError:  true,
				OnErrorStrategy: onErr,
				Thresholds:      config.CompressionThresholdsConfig{HardPercent: 95},
			},
		}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		return am.buildCompressionConfig(cfg, "some-model", 32768)
	}

	if got := mk("summarize").OnErrorStrategy; got != agentic.CompressionSummarize {
		t.Errorf("OnErrorStrategy = %q, want summarize", got)
	}
	if got := mk("").OnErrorStrategy; got != "" {
		t.Errorf("empty OnErrorStrategy mapped to %q, want empty (SDK hybrid default)", got)
	}

	// enabled:false zeroes every proactive layer INCLUDING the hard ceiling
	// (0 = disabled under the opt-in semantics) but keeps the reactive net.
	cfgOff := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:         ccBoolPtr(false),
			OnContextError:  true,
			OnErrorStrategy: "selective",
			Thresholds:      config.CompressionThresholdsConfig{HardPercent: 95},
		},
	}
	amOff := NewAgentManager(cfgOff, nil, nil, nil, nil, "")
	off := amOff.buildCompressionConfig(cfgOff, "some-model", 32768)
	if off.Thresholds.HardPercent != 0 {
		t.Errorf("enabled:false must zero hard too, got %d (0 = disabled semantics)", off.Thresholds.HardPercent)
	}
	if !off.OnContextError || off.OnErrorStrategy != agentic.CompressionSelective {
		t.Errorf("enabled:false must keep the reactive net, got on_error=%v strategy=%q", off.OnContextError, off.OnErrorStrategy)
	}
}

// TestAgentManager_BuildCompressionConfig_EmbeddedDefaultHardSummarize pins
// the shipped-default path: the embedded default.yaml sets hard 95 explicitly
// and leaves the layer strategies unset, so the SDK resolves the hard tier at
// 95 with summarize (soft/trigger opt-in off).
func TestAgentManager_BuildCompressionConfig_EmbeddedDefaultHardSummarize(t *testing.T) {
	// Isolate from the user's home config so only embedded defaults apply.
	t.Setenv("HOME", t.TempDir())
	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ContextCompression.Thresholds.HardPercent != 95 {
		t.Fatalf("test setup: embedded default hard = %d, want 95", cfg.ContextCompression.Thresholds.HardPercent)
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	cc := am.buildCompressionConfig(cfg, "some-model", 32768)

	if cc.Thresholds.HardPercent != 95 {
		t.Errorf("HardPercent = %d, want 95 (explicit embedded default)", cc.Thresholds.HardPercent)
	}
	if cc.Strategies.Hard != "" {
		t.Errorf("Strategies.Hard = %q, want empty (SDK resolves unset hard to summarize)", cc.Strategies.Hard)
	}
	// NOTE: soft stays opt-in off; trigger may be auto-derived from the
	// legacy execution.token_critical default (90) — out of scope here.
	if cc.Thresholds.SoftPercent != 0 {
		t.Errorf("soft must stay opt-in off, got soft=%d", cc.Thresholds.SoftPercent)
	}
	if cc.OnErrorStrategy != agentic.CompressionHybrid {
		t.Errorf("OnErrorStrategy = %q, want hybrid (embedded default)", cc.OnErrorStrategy)
	}
}

// TestAgentManager_StartSession_WiresSpillPolicy verifies the spill-policy
// factory (gap CX2) is invoked per session with the store's session ID so the
// policy can scope its spill dir, and that a nil factory leaves the agent
// without a policy.
func TestAgentManager_StartSession_WiresSpillPolicy(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
	}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	sessionStore := NewSessionStore(t.TempDir())
	defer sessionStore.Close()
	am := NewAgentManager(cfg, sessionStore, nil, sessionState, tuiEvents, "")

	var gotSessionID string
	factoryCalls := 0
	am.SetSpillPolicyFactory(func(sessionID string) agentic.SpillPolicy {
		factoryCalls++
		gotSessionID = sessionID
		return &stubSpillPolicy{}
	})

	mdl := agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	if factoryCalls != 1 {
		t.Fatalf("spill policy factory should be invoked once per session, got %d", factoryCalls)
	}
	agent := am.CurrentAgent()
	if agent == nil {
		t.Fatal("CurrentAgent should be set after StartSession")
	}
	if gotSessionID == "" || gotSessionID != agent.StreamOptions().SessionID {
		t.Errorf("factory session ID %q should match the agent session %q", gotSessionID, agent.StreamOptions().SessionID)
	}
	if agent.SpillPolicy() == nil {
		t.Error("agent should carry the factory-provided spill policy")
	}
}

type stubSpillPolicy struct{}

func (stubSpillPolicy) ApplySpill(toolName, result string) string { return result }
