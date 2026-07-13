// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// IsLocalProvider reports whether the active provider points at a local LLM
// server that may expose context window metadata.
func (pm *ProviderManager) IsLocalProvider() bool {
	pCfg, _ := pm.Active()
	if pCfg == nil {
		return false
	}
	return isLocalProvider(pCfg.Endpoint)
}

// isLocalProvider returns true if the endpoint points to a local LLM server
// (LM Studio, llama.cpp, Ollama) that may expose context window metadata.
func isLocalProvider(endpoint string) bool {
	e := strings.ToLower(endpoint)
	return strings.Contains(e, "localhost:1234") || strings.Contains(e, "127.0.0.1:1234") ||
		strings.Contains(e, "localhost:11434") || strings.Contains(e, "127.0.0.1:11434") ||
		strings.Contains(e, "localhost") || strings.Contains(e, "127.0.0.1")
}

// detectFromLMStudioModels queries LM Studio's /api/v0/models endpoint for the
// loaded context length of the active model, falling back to context_length
// and then max_context_length.
func detectFromLMStudioModels(client *http.Client, baseURL *url.URL, modelName, apiKey string) int {
	modelsURL := baseURL.ResolveReference(&url.URL{Path: "/api/v0/models"})
	req, err := http.NewRequest("GET", modelsURL.String(), nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, _ := io.ReadAll(resp.Body)
	type modelEntry struct {
		ID                  string `json:"id"`
		MaxContextLength    int    `json:"max_context_length"`
		LoadedContextLength int    `json:"loaded_context_length"`
		ContextLength       int    `json:"context_length"`
	}
	var result struct {
		Data []modelEntry `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil {
		return 0
	}
	for _, m := range result.Data {
		if m.ID == modelName {
			return firstPositive(m.LoadedContextLength, m.ContextLength, m.MaxContextLength)
		}
	}
	return 0
}

// firstPositive returns the first positive value from the provided arguments.
func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// detectLocalContextWindow queries a local LLM provider to discover the context
// window size. Uses the provider identity to pick the right strategy:
//  1. LM Studio /api/v0/models — reads loaded_context_length, context_length, max_context_length
//  2. llama.cpp /props endpoint — reads default_generation_settings.n_ctx
//  3. llama.cpp /v1/models endpoint — reads meta.n_ctx (then meta.n_ctx_train)
//
// Returns 0 if detection fails or the provider doesn't expose this info.
func detectLocalContextWindow(pCfg config.ProviderConfig, modelName, apiKey string) int {
	endpoint := pCfg.Endpoint
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return 0
	}
	baseURL.Path = stripAPIPath(baseURL.Path)

	client := &http.Client{Timeout: 5 * time.Second}

	// LM Studio exposes /api/v0/models with loaded_context_length.
	if prov, _ := inferProviderIdentity(pCfg); prov == provider.ProviderLMStudio {
		if nCtx := detectFromLMStudioModels(client, baseURL, modelName, apiKey); nCtx > 0 {
			return nCtx
		}
	}

	// llama.cpp exposes the loaded context via /props and /v1/models.
	if nCtx := detectFromProps(client, baseURL, apiKey); nCtx > 0 {
		return nCtx
	}
	if nCtx := detectFromModelMeta(client, baseURL, modelName, apiKey); nCtx > 0 {
		return nCtx
	}

	return 0
}

// stripAPIPath removes /v1/chat/completions or /v1 suffix from a URL path.
func stripAPIPath(path string) string {
	path = strings.TrimSuffix(path, "/v1/chat/completions")
	path = strings.TrimSuffix(path, "/v1")
	return strings.TrimSuffix(path, "/")
}

// detectFromProps queries the /props endpoint (llama.cpp) for n_ctx.
func detectFromProps(client *http.Client, baseURL *url.URL, apiKey string) int {
	propsURL := baseURL.ResolveReference(&url.URL{Path: "/props"})
	req, err := http.NewRequest("GET", propsURL.String(), nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if json.Unmarshal(body, &result) != nil || result.DefaultGenerationSettings.NCtx <= 0 {
		return 0
	}
	return result.DefaultGenerationSettings.NCtx
}

// detectFromModelMeta queries the /v1/models endpoint for meta.n_ctx_train.
func detectFromModelMeta(client *http.Client, baseURL *url.URL, modelName, apiKey string) int {
	modelsURL := baseURL.ResolveReference(&url.URL{Path: "/v1/models"})
	req, err := http.NewRequest("GET", modelsURL.String(), nil)
	if err != nil {
		return 0
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	body, _ := io.ReadAll(resp.Body)
	type modelEntry struct {
		ID   string `json:"id"`
		Meta struct {
			NCtx      int `json:"n_ctx"`
			NCtxTrain int `json:"n_ctx_train"`
		} `json:"meta"`
	}
	var result struct {
		Data []modelEntry `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil {
		return 0
	}
	for _, m := range result.Data {
		if m.ID == modelName {
			if m.Meta.NCtx > 0 {
				return m.Meta.NCtx
			}
			if m.Meta.NCtxTrain > 0 {
				return m.Meta.NCtxTrain
			}
		}
	}
	return 0
}
