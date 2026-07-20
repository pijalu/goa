// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/provider"
)

// TestPluginProvidersMap_AuthStoreKeyExposed verifies a provider whose API key
// lives in the auth store (set via /login, not in ProviderConfig.APIKey) is
// exposed to plugins with that resolved key — otherwise the quota plugin sees
// no_api_key and the provider vanishes from /quota (bugs.md z.ai #6).
func TestPluginProvidersMap_AuthStoreKeyExposed(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "zai", Name: "Z.ai Coding", Provider: "zai", Endpoint: "https://api.z.ai/api/coding/paas/v4"},
		},
	}
	pm := provider.NewProviderManager(cfg)
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetAPIKey("zai", "zai-secret"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	pm.SetAuthStore(store)

	s := &subsystems{cfg: cfg, providerMgr: pm}
	m := pluginProvidersMap(s)

	entry, ok := m["zai"].(map[string]any)
	if !ok {
		t.Fatalf("zai provider missing from plugin providers map: %v", m)
	}
	if got := entry["apiKey"]; got != "zai-secret" {
		t.Errorf("apiKey = %v, want %q (auth store fallback)", got, "zai-secret")
	}
	if got := entry["provider"]; got != "zai" {
		t.Errorf("provider = %v, want %q", got, "zai")
	}
}

// TestPluginProvidersMap_NoKeyAnywhere verifies a provider with no key in
// config or auth store still appears (with empty key) — the plugin then
// reports no_api_key rather than the entry being absent.
func TestPluginProvidersMap_NoKeyAnywhere(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "zai", Provider: "zai"}},
	}
	pm := provider.NewProviderManager(cfg)
	s := &subsystems{cfg: cfg, providerMgr: pm}
	m := pluginProvidersMap(s)
	entry, ok := m["zai"].(map[string]any)
	if !ok {
		t.Fatalf("zai provider missing: %v", m)
	}
	if got := entry["apiKey"]; got != "" {
		t.Errorf("apiKey = %v, want empty", got)
	}
}
