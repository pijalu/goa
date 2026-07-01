// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryLoader_Load_FetchesAndReturnsProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegistryPayload{
			Providers: []ProviderConfig{
				{ID: "team-openai", Endpoint: "https://proxy.example.com/v1", APIKey: "sk-test"},
			},
			Models: []ModelConfig{
				{ID: "gpt-4o", Provider: "team-openai", Model: "gpt-4o", ContextWindow: 128000},
			},
		})
	}))
	defer server.Close()

	rl := NewRegistryLoader([]RegistrySource{{URL: server.URL}})
	providers, models, err := rl.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].ID != "team-openai" {
		t.Errorf("provider ID = %q, want %q", providers[0].ID, "team-openai")
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Errorf("model ID = %q, want %q", models[0].ID, "gpt-4o")
	}
}

func TestRegistryLoader_AuthHeader(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(RegistryPayload{})
	}))
	defer server.Close()

	rl := NewRegistryLoader([]RegistrySource{{URL: server.URL, BearerToken: "test-token"}})
	_, _, err := rl.Load()
	if err != nil {
		t.Fatal(err)
	}
	if authHeader != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want %q", authHeader, "Bearer test-token")
	}
}

func TestRegistryLoader_HTTPError_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	rl := NewRegistryLoader([]RegistrySource{{URL: server.URL}})
	_, _, err := rl.Load()
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestRegistryLoader_EmptySources_ReturnsEmpty(t *testing.T) {
	rl := NewRegistryLoader(nil)
	p, m, err := rl.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 0 || len(m) != 0 {
		t.Errorf("expected empty, got %d providers, %d models", len(p), len(m))
	}
}

func TestRegistryLoader_MultipleSources(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegistryPayload{
			Providers: []ProviderConfig{{ID: "provider-a"}},
		})
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(RegistryPayload{
			Models: []ModelConfig{{ID: "model-b"}},
		})
	}))
	defer server2.Close()

	rl := NewRegistryLoader([]RegistrySource{
		{URL: server1.URL},
		{URL: server2.URL},
	})
	providers, models, err := rl.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].ID != "provider-a" {
		t.Errorf("expected provider-a, got %+v", providers)
	}
	if len(models) != 1 || models[0].ID != "model-b" {
		t.Errorf("expected model-b, got %+v", models)
	}
}

func TestRegistryLoader_InvalidJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	rl := NewRegistryLoader([]RegistrySource{{URL: server.URL}})
	_, _, err := rl.Load()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRegistryLoader_ConnectionRefused_ReturnsError(t *testing.T) {
	rl := NewRegistryLoader([]RegistrySource{{URL: "http://localhost:1/nonexistent"}})
	_, _, err := rl.Load()
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}
