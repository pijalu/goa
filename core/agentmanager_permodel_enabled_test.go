// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
)

func ccBool(v bool) *bool { return &v }

// TestBuildCompressionConfig_PerModelCeilingSurvivesGlobalDisable is the
// frigolite regression (bugs.md 2026-08-26): a blanket `enabled: false`
// landing at a higher cascade layer (stale project dump) must NOT zero the
// per-model hard ceiling — a stated per-model ceiling is an explicit opt-in
// for that model.
func TestBuildCompressionConfig_PerModelCeilingSurvivesGlobalDisable(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:        ccBool(false), // the stale project-layer kill
			OnContextError: false,         // the stale dump's reactive off
			PerModel: map[string]config.ModelCompressionOverride{
				"glm-5-3-flash": {
					Thresholds: config.CompressionThresholdsConfig{HardPercent: 20},
					Strategies: config.CompressionLayerStrategiesConfig{Hard: "summarize"},
					CacheGate:  "off",
				},
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, "glm-5-3-flash", 1000000)
	if cc.Thresholds.HardPercent != 20 {
		t.Errorf("HardPercent = %d, want 20 (per-model ceiling survives the global disable)", cc.Thresholds.HardPercent)
	}
	if cc.Strategies.Hard != agentic.CompressionSummarize {
		t.Errorf("hard strategy = %q, want summarize", cc.Strategies.Hard)
	}
	if !cc.DisableCacheGate {
		t.Error("cache_gate off must surface as DisableCacheGate=true")
	}

	// A model WITHOUT an override stays fully disabled under the global kill.
	ccOther := am.buildCompressionConfig(cfg, "plain-model", 1000000)
	if ccOther.Thresholds.HardPercent != 0 {
		t.Errorf("plain model HardPercent = %d, want 0 (global disable governs)", ccOther.Thresholds.HardPercent)
	}
}

// TestBuildCompressionConfig_PerModelExplicitOptOut: per-model enabled:false
// takes that model fully off even under a globally enabled compression.
func TestBuildCompressionConfig_PerModelExplicitOptOut(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:    ccBool(true),
			Thresholds: config.CompressionThresholdsConfig{HardPercent: 95},
			PerModel: map[string]config.ModelCompressionOverride{
				"off-model": {
					Enabled:    ccBool(false),
					Thresholds: config.CompressionThresholdsConfig{HardPercent: 20},
				},
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, "off-model", 32768)
	if cc.Thresholds.HardPercent != 0 {
		t.Errorf("HardPercent = %d, want 0 (per-model explicit opt-out zeroes every layer)", cc.Thresholds.HardPercent)
	}
	if cc.Thresholds.SoftPercent != 0 {
		t.Errorf("SoftPercent = %d, want 0", cc.Thresholds.SoftPercent)
	}
}

// TestBuildCompressionConfig_PerModelExplicitOptIn: per-model enabled:true
// force-enables compression for that model under a global disable, with the
// global thresholds applying (no per-model thresholds stated).
func TestBuildCompressionConfig_PerModelExplicitOptIn(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:    ccBool(false),
			Thresholds: config.CompressionThresholdsConfig{HardPercent: 90},
			PerModel: map[string]config.ModelCompressionOverride{
				"on-model": {Enabled: ccBool(true)},
			},
		},
	}
	am := NewAgentManager(cfg, nil, nil, nil, nil, "")

	cc := am.buildCompressionConfig(cfg, "on-model", 32768)
	if cc.Thresholds.HardPercent != 90 {
		t.Errorf("HardPercent = %d, want 90 (global ceiling applies under per-model opt-in)", cc.Thresholds.HardPercent)
	}
}

// TestSetModel_AlwaysPushesCompressionConfig pins the apply-site drift
// (bugs.md 2026-08-26): switching from an override model to a plain model
// must re-resolve and push the GLOBAL config — the previous model's ceiling
// must never stick on the agent.
func TestSetModel_AlwaysPushesCompressionConfig(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:    ccBool(true),
			Thresholds: config.CompressionThresholdsConfig{HardPercent: 95},
			PerModel: map[string]config.ModelCompressionOverride{
				"tight-model": {Thresholds: config.CompressionThresholdsConfig{HardPercent: 20}},
			},
		},
	}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	tight := agenticprovider.Model{ID: "tight-model", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.ProviderLMStudio, ContextWindow: 100000}
	if _, err := am.StartSession(tight, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if got := am.CurrentAgent().CompressionConfig().Thresholds.HardPercent; got != 20 {
		t.Fatalf("tight model hard = %d, want 20", got)
	}

	// Switch to a plain model: no per-model entry, no max_tokens. The agent
	// must drop the stale 20% ceiling and run the global 95%.
	plain := agenticprovider.Model{ID: "plain-model", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.ProviderLMStudio, ContextWindow: 100000}
	am.SetModel(plain)
	got := am.CurrentAgent().CompressionConfig().Thresholds.HardPercent
	if got != 95 {
		t.Errorf("after SetModel(plain) hard = %d, want 95 (global re-applied, stale 20 dropped)", got)
	}
}

// TestStartSession_ReactiveNetAppliedWhenProactiveDisabled pins the
// documented contract ("the reactive safety net is unaffected" by the enable
// flag): even with proactive compression disabled, the agent must receive
// OnContextError/OnErrorStrategy — the whole compression block must not be
// skipped.
func TestStartSession_ReactiveNetAppliedWhenProactiveDisabled(t *testing.T) {
	cfg := &config.Config{
		ContextCompression: config.ContextCompressionConfig{
			Enabled:         ccBool(false), // proactive off
			OnContextError:  true,          // reactive net explicitly on
			OnErrorStrategy: "selective",
		},
	}
	sessionState := NewSessionState(internal.ModeState{Major: internal.MajorCoder})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := NewAgentManager(cfg, nil, nil, sessionState, tuiEvents, "")

	mdl := agenticprovider.Model{ID: "m1", Api: agenticprovider.ApiOpenAICompletions, Provider: agenticprovider.ProviderLMStudio, ContextWindow: 32768}
	if _, err := am.StartSession(mdl, agenticprovider.StreamOptions{}, "sys", nil, cfg); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	cc := am.CurrentAgent().CompressionConfig()
	if !cc.OnContextError {
		t.Error("OnContextError must reach the agent even when proactive compression is disabled")
	}
	if cc.OnErrorStrategy != agentic.CompressionSelective {
		t.Errorf("OnErrorStrategy = %q, want selective", cc.OnErrorStrategy)
	}
	if cc.Thresholds.HardPercent != 0 {
		t.Errorf("HardPercent = %d, want 0 (proactive disabled)", cc.Thresholds.HardPercent)
	}
}
