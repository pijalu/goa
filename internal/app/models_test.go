// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
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

// TestCompanionModelDisplay_UsesCompanionProvider is bugs.md Bug B: the
// status bar showed the ACTIVE provider ("(opencode-go) glm-5.2") instead of
// the configured companion provider ("(zai) glm-5.2").
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