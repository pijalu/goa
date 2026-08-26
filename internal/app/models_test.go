// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	agentic "github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
)

// companionSubsystems builds subsystems with a two-provider config: active
// provider opencode-go, companion model "glm" bound to companionProvider.
// Model config IDs are unique config-wide (providerIDForModel relies on the
// same invariant), so each ID pins one provider+model.
func companionSubsystems(companionProvider string) *subsystems {
	cfg := &config.Config{
		ActiveProvider: "opencode-go",
		ActiveModel:    "ds",
		Providers: []config.ProviderConfig{
			{ID: "opencode-go", Endpoint: "https://example.com/go"},
			{ID: "zai", Endpoint: "https://example.com/zai"},
		},
		Models: []config.ModelConfig{
			{ID: "glm", ProviderID: "zai", Model: "glm-5.2"},
			{ID: "ds", ProviderID: "opencode-go", Model: "deepseek-v4-flash"},
		},
	}
	cfg.MultiAgent.CompanionModel = "glm"
	cfg.MultiAgent.CompanionProvider = companionProvider
	return &subsystems{
		cfg:         cfg,
		providerMgr: provider.NewProviderManager(cfg),
	}
}

// TestCompanionModelDisplay_UsesCompanionProvider is Bug B: the
// status bar showed the ACTIVE provider ("(opencode-go) glm-5.2") instead of
// the configured companion provider ("(zai) glm-5.2").
func TestSessionProviderID_UsesLiveProvider(t *testing.T) {
	cfg := &config.Config{ActiveProvider: "configured", Providers: []config.ProviderConfig{
		{ID: "configured", Endpoint: "https://configured.example"},
		{ID: "session", Endpoint: "https://session.example"},
	}}
	pm := provider.NewProviderManager(cfg)
	subs := &subsystems{cfg: cfg, providerMgr: pm}
	if err := pm.SetActive("session", ""); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got := sessionProviderID(subs); got != "session" {
		t.Fatalf("provider = %q, want session", got)
	}
}

func TestCompanionModelDisplay_UsesCompanionProvider(t *testing.T) {
	got := companionModelDisplay(companionSubsystems("zai"))
	if !strings.Contains(got, "(zai)") {
		t.Errorf("companion display %q missing companion provider (zai)", got)
	}
	if !strings.Contains(got, "glm-5.2") {
		t.Errorf("companion display %q missing resolved model glm-5.2", got)
	}
	if strings.Contains(got, "opencode-go") {
		t.Errorf("companion display %q leaked the active provider", got)
	}
}

// TestCompanionModelDisplay_LegacyFallback keeps configs without a companion
// provider working: they display against the active provider as before.
func TestCompanionModelDisplay_LegacyFallback(t *testing.T) {
	got := companionModelDisplay(companionSubsystems(""))
	if !strings.Contains(got, "(opencode-go)") {
		t.Errorf("legacy display %q should fall back to the active provider", got)
	}
}

// TestCompanionModelDisplay_NoModel: no companion model configured shows
// nothing (the footer falls back to the main model itself).
func TestCompanionModelDisplay_NoModel(t *testing.T) {
	subs := companionSubsystems("zai")
	subs.cfg.MultiAgent.CompanionModel = ""
	if got := companionModelDisplay(subs); got != "" {
		t.Errorf("expected empty display without companion model, got %q", got)
	}
}

// TestActiveModelDisplay_PrefersBoundSessionModel is the status-line bug
// regression: the footer showed the LATEST SELECTED model while the running
// session was still bound to another model (e.g. after a hot reload carried
// in a different persisted default). The display must reflect the model the
// session's active agent will actually send on the next turn, with the
// provider profile recovered from the bound BaseURL — never the selection pair.
func TestActiveModelDisplay_PrefersBoundSessionModel(t *testing.T) {
	subs := companionSubsystems("zai") // selection: opencode-go / ds → deepseek-v4-flash
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(4, 4, 4, 4), "")
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{
			ID:      "glm-5.2",
			Name:    "glm-5.2",
			BaseURL: "https://example.com/zai/chat/completions",
		},
	}))
	subs.agentMgr = am

	got := activeModelDisplay(subs)
	if !strings.Contains(got, "glm-5.2") {
		t.Errorf("display %q must show the bound session model glm-5.2", got)
	}
	if !strings.Contains(got, "zai") {
		t.Errorf("display %q must recover provider zai from the bound BaseURL", got)
	}
	if strings.Contains(got, "opencode-go") || strings.Contains(got, "deepseek-v4-flash") {
		t.Errorf("display %q leaked the divergent selection; it must show what the session uses", got)
	}
}

// TestActiveModelDisplay_FallbackWithoutBoundModel keeps early-boot behavior:
// with no agent attached there is no session truth yet, so the selection-
// derived pair is shown as before.
func TestActiveModelDisplay_FallbackWithoutBoundModel(t *testing.T) {
	subs := companionSubsystems("zai")
	subs.agentMgr = core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(4, 4, 4, 4), "")

	got := activeModelDisplay(subs)
	if !strings.Contains(got, "(opencode-go)") || !strings.Contains(got, "deepseek-v4-flash") {
		t.Errorf("fallback display %q should be the selection pair", got)
	}
}

// TestActiveModelDisplay_NoProviderLabelOverWrongLabel: when neither the
// endpoint nor an agreeing selection identifies the provider, the model is
// shown bare instead of pairing it with an unrelated provider label.
func TestActiveModelDisplay_NoProviderLabelOverWrongLabel(t *testing.T) {
	subs := companionSubsystems("zai") // selection resolves to deepseek-v4-flash
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(4, 4, 4, 4), "")
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "glm-5.2", Name: "glm-5.2", BaseURL: "https://unknown.example/v1"},
	}))
	subs.agentMgr = am

	if got := activeModelDisplay(subs); got != "glm-5.2" {
		t.Errorf("display = %q, want bare \"glm-5.2\" without a mismatched provider label", got)
	}
}

// TestProviderIDForBoundModel pins the endpoint matching: BaseURL carries the
// chat-completions suffix the profile endpoint lacks; trailing slashes and
// non-matching endpoints must behave.
func TestProviderIDForBoundModel(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{
		{ID: "go", Endpoint: "https://example.com/go"},
		{ID: "zai", Endpoint: "https://example.com/zai/"},
	}}

	tests := []struct {
		name, baseURL, want string
	}{
		{"exact match", "https://example.com/go", "go"},
		{"suffix stripped", "https://example.com/zai/chat/completions", "zai"},
		{"trailing slash normalized", "https://example.com/zai/", "zai"},
		{"no match", "https://other.example/x", ""},
		{"empty baseURL", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerIDForBoundModel(cfg, tc.baseURL); got != tc.want {
				t.Errorf("providerIDForBoundModel(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
	if got := providerIDForBoundModel(nil, "https://x"); got != "" {
		t.Errorf("nil config must yield empty provider, got %q", got)
	}
}

// TestApplyReloadedConfigKeepsSessionSelection is the multi-instance
// regression at the watcher-consumer level: instance A persisted its /model
// switch to the shared cascade, the config watcher hands instance B a freshly
// loaded config via applyReloadedConfig — B's live config (next requests),
// footer provider/model display and plugin-visible ActiveProvider must all
// keep B's own session pick. A session that never switched keeps adopting
// disk values (P22 hot-apply contract).
func TestApplyReloadedConfigKeepsSessionSelection(t *testing.T) {
	boot := func() *config.Config {
		return &config.Config{
			ActiveProvider: "opencode-go",
			ActiveModel:    "ds",
			Providers: []config.ProviderConfig{
				{ID: "opencode-go", Endpoint: "https://example.com/go"},
				{ID: "zai", Endpoint: "https://example.com/zai"},
			},
			Models: []config.ModelConfig{
				{ID: "glm", ProviderID: "zai", Model: "glm-5.2"},
				{ID: "ds", ProviderID: "opencode-go", Model: "deepseek-v4-flash"},
			},
		}
	}
	fromDisk := func(providerID, modelID string) *config.Config {
		cfg := boot()
		cfg.ActiveProvider = providerID
		cfg.ActiveModel = modelID
		return cfg
	}

	t.Run("session pick survives other instance's persisted switch", func(t *testing.T) {
		subs := &subsystems{cfg: boot(), providerMgr: provider.NewProviderManager(boot())}
		if err := subs.providerMgr.SetActive("zai", "glm"); err != nil {
			t.Fatalf("SetActive: %v", err)
		}

		subs.applyReloadedConfig(fromDisk("opencode-go", "ds")) // what A wrote to disk

		if got := subs.liveConfig().ActiveProvider; got != "zai" {
			t.Errorf("liveConfig().ActiveProvider = %q, want zai (requests would route to the other instance's provider)", got)
		}
		if got := activeModelDisplay(subs); !strings.Contains(got, "zai") || !strings.Contains(got, "glm-5.2") {
			t.Errorf("footer display = %q, want zai + resolved glm-5.2", got)
		}
	})

	t.Run("no explicit pick adopts disk reload", func(t *testing.T) {
		subs := &subsystems{cfg: boot(), providerMgr: provider.NewProviderManager(boot())}

		subs.applyReloadedConfig(fromDisk("zai", "glm"))

		if got := subs.liveConfig().ActiveProvider; got != "zai" {
			t.Errorf("liveConfig().ActiveProvider = %q, want zai (P22: manual edits apply without restart)", got)
		}
	})
}
