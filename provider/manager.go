// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package provider manages LLM provider configuration, model listing,
// connection testing, and active provider tracking.
package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai"
)

// ModelInfo describes an LLM model from a provider's model list.
type ModelInfo struct {
	ID string `json:"id"`
}

// ModelListResponse represents the OpenAI-compatible /models response.
type ModelListResponse struct {
	Data []ModelInfo `json:"data"`
}

// ProviderManager manages active provider selection, model listing,
// and connection testing.
type ProviderManager struct {
	cfg    *config.Config
	client *http.Client
	Cache  *ModelCache
}

// NewProviderManager creates a provider manager.
func NewProviderManager(cfg *config.Config) *ProviderManager {
	return &ProviderManager{
		cfg: cfg,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		Cache: NewModelCache(),
	}
}

// Active returns the currently active provider config and resolved model name.
// The model name is resolved through the ModelConfig system (model config ID →
// actual model name) so callers can use it directly in API requests.
// Returns nil provider if no providers are configured, or if the explicitly
// configured active provider is not found (no silent fallback to a different
// provider, which would route requests to the wrong endpoint).
func (pm *ProviderManager) Active() (*config.ProviderConfig, string) {
	provider := pm.cfg.GetProviderByID(pm.cfg.ActiveProvider)
	if provider == nil && pm.cfg.ActiveProvider == "" {
		provider = pm.cfg.PreferredProvider()
	}
	if provider == nil {
		return nil, ""
	}
	model := pm.resolveModelName(*provider)
	return provider, model
}

// SetActive updates the active provider and model.
func (pm *ProviderManager) SetActive(providerID, model string) error {
	if providerID != "" {
		if pm.cfg.GetProviderByID(providerID) == nil {
			return fmt.Errorf("provider %q not found", providerID)
		}
		pm.cfg.ActiveProvider = providerID
	}
	if model != "" {
		pm.cfg.ActiveModel = model
	}
	return nil
}

// modelsEndpoint derives the /v1/models URL from a provider endpoint.
// Accepts both full chat-completions URLs and base API URLs.
func modelsEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint // fallback
	}
	// Strip /chat/completions suffix if present, then append /models.
	u.Path = strings.TrimRight(strings.TrimSuffix(u.Path, "/chat/completions"), "/") + "/models"
	return u.String()
}

// needsChatCompletionsSuffix returns true for API types that use the
// OpenAI-compatible /chat/completions endpoint. Non-OpenAI APIs (Anthropic,
// Google, Bedrock, Mistral) manage their own URL in the provider streamer.
func needsChatCompletionsSuffix(api agenticprovider.Api) bool {
	switch api {
	case agenticprovider.ApiOpenAICompletions, agenticprovider.ApiOpenAIResponses, agenticprovider.ApiAzureOpenAIResponses:
		return true
	default:
		return false
	}
}

// ChatCompletionsEndpoint ensures the endpoint URL points to /chat/completions.
// Accepts base API URLs (http://host/v1) and returns the full chat completions URL.
// When api is provided and the Api does NOT use /chat/completions (e.g. Anthropic,
// Google, Bedrock), the endpoint is returned unchanged.
func ChatCompletionsEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint // fallback
	}
	// Strip trailing slash for clean path joining
	u.Path = strings.TrimRight(u.Path, "/")
	// Append /chat/completions if not already present
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
	}
	return u.String()
}

// ListModelsCached returns the provider's model list, using the cache when
// fresh. On cache miss it fetches via ListModels and stores the result.
func (pm *ProviderManager) ListModelsCached(providerID string, ttl time.Duration) ([]ModelInfo, error) {
	if pm.Cache != nil {
		if models, ok := pm.Cache.Get(providerID, ttl); ok {
			return models, nil
		}
	}
	models, err := pm.ListModels(providerID)
	if err != nil {
		return nil, err
	}
	if pm.Cache != nil {
		pm.Cache.Set(providerID, models)
	}
	return models, nil
}

// ListModels queries the provider's /models endpoint.
func (pm *ProviderManager) ListModels(providerID string) ([]ModelInfo, error) {
	provider := pm.cfg.GetProviderByID(providerID)
	if provider == nil {
		return nil, fmt.Errorf("provider %q not found", providerID)
	}

	endpoint := modelsEndpoint(provider.Endpoint)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}

	resp, err := pm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("provider %q does not support /models endpoint", providerID)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("provider returned status %d: %s", resp.StatusCode, string(body))
	}

	var result ModelListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse model list: %w", err)
	}

	return result.Data, nil
}

// TestConnection tests connectivity to a provider by listing models.
func (pm *ProviderManager) TestConnection(providerID string) (latency time.Duration, modelCount int, err error) {
	start := time.Now()
	models, err := pm.ListModels(providerID)
	latency = time.Since(start)
	if err != nil {
		return latency, 0, err
	}
	return latency, len(models), nil
}

// ResolveModelName resolves the actual model name to send to the API.
// This can take either a model config ID (from models[]) or a raw model name.
// Resolution order:
//  1. If input matches a ModelConfig.ID, return its Model field
//  2. Otherwise return the input verbatim (already a raw model name)
//  3. If input is empty, fall back to the provider's DefaultModel
//  4. If still empty, fall back to the first ModelConfig for the provider
func (pm *ProviderManager) ResolveModelName(cfg config.ProviderConfig, modelID string) string {
	// 1. Look up by model config ID
	if modelID != "" {
		if mc := pm.cfg.GetModelByID(modelID); mc != nil && mc.Model != "" {
			return mc.Model
		}
		// Not a model config ID — return verbatim (raw model name)
		return modelID
	}

	// 2. Fall back to first ModelConfig for this provider
	for i := range pm.cfg.Models {
		if pm.cfg.Models[i].ProviderID == cfg.ID && pm.cfg.Models[i].Model != "" {
			return pm.cfg.Models[i].Model
		}
	}

	return ""
}

// resolveModelName is a convenience wrapper using ActiveModel from config.
func (pm *ProviderManager) resolveModelName(cfg config.ProviderConfig) string {
	return pm.ResolveModelName(cfg, pm.cfg.ActiveModel)
}

// ResolveActiveModel resolves the active model through the agentic model registry.
// Returns the Model with populated capabilities (thinking levels, context window,
// pricing, compat flags) so callers can use provider.Stream() directly.
//
// Falls back to a minimal Model if the active model isn't in the built-in registry
// (e.g., custom/local models). In that case, Provider and Api are inferred from
// the active provider config's endpoint and preset mapping. Context window is taken
// from the model config if set, otherwise the built-in registry default is used.
// The real loaded context length for local providers (LM Studio, llama.cpp) is
// refreshed after the model has loaded and the first tokens have been received;
// see ProviderManager.RefreshLocalContextWindow.
func (pm *ProviderManager) ResolveActiveModel() (agenticprovider.Model, error) {
	pCfg, modelName := pm.Active()
	if pCfg == nil {
		return agenticprovider.Model{}, fmt.Errorf("no active provider configured")
	}
	if modelName == "" {
		return agenticprovider.Model{}, fmt.Errorf("no model name resolved for provider %q", pCfg.ID)
	}

	mCfg, err := pm.cfg.GetActiveModelConfig()
	if err != nil {
		mCfg = config.ModelConfig{}
	}

	var mdl agenticprovider.Model

	// Try the built-in model registry first.
	if m := models.GetModel(modelName); m != nil {
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
	if !isLocalProvider(pCfg.Endpoint) {
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
	if mCfg.Reasoning {
		mdl.Reasoning = true
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
		mdl.ThinkingLevelMap = defaultThinkingLevelMap()
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
var knownProviderPrefixes = []string{
	string(agenticprovider.ProviderOpenAI),
	string(agenticprovider.ProviderAnthropic),
	string(agenticprovider.ProviderGoogle),
	string(agenticprovider.ProviderMistral),
	string(agenticprovider.ProviderAWS),
	string(agenticprovider.ProviderAzure),
	string(agenticprovider.ProviderGitHub),
	string(agenticprovider.ProviderTogether),
	string(agenticprovider.ProviderFireworks),
	string(agenticprovider.ProviderGroq),
	string(agenticprovider.ProviderPerplexity),
	string(agenticprovider.ProviderDeepSeek),
	string(agenticprovider.ProviderOpenRouter),
	string(agenticprovider.ProviderLMStudio),
	string(agenticprovider.ProviderOllama),
	string(agenticprovider.ProviderKimi),
	string(agenticprovider.ProviderKimiCode),
	string(agenticprovider.ProviderCustom),
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
	mdl.Provider = prov
	mdl.Api = api

	setModelBaseURL(&mdl, pCfg.Endpoint, api)
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

	baseURL := pCfg.Endpoint
	if needsChatCompletionsSuffix(api) {
		baseURL = ChatCompletionsEndpoint(pCfg.Endpoint)
	}
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
	if mCfg.Reasoning {
		mdl.Reasoning = true
	}
	if mCfg.ThinkingLevel != "" {
		mdl.ThinkingLevelMap = defaultThinkingLevelMap()
	}
	if budgets := effectiveThinkingBudgets(mCfg); len(budgets) > 0 {
		mdl.ThinkingBudgets = budgets
	}
	if mCfg.Compat != "" {
		mdl.Compat = parseCompatJSON(api, mCfg.Compat)
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

// defaultThinkingLevelMap returns the canonical thinking-level mapping.
func defaultThinkingLevelMap() agenticprovider.ThinkingLevelMap {
	return agenticprovider.ThinkingLevelMap{
		agenticprovider.ThinkingOff:     string(agenticprovider.ThinkingOff),
		agenticprovider.ThinkingMinimal: string(agenticprovider.ThinkingMinimal),
		agenticprovider.ThinkingLow:     string(agenticprovider.ThinkingLow),
		agenticprovider.ThinkingMedium:  string(agenticprovider.ThinkingMedium),
		agenticprovider.ThinkingHigh:    string(agenticprovider.ThinkingHigh),
		agenticprovider.ThinkingXHigh:   string(agenticprovider.ThinkingXHigh),
		agenticprovider.ThinkingMax:     string(agenticprovider.ThinkingMax),
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

// parseCompatJSON unmarshals a provider compat JSON blob into the concrete
// compat type for the given API. Unknown APIs return the raw string.
var endpointHeuristics = []struct {
	pattern  string
	provider agenticprovider.Provider
	api      agenticprovider.Api
}{
	{"localhost:1234", agenticprovider.ProviderLMStudio, agenticprovider.ApiOpenAICompletions},
	{"127.0.0.1:1234", agenticprovider.ProviderLMStudio, agenticprovider.ApiOpenAICompletions},
	{"localhost:11434", agenticprovider.ProviderOllama, agenticprovider.ApiOpenAICompletions},
	{"127.0.0.1:11434", agenticprovider.ProviderOllama, agenticprovider.ApiOpenAICompletions},
	{"api.moonshot.cn", agenticprovider.ProviderKimi, agenticprovider.ApiOpenAICompletions},
	{"api.moonshot.ai", agenticprovider.ProviderKimi, agenticprovider.ApiOpenAICompletions},
	{"api.kimi.com/coding", agenticprovider.ProviderKimiCode, agenticprovider.ApiOpenAICompletions},
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
	pCfg := pm.cfg.GetProviderByID(providerID)
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
func (pm *ProviderManager) BuildStreamOptions() agenticprovider.StreamOptions {
	pCfg := pm.cfg.GetActiveProviderConfig()
	mCfg, err := pm.cfg.GetActiveModelConfig()
	if err != nil {
		mCfg = config.ModelConfig{}
	}

	opts := agenticprovider.StreamOptions{MaxRetries: 2}
	applyProviderStreamOptions(&opts, pCfg)
	applyModelStreamOptions(&opts, mCfg)
	if opts.CacheRetention == "" {
		opts.CacheRetention = agenticprovider.CacheRetentionShort
	}
	opts.Headers = buildStreamHeaders(pCfg, mCfg)
	return opts
}

func applyProviderStreamOptions(opts *agenticprovider.StreamOptions, pCfg *config.ProviderConfig) {
	if pCfg == nil {
		return
	}
	if pCfg.APIKey != "" {
		opts.APIKey = pCfg.APIKey
	}
	if d := parsePositiveDuration(pCfg.Timeout); d > 0 {
		opts.Timeout = d
	}
	if pCfg.MaxRetries > 0 {
		opts.MaxRetries = pCfg.MaxRetries
	}
	if d := parsePositiveDuration(pCfg.MaxRetryDelay); d > 0 {
		opts.MaxRetryDelay = d
	}
	if pCfg.Transport != "" {
		opts.Transport = agenticprovider.Transport(pCfg.Transport)
	}
	if pCfg.CacheRetention != "" {
		opts.CacheRetention = agenticprovider.CacheRetention(pCfg.CacheRetention)
	}
	if pCfg.SessionID != "" {
		opts.SessionID = pCfg.SessionID
	}
	if len(pCfg.Metadata) > 0 {
		opts.Metadata = make(map[string]any, len(pCfg.Metadata))
		for k, v := range pCfg.Metadata {
			opts.Metadata[k] = v
		}
	}
}

func applyModelStreamOptions(opts *agenticprovider.StreamOptions, mCfg config.ModelConfig) {
	if mCfg.Temperature != 0 {
		opts.Temperature = &mCfg.Temperature
	}
	if mCfg.MaxTokens > 0 {
		opts.MaxTokens = mCfg.MaxTokens
	}
}

func buildStreamHeaders(pCfg *config.ProviderConfig, mCfg config.ModelConfig) map[string]string {
	ua := ""
	if pCfg != nil {
		ua = pCfg.UserAgent
	}
	if ua == "" {
		ua = "goa/" + internal.Version
	}

	headers := make(map[string]string)
	if ua != "" {
		headers["User-Agent"] = ua
	}
	if pCfg != nil {
		for k, v := range pCfg.Headers {
			headers[k] = v
		}
	}
	for k, v := range mCfg.Headers {
		headers[k] = v
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func parsePositiveDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// isLocalProvider returns true if the endpoint points to a local LLM server
// (LM Studio, llama.cpp, Ollama) that may expose context window metadata.
func isLocalProvider(endpoint string) bool {
	e := strings.ToLower(endpoint)
	return strings.Contains(e, "localhost:1234") || strings.Contains(e, "127.0.0.1:1234") ||
		strings.Contains(e, "localhost:11434") || strings.Contains(e, "127.0.0.1:11434") ||
		strings.Contains(e, "localhost") || strings.Contains(e, "127.0.0.1")
}

// detectFromLMStudioModels queries LM Studio's /api/v0/models endpoint for the
// loaded context length of the active model, falling back to context_length
// and then max_context_length.
func detectFromLMStudioModels(client *http.Client, baseURL *url.URL, modelName, apiKey string) int {
	modelsURL := baseURL.ResolveReference(&url.URL{Path: "/api/v0/models"})
	req, err := http.NewRequest("GET", modelsURL.String(), nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, _ := io.ReadAll(resp.Body)
	type modelEntry struct {
		ID                  string `json:"id"`
		MaxContextLength    int    `json:"max_context_length"`
		LoadedContextLength int    `json:"loaded_context_length"`
		ContextLength       int    `json:"context_length"`
	}
	var result struct {
		Data []modelEntry `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil {
		return 0
	}
	for _, m := range result.Data {
		if m.ID == modelName {
			return firstPositive(m.LoadedContextLength, m.ContextLength, m.MaxContextLength)
		}
	}
	return 0
}

// firstPositive returns the first positive value from the provided arguments.
func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// detectLocalContextWindow queries a local LLM provider to discover the context
// window size. Uses the provider identity to pick the right strategy:
//  1. LM Studio /api/v0/models — reads loaded_context_length, context_length, max_context_length
//  2. llama.cpp /props endpoint — reads default_generation_settings.n_ctx
//  3. llama.cpp /v1/models endpoint — reads meta.n_ctx (then meta.n_ctx_train)
//
// Returns 0 if detection fails or the provider doesn't expose this info.
func detectLocalContextWindow(pCfg config.ProviderConfig, modelName, apiKey string) int {
	endpoint := pCfg.Endpoint
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return 0
	}
	baseURL.Path = stripAPIPath(baseURL.Path)

	client := &http.Client{Timeout: 5 * time.Second}

	// LM Studio exposes /api/v0/models with loaded_context_length.
	if prov, _ := inferProviderIdentity(pCfg); prov == agenticprovider.ProviderLMStudio {
		if nCtx := detectFromLMStudioModels(client, baseURL, modelName, apiKey); nCtx > 0 {
			return nCtx
		}
	}

	// llama.cpp exposes the loaded context via /props and /v1/models.
	if nCtx := detectFromProps(client, baseURL, apiKey); nCtx > 0 {
		return nCtx
	}
	if nCtx := detectFromModelMeta(client, baseURL, modelName, apiKey); nCtx > 0 {
		return nCtx
	}

	return 0
}

// stripAPIPath removes /v1/chat/completions or /v1 suffix from a URL path.
func stripAPIPath(path string) string {
	path = strings.TrimSuffix(path, "/v1/chat/completions")
	path = strings.TrimSuffix(path, "/v1")
	return strings.TrimSuffix(path, "/")
}

// detectFromProps queries the /props endpoint (llama.cpp) for n_ctx.
func detectFromProps(client *http.Client, baseURL *url.URL, apiKey string) int {
	propsURL := baseURL.ResolveReference(&url.URL{Path: "/props"})
	req, err := http.NewRequest("GET", propsURL.String(), nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if json.Unmarshal(body, &result) != nil || result.DefaultGenerationSettings.NCtx <= 0 {
		return 0
	}
	return result.DefaultGenerationSettings.NCtx
}

// detectFromModelMeta queries the /v1/models endpoint for meta.n_ctx_train.
func detectFromModelMeta(client *http.Client, baseURL *url.URL, modelName, apiKey string) int {
	modelsURL := baseURL.ResolveReference(&url.URL{Path: "/v1/models"})
	req, err := http.NewRequest("GET", modelsURL.String(), nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, _ := io.ReadAll(resp.Body)
	type modelEntry struct {
		ID   string `json:"id"`
		Meta struct {
			NCtx      int `json:"n_ctx"`
			NCtxTrain int `json:"n_ctx_train"`
		} `json:"meta"`
	}
	var result struct {
		Data []modelEntry `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil {
		return 0
	}
	for _, m := range result.Data {
		if m.ID == modelName {
			if m.Meta.NCtx > 0 {
				return m.Meta.NCtx
			}
			if m.Meta.NCtxTrain > 0 {
				return m.Meta.NCtxTrain
			}
		}
	}
	return 0
}
