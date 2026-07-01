// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package config defines known LLM provider presets.
//
// Preset providers ship with Goa so users can select them from the setup
// wizard or reference them in config files without remembering exact
// endpoints. Users can always add custom providers via endpoint.
package config

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

// PresetProviders returns the list of known provider presets.
// These cover the most common OpenAI-compatible LLM providers:
// local-first (LM Studio, Ollama) and cloud (OpenAI, OpenRouter,
// DeepSeek, Moonshot, Kimi Code, Opencode). Users can add additional
// providers via the "Custom" wizard option or by editing their config.
func PresetProviders() []ProviderPreset {
	return []ProviderPreset{
		{
			ID:           "openai",
			Name:         "OpenAI",
			Endpoint:     "https://api.openai.com/v1",
			DefaultModel: "gpt-4o",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderOpenAI,
			API:          AgenticAPIOpenAICompletions,
		},
		{
			ID:           "lmstudio",
			Name:         "LM Studio",
			Endpoint:     "http://localhost:1234/v1",
			DefaultModel: "local-model",
			NeedsAPIKey:  false,
			Provider:     AgenticProviderLMStudio,
			API:          AgenticAPIOpenAICompletions,
		},
		{
			ID:           "ollama",
			Name:         "Ollama",
			Endpoint:     "http://localhost:11434/v1",
			DefaultModel: "qwen/qwen3.5-9b",
			NeedsAPIKey:  false,
			Provider:     AgenticProviderOllama,
			API:          AgenticAPIOpenAICompletions,
		},
		{
			ID:           "openrouter",
			Name:         "OpenRouter",
			Endpoint:     "https://openrouter.ai/api/v1",
			DefaultModel: "openrouter/free",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderOpenRouter,
			API:          AgenticAPIOpenAICompletions,
		},
		{
			ID:           "opencode",
			Name:         "OpenCode Zen",
			Endpoint:     "https://opencode.ai/zen/v1",
			DefaultModel: "deepseek-v4-flash",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderOpenCode,
			API:          AgenticAPIOpenAICompletions,
		},
		{
			ID:           "opencode-go",
			Name:         "OpenCode Zen Go",
			Endpoint:     "https://opencode.ai/zen/go/v1",
			DefaultModel: "deepseek-v4-flash",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderOpenCodeGo,
			API:          AgenticAPIOpenAICompletions,
			Extra: map[string]any{
				"reasoning_key":               "reasoning_content",
				"thinking_extra_body":         true,
				"normalize_null_descriptions": true,
				"tool_call_id_max_length":     64,
			},
		},
		{
			ID:           "deepseek",
			Name:         "DeepSeek",
			Endpoint:     "https://api.deepseek.com",
			DefaultModel: "deepseek-v4-flash",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderDeepSeek,
			API:          AgenticAPIOpenAICompletions,
		},
		{
			ID:           "kimi",
			Name:         "Moonshot",
			Endpoint:     "https://api.moonshot.cn/v1",
			DefaultModel: "kimi-k2.6",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderKimi,
			API:          AgenticAPIOpenAICompletions,
			Extra: map[string]any{
				"reasoning_key":               "reasoning_content",
				"thinking_extra_body":         true,
				"normalize_null_descriptions": true,
				"tool_call_id_max_length":     64,
			},
		},
		{
			ID:           "kimi-code",
			Name:         "Kimi Code",
			Endpoint:     "https://api.kimi.com/coding/v1",
			DefaultModel: "kimi-for-coding",
			NeedsAPIKey:  true,
			Provider:     AgenticProviderKimiCode,
			API:          AgenticAPIOpenAICompletions,
			Extra: map[string]any{
				"reasoning_key":               "reasoning_content",
				"thinking_extra_body":         true,
				"normalize_null_descriptions": true,
				"tool_call_id_max_length":     64,
			},
		},
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
