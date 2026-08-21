// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// modelsdev.go — runtime models.dev catalog with on-disk cache.
//
// The embedded api.json snapshot (go:embed api.json in overrides.go) is the
// always-available floor. On top of it, Goa keeps a runtime catalog fetched
// from
// https://models.dev/api.json and cached at ~/.goa/cache/models.dev.json:
//
//   - First use within a session returns whatever is already loaded
//     (embedded data, or the disk cache if it was loaded at startup).
//   - When the disk cache is missing or older than cacheTTL, a background
//     refresh is kicked off; the session keeps using the previous catalog
//     until the refresh lands (stale-while-revalidate).
//   - When models.dev is unreachable, the previous catalog (or embedded
//     data) keeps serving — the runtime layer never breaks model lookup.
//
// models.dev ids → Goa models via providerMappings (shared semantics with
// the embedded catalog parser: per-million-token costs are converted to
// per-token rates).

const (
	// ModelsDevURL is the canonical catalog endpoint.
	ModelsDevURL = "https://models.dev/api.json"
	// cacheTTL is how long the on-disk cache is considered fresh.
	cacheTTL = 24 * time.Hour
	// fetchTimeout bounds a catalog refresh HTTP call.
	fetchTimeout = 10 * time.Second
)

// modelsDevProviderMapping maps a models.dev provider key to Goa
// provider/API identities and the default base URL.
type modelsDevProviderMapping struct {
	Provider provider.Provider
	API      provider.Api
	BaseURL  string
}

// modelsDevProviderMappings defines the hand-curated provider identity
// mappings. Derived from the provider catalog: any catalog entry with a
// ModelsDevKey produces a mapping from that key to its identity and base URL.
// Catalog entries override the API to the models.dev wire protocol where the
// catalog's default differs (models.dev lists each provider's primary API).
var modelsDevProviderMappings = buildModelsDevMappings()

// modelsDevAPIOverrides records the API each provider speaks on models.dev
// when it differs from the catalog's chat-completions preset default. The
// models.dev catalog tracks the provider's canonical API surface.
var modelsDevAPIOverrides = map[string]provider.Api{
	"openai":    provider.ApiOpenAIResponses,
	"anthropic": provider.ApiAnthropicMessages,
	"google":    provider.ApiGoogleGenerativeAI,
	"mistral":   provider.ApiMistralConversations,
}

func buildModelsDevMappings() map[string]modelsDevProviderMapping {
	out := make(map[string]modelsDevProviderMapping)
	for _, d := range schema.ProviderCatalog() {
		if d.ModelsDevKey == "" {
			continue
		}
		api := d.API
		if override, ok := modelsDevAPIOverrides[d.ModelsDevKey]; ok {
			api = override
		}
		out[d.ModelsDevKey] = modelsDevProviderMapping{Provider: d.Provider, API: api, BaseURL: d.BaseURL}
	}
	return out
}

// modelsDevModel mirrors the models.dev per-model JSON shape.
type modelsDevModel struct {
	Name        string   `json:"name"`
	ToolCall    *bool    `json:"tool_call,omitempty"`
	Reasoning   *bool    `json:"reasoning,omitempty"`
	Limit       mdLimit  `json:"limit,omitempty"`
	Cost        mdCost   `json:"cost,omitempty"`
	Modalities  mdModals `json:"modalities,omitempty"`
	InputTypes_ []string `json:"-"`
}

type mdLimit struct {
	Context int `json:"context,omitempty"`
	Output  int `json:"output,omitempty"`
}

type mdCost struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

type mdModals struct {
	Input []string `json:"input,omitempty"`
}

// runtimeCatalog holds the models.dev-derived registrations.
type runtimeCatalog struct {
	loaded bool
	models map[string]provider.Model              // ID → model (first-wins per provider applied at load)
	byProv map[provider.Provider][]provider.Model // provider → models
}

var runtime struct {
	mu      sync.RWMutex
	cat     *runtimeCatalog
	refresh sync.Once
}

// runtimeFetch is the catalog fetcher; tests may stub it.
var runtimeFetch = func(url string) ([]byte, error) { return fetchURL(url, fetchTimeout) }

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// EnableModelsDevCatalog loads the cached models.dev catalog from cacheDir
// (typically ~/.goa/cache) and, when the cache is stale or missing, kicks
// off a background refresh (stale-while-revalidate). Safe to call once at
// startup; later calls are no-ops except triggering a revalidation when the
// cache aged out.
func EnableModelsDevCatalog(cacheDir string) {
	loadCachedCatalog(cacheDir)
	maybeRefresh(cacheDir)
}

// RefreshModelsDevCatalog forces a synchronous refresh of the models.dev
// catalog into cacheDir. Returns the number of providers loaded. On fetch
// failure the previous catalog stays active and the error is returned.
func RefreshModelsDevCatalog(cacheDir string) (int, error) {
	raw, err := runtimeFetch(ModelsDevURL)
	if err != nil {
		return 0, fmt.Errorf("models.dev fetch: %w", err)
	}
	cat, err := parseModelsDev(raw)
	if err != nil {
		return 0, err
	}
	if err := writeCatalogCache(cacheDir, raw); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot write models.dev cache: %v\n", err)
	}
	runtime.mu.Lock()
	runtime.cat = cat
	runtime.mu.Unlock()
	return len(cat.byProv), nil
}

// GetRuntimeModel returns the models.dev catalog entry for id, preferring
// the provider-exact entry when providerName is given.
func GetRuntimeModel(providerName provider.Provider, id string) *provider.Model {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	if runtime.cat == nil {
		return nil
	}
	if providerName != "" {
		for _, m := range runtime.cat.byProv[providerName] {
			if m.ID == id {
				cp := m
				return &cp
			}
		}
	}
	if m, ok := runtime.cat.models[id]; ok {
		cp := m
		return &cp
	}
	return nil
}

// GetRuntimeModels returns all models.dev catalog models for a provider.
func GetRuntimeModels(providerName provider.Provider) []provider.Model {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	if runtime.cat == nil {
		return nil
	}
	out := make([]provider.Model, len(runtime.cat.byProv[providerName]))
	copy(out, runtime.cat.byProv[providerName])
	return out
}

// ModelsDevProvider describes one models.dev catalog provider as imported by
// Goa: the models.dev key, the Goa provider identity it maps to (the curated
// catalog identity for mapped providers, the key itself for the fallback),
// the display name, the default base URL, the wire API, and the tool-calling
// model IDs that provider serves. Name/BaseURL/API are surfaced so the
// "/provider add" buildProviderPresetItems can turn any models.dev provider
// (e.g. tensorx) into a selectable, configurable preset without a catalog
// ProviderDef.
type ModelsDevProvider struct {
	Key      string
	Identity provider.Provider
	Name     string
	BaseURL  string
	API      provider.Api
	ModelIDs []string
}

// ModelsDevProviders returns the canonical enumeration of "all providers from
// models.dev" that Goa imports, derived from the embedded api.json: every
// provider key serving at least one tool-calling model, with its Goa identity
// and tool-calling model IDs. Sorted by key for deterministic tests. Tests and
// tooling use it to assert coverage without re-parsing api.json.
func ModelsDevProviders() []ModelsDevProvider {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(embeddedAPIJSON, &top); err != nil {
		return nil
	}
	out := make([]ModelsDevProvider, 0, len(top))
	for key, raw := range top {
		if p, ok := modelsDevProviderFromEntry(key, raw); ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// modelsDevProviderFromEntry builds a ModelsDevProvider from one embedded
// api.json entry, or (zero, false) when the provider serves no tool-calling
// model (nothing Goa can use) or the entry is malformed.
func modelsDevProviderFromEntry(key string, raw json.RawMessage) (ModelsDevProvider, bool) {
	var prov modelsDevProviderEntry
	if err := json.Unmarshal(raw, &prov); err != nil {
		return ModelsDevProvider{}, false
	}
	var ids []string
	for id, m := range prov.Models {
		if m.ToolCall != nil && *m.ToolCall {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ModelsDevProvider{}, false
	}
	name := prov.Name
	if name == "" {
		name = key
	}
	identity := provider.Provider(key)
	baseURL := prov.API
	api := synthesizeMapping(key, prov.modelsDevProviderInfo).API
	if mapping, ok := modelsDevProviderMappings[key]; ok {
		identity = mapping.Provider
		baseURL = mapping.BaseURL
		api = mapping.API
	}
	sort.Strings(ids)
	return ModelsDevProvider{
		Key:      key,
		Identity: identity,
		Name:     name,
		BaseURL:  baseURL,
		API:      api,
		ModelIDs: ids,
	}, true
}

// ---------------------------------------------------------------------------
// Cache load / refresh
// ---------------------------------------------------------------------------

func cacheFile(cacheDir string) string {
	return filepath.Join(cacheDir, "models.dev.json")
}

func loadCachedCatalog(cacheDir string) {
	runtime.mu.RLock()
	loaded := runtime.cat != nil
	runtime.mu.RUnlock()
	if loaded {
		return
	}
	raw, err := os.ReadFile(cacheFile(cacheDir))
	if err != nil {
		return
	}
	cat, err := parseModelsDev(raw)
	if err != nil {
		return // corrupt cache: ignore, embedded registry still serves
	}
	runtime.mu.Lock()
	if runtime.cat == nil {
		runtime.cat = cat
	}
	runtime.mu.Unlock()
}

func maybeRefresh(cacheDir string) {
	stale := true
	if st, err := os.Stat(cacheFile(cacheDir)); err == nil {
		stale = time.Since(st.ModTime()) > cacheTTL
	}
	if !stale {
		return
	}
	runtime.refresh.Do(func() {
		go func() {
			_, _ = RefreshModelsDevCatalog(cacheDir)
			// Allow future refreshes after TTL expiry.
			time.AfterFunc(cacheTTL, func() { runtime.refresh = sync.Once{} })
		}()
	})
}

func writeCatalogCache(cacheDir string, raw []byte) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(cacheFile(cacheDir), raw, 0o644)
}

// ---------------------------------------------------------------------------
// Parsing (shared by embedded api.json and runtime catalog)
// ---------------------------------------------------------------------------

// modelsDevProviderInfo captures the provider-level metadata that models.dev
// publishes alongside each provider entry. Used by the unmapped-provider
// fallback to synthesize a mapping from scratch.
type modelsDevProviderInfo struct {
	API  string `json:"api"`  // base URL (e.g. "https://api.tensorx.ai/v1")
	NPM  string `json:"npm"`  // AI-SDK package hint for wire protocol
	Name string `json:"name"` // display name
}

// modelsDevProviderEntry is a full models.dev provider entry: the provider
// metadata plus the nested models map. Embedding modelsDevProviderInfo lets
// the fallback decode both in one pass.
type modelsDevProviderEntry struct {
	modelsDevProviderInfo
	Models map[string]modelsDevModel `json:"models"`
}

// npmToAPI maps models.dev npm package identifiers to Goa API types. Most
// models.dev providers are OpenAI-compatible; the few with native protocols
// are listed explicitly. The zero value ("") defaults to OpenAI completions.
var npmToAPI = map[string]provider.Api{
	"@ai-sdk/anthropic":     provider.ApiAnthropicMessages,
	"@ai-sdk/google":        provider.ApiGoogleGenerativeAI,
	"@ai-sdk/google-vertex": provider.ApiGoogleVertex,
	"@ai-sdk/mistral":       provider.ApiMistralConversations,
}

// parseModelsDev builds the runtime catalog from the models.dev api.json.
//
// Two passes ensure ALL providers are covered:
//   - Pass 1 (mapped): providers with a hand-curated ProviderDef carrying a
//     ModelsDevKey use that mapping (detailed compat, correct API override,
//     provider-specific identity like zai-api vs zai).
//   - Pass 2 (fallback): every remaining models.dev key that has no mapping
//     gets a synthesized mapping built from the provider-level metadata (api
//     URL, npm protocol). This makes providers like tensorx — which exist on
//     models.dev but have no ProviderDef — visible in the runtime catalog
//     under a provider identity matching the models.dev key (and matching
//     DeriveProviderID for endpoint-based provider config).
func parseModelsDev(raw []byte) (*runtimeCatalog, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("models.dev decode: %w", err)
	}
	cat := &runtimeCatalog{
		loaded: true,
		models: map[string]provider.Model{},
		byProv: map[provider.Provider][]provider.Model{},
	}
	// Pass 1: mapped providers (hand-curated, detailed compat).
	for key, mapping := range modelsDevProviderMappings {
		rawProv, ok := top[key]
		if !ok {
			continue
		}
		var prov struct {
			Models map[string]modelsDevModel `json:"models"`
		}
		if err := json.Unmarshal(rawProv, &prov); err != nil {
			continue
		}
		addProviderModels(cat, mapping, prov.Models)
	}
	// Pass 2: unmapped providers (fallback — synthesize mapping from metadata).
	for key, rawProv := range top {
		if _, mapped := modelsDevProviderMappings[key]; mapped {
			continue
		}
		var prov modelsDevProviderEntry
		if err := json.Unmarshal(rawProv, &prov); err != nil {
			continue
		}
		if len(prov.Models) == 0 {
			continue
		}
		mapping := synthesizeMapping(key, prov.modelsDevProviderInfo)
		addProviderModels(cat, mapping, prov.Models)
	}
	return cat, nil
}

// addProviderModels converts and appends all models from a models.dev provider
// entry into the runtime catalog. Shared by both the mapped and fallback
// passes. First-wins for the global ID index; per-provider slice is append-only.
func addProviderModels(cat *runtimeCatalog, mapping modelsDevProviderMapping, models map[string]modelsDevModel) {
	for id, mm := range models {
		m, ok := convertModelsDevModel(id, mm, mapping)
		if !ok {
			continue
		}
		cat.byProv[mapping.Provider] = append(cat.byProv[mapping.Provider], m)
		if _, exists := cat.models[id]; !exists {
			cat.models[id] = m
		}
	}
}

// synthesizeMapping builds a modelsDevProviderMapping for an unmapped provider
// using the models.dev provider-level metadata. The provider identity is the
// models.dev key itself (e.g. "tensorx"), which matches DeriveProviderID for
// the provider's endpoint URL — so ListRegistryModels finds the models when a
// user configures that provider.
func synthesizeMapping(key string, info modelsDevProviderInfo) modelsDevProviderMapping {
	api := provider.ApiOpenAICompletions
	if a, ok := npmToAPI[info.NPM]; ok {
		api = a
	}
	return modelsDevProviderMapping{
		Provider: provider.Provider(key),
		API:      api,
		BaseURL:  info.API,
	}
}

func convertModelsDevModel(id string, mm modelsDevModel, mapping modelsDevProviderMapping) (provider.Model, bool) {
	if mm.ToolCall == nil || !*mm.ToolCall {
		return provider.Model{}, false // tool use is required for agentic work
	}
	m := provider.Model{
		ID:       id,
		Name:     mm.Name,
		Api:      mapping.API,
		Provider: mapping.Provider,
		BaseURL:  mapping.BaseURL,
	}
	if m.Name == "" {
		m.Name = id
	}
	if mm.Reasoning != nil {
		m.Reasoning = *mm.Reasoning
	}
	if mm.Limit.Context > 0 {
		m.ContextWindow = mm.Limit.Context
	}
	if mm.Limit.Output > 0 {
		m.MaxTokens = mm.Limit.Output
	}
	// models.dev costs are USD per million tokens → per-token rates.
	m.Cost = provider.ModelPricing{
		Input:      perMillionToPerToken(mm.Cost.Input),
		Output:     perMillionToPerToken(mm.Cost.Output),
		CacheRead:  perMillionToPerToken(mm.Cost.CacheRead),
		CacheWrite: perMillionToPerToken(mm.Cost.CacheWrite),
	}
	m.InputTypes = []string{"text"}
	for _, t := range mm.Modalities.Input {
		if t == "image" {
			m.InputTypes = []string{"text", "image"}
		}
	}
	return m, true
}

func perMillionToPerToken(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p / 1e6
}

// fetchURL performs a GET with a timeout and returns the body.
func fetchURL(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	const maxCatalogBytes = 32 << 20 // 32 MiB guard
	return io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
}
