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

import "github.com/pijalu/goa/internal/agentic/provider/schema"

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

// presetIDs is the ordered set of catalog IDs exposed as wizard presets.
// Order matters: it numbers the wizard options and is pinned by
// TestPresetProviders_StableOrder. Only catalog entries listed here become
// presets; the rest are agentic-only identities (anthropic, google, ...).
var presetIDs = []string{
	"openai", "lmstudio", "ollama", "openrouter",
	"opencode", "opencode-go", "deepseek", "kimi", "kimi-code",
	"zai", "zai-api", "poolside",
}

// PresetProviders returns the list of known provider presets, derived from
// the provider catalog. These cover the most common OpenAI-compatible LLM
// providers: local-first (LM Studio, Ollama) and cloud (OpenAI, OpenRouter,
// DeepSeek, Moonshot, Kimi Code, Z.ai, Poolside). Users can add additional
// providers via the "Custom" wizard option or by editing their config.
//
// NOTE: the wizard preset uses the OpenAI-completions base URL even for
// providers whose catalog default API differs (e.g. OpenAI's responses API),
// because the wizard's default flow streams via chat completions.
func PresetProviders() []ProviderPreset {
	out := make([]ProviderPreset, 0, len(presetIDs))
	for _, id := range presetIDs {
		d := schema.LookupProviderDefByID(id)
		if d == nil {
			continue
		}
		out = append(out, presetFromDef(d))
	}
	return out
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

// FindPreset returns the preset with the given ID, or nil if not found.
func FindPreset(id string) *ProviderPreset {
	for _, p := range PresetProviders() {
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
