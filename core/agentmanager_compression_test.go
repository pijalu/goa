// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

// TestNewAgentManager_WithModeFields verifies the new constructor accepts mode state.

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
	am, prov := setupBlockingAgentManager(t)

	waitForProviderStart(t, prov)
	waitForAgentManagerRunning(t, am, true)

	if err := am.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	waitForEndEvent(t, am)
	waitForAgentManagerRunning(t, am, false)
}

func setupBlockingAgentManager(t *testing.T) (*AgentManager, *blockingProvider) {
	t.Helper()
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
	return am, prov
}

func waitForProviderStart(t *testing.T, prov *blockingProvider) {
	t.Helper()
	select {
	case <-prov.started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider Stream did not start")
	}
}

func waitForAgentManagerRunning(t *testing.T, am *AgentManager, wantRunning bool) {
	t.Helper()
	waitForCondition(t, func() bool {
		am.mu.Lock()
		defer am.mu.Unlock()
		if wantRunning {
			return am.running
		}
		return !am.running && am.cancel == nil
	}, 100*time.Millisecond)
}

func waitForEndEvent(t *testing.T, am *AgentManager) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case ev := <-am.Events():
			if ev.Type == agentic.EventEnd {
				if ev.Text != "" {
					t.Fatalf("expected empty Text for user cancellation, got %q", ev.Text)
				}
				if ev.Metadata["cancelled"] != "true" {
					t.Fatalf("expected cancelled metadata for user cancellation, got %v", ev.Metadata)
				}
				return
			}
		case <-deadline:
			t.Fatal("turn did not terminate within 100 ms of Interrupt")
		}
	}
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
// when the user does not configure a compression limit, AgentManager leaves
// MaxTokens at 0 so the agent can fall back to the runtime model window. This
// avoids a stale hard limit when the local model's loaded context window is
// refreshed later.
func TestAgentManager_BuildCompressionConfig_AutoFromModelWindow(t *testing.T) {
	cfg := &config.Config{
		Execution: config.ExecutionConfig{TokenCritical: 80},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, "some-model", 32768)
	if cc.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 (auto from model window)", cc.MaxTokens)
	}
	if cc.Thresholds.HardPercent != 80 {
		t.Errorf("Thresholds.HardPercent = %d, want 80 (from deprecated token_critical fallback)", cc.Thresholds.HardPercent)
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

	cc := am.buildCompressionConfig(cfg, "some-model", 32768)
	if cc.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", cc.MaxTokens)
	}
	if cc.Thresholds.HardPercent != 50 {
		t.Errorf("Thresholds.HardPercent = %d, want 50 (legacy alias wins)", cc.Thresholds.HardPercent)
	}
	// The legacy whole-config strategy maps onto the hard layer.
	if cc.Strategies.Hard != agentic.CompressionSelective {
		t.Errorf("Strategies.Hard = %q, want selective", cc.Strategies.Hard)
	}
}

// perModelOverlayBaseConfig returns the shared global compression fixture for
// the per-model overlay tests: micro legacy strategy, explicit hard ceiling
// 95, plus a full override ("local-qwen") and a partial override
// ("partial-model").
func perModelOverlayBaseConfig() *config.Config {
	return &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:             ccBoolPtr(true),
			MaxTokens:           0,
			Strategy:            config.AgenticCompressionMicro,
			PreserveRecentTurns: 4,
			Thresholds: config.CompressionThresholdsConfig{
				SoftPercent:    0,
				TriggerPercent: 80,
				HardPercent:    95,
			},
			PerModel: map[string]config.ModelCompressionOverride{
				"local-qwen": {
					MaxTokens: 24576,
					Strategy:  config.AgenticCompressionHybrid,
					Thresholds: config.CompressionThresholdsConfig{
						SoftPercent:    40,
						TriggerPercent: 65,
						HardPercent:    90,
					},
				},
				"partial-model": {
					Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 70},
				},
			},
		},
	}
}

// Scenario verifiers for the per-model overlay matrix. Each lives at package
// level so complexity tools attribute their branches to themselves, keeping
// the table-driven test itself trivial.

// assertFullOverrideApplied checks the local-qwen full override wins on every
// overridden field.
func assertFullOverrideApplied(t *testing.T, cc agentic.ContextCompressionConfig) {
	t.Helper()
	if cc.MaxTokens != 24576 {
		t.Errorf("MaxTokens = %d, want 24576", cc.MaxTokens)
	}
	// Legacy whole-config strategy maps onto the hard layer.
	if cc.Strategies.Hard != agentic.CompressionHybrid {
		t.Errorf("Strategies.Hard = %q, want hybrid", cc.Strategies.Hard)
	}
	// Soft 40 and hard 90 come through; trigger_percent (65) is dropped
	// (no trigger layer) — explicit hard_percent wins the ceiling.
	if cc.Thresholds.SoftPercent != 40 || cc.Thresholds.HardPercent != 90 {
		t.Errorf("Thresholds = %+v, want {Soft:40 Hard:90}", cc.Thresholds)
	}
}

// assertPartialInheritsGlobal checks the partial-model override only replaces
// what it declares, inheriting global values elsewhere.
func assertPartialInheritsGlobal(t *testing.T, cc agentic.ContextCompressionConfig) {
	t.Helper()
	// Explicit global hard_percent (95) wins over the per-model
	// trigger_percent (70): trigger no longer feeds the ceiling.
	if cc.Thresholds.HardPercent != 95 {
		t.Errorf("HardPercent = %d, want 95 (explicit global hard wins)", cc.Thresholds.HardPercent)
	}
	// Global legacy strategy (micro) maps onto the hard layer.
	if cc.Strategies.Hard != agentic.CompressionMicro {
		t.Errorf("Strategies.Hard = %q, want micro (inherited)", cc.Strategies.Hard)
	}
	if cc.PreserveRecentTurns != 4 {
		t.Errorf("PreserveRecentTurns = %d, want 4 (inherited)", cc.PreserveRecentTurns)
	}
}

// assertGlobalHardCeilingInherited checks the merged config kept the global
// hard ceiling of 95 (default inheritance expectation).
func assertGlobalHardCeilingInherited(t *testing.T, cc agentic.ContextCompressionConfig) {
	t.Helper()
	if cc.Thresholds.HardPercent != 95 {
		t.Errorf("HardPercent = %d, want 95 (global)", cc.Thresholds.HardPercent)
	}
}

// assertLegacyAliasCeiling maps onto the hard ceiling expectation for the
// legacy per-model threshold_percent alias.
func assertLegacyAliasCeiling(t *testing.T, cc agentic.ContextCompressionConfig) {
	t.Helper()
	// Legacy threshold_percent maps onto the hard ceiling.
	if cc.Thresholds.HardPercent != 55 {
		t.Errorf("HardPercent = %d, want 55 (legacy per-model alias)", cc.Thresholds.HardPercent)
	}
}

// enableLegacyAlias clears explicit ceilings and adds a legacy
// threshold_percent override so the alias alone defines the ceiling.
func enableLegacyAlias(cfg *config.Config) {
	cfg.ContextCompression.Thresholds.HardPercent = 0
	cfg.ContextCompression.PerModel["legacy-model"] =
		config.ModelCompressionOverride{ThresholdPercent: 55}
}

// TestAgentManager_BuildCompressionConfig_PerModelOverlay verifies that
// per-model overrides apply on top of the global section: matching model IDs
// get the overridden fields, non-matching IDs get the global values, and
// partial overrides inherit the rest.
func TestAgentManager_BuildCompressionConfig_PerModelOverlay(t *testing.T) {
	scenarios := []struct {
		name string
		// model is the model ID buildCompressionConfig resolves against.
		model string
		// mutate optionally adjusts the base config before building.
		mutate func(cfg *config.Config)
		// verify asserts the merged ContextCompressionConfig.
		verify func(t *testing.T, cc agentic.ContextCompressionConfig)
	}{
		{
			name:   "full override applies",
			model:  "local-qwen",
			verify: assertFullOverrideApplied,
		},
		{
			name:   "partial override inherits global",
			model:  "partial-model",
			verify: assertPartialInheritsGlobal,
		},
		{
			name:   "unknown model gets global",
			model:  "other-model",
			verify: assertGlobalHardCeilingInherited,
		},
		{
			name:   "empty model ID gets global",
			model:  "",
			verify: assertGlobalHardCeilingInherited,
		},
		{
			name:   "legacy threshold_percent in per-model override",
			model:  "legacy-model",
			mutate: enableLegacyAlias,
			verify: assertLegacyAliasCeiling,
		},
	}

	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			cfg := perModelOverlayBaseConfig()
			if tc.mutate != nil {
				tc.mutate(cfg)
			}
			am := NewAgentManager(cfg, nil, nil, nil, nil, "")
			tc.verify(t, am.buildCompressionConfig(cfg, tc.model, 32768))
		})
	}
}

// TestAgentManager_BuildCompressionConfig_ToolResultPruning verifies the CX1
// pruner settings flow from YAML config into the SDK compression config:
// explicit values win, zero values inherit the documented defaults.
func TestAgentManager_BuildCompressionConfig_ToolResultPruning(t *testing.T) {
	t.Run("explicit values win", func(t *testing.T) {
		cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
			ToolResultPruning: config.ToolResultPruningSettings{
				ThresholdChars: 4096, HeadChars: 2048, TailChars: 512,
			},
		}}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		cc := am.buildCompressionConfig(cfg, "some-model", 32768)
		want := agentic.ToolResultPruningConfig{ThresholdChars: 4096, HeadChars: 2048, TailChars: 512}
		if cc.ToolResultPruning != want {
			t.Errorf("ToolResultPruning = %+v, want %+v", cc.ToolResultPruning, want)
		}
	})

	t.Run("zero inherits SDK defaults", func(t *testing.T) {
		cfg := &config.Config{}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		cc := am.buildCompressionConfig(cfg, "some-model", 32768)
		if cc.ToolResultPruning != agentic.DefaultToolResultPruningConfig {
			t.Errorf("ToolResultPruning = %+v, want defaults %+v", cc.ToolResultPruning, agentic.DefaultToolResultPruningConfig)
		}
	})

	t.Run("partial override inherits per field", func(t *testing.T) {
		cfg := &config.Config{ContextCompression: config.ContextCompressionConfig{
			ToolResultPruning: config.ToolResultPruningSettings{ThresholdChars: 4096},
		}}
		am := NewAgentManager(cfg, nil, nil, nil, nil, "")
		cc := am.buildCompressionConfig(cfg, "some-model", 32768)
		want := agentic.ToolResultPruningConfig{ThresholdChars: 4096, HeadChars: 4096, TailChars: 1024}
		if cc.ToolResultPruning != want {
			t.Errorf("ToolResultPruning = %+v, want %+v", cc.ToolResultPruning, want)
		}
	})
}

// TestAgentManager_BuildCompressionConfig_EnabledFalseDisablesProactive
// verifies the Enabled toggle is honored end-to-end: an explicit
// `enabled: false` zeroes every proactive threshold (soft/trigger/hard), so
// no threshold-triggered compression fires — while the reactive on-error net
// (OnContextError) is passed through untouched.
func TestAgentManager_BuildCompressionConfig_EnabledFalseDisablesProactive(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:        ccBoolPtr(false),
			OnContextError: true,
			Strategy:       config.AgenticCompressionToolElision,
			Thresholds: config.CompressionThresholdsConfig{
				SoftPercent:    30,
				TriggerPercent: 80,
				HardPercent:    95,
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, "some-model", 32768)
	if cc.Thresholds.SoftPercent != 0 || cc.Thresholds.HardPercent != 0 {
		t.Errorf("enabled:false must zero proactive thresholds, got %+v", cc.Thresholds)
	}
	if !cc.OnContextError {
		t.Error("enabled:false must not disable the reactive on_context_error net")
	}

	// Sanity: enabled (nil default) preserves the configured ceiling (the
	// legacy trigger_percent feeds the hard ceiling when hard is unset).
	cfgOn := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Strategy:   config.AgenticCompressionToolElision,
			Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 80},
		},
	}
	amOn := NewAgentManager(cfgOn, nil, nil, nil, nil, "")
	ccOn := amOn.buildCompressionConfig(cfgOn, "some-model", 32768)
	if ccOn.Thresholds.HardPercent != 80 {
		t.Errorf("enabled (default) must keep ceiling=80, got %d", ccOn.Thresholds.HardPercent)
	}
}

// TestAgentManager_SetModel_AppliesPerModelOverride verifies that switching
// the model mid-session re-resolves the compression config so the new model's
// per-model override takes effect (even with MaxTokens=0/auto).
func TestAgentManager_SetModel_AppliesPerModelOverride(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:  ccBoolPtr(true),
			Strategy: config.AgenticCompressionToolElision,
			Thresholds: config.CompressionThresholdsConfig{
				TriggerPercent: 80,
				HardPercent:    95,
			},
			PerModel: map[string]config.ModelCompressionOverride{
				"small-local": {
					Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 60, HardPercent: 85},
				},
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	if _, err := am.StartSession(
		agenticprovider.Model{ID: "big-model", Api: agenticprovider.ApiOpenAICompletions, ContextWindow: 131072},
		agenticprovider.StreamOptions{},
		"sys",
		nil,
		cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	am.SetModel(agenticprovider.Model{ID: "small-local", Api: agenticprovider.ApiOpenAICompletions, ContextWindow: 32768})

	got := am.CurrentAgent().CompressionConfig()
	// Per-model explicit hard_percent (85) wins the ceiling; trigger no longer
	// feeds it, so the legacy per-model trigger (60) is dropped.
	if got.Thresholds.HardPercent != 85 {
		t.Errorf("after SetModel HardPercent = %d, want 85 (per-model override)", got.Thresholds.HardPercent)
	}
}

// TestAgentManager_RefreshContextCompression verifies that changing
// context_compression config and calling RefreshContextCompression applies
// the new thresholds to the live agent (used by /config live-sync).
func TestAgentManager_RefreshContextCompression(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:    ccBoolPtr(true),
			Thresholds: config.CompressionThresholdsConfig{TriggerPercent: 80, HardPercent: 95},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")
	if _, err := am.StartSession(
		agenticprovider.Model{ID: "m1", Api: agenticprovider.ApiOpenAICompletions, ContextWindow: 32768},
		agenticprovider.StreamOptions{},
		"sys",
		nil,
		cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Explicit hard_percent (95) wins the ceiling; the legacy trigger (80) is
	// dropped once a hard ceiling is set.
	if got := am.CurrentAgent().CompressionConfig().Thresholds.HardPercent; got != 95 {
		t.Fatalf("initial HardPercent = %d, want 95", got)
	}

	// User changes thresholds via /config (in-memory config mutated by the
	// setter, then runtime sync calls RefreshContextCompression).
	cfg.ContextCompression.Thresholds.SoftPercent = 40
	cfg.ContextCompression.Thresholds.HardPercent = 85
	am.RefreshContextCompression()

	got := am.CurrentAgent().CompressionConfig()
	if got.Thresholds.HardPercent != 85 {
		t.Errorf("after refresh HardPercent = %d, want 85", got.Thresholds.HardPercent)
	}
	if got.Thresholds.SoftPercent != 40 {
		t.Errorf("after refresh SoftPercent = %d, want 40", got.Thresholds.SoftPercent)
	}
}

// TestAgentManager_HandleLoopWarningCriticalInterrupts guards STUB-2: the
// TestAgentManager_ThinkingLoopInterrupts verifies that a thinking/reasoning
// loop cancels the in-flight turn, mirroring the tool-loop interrupt path. This
// reproduces the failure captured in where the assistant repeated the
// same reasoning paragraph many times with no loop protection firing.
