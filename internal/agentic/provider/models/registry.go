// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// addModel adds a model to the built-in registry and the prefix-lookup slice.
// Calling addModel multiple times with the same ID does NOT overwrite the
// existing entry — the first registration wins. This ensures that
// hand-curated overrides (with detailed ThinkingFormat, Compat, and
// ThinkingLevelMap settings that models.dev does not provide) take priority
// over generated entries.
//
// Two indexes are maintained:
//   - builtinModels: ID → model, first-wins (GetModel contract).
//   - providerModels: provider → ID → model, first-wins PER PROVIDER, so the
//     same model ID can exist under several providers with their own
//     metadata (e.g. glm-5.2 on "zai" with quota pricing vs on "zai-api"
//     with per-token pricing) without one registration evicting the other.
func addModel(m provider.Model) {
	if _, exists := providerModels[m.Provider]; !exists {
		providerModels[m.Provider] = map[string]provider.Model{}
	}
	if _, exists := providerModels[m.Provider][m.ID]; exists {
		return // existing entry for this provider wins
	}
	providerModels[m.Provider][m.ID] = m
	// Global index: a canonical provider replaces a non-canonical one (e.g.
	// openai replaces an aggregator like abacus). Among same-tier providers,
	// first-wins.
	if existing, ok := builtinModels[m.ID]; ok {
		if canonicalProviders[m.Provider] && !canonicalProviders[existing.Provider] {
			builtinModels[m.ID] = m // canonical replaces non-canonical
		}
		return
	}
	modelDefs = append(modelDefs, m)
	builtinModels[m.ID] = m
}

// GetModel looks up a model by ID in the built-in registry.
// Returns nil if the model is not found.
func GetModel(id string) *provider.Model {
	if m, ok := builtinModels[id]; ok {
		return &m
	}
	return nil
}

// GetModelForProvider looks up a model by ID for a specific provider,
// falling back to the ID-global entry. Use this when the caller knows the
// provider and needs provider-exact metadata (e.g. cost differences between
// a quota plan and a pay-per-token API for the same model ID).
func GetModelForProvider(providerName provider.Provider, id string) *provider.Model {
	if byID, ok := providerModels[providerName]; ok {
		if m, ok := byID[id]; ok {
			return &m
		}
	}
	return GetModel(id)
}

// GetModels returns all models for a given provider.
func GetModels(providerName provider.Provider) []provider.Model {
	var result []provider.Model
	for _, m := range providerModels[providerName] {
		result = append(result, m)
	}
	return result
}

// AllModels returns all built-in models.
func AllModels() []provider.Model {
	result := make([]provider.Model, 0, len(builtinModels))
	for _, m := range builtinModels {
		result = append(result, m)
	}
	return result
}

// LookupByPrefix finds the first model whose canonical ID starts with
// the given prefix. Returns nil if no match is found.
//
// This is used as a fallback when GetModel(id) returns nil — unknown/local
// model names often share prefixes with known families, so we can still infer
// capabilities like context window, vision, and thinking support.
//
// Prefixes are matched case-insensitively. The longest matching prefix wins,
// so "claude-sonnet-4-" takes priority over the shorter "claude-".
// The returned model's ID is set to the queried modelName so downstream
// code uses the correct model identifier.
//
// When multiple entries share the same prefix length (e.g. a model exists on
// both its canonical provider and an aggregator), canonical providers are
// preferred over aggregators.
func LookupByPrefix(modelName string) *provider.Model {
	if modelName == "" {
		return nil
	}
	// Fast path: exact ID match in the global index (canonical provider wins).
	if m, ok := builtinModels[modelName]; ok {
		cp := m
		return &cp
	}
	lower := strings.ToLower(modelName)

	// Two-pass prefix match: canonical providers first, then all providers.
	// This prevents aggregators from shadowing canonical providers even when
	// the aggregator has a longer (date-suffixed) model ID.
	for _, canonicalOnly := range []bool{true, false} {
		var best *provider.Model
		bestLen := 0
		for _, m := range modelDefs {
			if canonicalOnly && !canonicalProviders[m.Provider] {
				continue
			}
			prefix := strings.ToLower(m.ID)
			if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
				cp := m
				cp.ID = modelName
				best = &cp
				bestLen = len(prefix)
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// canonicalProviders lists providers that are the original source of a
// model family, as opposed to aggregators/gateways that re-host the same
// models. Used by LookupByPrefix to prefer canonical entries.
var canonicalProviders = map[provider.Provider]bool{
	provider.ProviderOpenAI: true, provider.ProviderAnthropic: true,
	provider.ProviderGoogle: true, provider.ProviderDeepSeek: true,
	provider.ProviderMistral: true, provider.ProviderKimi: true,
	provider.ProviderKimiCode: true, provider.ProviderZai: true,
	provider.ProviderZaiApi: true,
	provider.Provider("xai"): true,
	provider.ProviderTogether: true, provider.ProviderFireworks: true,
	provider.ProviderGroq: true, provider.ProviderPerplexity: true,
}

// builtinModels is the curated registry of models, keyed by model ID
// (first registration wins).
var builtinModels = map[string]provider.Model{}

// modelDefs is the flat slice used by LookupByPrefix. Populated by addModel
// as models are registered (embedded catalog + YAML overrides).
var modelDefs []provider.Model

// providerModels indexes models per provider so identical model IDs can
// coexist under multiple providers with provider-specific metadata.
var providerModels = map[provider.Provider]map[string]provider.Model{}

func init() {
	// Phase 1: Load the embedded models.dev api.json snapshot. This populates
	// the registry with every known provider/model (no internet needed).
	for _, m := range loadEmbeddedCatalog() {
		addModel(m)
	}

	// Phase 2: Apply hand-curated overrides from the embedded YAML. These
	// carry Goa-specific behavioral metadata (thinking format, level maps,
	// compat quirks) that models.dev cannot provide. Overrides replace any
	// generated entry with the same (provider, ID).
	overrides := loadOverrides()

	// Prefix-ID overrides FIRST (e.g. "deepseek-") — they fill gaps for
	// models without an exact override. Exact overrides run SECOND so they
	// take priority (last-write-wins within the same provider+ID).
	for _, ov := range overrides {
		if !isPrefixID(ov.ID) {
			continue
		}
		applyPrefixOverride(ov)
	}

	// Exact-ID overrides — these win over prefix overrides.
	for _, ov := range overrides {
		if isPrefixID(ov.ID) {
			continue
		}
		m := applyOverride(findBaseForOverride(ov), ov)
		registerCurated(m)
	}

	// Phase 3: Rebuild modelDefs for prefix lookups. For each unique model ID,
	// prefer the entry from builtinModels (first-registered canonical entry,
	// which reflects the YAML override order). This ensures LookupByPrefix
	// returns the canonical entry (e.g. deepseek-v4-flash from "deepseek",
	// not from an aggregator or a secondary canonical provider).
	modelDefs = modelDefs[:0]
	seen := map[string]bool{}
	// First: the global canonical entries (one per ID).
	for id, m := range builtinModels {
		modelDefs = append(modelDefs, m)
		seen[id] = true
	}
	// Then: per-provider entries that don't have a global entry (rare:
	// models that exist only under a non-default provider).
	for _, byID := range providerModels {
		for id, m := range byID {
			if !seen[id] {
				modelDefs = append(modelDefs, m)
				seen[id] = true
			}
		}
	}
}

// findBaseForOverride returns the existing catalog entry for an override's
// (provider, ID), or a minimal stub if none exists (override-only models like
// local defaults or bedrock variants).
func findBaseForOverride(ov overrideModel) provider.Model {
	if existing := GetModelForProvider(provider.Provider(ov.Provider), ov.ID); existing != nil {
		return *existing
	}
	return provider.Model{ID: ov.ID, Provider: provider.Provider(ov.Provider)}
}

// applyPrefixOverride applies a prefix override (ID ending with "-") to every
// model in the registry whose ID starts with the prefix and shares the same
// provider. Only fields explicitly set in the override are applied.
func applyPrefixOverride(ov overrideModel) {
	prov := provider.Provider(ov.Provider)
	byID, ok := providerModels[prov]
	if !ok {
		return
	}
	prefix := strings.ToLower(ov.ID)
	for id, base := range byID {
		if !strings.HasPrefix(strings.ToLower(id), prefix) {
			continue
		}
		merged := applyOverride(base, ov)
		merged.ID = id // keep the real model ID, not the prefix
		byID[id] = merged
		// Also update the global index if this entry owns it.
		if builtinModels[id].Provider == prov {
			builtinModels[id] = merged
		}
	}
}

// registerCurated registers an override model. It always updates the
// per-provider index (so each provider has its own metadata), but only
// claims the global ID index if no override has claimed it yet — preserving
// the first override as the canonical entry for prefix lookups. Catalog
// entries (from addModel) are always replaced by the first override.
var overrideClaimed = map[string]bool{}

func registerCurated(m provider.Model) {
	if _, exists := providerModels[m.Provider]; !exists {
		providerModels[m.Provider] = map[string]provider.Model{}
	}
	providerModels[m.Provider][m.ID] = m
	if !overrideClaimed[m.ID] {
		builtinModels[m.ID] = m
		overrideClaimed[m.ID] = true
	}
}
