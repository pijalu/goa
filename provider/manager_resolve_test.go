// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
)

func TestDetectFromLMStudioModels_ContextLengthAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "qwen/qwen3.5-9b", "max_context_length": 262144, "context_length": 32768},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "qwen/qwen3.5-9b", "")
	if nCtx != 32768 {
		t.Errorf("detectFromLMStudioModels = %d, want 32768 (context_length alias)", nCtx)
	}
}

func TestDetectFromLMStudioModels_LoadedContextLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
				{"id": "other-model", "max_context_length": 4096},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "google/gemma-4-e4b", "")
	if nCtx != 8192 {
		t.Errorf("detectFromLMStudioModels = %d, want 8192", nCtx)
	}
}

func TestDetectFromLMStudioModels_FallsBackToMax(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "google/gemma-4-e4b", "max_context_length": 65536},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "google/gemma-4-e4b", "")
	if nCtx != 65536 {
		t.Errorf("detectFromLMStudioModels = %d, want 65536", nCtx)
	}
}

func TestDetectFromLMStudioModels_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "other-model", "max_context_length": 4096},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "missing-model", "")
	if nCtx != 0 {
		t.Errorf("detectFromLMStudioModels = %d, want 0", nCtx)
	}
}

func TestResolveActiveModel_NoEagerLocalContextDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: server.URL + "/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "google/gemma-4-e4b", ProviderID: "lmstudio", Model: "google/gemma-4-e4b"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	// ResolveActiveModel must not query the local provider before the model is
	// loaded, so it keeps the registry default rather than the loaded length.
	if mdl.ContextWindow != 131072 {
		t.Errorf("ContextWindow = %d, want 131072 (no eager detection before model is loaded)", mdl.ContextWindow)
	}
	// RefreshLocalContextWindow is the deferred path used after first tokens.
	if got := pm.RefreshLocalContextWindow(); got != 8192 {
		t.Errorf("RefreshLocalContextWindow = %d, want 8192", got)
	}
}

func TestDetectLocalContextWindow_LMStudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
			},
		})
	}))
	defer server.Close()

	pCfg := config.ProviderConfig{ID: "lmstudio", Endpoint: server.URL + "/v1"}
	nCtx := detectLocalContextWindow(pCfg, "google/gemma-4-e4b", "")
	if nCtx != 8192 {
		t.Errorf("detectLocalContextWindow = %d, want 8192", nCtx)
	}
}

func TestDetectLocalContextWindow_NonLMStudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	pCfg := config.ProviderConfig{ID: "ollama", Endpoint: server.URL + "/v1"}
	nCtx := detectLocalContextWindow(pCfg, "google/gemma-4-e4b", "")
	if nCtx != 0 {
		t.Errorf("detectLocalContextWindow for non-LM-Studio = %d, want 0", nCtx)
	}
}

func TestDetectFromModelMeta_LlamaCPP_LoadedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "my-model", "meta": map[string]any{"n_ctx": 8192, "n_ctx_train": 131072}},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromModelMeta(&http.Client{Timeout: 5 * time.Second}, baseURL, "my-model", "")
	if nCtx != 8192 {
		t.Errorf("detectFromModelMeta = %d, want 8192", nCtx)
	}
}

func TestDetectFromModelMeta_LlamaCPP_FallsBackToTrain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "my-model", "meta": map[string]any{"n_ctx_train": 131072}},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromModelMeta(&http.Client{Timeout: 5 * time.Second}, baseURL, "my-model", "")
	if nCtx != 131072 {
		t.Errorf("detectFromModelMeta = %d, want 131072", nCtx)
	}
}

// mustAuthStore builds an auth store in a temp dir, failing the test on error.
func mustAuthStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	return s
}

func TestBuildStreamOptions_UsesAuthStoreAPIKey(t *testing.T) {
	store := mustAuthStore(t)
	_ = store.SetAPIKey("openai", "stored-key")

	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	cfg.ActiveModel = "gpt-4o"
	pm := NewProviderManager(cfg)
	pm.SetAuthStore(store)
	opts := pm.BuildStreamOptions()
	if opts.APIKey != "stored-key" {
		t.Errorf("APIKey = %q, want stored-key", opts.APIKey)
	}
}

func TestBuildStreamOptions_UsesAuthStoreOAuthAccessToken(t *testing.T) {
	store := mustAuthStore(t)
	_ = store.SetOAuth("openai", &oauth.Tokens{AccessToken: "oauth-access-token", TokenType: "bearer"})

	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	cfg.ActiveModel = "gpt-4o"
	pm := NewProviderManager(cfg)
	pm.SetAuthStore(store)
	opts := pm.BuildStreamOptions()
	if opts.APIKey != "oauth-access-token" {
		t.Errorf("APIKey = %q, want oauth-access-token", opts.APIKey)
	}
}

// TestListModels_UsesAuthStoreAPIKey verifies ListModels resolves the API key
// from the auth store when the provider config has none (E1): OAuth/API-key

// TestActive_NilReceiver verifies Active is safe on a nil *ProviderManager
// (typed-nil interface), returning no provider instead of panicking.
func TestActive_NilReceiver(t *testing.T) {
	var pm *ProviderManager
	p, model := pm.Active()
	if p != nil || model != "" {
		t.Errorf("Active() = (%v, %q), want (nil, \"\")", p, model)
	}
}

// TestResolveAPIKey_AuthStoreFallback verifies ResolveAPIKey returns the key
// from the auth store when ProviderConfig.APIKey is empty (the /login case),
// so plugins see the provider as authenticated (z.ai #6).
func TestResolveAPIKey_AuthStoreFallback(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "zai", Provider: "zai"}},
	}
	pm := NewProviderManager(cfg)
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetAPIKey("zai", "test-zai-key"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	pm.SetAuthStore(store)

	if got := pm.ResolveAPIKey("zai"); got != "test-zai-key" {
		t.Errorf("ResolveAPIKey = %q, want %q (auth store fallback)", got, "test-zai-key")
	}
	if got := pm.ResolveAPIKey("unknown"); got != "" {
		t.Errorf("ResolveAPIKey(unknown) = %q, want empty", got)
	}
	var nilPM *ProviderManager
	if got := nilPM.ResolveAPIKey("zai"); got != "" {
		t.Errorf("nil ResolveAPIKey = %q, want empty", got)
	}
}

// TestResolveAPIKey_ConfigKeyWins verifies an explicit ProviderConfig.APIKey
// takes precedence over the auth store.
func TestResolveAPIKey_ConfigKeyWins(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "zai", Provider: "zai", APIKey: "config-key"}},
	}
	pm := NewProviderManager(cfg)
	store, _ := auth.NewStore("")
	_ = store.SetAPIKey("zai", "store-key")
	pm.SetAuthStore(store)
	if got := pm.ResolveAPIKey("zai"); got != "config-key" {
		t.Errorf("ResolveAPIKey = %q, want config key %q", got, "config-key")
	}
}

// TestSetConfig_HotReloadSwapsActiveProvider verifies SetConfig swaps the
// config the next request resolves: after a hot reload, Active() and
// BuildStreamOptions() reflect the new provider profile (P22/DS6).
func TestSetConfig_HotReloadSwapsActiveProvider(t *testing.T) {
	oldCfg := &config.Config{
		ActiveProvider: "old-provider",
		ActiveModel:    "old-model",
		Providers: []config.ProviderConfig{{
			ID:       "old-provider",
			Endpoint: "https://old.example/v1",
		}},
		Models: []config.ModelConfig{{ID: "old-model", ProviderID: "old-provider", Model: "old-model"}},
	}
	pm := NewProviderManager(oldCfg)

	oldProvider, oldModel := pm.Active()
	if oldProvider == nil || oldProvider.ID != "old-provider" || oldModel != "old-model" {
		t.Fatalf("boot Active() = (%v, %q), want (old-provider, old-model)", oldProvider, oldModel)
	}

	newCfg := &config.Config{
		ActiveProvider: "new-provider",
		ActiveModel:    "new-model",
		Providers: []config.ProviderConfig{{
			ID:       "new-provider",
			Endpoint: "https://new.example/v1",
		}},
		Models: []config.ModelConfig{{ID: "new-model", ProviderID: "new-provider", Model: "new-model"}},
	}
	pm.SetConfig(newCfg)

	// Next request sees the new profile.
	newProvider, newModel := pm.Active()
	if newProvider == nil || newProvider.ID != "new-provider" || newModel != "new-model" {
		t.Errorf("after SetConfig Active() = (%v, %q), want (new-provider, new-model)", newProvider, newModel)
	}
	if pm.Config() != newCfg {
		t.Errorf("Config() did not return the reloaded config")
	}

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.BaseURL != "https://new.example/v1/chat/completions" {
		t.Errorf("ResolveActiveModel BaseURL = %q, want new-provider endpoint", mdl.BaseURL)
	}
}

// TestSetConfig_NilConfigSafe verifies SetConfig(nil) and Config() are safe.
func TestSetConfig_NilConfigSafe(t *testing.T) {
	pm := NewProviderManager(&config.Config{})
	pm.SetConfig(nil)
	if pm.Config() != nil {
		t.Errorf("Config() after SetConfig(nil) = %v, want nil", pm.Config())
	}
	if p, m := pm.Active(); p != nil || m != "" {
		t.Errorf("Active() after SetConfig(nil) = (%v, %q), want (nil, \"\")", p, m)
	}
}

// TestListRegistryModels_OpenAICodexServesCodexFamily pins the codex registry
// alias: the openai-codex subscription provider has no models.dev mapping of
// its own and its endpoint serves no /models route, so ListRegistryModels
// must alias to the openai catalog filtered to the codex family (the picker
// showed ONLY "custom model" before the alias, behind a misleading "using
// known models" flash).
func TestListRegistryModels_OpenAICodexServesCodexFamily(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "codex", Name: "OpenAI Codex", Endpoint: "https://chatgpt.com/backend-api"},
			{ID: "openai", Name: "OpenAI", Endpoint: "https://api.openai.com/v1"},
		},
	}
	pm := NewProviderManager(cfg)

	codex := pm.ListRegistryModels("codex")
	if len(codex) == 0 {
		t.Fatal("ListRegistryModels(openai-codex endpoint) returned no models; the picker can only offer a custom row")
	}
	hasSpark := false
	for _, m := range codex {
		if m.ID == "gpt-5.3-codex-spark" {
			hasSpark = true
		}
		if !isCodexFamilyModel(m.ID) {
			t.Errorf("non-codex-family model %q leaked into the openai-codex list", m.ID)
		}
	}
	if !hasSpark {
		t.Errorf("codex list missing gpt-5.3-codex-spark (Pi codex catalog); got %v", codex)
	}

	// The plain openai provider list must be unaffected by the alias.
	openai := pm.ListRegistryModels("openai")
	if len(openai) == 0 {
		t.Fatal("ListRegistryModels(openai) returned no models")
	}
	hasGPT4o := false
	for _, m := range openai {
		if m.ID == "gpt-4o" {
			hasGPT4o = true
		}
	}
	if !hasGPT4o {
		t.Error("openai registry list lost gpt-4o — the codex alias must not narrow the openai list")
	}
}

// TestIsCodexFamilyModel pins the codex-served model filter: the codex
// subscription endpoint serves the explicit codex models plus the gpt-5.4+
// generations (Pi scripts/generate-models.ts codexModels).
func TestIsCodexFamilyModel(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gpt-5.3-codex-spark", true},
		{"gpt-5.3-codex", true},
		{"gpt-5.4", true},
		{"gpt-5.4-mini", true},
		{"gpt-5.5", true},
		{"gpt-5.6-luna", true},
		{"gpt-5.6-sol", true},
		{"gpt-5.6-terra", true},
		{"gpt-5", false},
		{"gpt-5.1", false},
		{"gpt-5.2", false},
		{"gpt-5.2-chat-latest", false},
		{"gpt-5.4-pro", false},
		{"gpt-5.4-nano", false},
		{"gpt-4o", false},
		{"gpt-4.1", false},
		{"o3", false},
		{"chatgpt-image-latest", false},
	}
	for _, tt := range tests {
		if got := isCodexFamilyModel(tt.id); got != tt.want {
			t.Errorf("isCodexFamilyModel(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
