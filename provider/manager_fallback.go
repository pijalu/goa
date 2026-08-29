// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

var knownProviderPrefixes = buildKnownProviderPrefixes()

func buildKnownProviderPrefixes() []string {
	cat := schema.ProviderCatalog()
	out := make([]string, 0, len(cat))
	for _, d := range cat {
		if d.Provider != "" {
			out = append(out, string(d.Provider))
		}
	}
	return out
}

// stripKnownProviderPrefix returns the part of name after a leading known
// provider prefix. If the prefix is not a known provider, name is returned
// unchanged.
func stripKnownProviderPrefix(name string) string {
	idx := strings.Index(name, "/")
	if idx <= 0 {
		return name
	}
	prefix := strings.ToLower(name[:idx])
	for _, p := range knownProviderPrefixes {
		if strings.ToLower(p) == prefix {
			return name[idx+1:]
		}
	}
	return name
}

// mergeRegistryModel combines a built-in registry model's capabilities with
// the active provider's identity and endpoint. The original modelName is kept
// as the ID so the API receives the exact name the user configured.
func mergeRegistryModel(m agenticprovider.Model, pCfg config.ProviderConfig, mCfg config.ModelConfig, modelName string) agenticprovider.Model {
	mdl := m
	mdl.ID = modelName
	mdl.Name = modelName

	prov, api := inferEffectiveProviderAPI(pCfg, mCfg)
	// Per-model catalog API pins (model_overrides.yaml `api:` — e.g. Muse
	// Spark's openai-responses on the opencode gateways) apply only when the
	// registry entry describes THIS provider: a catalog entry borrowed from a
	// different provider (Google's gemma entry prefix-matched on LM Studio)
	// must keep the serving provider's wire API. An explicit model-config api
	// always wins.
	if mCfg.API == "" && m.Api != "" && m.Provider == prov {
		api = m.Api
	}
	mdl.Provider = prov
	mdl.Api = api

	mdl.BaseURL = modelEndpointURL(api, pCfg.Endpoint)
	applyModelConfigCapabilities(&mdl, mCfg, api)
	applyProviderExtra(&mdl, pCfg)
	return mdl
}

// buildFallbackModel constructs a Model from provider/model config.
// First tries prefix-based model lookup, then checks for a provider-prefixed
// known model (e.g., "google/gemma-4-e4b"), and finally falls back to a
// minimal config.
func buildFallbackModel(pCfg config.ProviderConfig, mCfg config.ModelConfig, modelName string) agenticprovider.Model {
	// Try prefix-based lookup for known model families (e.g., "gpt-4o-" matches "gpt-4o").
	if m := models.LookupByPrefix(modelName); m != nil {
		return mergeRegistryModel(*m, pCfg, mCfg, modelName)
	}

	// Active model names sometimes carry a provider-family prefix that the
	// built-in registry does not include (e.g., "google/gemma-4-e4b"). Strip a
	// known provider prefix and try exact and prefix lookup again.
	if stripped := stripKnownProviderPrefix(modelName); stripped != modelName {
		if m := models.GetModel(stripped); m != nil {
			return mergeRegistryModel(*m, pCfg, mCfg, modelName)
		}
		if m := models.LookupByPrefix(stripped); m != nil {
			return mergeRegistryModel(*m, pCfg, mCfg, modelName)
		}
	}

	prov, api := inferProviderIdentity(pCfg)
	if mCfg.Provider != "" {
		prov = agenticprovider.Provider(mCfg.Provider)
	}
	if mCfg.API != "" {
		api = agenticprovider.Api(mCfg.API)
	}

	inputTypes := mCfg.InputTypes
	if len(inputTypes) == 0 {
		inputTypes = []string{"text"}
	}

	baseURL := modelEndpointURL(api, pCfg.Endpoint)
	mdl := agenticprovider.Model{
		ID:         modelName,
		Name:       modelName,
		Api:        api,
		Provider:   prov,
		BaseURL:    baseURL,
		InputTypes: inputTypes,
	}

	applyModelConfigToFallback(&mdl, mCfg, api)
	applyProviderExtraToFallback(&mdl, pCfg)
	return mdl
}

func applyModelConfigToFallback(mdl *agenticprovider.Model, mCfg config.ModelConfig, api agenticprovider.Api) {
	if mCfg.MaxTokens > 0 {
		mdl.MaxTokens = mCfg.MaxTokens
	}
	if mCfg.ContextWindow > 0 {
		mdl.ContextWindow = mCfg.ContextWindow
	}
	// Reasoning defaults to enabled when omitted: most models emit thinking
	// blocks when asked, so an unset config should not suppress them. An
	// explicit `reasoning: false` still disables.
	if mCfg.Reasoning != nil {
		mdl.Reasoning = *mCfg.Reasoning
	} else {
		mdl.Reasoning = true
	}
	if mCfg.ThinkingLevel != "" {
		mdl.Reasoning = true
	}
	if native := nativeThinkingLevelMap(mCfg); len(native) > 0 {
		mdl.ThinkingLevelMap = native
	}
	inferProviderModelTraits(mdl)
	if budgets := effectiveThinkingBudgets(mCfg); len(budgets) > 0 {
		mdl.ThinkingBudgets = budgets
	}
	if mCfg.Compat != "" {
		mdl.Compat = parseCompatJSON(api, mCfg.Compat)
	}
}

// inferProviderModelTraits fills reasoning/thinking capabilities for known
// provider+model families when a raw model ID bypasses the registry (e.g. the
// user typed "glm-5.2" instead of picking it from the model list). Without
// this, z.ai GLM models resolved through the fallback path would not send the
// thinking body, silently disabling reasoning.
func inferProviderModelTraits(mdl *agenticprovider.Model) {
	detected := agenticprovider.DetectOpenAICompat(*mdl)
	format := ""
	if detected.ThinkingFormat != nil {
		format = *detected.ThinkingFormat
	}
	switch format {
	case "zai":
		// All GLM chat models on z.ai reasoning-enable via the thinking body.
		mdl.Reasoning = true
		mdl.ThinkingFormat = agenticprovider.ThinkingFormatZai
	case "deepseek":
		mdl.Reasoning = true
		mdl.ThinkingFormat = agenticprovider.ThinkingFormatChunkedReasoning
	case "openai":
		// Poolside and other OpenAI-compatible providers that return
		// reasoning_content in streaming responses. Enable reasoning so the
		// thinking body is sent and thinking blocks are displayed.
		if mdl.Provider == agenticprovider.ProviderPoolside {
			mdl.Reasoning = true
		}
	}
}

func applyProviderExtraToFallback(mdl *agenticprovider.Model, pCfg config.ProviderConfig) {
	if len(pCfg.Extra) == 0 {
		return
	}
	mdl.Extra = make(map[string]any, len(pCfg.Extra))
	for k, v := range pCfg.Extra {
		mdl.Extra[k] = v
	}
}

// effectiveThinkingBudgets returns the thinking budgets to apply for a model
// config. It prefers the explicit ThinkingLevelMap, falls back to a uniform
// budget from ThinkingBudget, and finally returns the package default map.
func effectiveThinkingBudgets(mCfg config.ModelConfig) agenticprovider.ThinkingBudgets {
	if len(mCfg.ThinkingLevelMap) > 0 {
		b := make(agenticprovider.ThinkingBudgets, len(mCfg.ThinkingLevelMap))
		for k, v := range mCfg.ThinkingLevelMap {
			b[agenticprovider.ThinkingLevel(k)] = v
		}
		return b
	}
	if mCfg.ThinkingBudget > 0 {
		return uniformThinkingBudgets(mCfg.ThinkingBudget)
	}
	b := make(agenticprovider.ThinkingBudgets, len(config.DefaultThinkingLevelMap))
	for k, v := range config.DefaultThinkingLevelMap {
		b[agenticprovider.ThinkingLevel(k)] = v
	}
	return b
}

// uniformThinkingBudgets returns a ThinkingBudgets map using budget for all levels.
func uniformThinkingBudgets(budget int) agenticprovider.ThinkingBudgets {
	return agenticprovider.ThinkingBudgets{
		agenticprovider.ThinkingMinimal: budget,
		agenticprovider.ThinkingLow:     budget,
		agenticprovider.ThinkingMedium:  budget,
		agenticprovider.ThinkingHigh:    budget,
		agenticprovider.ThinkingXHigh:   budget,
	}
}

// nativeThinkingLevelMap converts the config's ThinkingLevelNativeMap
// (canonical level → provider-native string) into the model's ThinkingLevelMap
// so resolveThinkingLevel translates the level before it hits the wire. This is
// the direct per-model escape hatch for quick-fixing new or always-thinking
// models whose accepted levels differ from Goa's canonical set. It applies only
// when the resolved variant profile carries no map of its own (the migration
// bridge in schema.ResolveProfile skips the copy when the profile has one).
func nativeThinkingLevelMap(mCfg config.ModelConfig) agenticprovider.ThinkingLevelMap {
	if len(mCfg.ThinkingLevelNativeMap) == 0 {
		return nil
	}
	m := make(agenticprovider.ThinkingLevelMap, len(mCfg.ThinkingLevelNativeMap))
	for k, v := range mCfg.ThinkingLevelNativeMap {
		m[agenticprovider.ThinkingLevel(k)] = v
	}
	return m
}

// parseCompatJSON unmarshals a provider compat JSON blob into the concrete
// compat type for the given API. Unknown APIs return the raw string.
//
// endpointHeuristics maps endpoint URL substrings to provider identity, used
// when a provider config has no explicit Provider field. Derived from the
// provider catalog URLPatterns (single source of truth); catalog order
// defines precedence so substring-superset endpoints (z.ai coding ⊃ z.ai
// general, kimi coding ⊃ moonshot) resolve to the more-specific identity.
