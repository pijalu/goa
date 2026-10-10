// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
)

func seedTestConfig(providerID, prov, endpoint string) *config.Config {
	return &config.Config{
		ActiveProvider: providerID,
		Providers: []config.ProviderConfig{
			{ID: providerID, Provider: prov, Endpoint: endpoint, APIKey: "k"},
		},
	}
}

// A custom (non-registry) model on a provider whose profile has no thinking
// map (opencode) seeds the canonical identity map — editable, behavior-neutral.
func TestCustomModelThinkingSeed_IdentityForProfileWithoutMap(t *testing.T) {
	cfg := seedTestConfig("opencode", "opencode", "https://opencode.ai/zen/v1")
	seed := CustomModelThinkingSeed(cfg, "opencode", "x-preview-f-free")
	if seed == nil {
		t.Fatal("seed = nil, want identity map for a custom model on a map-less profile")
	}
	for _, l := range []string{"off", "minimal", "low", "medium", "high", "xhigh"} {
		if seed[l] != l {
			t.Errorf("seed[%q] = %q, want identity %q", l, seed[l], l)
		}
	}
}

// A custom model on a provider whose profile HAS a map (kimi-code, all→max)
// seeds a copy of that effective map so the user edits real values, and the
// copy must not alias the profile's internal map.
func TestCustomModelThinkingSeed_CopiesProfileMap(t *testing.T) {
	cfg := seedTestConfig("kimi-code", "kimi-code", "https://api.kimi.com/coding/v1")
	seed := CustomModelThinkingSeed(cfg, "kimi-code", "k3.5-turbo")
	if seed == nil {
		t.Fatal("seed = nil, want a copy of the kimi-code profile map")
	}
	if seed["high"] != "max" || seed["xhigh"] != "max" || seed["minimal"] != "max" {
		t.Errorf("seed = %v, want kimi-code profile values (all max)", seed)
	}
}

// Registry-known models need no seed: their mapping is already curated by the
// registry entry or the provider's variant profile.
func TestCustomModelThinkingSeed_NilForRegistryModel(t *testing.T) {
	cfg := seedTestConfig("openai", "openai", "https://api.openai.com/v1")
	if seed := CustomModelThinkingSeed(cfg, "openai", "o1"); seed != nil {
		t.Errorf("seed = %v, want nil for registry-known model o1", seed)
	}
}

// An unknown provider cannot resolve a profile — no seed.
func TestCustomModelThinkingSeed_NilForUnknownProvider(t *testing.T) {
	cfg := seedTestConfig("opencode", "opencode", "https://opencode.ai/zen/v1")
	if seed := CustomModelThinkingSeed(cfg, "nonexistent", "whatever"); seed != nil {
		t.Errorf("seed = %v, want nil for unknown provider", seed)
	}
}
