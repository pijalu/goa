// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawFixture mirrors the models.dev document shape with the verbose fields
// the slim transform must drop (doc, description, release_date, ...) and the
// fields it must keep (api/npm/env, cost/limit/modalities, tool_call).
const rawFixture = `{
  "zetaprovider": {
    "id": "zetaprovider",
    "name": "Zeta Provider",
    "api": "https://api.zeta.example/v1",
    "npm": "@ai-sdk/openai-compatible",
    "doc": "https://zeta.example/docs",
    "env": ["ZETA_API_KEY"],
    "models": {
      "zeta-1": {
        "id": "zeta-1",
        "name": "Zeta One",
        "tool_call": true,
        "reasoning": false,
        "release_date": "2026-01-01",
        "last_updated": "2026-08-01",
        "description": "verbose marketing copy",
        "family": "zeta",
        "attachment": true,
        "open_weights": false,
        "temperature": true,
        "cost": {"input": 1.5, "output": 3, "cache_read": 0.2, "cache_write": 0},
        "limit": {"context": 204800, "output": 131072},
        "modalities": {"input": ["text", "image"], "output": ["text"]}
      }
    }
  },
  "alphaprovider": {
    "id": "alphaprovider",
    "name": "Alpha Provider",
    "doc": "https://alpha.example/docs",
    "env": ["ALPHA_API_KEY"],
    "models": {
      "alpha-mini": {
        "id": "alpha-mini",
        "name": "Alpha Mini",
        "tool_call": false
      }
    }
  }
}`

func TestSlimCatalog(t *testing.T) {
	slim, err := slimCatalog([]byte(rawFixture))
	if err != nil {
		t.Fatalf("slimCatalog: %v", err)
	}

	var got map[string]map[string]any
	if err := json.Unmarshal(slim, &got); err != nil {
		t.Fatalf("re-decode slim output: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("providers = %d, want 2", len(got))
	}

	zeta, ok := got["zetaprovider"]
	if !ok {
		t.Fatal("zetaprovider missing from slim output")
	}

	// Provider-level: doc dropped, identity + synthesis fields kept.
	if _, has := zeta["doc"]; has {
		t.Error("provider field 'doc' not stripped")
	}
	for _, k := range []string{"id", "name", "api", "npm", "env", "models"} {
		if _, has := zeta[k]; !has {
			t.Errorf("provider field %q unexpectedly dropped", k)
		}
	}

	// Model-level: verbose metadata dropped, registry fields kept.
	models, _ := zeta["models"].(map[string]any)
	zeta1, _ := models["zeta-1"].(map[string]any)
	for _, k := range []string{"id", "release_date", "last_updated", "description", "family", "attachment", "open_weights", "temperature"} {
		if _, has := zeta1[k]; has {
			t.Errorf("model field %q not stripped", k)
		}
	}
	for _, k := range []string{"name", "tool_call", "reasoning", "cost", "limit", "modalities"} {
		if _, has := zeta1[k]; !has {
			t.Errorf("model field %q unexpectedly dropped", k)
		}
	}

	// Explicit zero cost must survive (pointer fields, not omitempty floats).
	cost, _ := zeta1["cost"].(map[string]any)
	cw, has := cost["cache_write"]
	if !has {
		t.Fatal("cache_write: 0 dropped from cost")
	}
	if cw.(float64) != 0 {
		t.Errorf("cache_write = %v, want 0", cw)
	}

	// Modalities: input kept, output dropped (registry only parses input).
	modalities, _ := zeta1["modalities"].(map[string]any)
	if _, has := modalities["input"]; !has {
		t.Error("modalities.input dropped")
	}
	if _, has := modalities["output"]; has {
		t.Error("modalities.output not stripped")
	}

	// Model without optional fields still decodes.
	alpha, _ := got["alphaprovider"]["models"].(map[string]any)
	mini, _ := alpha["alpha-mini"].(map[string]any)
	if mini["name"] != "Alpha Mini" {
		t.Errorf("alpha-mini name = %v", mini["name"])
	}
	if _, has := mini["cost"]; has {
		t.Error("absent cost synthesized for alpha-mini")
	}
}

func TestSlimCatalogDeterministic(t *testing.T) {
	// Re-encoding must be byte-identical across runs (sorted map keys) so
	// repeated `go generate` runs produce clean diffs.
	a, err := slimCatalog([]byte(rawFixture))
	if err != nil {
		t.Fatalf("first slim: %v", err)
	}
	b, err := slimCatalog([]byte(rawFixture))
	if err != nil {
		t.Fatalf("second slim: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("slimCatalog output not deterministic")
	}
	// Compact single-line output, matching the committed snapshot style.
	if strings.Contains(string(a), "\n") {
		t.Error("slim output contains newlines; want compact single-line JSON")
	}
	// Sorted keys: alphaprovider sorts before zetaprovider.
	if !strings.HasPrefix(string(a), `{"alphaprovider"`) {
		t.Errorf("slim output starts with %.30q, want alphaprovider first", string(a))
	}
}

func TestSlimCatalogRejectsGarbage(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":       `{broken`,
		"empty document": `{}`,
	} {
		if _, err := slimCatalog([]byte(raw)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestFetchCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := fetchCatalog(srv.URL)
	if err != nil {
		t.Fatalf("fetchCatalog: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

func TestFetchCatalogHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := fetchCatalog(srv.URL); err == nil {
		t.Fatal("expected error on HTTP 502, got nil")
	}
}

// TestRunFetchFailureKeepsExisting covers the lenient default: a failed fetch
// keeps the committed snapshot (builds never break on a network blip).
func TestRunFetchFailureKeepsExisting(t *testing.T) {
	out := filepath.Join(t.TempDir(), "api.json")
	if err := os.WriteFile(out, []byte(`{"existing":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := run(out, srv.URL, false, false); err != nil {
		t.Fatalf("lenient run: %v", err)
	}
	data, _ := os.ReadFile(out)
	if string(data) != `{"existing":true}` {
		t.Errorf("out = %q, want existing file untouched", data)
	}

	if err := run(out, srv.URL, false, true); err == nil {
		t.Fatal("strict run: expected error, got nil")
	}
}

func TestRunWritesSlimCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(rawFixture))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "sub", "api.json")
	if err := run(out, srv.URL, false, false); err != nil {
		t.Fatalf("run: %v", err)
	}

	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var providers map[string]any
	if err := json.Unmarshal(written, &providers); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if len(providers) != 2 {
		t.Errorf("providers = %d, want 2", len(providers))
	}
}

// TestRunCheck covers -check: exit nil when fresh, error when stale.
func TestRunCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(rawFixture))
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "api.json")

	if err := run(out, srv.URL, true, false); err == nil {
		t.Fatal("check on missing file: expected error, got nil")
	}

	if err := run(out, srv.URL, false, false); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := run(out, srv.URL, true, false); err != nil {
		t.Errorf("check on fresh file: %v", err)
	}

	if err := os.WriteFile(out, []byte(`{"stale":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(out, srv.URL, true, false); err == nil {
		t.Error("check on stale file: expected error, got nil")
	}
}
