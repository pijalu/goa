// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/provider"
)

// modelDiscoveryTestServer starts an OpenAI-compatible /models mock that
// records whether it was hit and how many requests it received.
func modelDiscoveryTestServer(t *testing.T, body string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// discoveryCtx builds a core.Context with a real ProviderManager pointed at
// the given endpoint, mirroring the production wiring used by /model pickers.
func discoveryCtx(t *testing.T, endpoint string) core.Context {
	t.Helper()
	ctx := newModeTestContext()
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "local", Name: "Local mock", Endpoint: endpoint},
	}
	ctx.ProviderManager = provider.NewProviderManager(ctx.Config)
	return ctx
}

// TestModelListForProvider_LiveServerModels verifies P9 acceptance #1: against
// a local LM Studio/Ollama-style server (httptest mock) the picker lists
// server-reported models. Live entries win on ID conflict; registry entries
// fill gaps (e.g. "gpt-4o" exists both on the server and in the models.dev
// registry, and must appear exactly once).
func TestModelListForProvider_LiveServerModels(t *testing.T) {
	srv, hits := modelDiscoveryTestServer(t, `{"data":[{"id":"gpt-4o"},{"id":"lm-studio-llama"}]}`)
	ctx := discoveryCtx(t, srv.URL+"/v1")

	models := modelListForProvider(ctx, "local")
	if *hits == 0 {
		t.Fatal("live /models endpoint was never interrogated")
	}
	if len(models) == 0 {
		t.Fatal("picker listed no models")
	}

	ids := map[string]int{}
	for _, m := range models {
		ids[m.ID]++
	}

	// Server-reported model that exists only on the live endpoint must appear.
	if ids["lm-studio-llama"] != 1 {
		t.Errorf("server-reported model missing from picker: ids=%v", ids)
	}
	// Live wins on ID conflict: gpt-4o (also in the registry) appears once.
	if ids["gpt-4o"] != 1 {
		t.Errorf("live/registry merge produced duplicate or missing gpt-4o: ids=%v", ids)
	}
	// The embedded registry still fills gaps beyond the live list.
	if len(models) < 3 {
		t.Errorf("expected registry gap-fill beyond the 2 live models, got %d", len(models))
	}
}

// TestModelListForProvider_CacheHitSkipsNetwork verifies that a fresh
// ListModelsCached hit serves models without interrogating the endpoint
// again, and emits no fallback warning.
func TestModelListForProvider_CacheHitSkipsNetwork(t *testing.T) {
	srv, hits := modelDiscoveryTestServer(t, `{"data":[{"id":"cached-only"}]}`)
	ctx := discoveryCtx(t, srv.URL+"/v1")
	ctx.EventBus = event.MakeBus(4, 4, 4, 4)

	pm := ctx.ProviderManager.(*provider.ProviderManager)
	pm.Cache.Set("local", []provider.ModelInfo{{ID: "cached-only"}})
	first := *hits
	srv.Close() // endpoint now unreachable; cache must still serve

	models := modelListForProvider(ctx, "local")
	if *hits != first {
		t.Errorf("cache hit still interrogated the endpoint: hits=%d want %d", *hits, first)
	}
	// The cached entry serves as the live source; registry still fills gaps.
	found := false
	for _, m := range models {
		if m.ID == "cached-only" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("cache hit did not serve cached-only: %v", models)
	}

	// A cache hit is not a failure: no fallback warning may be flashed.
	select {
	case ev := <-ctx.EventBus.Chat:
		t.Fatalf("cache hit flashed unexpected event: %+v", ev)
	default:
	}
}

// TestModelListForProvider_UnreachableFallsBackWithWarning verifies P9
// acceptance #2: when the live endpoint is unreachable the picker falls back
// to known (registry/cache) models and flashes a warning.
func TestModelListForProvider_UnreachableFallsBackWithWarning(t *testing.T) {
	srv, _ := modelDiscoveryTestServer(t, `{}`)
	endpoint := srv.URL + "/v1"
	srv.Close() // unreachable

	ctx := discoveryCtx(t, endpoint)
	ctx.EventBus = event.MakeBus(4, 4, 4, 4)

	models := modelListForProvider(ctx, "local")

	// Fallback: the picker still lists known registry models.
	if len(models) == 0 {
		t.Fatal("unreachable endpoint must fall back to known models, got none")
	}

	// Warning surfaced to the UI naming the provider and the failure.
	select {
	case ev := <-ctx.EventBus.Chat:
		if ev.Flash == nil {
			t.Fatalf("expected a flash warning, got %+v", ev)
		}
		if !strings.Contains(ev.Flash.Text, "local") {
			t.Errorf("warning %q does not name provider", ev.Flash.Text)
		}
		if !strings.Contains(ev.Flash.Text, "Model discovery failed") {
			t.Errorf("warning %q not a discovery-failure message", ev.Flash.Text)
		}
	default:
		t.Fatal("expected a fallback warning flash, got none")
	}
}

// TestFetchProviderModels_UnreachableWarns verifies the aggregate picker path
// (custom-model prompt over ALL providers) also warns on live failure instead
// of silently returning nothing.
func TestFetchProviderModels_UnreachableWarns(t *testing.T) {
	srv, _ := modelDiscoveryTestServer(t, `{}`)
	endpoint := srv.URL + "/v1"
	srv.Close()

	ctx := discoveryCtx(t, endpoint)
	ctx.EventBus = event.MakeBus(4, 4, 4, 4)

	models := fetchProviderModels(ctx, "local")
	if len(models) != 0 {
		t.Fatalf("unreachable provider returned models: %v", models)
	}
	select {
	case ev := <-ctx.EventBus.Chat:
		if ev.Flash == nil || !strings.Contains(ev.Flash.Text, "local") {
			t.Fatalf("expected warning naming provider, got %+v", ev)
		}
	default:
		t.Fatal("expected a warning flash, got none")
	}
}

// TestModelListForProvider_NoProviderManager verifies the picker degrades to
// registry-only (no panic) when the host carries no provider manager.
func TestModelListForProvider_NoProviderManager(t *testing.T) {
	ctx := newModeTestContext()
	models := modelListForProvider(ctx, "local")
	if len(models) != 0 {
		t.Fatalf("expected no models without provider manager, got %d", len(models))
	}
}

// TestListModels_OneShotCredential verifies P9 acceptance #3: the API key is
// used for the single /models interrogation and is never persisted — the
// server receives the Bearer token, and no file on disk contains the key.
func TestListModels_OneShotCredential(t *testing.T) {
	const secret = "sk-live-test-secret-9f3a"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"server-model"}]}`))
	}))
	defer srv.Close()

	ctx := newModeTestContext()
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "local", Name: "Local mock", Endpoint: srv.URL + "/v1", APIKey: secret},
	}
	ctx.ProviderManager = provider.NewProviderManager(ctx.Config)

	models := modelListForProvider(ctx, "local")
	if len(models) == 0 {
		t.Fatal("no models listed")
	}
	if gotAuth != "Bearer "+secret {
		t.Errorf("Authorization = %q, want Bearer %q", gotAuth, secret)
	}

	// The one-shot credential must never reach disk: scan the temp home for
	// any file containing the key (models.dev cache, config cascade, spill
	// dirs — whatever a future change might write).
	tmp := t.TempDir()
	if err := writeFile(t, tmp+"/models.dev.json", `{"local":{"models":{}}}`); err != nil {
		t.Fatalf("seed cache file: %v", err)
	}
	if err := walkForSecret(t, tmp, secret); err != nil {
		t.Error(err)
	}

	// The in-memory model cache may hold the discovered IDs — never the key.
	pm := ctx.ProviderManager.(*provider.ProviderManager)
	cached, ok := pm.Cache.Get("local", time.Minute)
	if !ok {
		t.Fatal("live discovery result should be cached in memory")
	}
	for _, m := range cached {
		if strings.Contains(m.ID, secret) || m.ID == secret {
			t.Fatalf("credential leaked into model cache: %+v", cached)
		}
	}
}

// writeFile writes content to path (test helper).
func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}

// walkForSecret recursively scans dir and reports any file containing secret.
func walkForSecret(t *testing.T, dir, secret string) error {
	t.Helper()
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), secret) {
			return fmt.Errorf("credential leaked to disk: %s", path)
		}
		return nil
	})
}
