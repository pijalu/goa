// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package config defines known LLM provider presets.
//
// Preset providers ship with Goa so users can select them from the setup
// wizard or reference them in config files without remembering exact
// endpoints. The preset list is derived from the single source of truth —
// the provider catalog in internal/agentic/provider/schema — so adding a
// provider to the catalog automatically surfaces it here. Users can always
// add custom providers via endpoint.
package config

import (
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// ProviderPreset defines a known provider preset with default settings.
type ProviderPreset struct {
	// ID is the short identifier used in config files (e.g. "openrouter").
	ID string
	// Name is the human-readable display name (e.g. "OpenRouter").
	Name string
	// Endpoint is the OpenAI-compatible API base URL.
	Endpoint string
	// DefaultModel is the suggested model identifier for this provider.
	DefaultModel string
	// NeedsAPIKey indicates whether the provider requires an API key.
	NeedsAPIKey bool
	// Provider is the agentic provider identifier (e.g. "openai", "lm-studio").
	Provider string
	// API is the agentic API identifier (e.g. "openai-completions").
	API string
	// Extra holds per-provider configuration overrides (optional).
	Extra map[string]any
}

// PresetProviders returns the list of known provider presets, derived from
// the provider catalog — the single source of truth. Every catalog provider
// that carries a wizard-configurable base URL is surfaced as a preset, in
// catalog order (which is stable and pinned by TestPresetProviders_StableOrder).
// This covers local-first (LM Studio, Ollama) and cloud providers (OpenAI,
// OpenRouter, DeepSeek, Moonshot, Kimi Code, Z.ai, Poolside, Anthropic,
// Google, Mistral, Groq, xAI, Together, Fireworks, Perplexity, GitHub).
//
// Catalog entries without a base URL are not presets: "aws" and "azure" need
// provider-specific auth/deployment handling rather than an endpoint+key, and
// "custom" is the catch-all identity served by the dedicated "__custom__"
// picker option (no fixed endpoint to preset).
//
// NOTE: the wizard preset uses the OpenAI-completions base URL even for
// providers whose catalog default API differs (e.g. OpenAI's responses API),
// because the wizard's default flow streams via chat completions. The stored
// provider config only records endpoint + key; the wire identity is inferred
// from the base URL at resolve time, so the preset endpoint remains correct.
func PresetProviders() []ProviderPreset {
	cat := schema.ProviderCatalog()
	out := make([]ProviderPreset, 0, len(cat))
	for i := range cat {
		d := &cat[i]
		if !wizardPresetAddable(d) {
			continue
		}
		out = append(out, presetFromDef(d))
	}
	return out
}

// wizardPresetAddable reports whether a catalog provider can be configured via
// the endpoint+key wizard. It must have a concrete base URL to preset; the
// generic "custom" identity is handled by its own "__custom__" picker path.
func wizardPresetAddable(d *schema.ProviderDef) bool {
	return d.BaseURL != "" && d.ID != "custom"
}

// presetFromDef converts a catalog definition into a wizard preset. The
// preset endpoint is always the OpenAI-completions base URL and the preset
// API is openai-completions, regardless of the catalog's default API: the
// setup wizard configures providers for the chat-completions flow.
func presetFromDef(d *schema.ProviderDef) ProviderPreset {
	api := d.API
	if api != schema.ApiOpenAICompletions {
		api = schema.ApiOpenAICompletions
	}
	return ProviderPreset{
		ID:           d.ID,
		Name:         d.Name,
		Endpoint:     d.BaseURL,
		DefaultModel: d.DefaultModel,
		NeedsAPIKey:  d.NeedsAPIKey(),
		Provider:     string(d.Provider),
		API:          string(api),
		Extra:        d.Extra,
	}
}

// AllProviderPresets returns PresetProviders (catalog, stable order) followed
// by every models.dev provider that Goa imports but the catalog does not
// cover — e.g. tensorx. This is the complete list the "/provider add" and
// "/config → add provider" pickers surface, so the user can add ANY provider,
// not just the hand-curated catalog entries. Models.dev providers whose
// identity already has a catalog preset are skipped (no duplicate rows).
//
// Setup-wizard consumers keep using PresetProviders() alone; the add pickers
// use this superset.
func AllProviderPresets() []ProviderPreset {
	out := PresetProviders()
	covered := make(map[string]bool, len(out))
	for _, p := range out {
		covered[p.ID] = true
	}
	for _, md := range models.ModelsDevProviders() {
		id := string(md.Identity)
		if covered[id] {
			continue
		}
		covered[id] = true
		out = append(out, presetFromModelsDev(md))
	}
	return out
}

// presetFromModelsDev converts a models.dev provider into an add-picker preset.
// The provider identity is the models.dev-derived Goa identity (== the models.dev
// key for unmapped providers, matching DeriveProviderID for the endpoint URL),
// and the wire API is the provider's native protocol. NeedsAPIKey defaults true
// because models.dev providers are cloud endpoints; DefaultModel is the first
// tool-calling model (the familiar suggestion for that provider).
func presetFromModelsDev(md models.ModelsDevProvider) ProviderPreset {
	defModel := ""
	if len(md.ModelIDs) > 0 {
		defModel = md.ModelIDs[0]
	}
	local := md.BaseURL == ""
	return ProviderPreset{
		ID:           string(md.Identity),
		Name:         md.Name,
		Endpoint:     md.BaseURL,
		DefaultModel: defModel,
		NeedsAPIKey:  !local,
		Provider:     string(md.Identity),
		API:          string(md.API),
	}
}

// FindPreset returns the preset with the given ID across the full addable set
// (catalog + models.dev providers), or nil if not found. Returning models.dev
// presets here is what lets "/provider add tensorx" resolve, and lets
// provider/manager identity inference map a user-configured models.dev
// provider to its real provider/API instead of the OpenAI-completions fallback.
func FindPreset(id string) *ProviderPreset {
	for _, p := range AllProviderPresets() {
		if p.ID == id {
			return &p
		}
	}
	return nil
}

// IsPresetID returns true if the given ID matches a known preset.
func IsPresetID(id string) bool {
	return FindPreset(id) != nil
}
