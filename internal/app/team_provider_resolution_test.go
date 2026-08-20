// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
)

// recordingProviderManager captures the provider+model the team session
// controller switches to, so tests can assert the resolved provider.
type recordingProviderManager struct {
	setProvider, setModel string
}

func (m *recordingProviderManager) Active() (*config.ProviderConfig, string) {
	return &config.ProviderConfig{}, m.setProvider
}
func (m *recordingProviderManager) SetActive(providerID, model string) error {
	m.setProvider, m.setModel = providerID, model
	return nil
}
func (m *recordingProviderManager) ListModels(string) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (m *recordingProviderManager) ListModelsCached(string, time.Duration) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (m *recordingProviderManager) TestConnection(string) (time.Duration, int, error) {
	return 0, 0, nil
}
func (m *recordingProviderManager) ResolveActiveModel() (agenticprovider.Model, error) {
	return agenticprovider.Model{}, nil
}
func (m *recordingProviderManager) BuildStreamOptions() agenticprovider.StreamOptions {
	return agenticprovider.StreamOptions{}
}

// newProviderTestController builds a controller wired through a real
// AgentManager so the full (non-headless) switch path runs, with the
// provider manager recording the resolved provider+model.
func newProviderTestController(cfg *config.Config) (*teamSessionController, *recordingProviderManager) {
	pm := &recordingProviderManager{}
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(8, 8, 8, 8), "")
	return &teamSessionController{cfg: cfg, pm: pm, am: am}, pm
}

// providerTestConfig mirrors the reported scenario: session active on
// kimi-code, while the team's main model (gemma) is configured for lmstudio.
func providerTestConfig() *config.Config {
	return &config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "kimi-for-coding",
		Models: []config.ModelConfig{
			{ID: "kimi-for-coding", ProviderID: "kimi-code", Model: "kimi-for-coding"},
			{ID: "gemma-local", ProviderID: "lmstudio", Model: "google/gemma-4-e4b"},
			{ID: "qwen-local", ProviderID: "lmstudio", Model: "qwen/qwen3.5-9b"},
			{ID: "orphan-model", ProviderID: "", Model: "some/model"},
		},
	}
}

// Regression (bug: team activation does not switch to the member model's
// provider): a team main member with no explicit provider: whose model is
// configured with provider: lmstudio must activate the session on lmstudio,
// not reuse the current provider (kimi-code).
func TestTeamSessionController_SwitchModelResolvesModelProvider(t *testing.T) {
	cfg := providerTestConfig()
	c, pm := newProviderTestController(cfg)

	// Team member: main: {model: gemma-local} — no provider: override.
	if err := c.SwitchModel("", "gemma-local"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if cfg.ActiveProvider != "lmstudio" {
		t.Errorf("ActiveProvider=%q, want lmstudio (gemma's configured provider)", cfg.ActiveProvider)
	}
	if pm.setProvider != "lmstudio" {
		t.Errorf("ProviderManager.SetActive provider=%q, want lmstudio", pm.setProvider)
	}
	if cfg.ActiveModel != "gemma-local" || pm.setModel != "gemma-local" {
		t.Errorf("model=(%q,%q), want gemma-local", cfg.ActiveModel, pm.setModel)
	}
}

// An explicit member provider: override wins over the model's configured
// provider.
func TestTeamSessionController_SwitchModelExplicitProviderWins(t *testing.T) {
	cfg := providerTestConfig()
	c, pm := newProviderTestController(cfg)

	if err := c.SwitchModel("kimi-code", "gemma-local"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if cfg.ActiveProvider != "kimi-code" {
		t.Errorf("ActiveProvider=%q, want kimi-code (explicit override)", cfg.ActiveProvider)
	}
	if pm.setProvider != "kimi-code" {
		t.Errorf("SetActive provider=%q, want kimi-code", pm.setProvider)
	}
}

// A member model with no configured provider falls back to the current
// ActiveProvider (unchanged legacy behavior for unconfigured models).
func TestTeamSessionController_SwitchModelFallsBackToActiveProvider(t *testing.T) {
	cfg := providerTestConfig()
	c, pm := newProviderTestController(cfg)

	if err := c.SwitchModel("", "orphan-model"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if cfg.ActiveProvider != "kimi-code" {
		t.Errorf("ActiveProvider=%q, want kimi-code (fallback to current)", cfg.ActiveProvider)
	}
	if pm.setProvider != "kimi-code" {
		t.Errorf("SetActive provider=%q, want kimi-code", pm.setProvider)
	}
}

// Pool members (companion/worker) get the same resolution: a companion with
// no provider: resolves its model's configured provider so the pool's
// ProviderModelFactory lands it on the right endpoint.
func TestTeamMemberApplier_MemberConfigResolvesModelProvider(t *testing.T) {
	cfg := providerTestConfig()
	a := &teamMemberApplier{cfg: cfg}

	rm := config.ResolvedMember{
		Name:   "companion",
		Member: config.TeamMember{Model: "qwen-local", Role: "reviewer"},
	}
	ac, err := a.MemberConfig(config.TeamDefinition{}, rm)
	if err != nil {
		t.Fatalf("MemberConfig: %v", err)
	}
	if ac.ProviderID != "lmstudio" {
		t.Errorf("MemberConfig ProviderID=%q, want lmstudio (qwen's configured provider)", ac.ProviderID)
	}
	if ac.ModelName != "qwen-local" {
		t.Errorf("MemberConfig ModelName=%q, want qwen-local", ac.ModelName)
	}
}

// An explicit member provider: still wins in the pool config.
func TestTeamMemberApplier_MemberConfigExplicitProviderWins(t *testing.T) {
	cfg := providerTestConfig()
	a := &teamMemberApplier{cfg: cfg}

	rm := config.ResolvedMember{
		Name:   "companion",
		Member: config.TeamMember{Model: "qwen-local", Provider: "kimi-code", Role: "reviewer"},
	}
	ac, err := a.MemberConfig(config.TeamDefinition{}, rm)
	if err != nil {
		t.Fatalf("MemberConfig: %v", err)
	}
	if ac.ProviderID != "kimi-code" {
		t.Errorf("MemberConfig ProviderID=%q, want kimi-code (explicit override)", ac.ProviderID)
	}
}

// A member model with no configured provider keeps ProviderID empty (pool
// falls back to its default model wiring — legacy behavior preserved).
func TestTeamMemberApplier_MemberConfigNoConfiguredProviderStaysEmpty(t *testing.T) {
	cfg := providerTestConfig()
	a := &teamMemberApplier{cfg: cfg}

	rm := config.ResolvedMember{
		Name:   "worker",
		Member: config.TeamMember{Model: "orphan-model", Role: "worker"},
	}
	ac, err := a.MemberConfig(config.TeamDefinition{}, rm)
	if err != nil {
		t.Fatalf("MemberConfig: %v", err)
	}
	if ac.ProviderID != "" {
		t.Errorf("MemberConfig ProviderID=%q, want empty (no configured provider)", ac.ProviderID)
	}
}
