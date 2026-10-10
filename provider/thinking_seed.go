// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/agentic/provider/schema"

	"github.com/pijalu/goa/config"
)

// canonicalThinkingLevels lists Goa's canonical thinking levels in ascending
// order. It mirrors internal.AllThinkingLevels without pulling the internal
// package into this low-level helper.
var canonicalThinkingLevels = []agenticprovider.ThinkingLevel{
	agenticprovider.ThinkingOff,
	agenticprovider.ThinkingMinimal,
	agenticprovider.ThinkingLow,
	agenticprovider.ThinkingMedium,
	agenticprovider.ThinkingHigh,
	agenticprovider.ThinkingXHigh,
}

// CustomModelThinkingSeed computes the thinking_level_native_map written into
// the config when a model is added via the TUI add flows. The seed is the
// model's current effective wire mapping — the resolved variant profile's map
// (already merged with any registry map) when one exists, otherwise the
// canonical identity map — so persisting it is behavior-neutral while giving
// the user an editable copy of exactly what each level sends. This is the
// discoverable knob for always-thinking or level-restricted models (e.g.
// opencode's x-preview-f-free, which accepts only low/high/max): the user
// edits values instead of discovering the field.
//
// Returns nil when the model already carries a curated, non-empty
// registry-level ThinkingLevelMap (e.g. o1) — its mapping is complete and a
// config copy would only drift stale — or when the provider is not configured.
func CustomModelThinkingSeed(cfg *config.Config, providerID, modelName string) map[string]string {
	pCfg := cfg.GetProviderByID(providerID)
	if pCfg == nil {
		return nil
	}
	// A registry entry with its own non-empty map is fully curated at the
	// model level; seeding a copy adds noise and drift risk with no benefit.
	if reg := lookupRegistryModel(*pCfg, modelName); reg != nil && len(reg.ThinkingLevelMap) > 0 {
		return nil
	}
	prov, api := inferProviderIdentity(*pCfg)
	profile := schema.ResolveProfile(schema.Model{
		ID:       modelName,
		Api:      api,
		Provider: prov,
		BaseURL:  pCfg.Endpoint,
	})
	if len(profile.Defaults.ThinkingLevelMap) > 0 {
		seed := make(map[string]string, len(profile.Defaults.ThinkingLevelMap))
		for k, v := range profile.Defaults.ThinkingLevelMap {
			seed[string(k)] = v
		}
		return seed
	}
	return identityThinkingSeed()
}

// lookupRegistryModel resolves modelName against the built-in registry the
// same way the fallback resolve chain does: provider-exact, family prefix,
// then a known-provider-prefixed variant. Returns nil for unknown models.
func lookupRegistryModel(pCfg config.ProviderConfig, modelName string) *agenticprovider.Model {
	prov, _ := inferProviderIdentity(pCfg)
	if m := models.GetModelForProvider(prov, modelName); m != nil {
		return m
	}
	if m := models.LookupByPrefix(modelName); m != nil {
		return m
	}
	if stripped := stripKnownProviderPrefix(modelName); stripped != modelName {
		if m := models.GetModel(stripped); m != nil {
			return m
		}
		if m := models.LookupByPrefix(stripped); m != nil {
			return m
		}
	}
	return nil
}

// identityThinkingSeed returns the canonical identity map: each level maps to
// itself. This is the neutral starting point for providers whose profile has
// no map of its own (e.g. opencode) — the user edits the values the model
// actually accepts (e.g. xhigh→max for always-thinking models).
func identityThinkingSeed() map[string]string {
	seed := make(map[string]string, len(canonicalThinkingLevels))
	for _, l := range canonicalThinkingLevels {
		seed[string(l)] = string(l)
	}
	return seed
}
