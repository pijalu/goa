// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DetectContextWindow queries the LLM provider to discover the context window
// size. It returns the configured context length in tokens, or 0 if the
// provider does not expose this information.
//
// Detection order:
//  1. llama.cpp /props endpoint — reads default_generation_settings.n_ctx
//  2. llama.cpp /v1/models endpoint — reads meta.n_ctx_train
//  3. Other providers — returns 0
//
// Example:
//
//	maxTokens := agentic.DetectContextWindow("http://localhost:1234/v1/chat/completions", "my-model", "")
//	if maxTokens > 0 {
//	    cfg.ContextCompression.MaxTokens = maxTokens
//	}
func DetectContextWindow(endpoint, model, apiKey string) int {
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return 0
	}

	path := baseURL.Path
	path = strings.TrimSuffix(path, "/v1/chat/completions")
	path = strings.TrimSuffix(path, "/v1")
	path = strings.TrimSuffix(path, "/")
	baseURL.Path = path

	client := &http.Client{Timeout: 10 * time.Second}

	if nCtx := detectFromProps(client, baseURL, apiKey); nCtx > 0 {
		return nCtx
	}

	if nCtx := detectFromModels(client, baseURL, model, apiKey); nCtx > 0 {
		return nCtx
	}

	return 0
}

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}

	var result struct {
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0
	}

	return result.DefaultGenerationSettings.NCtx
}

func detectFromModels(client *http.Client, baseURL *url.URL, model, apiKey string) int {
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}

	entries := parseModelsResponse(body)
	return findModelContext(entries, model)
}

type modelContextEntry struct {
	ID        string
	Model     string
	NCtxTrain int
}

func parseModelsResponse(body []byte) []modelContextEntry {
	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Meta struct {
				NCtxTrain int `json:"n_ctx_train"`
			} `json:"meta"`
		} `json:"data"`
		Models []struct {
			Model string `json:"model"`
			Meta  struct {
				NCtxTrain int `json:"n_ctx_train"`
			} `json:"meta"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil
	}

	entries := make([]modelContextEntry, 0, len(result.Data)+len(result.Models))
	for _, m := range result.Data {
		entries = append(entries, modelContextEntry{ID: m.ID, NCtxTrain: m.Meta.NCtxTrain})
	}
	for _, m := range result.Models {
		entries = append(entries, modelContextEntry{Model: m.Model, NCtxTrain: m.Meta.NCtxTrain})
	}
	return entries
}

func findModelContext(entries []modelContextEntry, model string) int {
	for _, e := range entries {
		if (e.ID == model || e.Model == model) && e.NCtxTrain > 0 {
			return e.NCtxTrain
		}
	}
	return 0
}
