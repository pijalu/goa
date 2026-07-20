// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// modelsDevFixture is a minimal models.dev api.json covering the zai and
// zai-coding-plan providers (the mappings the runtime catalog must honor).
const modelsDevFixture = `{
  "zai": {
    "models": {
      "glm-5.2": {
        "name": "GLM-5.2",
        "tool_call": true,
        "reasoning": true,
        "limit": {"context": 1000000, "output": 131072},
        "cost": {"input": 1.4, "output": 4.4},
        "modalities": {"input": ["text"], "output": ["text"]}
      },
      "glm-4.5-flash": {
        "name": "GLM-4.5-Flash",
        "tool_call": false,
        "limit": {"context": 131072, "output": 98304}
      }
    }
  },
  "zai-coding-plan": {
    "models": {
      "glm-5.2": {
        "name": "GLM-5.2",
        "tool_call": true,
        "reasoning": true,
        "limit": {"context": 1000000, "output": 131072},
        "cost": {"input": 0, "output": 0}
      }
    }
  }
}`

func resetRuntimeCatalog(t *testing.T) {
	t.Helper()
	runtime.mu.Lock()
	runtime.cat = nil
	runtime.mu.Unlock()
}

// TestParseModelsDev_ZaiMappings verifies the runtime parser maps models.dev
// keys to Goa identities (zai → zai-api paid, zai-coding-plan → zai quota)
// and converts per-million-token costs to per-token rates.
func TestParseModelsDev_ZaiMappings(t *testing.T) {
	cat, err := parseModelsDev([]byte(modelsDevFixture))
	if err != nil {
		t.Fatalf("parseModelsDev: %v", err)
	}

	// zai-api paid entry with per-token cost conversion.
	apiModel := findInCatalog(cat, provider.ProviderZaiApi, "glm-5.2")
	if apiModel == nil {
		t.Fatal("zai-api glm-5.2 missing from catalog")
	}
	if apiModel.Cost.Input != 0.0000014 || apiModel.Cost.Output != 0.0000044 {
		t.Errorf("zai-api glm-5.2 cost = %+v, want per-token 1.4/4.4", apiModel.Cost)
	}
	if !apiModel.Reasoning {
		t.Error("zai-api glm-5.2 Reasoning = false, want true")
	}
	if apiModel.BaseURL != "https://api.z.ai/api/paas/v4" {
		t.Errorf("zai-api BaseURL = %q", apiModel.BaseURL)
	}

	// zai coding-plan entry (zero cost).
	zaiModel := findInCatalog(cat, provider.ProviderZai, "glm-5.2")
	if zaiModel == nil {
		t.Fatal("zai glm-5.2 missing from catalog")
	}
	if zaiModel.Cost.Input != 0 || zaiModel.Cost.Output != 0 {
		t.Errorf("zai glm-5.2 cost = %+v, want zero (quota)", zaiModel.Cost)
	}
	if zaiModel.BaseURL != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("zai BaseURL = %q", zaiModel.BaseURL)
	}

	// tool_call=false models are excluded.
	if findInCatalog(cat, provider.ProviderZaiApi, "glm-4.5-flash") != nil {
		t.Error("glm-4.5-flash (tool_call=false) must be excluded")
	}
}

func findInCatalog(cat *runtimeCatalog, p provider.Provider, id string) *provider.Model {
	for _, m := range cat.byProv[p] {
		if m.ID == id {
			cp := m
			return &cp
		}
	}
	return nil
}

// TestRuntimeCatalog_CacheRoundTripAndProviderLookup verifies a cache file
// is loaded and served per-provider, with provider-exact precedence.
func TestRuntimeCatalog_CacheRoundTripAndProviderLookup(t *testing.T) {
	resetRuntimeCatalog(t)
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.dev.json"), []byte(modelsDevFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stub the fetcher: offline. The cache must serve regardless.
	old := runtimeFetch
	runtimeFetch = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	defer func() { runtimeFetch = old }()

	loadCachedCatalog(dir)

	m := GetRuntimeModel(provider.ProviderZaiApi, "glm-5.2")
	if m == nil {
		t.Fatal("GetRuntimeModel(zai-api, glm-5.2) = nil after cache load")
	}
	if m.Cost.Input != 0.0000014 {
		t.Errorf("cost input = %v, want 0.0000014", m.Cost.Input)
	}

	z := GetRuntimeModel(provider.ProviderZai, "glm-5.2")
	if z == nil || z.Provider != provider.ProviderZai {
		t.Fatalf("GetRuntimeModel(zai, glm-5.2) = %+v, want zai entry", z)
	}

	if got := len(GetRuntimeModels(provider.ProviderZaiApi)); got != 1 {
		t.Errorf("GetRuntimeModels(zai-api) = %d, want 1 (tool_call=false excluded)", got)
	}
}

// TestRuntimeCatalog_OfflineKeepsEmbeddedFloor verifies that with no cache
// and a failing fetch, lookups simply return nil (callers fall back to the
// embedded registry) — the runtime layer never breaks model resolution.
func TestRuntimeCatalog_OfflineKeepsEmbeddedFloor(t *testing.T) {
	resetRuntimeCatalog(t)
	dir := t.TempDir() // empty: no cache file

	old := runtimeFetch
	runtimeFetch = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	defer func() { runtimeFetch = old }()

	loadCachedCatalog(dir)
	if m := GetRuntimeModel(provider.ProviderZai, "glm-5.2"); m != nil {
		t.Errorf("offline with no cache: GetRuntimeModel = %+v, want nil (embedded floor serves)", m)
	}

	// Forced refresh must fail without poisoning the (empty) catalog.
	if _, err := RefreshModelsDevCatalog(dir); err == nil {
		t.Error("RefreshModelsDevCatalog offline: expected error, got nil")
	}
	if m := GetRuntimeModel(provider.ProviderZai, "glm-5.2"); m != nil {
		t.Error("failed refresh must not replace the catalog")
	}
}

// TestRuntimeCatalog_RefreshPopulatesFromFetcher verifies a successful
// refresh swaps in the fetched catalog and writes the cache file.
func TestRuntimeCatalog_RefreshPopulatesFromFetcher(t *testing.T) {
	resetRuntimeCatalog(t)
	dir := t.TempDir()

	old := runtimeFetch
	runtimeFetch = func(string) ([]byte, error) { return []byte(modelsDevFixture), nil }
	defer func() { runtimeFetch = old }()

	n, err := RefreshModelsDevCatalog(dir)
	if err != nil {
		t.Fatalf("RefreshModelsDevCatalog: %v", err)
	}
	if n != 2 {
		t.Errorf("refreshed providers = %d, want 2 (zai + zai-coding-plan)", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "models.dev.json")); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
	if m := GetRuntimeModel(provider.ProviderZai, "glm-5.2"); m == nil {
		t.Error("catalog not populated after refresh")
	}
}
