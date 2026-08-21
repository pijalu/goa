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
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/auth"
)

// ModelInfo describes an LLM model from a provider's model list. Live
// /models endpoints typically return only an ID; ContextWindow, MaxTokens, and
// Pricing are populated from the built-in registry when available so model
// pickers can show capabilities and cost without a second lookup.
type ModelInfo struct {
	ID            string                `json:"id"`
	ContextWindow int                   `json:"context_window,omitempty"`
	MaxTokens     int                   `json:"max_tokens,omitempty"`
	Pricing       *config.PricingConfig `json:"pricing,omitempty"`
}

// ModelListResponse represents the OpenAI-compatible /models response.
type ModelListResponse struct {
	Data []ModelInfo `json:"data"`
}

// ProviderManager manages active provider selection, model listing,
// and connection testing.
type ProviderManager struct {
	// cfg holds the current configuration. It is an atomic pointer so a hot
	// reload (config watcher) can swap in a fresh config while request
	// goroutines resolve the active provider/model: an in-flight request keeps
	// the config it loaded; the next request sees the new one.
	cfg       atomic.Pointer[config.Config]
	client    *http.Client
	Cache     *ModelCache
	authStore *auth.Store
}

// NewProviderManager creates a provider manager.
func NewProviderManager(cfg *config.Config) *ProviderManager {
	pm := &ProviderManager{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		Cache: NewModelCache(),
	}
	pm.cfg.Store(cfg)
	return pm
}

// Config returns the config currently in effect for request resolution. It is
// the boot config until a hot reload applies a new one.
func (pm *ProviderManager) Config() *config.Config {
	if pm == nil {
		return nil
	}
	return pm.cfg.Load()
}

// SetConfig atomically swaps the provider configuration, e.g. after a hot
// reload of the config cascade. The next request resolves the new provider
// profile (endpoint, API key, models, effort); in-flight requests keep the
// config they loaded.
func (pm *ProviderManager) SetConfig(cfg *config.Config) {
	pm.cfg.Store(cfg)
}

// SetAuthStore wires the encrypted credential store so the provider manager
// can use stored OAuth tokens or API keys when the provider config does not
// specify an explicit API key.
func (pm *ProviderManager) SetAuthStore(store *auth.Store) {
	pm.authStore = store
}

// Active returns the currently active provider config and resolved model name.
// The model name is resolved through the ModelConfig system (model config ID →
// actual model name) so callers can use it directly in API requests.
// Returns nil provider if no providers are configured, or if the explicitly
// configured active provider is not found (no silent fallback to a different
// provider, which would route requests to the wrong endpoint).
func (pm *ProviderManager) Active() (*config.ProviderConfig, string) {
	if pm == nil {
		return nil, ""
	}
	cfg := pm.cfg.Load()
	if cfg == nil {
		return nil, ""
	}
	provider := cfg.GetProviderByID(cfg.ActiveProvider)
	if provider == nil && cfg.ActiveProvider == "" {
		provider = cfg.PreferredProvider()
	}
	if provider == nil {
		return nil, ""
	}
	model := pm.resolveModelName(*provider)
	return provider, model
}

// SetActive updates the active provider and model.
func (pm *ProviderManager) SetActive(providerID, model string) error {
	cfg := pm.cfg.Load()
	if providerID != "" {
		if cfg.GetProviderByID(providerID) == nil {
			return fmt.Errorf("provider %q not found", providerID)
		}
		cfg.ActiveProvider = providerID
	}
	if model != "" {
		cfg.ActiveModel = model
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
	provider := pm.cfg.Load().GetProviderByID(providerID)
	if provider == nil {
		return nil, fmt.Errorf("provider %q not found", providerID)
	}

	endpoint := modelsEndpoint(provider.Endpoint)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if key := pm.effectiveAPIKey(provider); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
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

	out := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		out = append(out, enrichModelInfo(m))
	}
	return out, nil
}

// ListRegistryModels returns catalog models for a provider config ID, as
// ModelInfo. Source priority: the models.dev runtime catalog (freshest,
// when loaded/refreshed) first, then the embedded built-in registry — so a
// models.dev-only model still shows up even before the next build embeds it.
// This complements ListModels (live /models fetch): providers whose endpoint
// does not serve a complete model list (e.g. z.ai's coding plan) still offer
// their known models in add-model pickers.
//
// The openai-codex subscription provider has NO models.dev mapping of its own
// and its endpoint (https://chatgpt.com/backend-api) serves no /models route
// (Cloudflare 403), so both the live fetch and a straight registry lookup come
// back empty. Codex subscriptions serve the codex model family of the openai
// catalog, so the lookup aliases to the openai registry filtered to codex
// models (matching Pi's hardcoded codex catalog: gpt-5.x-codex[-spark] plus
// the gpt-5.x codex-served generations). The provider identity used for
// streaming is unaffected — only the model-list lookup aliases.
func (pm *ProviderManager) ListRegistryModels(providerID string) []ModelInfo {
	pCfg := pm.cfg.Load().GetProviderByID(providerID)
	if pCfg == nil {
		return nil
	}
	prov, _ := inferProviderIdentity(*pCfg)

	filter := func(string) bool { return true }
	if prov == schema.ProviderOpenAICodex {
		prov = schema.ProviderOpenAI
		filter = isCodexFamilyModel
	}

	seen := map[string]bool{}
	var out []ModelInfo
	for _, m := range models.GetRuntimeModels(prov) {
		if !seen[m.ID] && filter(m.ID) {
			out = append(out, registryModelInfo(m))
			seen[m.ID] = true
		}
	}
	for _, m := range models.GetModels(prov) {
		if !seen[m.ID] && filter(m.ID) {
			out = append(out, registryModelInfo(m))
			seen[m.ID] = true
		}
	}
	return out
}

// registryModelInfo converts a built-in registry model into a ModelInfo,
// carrying over capability and pricing metadata for model pickers.
func registryModelInfo(m agenticprovider.Model) ModelInfo {
	return ModelInfo{
		ID:            m.ID,
		ContextWindow: m.ContextWindow,
		MaxTokens:     m.MaxTokens,
		Pricing:       registryPricing(m.Cost),
	}
}

// registryPricing converts a registry model's per-token pricing into the
// config.PricingConfig per-million-token representation used by the UI. A fully
// zero cost yields nil so callers degrade gracefully (no cost shown).
func registryPricing(cost agenticprovider.ModelPricing) *config.PricingConfig {
	if cost.Input == 0 && cost.Output == 0 && cost.CacheRead == 0 && cost.CacheWrite == 0 {
		return nil
	}
	return &config.PricingConfig{
		InputPer1M:      cost.Input * 1e6,
		OutputPer1M:     cost.Output * 1e6,
		CacheReadPer1M:  cost.CacheRead * 1e6,
		CacheWritePer1M: cost.CacheWrite * 1e6,
	}
}

// enrichModelInfo fills capability/pricing fields for a live /models entry from
// the built-in registry, which knows context windows and cost that most
// endpoints omit. Entries that already carry data are left unchanged.
func enrichModelInfo(info ModelInfo) ModelInfo {
	if info.ContextWindow != 0 && info.MaxTokens != 0 && info.Pricing != nil {
		return info
	}
	reg := models.GetModel(info.ID)
	if reg == nil {
		reg = models.LookupByPrefix(info.ID)
	}
	if reg == nil {
		return info
	}
	if info.ContextWindow == 0 {
		info.ContextWindow = reg.ContextWindow
	}
	if info.MaxTokens == 0 {
		info.MaxTokens = reg.MaxTokens
	}
	if info.Pricing == nil {
		info.Pricing = registryPricing(reg.Cost)
	}
	return info
}

// served by the ChatGPT Codex subscription endpoint: the explicitly codex
// models (gpt-5.x-codex[-spark]) plus the gpt-5.4+ generations Pi's codex
// catalog carries (gpt-5.4, gpt-5.4-mini, gpt-5.5, gpt-5.6-luna/sol/terra —
// scripts/generate-models.ts). Older gpt-5.x IDs (gpt-5, gpt-5.1..gpt-5.3) and
// pro/nano/chat-latest variants are not served by the subscription transport.
