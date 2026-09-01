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
	"sync"
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/auth"
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
	// cfg holds the current configuration. It is an atomic pointer so a hot
	// reload (config watcher) can swap in a fresh config while request
	// goroutines resolve the active provider/model: an in-flight request keeps
	// the config it loaded; the next request sees the new one.
	cfg       atomic.Pointer[config.Config]
	client    *http.Client
	Cache     *ModelCache
	authStore *auth.Store

	// selMu guards sessionSel, the SESSION-scoped provider/model selection
	// made at runtime via /model, /provider or team switching.
	//
	// Without it, the active provider/model lived ONLY inside whichever
	// config object was current — so a hot reload (config watcher) swapped
	// in whatever active_provider/active_model were persisted on disk. With
	// two goa instances sharing one cascade this leaked state ACROSS
	// PROCESSES: instance A's /model switch saved to the shared yaml,
	// instance B's watcher adopted it, and B's footer AND next requests
	// silently switched to A's provider. The disk value is only the DEFAULT;
	// once a session picks explicitly, its pick wins over every reload until
	// the session ends.
	selMu sync.Mutex
	// sessionSel is the explicit runtime pick; empty fields mean "no explicit
	// pick yet" and the config default applies.
	sessionSel sessionSelection
}

// sessionSelection is a runtime provider/model pick made by THIS session
// (/model, /provider, team switching). It outlives config hot reloads: see
// ProviderManager.selMu for why it must never live solely in the config.
type sessionSelection struct {
	providerID string
	modelID    string
}

// applySelectionLocked writes an explicit session pick onto cfg. Empty
// selection fields are skipped: they mean "this session never picked that
// half explicitly", so whatever the config carries (boot default or a disk
// reload) stands. Caller must hold selMu.
func applySelectionLocked(cfg *config.Config, sel sessionSelection) {
	if sel.providerID != "" {
		cfg.ActiveProvider = sel.providerID
	}
	if sel.modelID != "" {
		cfg.ActiveModel = sel.modelID
	}
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
//
// The session's explicit provider/model selection (see SetActive) is
// re-applied to the incoming config BEFORE it is published: a reload must
// refresh profiles (endpoints, keys, models) but never clobber what the
// session picked at runtime — with multiple instances sharing one config
// cascade, another process's persisted switch would otherwise bleed into
// this one.
func (pm *ProviderManager) SetConfig(cfg *config.Config) {
	pm.selMu.Lock()
	defer pm.selMu.Unlock()
	if cfg != nil {
		applySelectionLocked(cfg, pm.sessionSel)
	}
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
//
// Two things happen, deliberately:
//
//  1. The pick is recorded as SESSION state (selMu/sessionSel). Every config
//     published later via SetConfig gets the pick re-applied, so hot reloads
//     can never overwrite it (multi-instance isolation).
//  2. The current config object is updated in place, exactly as before:
//     commands persist cfg through the ConfigSaver right after switching, so
//     /model keeps persisting the user's choice as the STARTUP default for
//     future sessions without this manager having to know about saving.
func (pm *ProviderManager) SetActive(providerID, model string) error {
	pm.selMu.Lock()
	defer pm.selMu.Unlock()
	cfg := pm.cfg.Load()
	if cfg == nil {
		return fmt.Errorf("provider manager has no config")
	}
	if providerID != "" {
		if cfg.GetProviderByID(providerID) == nil {
			return fmt.Errorf("provider %q not found", providerID)
		}
	}
	// Validate BEFORE recording any state so a failed switch leaves both the
	// session selection and the config untouched.
	pm.sessionSel.providerID = providerID
	pm.sessionSel.modelID = model
	applySelectionLocked(cfg, pm.sessionSel)
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

// ResponsesEndpoint ensures the endpoint URL points to the OpenAI Responses
// route ({base}/responses). Accepts base API URLs (https://host/v1) and full
// responses URLs (idempotent). OpenCode's Zen gateway serves its Muse Spark
// family at https://opencode.ai/zen/v1/responses (npm @ai-sdk/openai), not on
// the chat-completions surface (bugs.md 2026-08-30).
func ResponsesEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint // fallback
	}
	// Strip trailing slash for clean path joining
	u.Path = strings.TrimRight(u.Path, "/")
	// Append /responses if not already present
	if !strings.HasSuffix(u.Path, "/responses") {
		u.Path += "/responses"
	}
	return u.String()
}

// MessagesEndpoint ensures the endpoint URL points to the Anthropic Messages
// route. Three input shapes are handled:
//   - bare host (https://api.anthropic.com)      → {host}/v1/messages
//   - versioned base (https://opencode.ai/zen/v1) → {base}/messages
//   - already-suffixed ({base}/messages)          → unchanged (idempotent)
//
// Gateway providers that serve anthropic-format models through a shared
// versioned base URL (OpenCode Zen/Go serve claude-* and qwen3.x-plus|max at
// {base}/messages) need the route appended the same way the OpenAI surfaces
// do; the anthropic transport otherwise POSTs the bare base URL.
func MessagesEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint // fallback
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(u.Path, "/messages") {
		return u.String()
	}
	// Bare host (no version prefix): insert the canonical /v1 before /messages.
	if u.Path == "" {
		u.Path = "/v1/messages"
		return u.String()
	}
	u.Path += "/messages"
	return u.String()
}

// modelEndpointURL adapts a configured provider endpoint to the wire URL for
// an API: /chat/completions for the OpenAI-compatible surface, /responses for
// the OpenAI Responses surfaces, /messages for the Anthropic Messages surface
// (the generic protocol runtime POSTs the URL verbatim, so the full route must
// be in the model's BaseURL). Google, Bedrock, and Mistral own their paths;
// Codex normalization happens at request time.
func modelEndpointURL(api agenticprovider.Api, endpoint string) string {
	if endpoint == "" {
		return ""
	}
	switch api {
	case agenticprovider.ApiOpenAICompletions:
		return ChatCompletionsEndpoint(endpoint)
	case agenticprovider.ApiOpenAIResponses, agenticprovider.ApiAzureOpenAIResponses:
		return ResponsesEndpoint(endpoint)
	case agenticprovider.ApiAnthropicMessages:
		return MessagesEndpoint(endpoint)
	default:
		return endpoint
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
		return nil, fmt.Errorf("provider returned status %d: %s", resp.StatusCode, summarizeModelErrBody(body))
	}

	var result ModelListResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse model list: %w", err)
	}

	return result.Data, nil
}

// modelErrBodyCap bounds the body snippet embedded in a discovery error, so a
// verbose error page can never overflow the flash that renders it.
const modelErrBodyCap = 160

// summarizeModelErrBody renders an HTTP error body as a bounded, single-line
// snippet for a discovery error (Bug B, 2026-08-27). The Cloudflare/HTML
// challenge pages that gated /models endpoints return must never reach the UI
// raw: markup collapses to a fixed note, while a short plaintext/JSON body is
// preserved (whitespace-collapsed) so the real reason stays visible. The
// result is always single-line and capped.
func summarizeModelErrBody(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ") // collapse all whitespace runs to one space
	if looksLikeHTML(s) {
		return "(HTML error page)"
	}
	if s == "" {
		return "(empty body)"
	}
	if len(s) > modelErrBodyCap {
		s = s[:modelErrBodyCap] + "…"
	}
	return s
}

// looksLikeHTML reports whether s is an HTML/XML document rather than a plain
// error message. The marker is a tag opener at the start or a known document
// tag anywhere — a plaintext body that merely mentions a tag is left intact.
func looksLikeHTML(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(l, "<") {
		return true
	}
	for _, tag := range []string{"<html", "<!doctype", "<head", "<body", "<svg", "<div", "<meta"} {
		if strings.Contains(l, tag) {
			return true
		}
	}
	return false
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
			out = append(out, ModelInfo{ID: m.ID})
			seen[m.ID] = true
		}
	}
	for _, m := range models.GetModels(prov) {
		if !seen[m.ID] && filter(m.ID) {
			out = append(out, ModelInfo{ID: m.ID})
			seen[m.ID] = true
		}
	}
	return out
}

// isCodexFamilyModel reports whether a model ID belongs to the codex family
// served by the ChatGPT Codex subscription endpoint: the explicitly codex
// models (gpt-5.x-codex[-spark]) plus the gpt-5.4+ generations Pi's codex
// catalog carries (gpt-5.4, gpt-5.4-mini, gpt-5.5, gpt-5.6-luna/sol/terra —
// scripts/generate-models.ts). Older gpt-5.x IDs (gpt-5, gpt-5.1..gpt-5.3) and
// pro/nano/chat-latest variants are not served by the subscription transport.
