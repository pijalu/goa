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
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// modelsdev.go — runtime models.dev catalog with on-disk cache.
//
// The built-in registry (modelDefs + models_generated.go) is the always-
// available floor. On top of it, Goa keeps a runtime catalog fetched from
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
// models.dev ids → Goa models via providerMappings (shared with the
// cmd/genmodels build-time generator semantics: per-million-token costs are
// converted to per-token rates).

const (
	// ModelsDevURL is the canonical catalog endpoint (same as genmodels).
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

// modelsDevProviderMappings mirrors cmd/genmodels supportedProviders. The
// two are kept in sync manually: adding a provider there means adding it
// here (same identities), so the runtime catalog matches the build-time
// embedded catalog.
var modelsDevProviderMappings = map[string]modelsDevProviderMapping{
	"openai":          {Provider: provider.ProviderOpenAI, API: provider.ApiOpenAIResponses, BaseURL: "https://api.openai.com/v1"},
	"anthropic":       {Provider: provider.ProviderAnthropic, API: provider.ApiAnthropicMessages, BaseURL: "https://api.anthropic.com"},
	"google":          {Provider: provider.ProviderGoogle, API: provider.ApiGoogleGenerativeAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
	"deepseek":        {Provider: provider.ProviderDeepSeek, API: provider.ApiOpenAICompletions, BaseURL: "https://api.deepseek.com"},
	"groq":            {Provider: provider.ProviderGroq, API: provider.ApiOpenAICompletions, BaseURL: "https://api.groq.com/openai/v1"},
	"mistral":         {Provider: provider.ProviderMistral, API: provider.ApiMistralConversations, BaseURL: "https://api.mistral.ai"},
	"xai":             {Provider: "xai", API: provider.ApiOpenAICompletions, BaseURL: "https://api.x.ai/v1"},
	"zai":             {Provider: provider.ProviderZaiApi, API: provider.ApiOpenAICompletions, BaseURL: "https://api.z.ai/api/paas/v4"},
	"zai-coding-plan": {Provider: provider.ProviderZai, API: provider.ApiOpenAICompletions, BaseURL: "https://api.z.ai/api/coding/paas/v4"},
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

type modelsDevFile struct {
	Providers map[string]map[string]modelsDevModel
}

// runtimeCatalog holds the models.dev-derived registrations.
type runtimeCatalog struct {
	loaded    bool
	models    map[string]provider.Model              // ID → model (first-wins per provider applied at load)
	byProv    map[provider.Provider][]provider.Model // provider → models
	fetchedAt time.Time
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
// Parsing (shared semantics with cmd/genmodels)
// ---------------------------------------------------------------------------

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
		for id, mm := range prov.Models {
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
	return cat, nil
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
