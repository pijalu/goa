// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// Muse Spark models on the OpenCode gateways are served through the OpenAI
// Responses API ({base}/responses, @ai-sdk/openai — bugs.md 2026-08-30).
// Resolution must honor the registry model's API (catalog override) on the
// main resolve path and build the /responses URL, not /chat/completions.
func TestResolveActiveModel_MuseSparkResponsesEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		provID   string
		endpoint string
		model    string
	}{
		{"zen-muse-spark", "opencode", "https://opencode.ai/zen/v1", "muse-spark-1.2"},
		{"zen-muse-free", "opencode", "https://opencode.ai/zen/v1", "muse-spark-1.2-contributor-free"},
		{"go-muse-contributor", "opencode-go", "https://opencode.ai/zen/go/v1", "muse-spark-1.2-contributor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				ActiveProvider: tc.provID,
				Providers: []config.ProviderConfig{
					{ID: tc.provID, Endpoint: tc.endpoint},
				},
				Models: []config.ModelConfig{
					{ID: "m", ProviderID: tc.provID, Model: tc.model},
				},
			}
			pm := NewProviderManager(cfg)
			mdl, err := pm.ResolveActiveModel()
			if err != nil {
				t.Fatalf("ResolveActiveModel: %v", err)
			}
			if mdl.Api != agenticprovider.ApiOpenAIResponses {
				t.Errorf("Api = %q, want openai-responses", mdl.Api)
			}
			want := tc.endpoint + "/responses"
			if mdl.BaseURL != want {
				t.Errorf("BaseURL = %q, want %q", mdl.BaseURL, want)
			}
			if mdl.ID != tc.model {
				t.Errorf("ID = %q, want %q", mdl.ID, tc.model)
			}
		})
	}
}

// Sibling opencode models keep the chat-completions surface byte-for-byte.
func TestResolveActiveModel_OpencodeCompletionsUnchanged(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "opencode",
		Providers: []config.ProviderConfig{
			{ID: "opencode", Endpoint: "https://opencode.ai/zen/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "opencode", Model: "deepseek-v4-flash"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.Api != agenticprovider.ApiOpenAICompletions {
		t.Errorf("Api = %q, want openai-completions", mdl.Api)
	}
	if mdl.BaseURL != "https://opencode.ai/zen/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want chat completions URL", mdl.BaseURL)
	}
}

// An explicit model-level api override still wins over the catalog —
// including forcing the Responses API onto a completions-surface model and
// forcing completions back onto a responses-surface model.
func TestResolveActiveModel_ExplicitModelAPIWins(t *testing.T) {
	forceResponses := &config.Config{
		ActiveProvider: "opencode",
		ActiveModel:    "m",
		Providers: []config.ProviderConfig{
			{ID: "opencode", Endpoint: "https://opencode.ai/zen/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "opencode", Model: "deepseek-v4-flash", API: "openai-responses"},
		},
	}
	pm := NewProviderManager(forceResponses)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.Api != agenticprovider.ApiOpenAIResponses {
		t.Errorf("Api = %q, want openai-responses (explicit model override)", mdl.Api)
	}
	if mdl.BaseURL != "https://opencode.ai/zen/v1/responses" {
		t.Errorf("BaseURL = %q, want responses URL", mdl.BaseURL)
	}

	forceCompletions := &config.Config{
		ActiveProvider: "opencode",
		ActiveModel:    "m",
		Providers: []config.ProviderConfig{
			{ID: "opencode", Endpoint: "https://opencode.ai/zen/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "opencode", Model: "muse-spark-1.2", API: "openai-completions"},
		},
	}
	pm = NewProviderManager(forceCompletions)
	mdl, err = pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.Api != agenticprovider.ApiOpenAICompletions {
		t.Errorf("Api = %q, want openai-completions (explicit model override)", mdl.Api)
	}
	if mdl.BaseURL != "https://opencode.ai/zen/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want chat completions URL", mdl.BaseURL)
	}
}

// Plain OpenAI (api key, api.openai.com): responses-API models must POST to
// {base}/responses — the responses protocol body sent to /chat/completions is
// rejected by the server (latent defect surfaced by the Muse Spark report).
func TestResolveActiveModel_OpenAIResponsesURL(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "openai", Model: "gpt-5"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.Api != agenticprovider.ApiOpenAIResponses {
		t.Fatalf("Api = %q, want openai-responses", mdl.Api)
	}
	if mdl.BaseURL != "https://api.openai.com/v1/responses" {
		t.Errorf("BaseURL = %q, want https://api.openai.com/v1/responses", mdl.BaseURL)
	}
}

// Catalog completions pins (gpt-4o, gpt-5.5) keep the chat-completions URL
// under the registry-API-honoring merge.
func TestResolveActiveModel_CompletionsPinsUnchanged(t *testing.T) {
	for _, model := range []string{"gpt-4o", "gpt-5.5"} {
		cfg := &config.Config{
			ActiveProvider: "openai",
			Providers: []config.ProviderConfig{
				{ID: "openai", Endpoint: "https://api.openai.com/v1"},
			},
			Models: []config.ModelConfig{
				{ID: "m", ProviderID: "openai", Model: model},
			},
		}
		pm := NewProviderManager(cfg)
		mdl, err := pm.ResolveActiveModel()
		if err != nil {
			t.Fatalf("ResolveActiveModel(%s): %v", model, err)
		}
		if mdl.Api != agenticprovider.ApiOpenAICompletions {
			t.Errorf("%s: Api = %q, want openai-completions", model, mdl.Api)
		}
		if mdl.BaseURL != "https://api.openai.com/v1/chat/completions" {
			t.Errorf("%s: BaseURL = %q, want chat completions URL", model, mdl.BaseURL)
		}
	}
}

// The per-role resolve path (sub-agents) applies the same provider-match
// rule: Muse Spark keeps its responses pin on its home provider; a catalog
// entry borrowed from a different provider (Google's gemma resolved on LM
// Studio) keeps the serving provider's wire API.
func TestResolveModelForProvider_PerModelAPIPin(t *testing.T) {
	t.Run("muse-spark-keeps-pin-on-opencode", func(t *testing.T) {
		cfg := &config.Config{
			ActiveProvider: "opencode",
			Providers: []config.ProviderConfig{
				{ID: "opencode", Endpoint: "https://opencode.ai/zen/v1"},
			},
			Models: []config.ModelConfig{
				{ID: "muse", ProviderID: "opencode", Model: "muse-spark-1.2"},
			},
		}
		pm := NewProviderManager(cfg)
		mdl, err := pm.ResolveModelForProvider("opencode", "muse")
		if err != nil {
			t.Fatalf("ResolveModelForProvider: %v", err)
		}
		if mdl.Api != agenticprovider.ApiOpenAIResponses {
			t.Errorf("Api = %q, want openai-responses", mdl.Api)
		}
		if mdl.BaseURL != "https://opencode.ai/zen/v1/responses" {
			t.Errorf("BaseURL = %q, want responses URL", mdl.BaseURL)
		}
	})

	t.Run("borrowed-gemma-entry-keeps-local-completions", func(t *testing.T) {
		cfg := &config.Config{
			ActiveProvider: "lmstudio",
			Providers: []config.ProviderConfig{
				{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
			},
			Models: []config.ModelConfig{
				{ID: "gemma", ProviderID: "lmstudio", Model: "gemma-4-e4b"},
			},
		}
		pm := NewProviderManager(cfg)
		mdl, err := pm.ResolveModelForProvider("lmstudio", "gemma")
		if err != nil {
			t.Fatalf("ResolveModelForProvider: %v", err)
		}
		if mdl.Api != agenticprovider.ApiOpenAICompletions {
			t.Errorf("Api = %q, want openai-completions on the local provider", mdl.Api)
		}
	})
}
