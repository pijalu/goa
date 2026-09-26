// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/auth"
)

// TestResolveAPIKey_EnvFallback pins the resolution chain
// config.api_key → auth store → catalog env var (bugs.md "Vercel AI Gateway: no
// way to add an API key"). The env fallback comes LAST so an explicit
// configuration always wins, and it makes headless/CI use work with only
// AI_GATEWAY_API_KEY in the environment.
func TestResolveAPIKey_EnvFallback(t *testing.T) {
	const envVar = "AI_GATEWAY_API_KEY"

	newManager := func(t *testing.T, store *auth.Store, cfgKey string) *ProviderManager {
		t.Helper()
		cfg := &config.Config{
			ActiveProvider: "vercel",
			Providers: []config.ProviderConfig{{
				ID: "vercel", Name: "Vercel AI Gateway", Provider: "vercel", APIKey: cfgKey,
			}},
		}
		pm := NewProviderManager(cfg)
		if store != nil {
			pm.SetAuthStore(store)
		}
		return pm
	}

	t.Run("env used when nothing else has a key", func(t *testing.T) {
		t.Setenv(envVar, "gw-env-key")
		t.Setenv("VERCEL_API_KEY", "")
		pm := newManager(t, mustAuthStore(t), "")
		if got := pm.ResolveAPIKey("vercel"); got != "gw-env-key" {
			t.Errorf("ResolveAPIKey(vercel) = %q, want the catalog env key", got)
		}
	})

	t.Run("auth store wins over env", func(t *testing.T) {
		t.Setenv(envVar, "gw-env-key")
		store := mustAuthStore(t)
		if err := store.SetAPIKey("vercel", "store-key"); err != nil {
			t.Fatalf("SetAPIKey: %v", err)
		}
		pm := newManager(t, store, "")
		if got := pm.ResolveAPIKey("vercel"); got != "store-key" {
			t.Errorf("ResolveAPIKey(vercel) = %q, want the stored key", got)
		}
	})

	t.Run("config wins over everything", func(t *testing.T) {
		t.Setenv(envVar, "gw-env-key")
		store := mustAuthStore(t)
		if err := store.SetAPIKey("vercel", "store-key"); err != nil {
			t.Fatalf("SetAPIKey: %v", err)
		}
		pm := newManager(t, store, "config-key")
		if got := pm.ResolveAPIKey("vercel"); got != "config-key" {
			t.Errorf("ResolveAPIKey(vercel) = %q, want the configured key", got)
		}
	})

	t.Run("no key anywhere resolves empty", func(t *testing.T) {
		t.Setenv(envVar, "")
		t.Setenv("VERCEL_API_KEY", "")
		pm := newManager(t, mustAuthStore(t), "")
		if got := pm.ResolveAPIKey("vercel"); got != "" {
			t.Errorf("ResolveAPIKey(vercel) = %q, want empty", got)
		}
	})
}
