// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"fmt"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

var endpointHeuristics = buildEndpointHeuristics()

type endpointHeuristic struct {
	pattern  string
	provider agenticprovider.Provider
	api      agenticprovider.Api
}

func buildEndpointHeuristics() []endpointHeuristic {
	var out []endpointHeuristic
	for _, d := range schema.ProviderCatalog() {
		api := d.API
		if api == "" {
			api = agenticprovider.ApiOpenAICompletions
		}
		for _, pat := range d.URLPatterns {
			out = append(out, endpointHeuristic{pattern: pat, provider: d.Provider, api: api})
		}
	}
	// Most-specific pattern first: a longer URL pattern is always a more
	// precise identity (e.g. "api.z.ai/api/coding" ⊃ "api.z.ai",
	// "opencode.ai/zen/go" ⊃ "opencode.ai"). Sorting by descending length
	// makes precedence independent of catalog declaration order. Stable sort
	// keeps catalog order among equal-length patterns.
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].pattern) > len(out[j].pattern)
	})
	return out
}

func matchProviderEndpoint(endpoint string) (agenticprovider.Provider, agenticprovider.Api) {
	e := strings.ToLower(endpoint)
	for _, h := range endpointHeuristics {
		if strings.Contains(e, h.pattern) {
			return h.provider, h.api
		}
	}
	return "", ""
}

func parseCompatJSON(api agenticprovider.Api, raw string) any {
	switch api {
	case agenticprovider.ApiOpenAICompletions:
		var c agenticprovider.OpenAICompletionsCompat
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			return &c
		}
	case agenticprovider.ApiAnthropicMessages:
		var c agenticprovider.AnthropicMessagesCompat
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			return &c
		}
	}
	return raw
}

// inferProviderIdentity maps a Goa provider config to agentic provider/api enums.
// Resolution order:
//  1. Explicit Provider/API fields on the config.
//  2. Preset lookup by provider ID.
//  3. Localhost endpoint heuristic (LM Studio / Ollama).
//  4. OpenAI-compatible fallback.
func inferProviderIdentity(pCfg config.ProviderConfig) (agenticprovider.Provider, agenticprovider.Api) {
	if pCfg.Provider != "" {
		prov := agenticprovider.Provider(pCfg.Provider)
		api := agenticprovider.ApiOpenAICompletions
		if pCfg.API != "" {
			api = agenticprovider.Api(pCfg.API)
		}
		return prov, api
	}

	if preset := config.FindPreset(pCfg.ID); preset != nil {
		prov := agenticprovider.Provider(preset.Provider)
		api := agenticprovider.Api(preset.API)
		if prov == "" {
			prov = agenticprovider.ProviderCustom
		}
		if api == "" {
			api = agenticprovider.ApiOpenAICompletions
		}
		return prov, api
	}

	if prov, api := matchProviderEndpoint(pCfg.Endpoint); prov != "" {
		return prov, api
	}
	return agenticprovider.ProviderOpenAI, agenticprovider.ApiOpenAICompletions
}

// ResolveModelByName looks up a model in the registry by its display name.
// Used by AgentPool to resolve per-role models (e.g., "qwen-coder-7b").
func (pm *ProviderManager) ResolveModelByName(modelName string) (agenticprovider.Model, error) {
	pCfg, _ := pm.Active()
	return pm.resolveModelByName(pCfg, modelName)
}

// ResolveModelByID resolves a model config ID to a full agentic Model.
// It first maps the ID to the actual model name via ResolveModelName, then
// looks up the registry and applies active provider overrides.
func (pm *ProviderManager) ResolveModelByID(modelID string) (agenticprovider.Model, error) {
	pCfg, _ := pm.Active()
	modelName := pm.ResolveModelName(*pCfg, modelID)
	if modelName == "" {
		modelName = modelID
	}
	return pm.resolveModelByName(pCfg, modelName)
}

// ResolveModelForProvider resolves a model config ID against a specific
// provider. This lets per-role agents (e.g., the companion) use a different
// provider than the main agent.
func (pm *ProviderManager) ResolveModelForProvider(providerID, modelID string) (agenticprovider.Model, error) {
	pCfg := pm.cfg.Load().GetProviderByID(providerID)
	if pCfg == nil {
		pCfg, _ = pm.Active()
	}
	if pCfg == nil {
		return agenticprovider.Model{}, fmt.Errorf("no provider configured")
	}
	modelName := pm.ResolveModelName(*pCfg, modelID)
	if modelName == "" {
		modelName = modelID
	}
	return pm.resolveModelByName(pCfg, modelName)
}

func (pm *ProviderManager) resolveModelByName(pCfg *config.ProviderConfig, modelName string) (agenticprovider.Model, error) {
	if pCfg == nil {
		return agenticprovider.Model{}, fmt.Errorf("no provider configured")
	}

	resolveURL := func(api agenticprovider.Api, endpoint string) string {
		if endpoint == "" {
			return ""
		}
		if needsChatCompletionsSuffix(api) {
			return ChatCompletionsEndpoint(endpoint)
		}
		return endpoint
	}

	if m := models.GetModel(modelName); m != nil {
		mdl := *m
		mdl.ID = modelName
		mdl.Name = modelName
		if pCfg.Endpoint != "" {
			mdl.BaseURL = resolveURL(mdl.Api, pCfg.Endpoint)
		}
		return mdl, nil
	}

	// Try prefix-based lookup for known model families.
	if m := models.LookupByPrefix(modelName); m != nil {
		mdl := *m
		mdl.ID = modelName
		mdl.Name = modelName
		prov, api := inferProviderIdentity(*pCfg)
		if mdl.Provider == "" {
			mdl.Provider = prov
		}
		if mdl.Api == "" {
			mdl.Api = api
		}
		if pCfg.Endpoint != "" {
			mdl.BaseURL = resolveURL(mdl.Api, pCfg.Endpoint)
		}
		return mdl, nil
	}

	prov, api := inferProviderIdentity(*pCfg)
	return agenticprovider.Model{
		ID:         modelName,
		Name:       modelName,
		Api:        api,
		Provider:   prov,
		BaseURL:    resolveURL(api, pCfg.Endpoint),
		InputTypes: []string{"text"},
	}, nil
}

// BuildStreamOptions constructs provider.StreamOptions from the active
// ProviderConfig and ModelConfig, applying defaults for timeout, retries,
// headers, transport, cache, and reasoning.
