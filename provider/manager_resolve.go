// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"fmt"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider/models"
)

func (pm *ProviderManager) ResolveActiveModel() (agenticprovider.Model, error) {
	pCfg, modelName := pm.Active()
	if pCfg == nil {
		return agenticprovider.Model{}, fmt.Errorf("no active provider configured")
	}
	if modelName == "" {
		return agenticprovider.Model{}, fmt.Errorf("no model name resolved for provider %q", pCfg.ID)
	}

	mCfg, err := pm.cfg.Load().GetActiveModelConfig()
	if err != nil {
		mCfg = config.ModelConfig{}
	}

	var mdl agenticprovider.Model

	// Try the built-in model registry first, provider-exact so shared model
	// IDs keep their provider-specific metadata (e.g. glm-5.2 quota pricing
	// on zai vs per-token pricing on zai-api).
	prov, _ := inferProviderIdentity(*pCfg)
	if m := models.GetModelForProvider(prov, modelName); m != nil {
		mdl = mergeRegistryModel(*m, *pCfg, mCfg, modelName)
	} else {
		// Fallback: construct a minimal Model for custom/local providers.
		mdl = buildFallbackModel(*pCfg, mCfg, modelName)
	}

	return mdl, nil
}

// RefreshLocalContextWindow re-detects the context window for the active local
// provider/model. It is meant to be called after the model has loaded and the
// first tokens have been received, so local servers can report the real loaded
// context length (e.g. LM Studio's loaded_context_length). Returns 0 for remote
// providers or when detection fails.
func (pm *ProviderManager) RefreshLocalContextWindow() int {
	pCfg, modelName := pm.Active()
	if pCfg == nil || modelName == "" {
		return 0
	}
	if !IsLocalEndpoint(pCfg.Endpoint) {
		return 0
	}
	return detectLocalContextWindow(*pCfg, modelName, pCfg.APIKey)
}

// inferEffectiveProviderAPI returns the provider/api identity for a model,
// letting the active provider config decide the wire protocol unless the model
// config explicitly overrides it.
func inferEffectiveProviderAPI(pCfg config.ProviderConfig, mCfg config.ModelConfig) (agenticprovider.Provider, agenticprovider.Api) {
	prov, api := inferProviderIdentity(pCfg)
	if mCfg.API != "" {
		api = agenticprovider.Api(mCfg.API)
	}
	if mCfg.Provider != "" {
		prov = agenticprovider.Provider(mCfg.Provider)
	}
	return prov, api
}

// setModelBaseURL sets BaseURL based on the resolved API and provider endpoint.
func setModelBaseURL(mdl *agenticprovider.Model, endpoint string, api agenticprovider.Api) {
	if endpoint == "" {
		return
	}
	if needsChatCompletionsSuffix(api) {
		mdl.BaseURL = ChatCompletionsEndpoint(endpoint)
	} else {
		mdl.BaseURL = endpoint
	}
}

// applyModelConfigCapabilities applies model-level overrides from config onto
// a registry model without replacing its built-in capabilities.
func applyModelConfigCapabilities(mdl *agenticprovider.Model, mCfg config.ModelConfig, api agenticprovider.Api) {
	if mCfg.Reasoning != nil {
		mdl.Reasoning = *mCfg.Reasoning
	}
	if budgets := effectiveThinkingBudgets(mCfg); len(budgets) > 0 {
		mdl.ThinkingBudgets = budgets
	}
	if mCfg.Compat != "" {
		mdl.Compat = parseCompatJSON(api, mCfg.Compat)
	}
	if mCfg.ContextWindow > 0 {
		mdl.ContextWindow = mCfg.ContextWindow
	}
	if mCfg.MaxTokens > 0 {
		mdl.MaxTokens = mCfg.MaxTokens
	}
	if len(mCfg.InputTypes) > 0 {
		mdl.InputTypes = mCfg.InputTypes
	}
	if mCfg.ThinkingLevel != "" {
		mdl.Reasoning = true
	}
	if native := nativeThinkingLevelMap(mCfg); len(native) > 0 {
		mdl.ThinkingLevelMap = native
	}
}

// applyProviderExtra merges provider-level Extra metadata into the model.
func applyProviderExtra(mdl *agenticprovider.Model, pCfg config.ProviderConfig) {
	if len(pCfg.Extra) == 0 {
		return
	}
	if mdl.Extra == nil {
		mdl.Extra = make(map[string]any, len(pCfg.Extra))
	}
	for k, v := range pCfg.Extra {
		mdl.Extra[k] = v
	}
}

// knownProviderPrefixes lists provider names that users may prepend to a model
// ID. When a model name like "google/gemma-4-e4b" is not found in the
// registry, stripping the known provider prefix lets us match the bare model ID
// (e.g., "gemma-4-e4b") and still use the active provider's endpoint.
// Derived from the provider catalog (single source of truth).
