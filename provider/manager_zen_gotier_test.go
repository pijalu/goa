// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// models.dev rotates each OpenCode Zen tier's model list wholesale: on
// 2026-10-15 it dropped qwen3.6-plus, qwen3.7-max and grok-4.6 from
// opencode-go (adding qwen3.7-plus and grok-4.7) while other providers kept
// listing the dropped IDs. Resolution must still land those configured models on
// the zen Go gateway with their family's wire API — decided by the registry's
// provider-scoped pin, not by a catalog row borrowed from an aggregator, which
// would also carry that aggregator's endpoint.
func TestResolveActiveModel_ZenGoRotatedFamilyMembers(t *testing.T) {
	const endpoint = "https://opencode.ai/zen/go/v1"
	cases := []struct {
		model string
		api   agenticprovider.Api
		path  string
	}{
		{"qwen3.6-plus", agenticprovider.ApiAnthropicMessages, "/messages"},
		{"qwen3.7-max", agenticprovider.ApiAnthropicMessages, "/messages"},
		{"grok-4.6", agenticprovider.ApiOpenAIResponses, "/responses"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			cfg := &config.Config{
				ActiveProvider: "opencode-go",
				ActiveModel:    "m",
				Providers: []config.ProviderConfig{
					{ID: "opencode-go", Endpoint: endpoint},
				},
				Models: []config.ModelConfig{
					{ID: "m", ProviderID: "opencode-go", Model: tc.model},
				},
			}
			pm := NewProviderManager(cfg)
			mdl, err := pm.ResolveActiveModel()
			if err != nil {
				t.Fatalf("ResolveActiveModel: %v", err)
			}
			if mdl.Api != tc.api {
				t.Errorf("Api = %q, want %q", mdl.Api, tc.api)
			}
			if want := endpoint + tc.path; mdl.BaseURL != want {
				t.Errorf("BaseURL = %q, want %q", mdl.BaseURL, want)
			}
			if mdl.ApiSource != "curated" {
				t.Errorf("ApiSource = %q, want curated (the YAML family pin decides it)", mdl.ApiSource)
			}
		})
	}
}
