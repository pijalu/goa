// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package kimi implements the Kimi/Moonshot provider adapter.
//
// The Kimi API is OpenAI Chat Completions–compatible but with Moonshot-specific
// features: reasoning_content field, thinking in extra_body, nullable tool
// schema descriptions, $prefix builtin functions, and video upload.
// Per-provider configuration is driven by the Model.Extra map.
package kimi

import (
	"github.com/pijalu/goa/internal/agentic/provider"
)

// KimiProvider implements provider.ApiProvider for Kimi/Moonshot-compatible APIs.
// It wraps the OpenAI completions provider with Moonshot-specific transforms.
type KimiProvider struct{}

// Provider constants used by the kimi provider.
const (
	ProviderName     = "kimi"
	ProviderNameCode = "kimi-code"
)

func (p *KimiProvider) API() provider.Api {
	return provider.ApiOpenAICompletions
}

func (p *KimiProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	// Apply Kimi-specific transforms to the model before streaming.
	model = applyKimiExtras(model)
	return provider.Stream(model, ctx, opts)
}

func (p *KimiProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	model = applyKimiExtras(model)
	base := provider.BuildSimpleOptions(model, opts)
	return provider.Stream(model, ctx, base)
}

// applyKimiExtras modifies the model in-place to apply Kimi-specific
// configuration from the Extra map. This ensures the generic OpenAI
// provider uses the correct settings for Moonshot.
func applyKimiExtras(model provider.Model) provider.Model {
	extra := model.Extra
	if extra == nil {
		return model
	}

	compat := ensureCompatMap(&model)
	setCompatBool(compat, extra, "thinking_extra_body")
	setCompatBool(compat, extra, "normalize_null_descriptions")
	setCompatIntFromFloat(compat, extra, "tool_call_id_max_length")
	return model
}

func ensureCompatMap(model *provider.Model) map[string]interface{} {
	if model.Compat == nil {
		model.Compat = map[string]interface{}{}
	}
	compat, _ := model.Compat.(map[string]interface{})
	if compat == nil {
		compat = map[string]interface{}{}
		model.Compat = compat
	}
	return compat
}

func setCompatBool(compat map[string]interface{}, extra map[string]interface{}, key string) {
	if v, ok := extra[key].(bool); ok && v {
		compat[key] = true
	}
}

func setCompatIntFromFloat(compat map[string]interface{}, extra map[string]interface{}, key string) {
	if v, ok := extra[key].(float64); ok && v > 0 {
		compat[key] = int(v)
	}
}

func init() {
	// Register for ApiOpenAICompletions — this won't override the existing
	// OpenAI provider because our init() checks if one is already registered.
	// Instead, we directly register kimi-specific handling via the provider
	// config's `provider` field, which is resolved at model resolution time.
	//
	// The kimi provider is activated when a provider config has:
	//   provider: "kimi"   (or "kimi-code")
	//   api: "openai-completions"
	//
	// At model resolution time (provider/manager.go), the Extra map is populated
	// from the provider config or preset, and applyKimiExtras() applies the
	// transforms before the generic OpenAI provider handles the stream.
	//
	// This keeps the implementation lightweight — no separate HTTP client,
	// no duplicate streaming logic, just configuration-driven transforms.
}
