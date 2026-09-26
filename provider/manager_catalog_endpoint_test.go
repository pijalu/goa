// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
)

// TestResolveActiveModel_VercelGatewayEndpoint replays the export that opened
// bugs.md "Vercel AI Gateway: no way to add an API key":
//
//	active_provider: vercel, active_model: stealth/pixel-canary,
//	provider entry {id: vercel, endpoint: ""}
//
// The resolved model must carry the gateway's chat-completions URL and the
// model id verbatim — before the fix the empty endpoint left BaseURL empty and
// the runtime posted the request to api.openai.com, which answered 400
// "invalid model ID" for a model that is valid ON the gateway.
func TestResolveActiveModel_VercelGatewayEndpoint(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "vercel",
		ActiveModel:    "m",
		Providers: []config.ProviderConfig{
			{ID: "vercel", Name: "Vercel AI Gateway", Provider: "vercel", Endpoint: ""},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "vercel", Model: "stealth/pixel-canary"},
		},
	}
	pm := NewProviderManager(cfg)

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.BaseURL != "https://ai-gateway.vercel.sh/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want the Vercel AI Gateway chat-completions URL", mdl.BaseURL)
	}
	if mdl.ID != "stealth/pixel-canary" {
		t.Errorf("model id = %q, want it passed through unchanged", mdl.ID)
	}
	if mdl.Provider != "vercel" {
		t.Errorf("provider = %q, want vercel", mdl.Provider)
	}
}

// TestResolveActiveModel_ConfiguredEndpointStillWins guards the override
// contract: an explicitly configured endpoint (e.g. a self-hosted gateway or a
// test server) must beat the catalog default.
func TestResolveActiveModel_ConfiguredEndpointStillWins(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "vercel",
		ActiveModel:    "m",
		Providers: []config.ProviderConfig{
			{ID: "vercel", Name: "Vercel AI Gateway", Provider: "vercel", Endpoint: "http://127.0.0.1:8080/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "vercel", Model: "stealth/pixel-canary"},
		},
	}
	pm := NewProviderManager(cfg)

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.BaseURL != "http://127.0.0.1:8080/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want the configured endpoint", mdl.BaseURL)
	}
}
