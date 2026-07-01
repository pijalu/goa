// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package config provides configuration loading, merging, and provider registry
// support for Goa. The Provider Custom Registry allows loading provider and
// model definitions from remote JSON URLs, enabling team-shared configurations.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RegistrySource defines a remote provider registry to fetch at startup.
type RegistrySource struct {
	URL         string `yaml:"url"`
	BearerToken string `yaml:"bearer_token,omitempty"`
}

// RegistryPayload is the JSON structure returned by a registry endpoint.
type RegistryPayload struct {
	Providers []ProviderConfig `json:"providers,omitempty"`
	Models    []ModelConfig    `json:"models,omitempty"`
}

// RegistryLoader fetches provider and model definitions from remote registries.
type RegistryLoader struct {
	sources []RegistrySource
	client  *http.Client
}

// NewRegistryLoader creates a loader for the given registry sources.
func NewRegistryLoader(sources []RegistrySource) *RegistryLoader {
	return &RegistryLoader{
		sources: sources,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Load fetches all registry sources and merges their provider/model definitions.
// Returns accumulated providers and models from all sources.
func (rl *RegistryLoader) Load() ([]ProviderConfig, []ModelConfig, error) {
	if len(rl.sources) == 0 {
		return nil, nil, nil
	}

	var allProviders []ProviderConfig
	var allModels []ModelConfig

	for _, src := range rl.sources {
		providers, models, err := rl.fetchOne(src)
		if err != nil {
			return nil, nil, fmt.Errorf("registry %q: %w", src.URL, err)
		}
		allProviders = append(allProviders, providers...)
		allModels = append(allModels, models...)
	}

	return allProviders, allModels, nil
}

// fetchOne fetches and decodes a single registry source.
func (rl *RegistryLoader) fetchOne(src RegistrySource) ([]ProviderConfig, []ModelConfig, error) {
	req, err := http.NewRequest("GET", src.URL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}

	if src.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+src.BearerToken)
	}

	resp, err := rl.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var payload RegistryPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("decode: %w", err)
	}

	return payload.Providers, payload.Models, nil
}
