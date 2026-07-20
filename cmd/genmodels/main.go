// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// genmodels fetches model metadata from models.dev and generates
// models_generated.go. If the remote fetch fails, the previously
// generated file serves as cache. A subsequent build re-runs this
// script if and only if go:generate is explicitly invoked.
//
// Usage from package root:
//
//	go generate ./internal/agentic/provider/models/
//
// Or to regenerate:
//
//	go run ./cmd/genmodels/main.go
package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ModelsDevModel mirrors the models.dev JSON structure.
type ModelsDevModel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ToolCall    *bool    `json:"tool_call,omitempty"`
	Reasoning   *bool    `json:"reasoning,omitempty"`
	Context     *int     `json:"context,omitempty"`
	OutputLimit *int     `json:"output_limit,omitempty"`
	InputCost   *float64 `json:"input_cost,omitempty"`
	OutputCost  *float64 `json:"output_cost,omitempty"`
	CacheRead   *float64 `json:"cache_read,omitempty"`
	CacheWrite  *float64 `json:"cache_write,omitempty"`
	InputTypes  []string `json:"input_types,omitempty"`
}

// ProviderMapping maps models.dev provider keys to Goa provider/API pairs.
type ProviderMapping struct {
	Key      string // models.dev provider key
	Provider string // Goa provider identifier
	API      string // Goa API identifier
	BaseURL  string // Default base URL for this provider
}

var supportedProviders = []ProviderMapping{
	{Key: "openai", Provider: "openai", API: "openai-responses", BaseURL: "https://api.openai.com/v1"},
	{Key: "anthropic", Provider: "anthropic", API: "anthropic-messages", BaseURL: "https://api.anthropic.com"},
	{Key: "google", Provider: "google", API: "google-generative-ai", BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
	{Key: "deepseek", Provider: "deepseek", API: "openai-completions", BaseURL: "https://api.deepseek.com"},
	{Key: "groq", Provider: "groq", API: "openai-completions", BaseURL: "https://api.groq.com/openai/v1"},
	{Key: "mistral", Provider: "mistral", API: "mistral-conversations", BaseURL: "https://api.mistral.ai"},
	{Key: "xai", Provider: "xai", API: "openai-completions", BaseURL: "https://api.x.ai/v1"},
	{Key: "zai", Provider: "zai-api", API: "openai-completions", BaseURL: "https://api.z.ai/api/paas/v4"},
	{Key: "zai-coding-plan", Provider: "zai", API: "openai-completions", BaseURL: "https://api.z.ai/api/coding/paas/v4"},
}

func main() {
	outPath := "models_generated.go"

	data, err := fetchModelsDev()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: models.dev fetch failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "Using existing generated file (if any) as cache.\n")
		// If generated file already exists, keep it. Otherwise write a stub.
		if _, err := os.Stat(outPath); err == nil {
			return
		}
		data = &modelsDevData{Providers: map[string]map[string]ModelsDevModel{}}
	}

	if err := generateFile(outPath, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing generated file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated %s with %d provider(s)\n", outPath, len(data.Providers))
	for k, v := range data.Providers {
		fmt.Printf("  %s: %d models\n", k, len(v))
	}
}

type modelsDevData struct {
	Providers map[string]map[string]ModelsDevModel `json:"providers,omitempty"`
}

func fetchModelsDev() (*modelsDevData, error) {
	raw, err := fetchRemoteJSON()
	if err != nil {
		return nil, err
	}

	result := &modelsDevData{Providers: make(map[string]map[string]ModelsDevModel)}
	for _, pm := range supportedProviders {
		models := parseProviderModels(raw, pm.Key)
		if len(models) > 0 {
			result.Providers[pm.Key] = models
		}
	}
	return result, nil
}

func fetchRemoteJSON() (map[string]any, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://models.dev/api.json")
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return raw, nil
}

func parseProviderModels(raw map[string]any, key string) map[string]ModelsDevModel {
	providerData, ok := raw[key].(map[string]any)
	if !ok {
		return nil
	}
	modelsRaw, ok := providerData["models"].(map[string]any)
	if !ok {
		return nil
	}

	models := make(map[string]ModelsDevModel)
	for id, val := range modelsRaw {
		model, ok := parseModel(id, val)
		if !ok {
			continue
		}
		models[id] = model
	}
	return models
}

func parseModel(id string, val any) (ModelsDevModel, bool) {
	m, ok := val.(map[string]any)
	if !ok {
		return ModelsDevModel{}, false
	}
	tc, _ := m["tool_call"].(bool)
	if !tc {
		return ModelsDevModel{}, false
	}

	model := ModelsDevModel{ID: id}
	model.Name = stringField(m, "name")
	model.Reasoning = boolPtrField(m, "reasoning")
	fillModelLimits(m, &model)
	fillModelCosts(m, &model)
	fillModelInputTypes(m, &model)
	return model, true
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolPtrField(m map[string]any, key string) *bool {
	if v, ok := m[key].(bool); ok {
		return &v
	}
	return nil
}

func fillModelLimits(m map[string]any, model *ModelsDevModel) {
	limits, ok := m["limit"].(map[string]any)
	if !ok {
		return
	}
	model.Context = intPtrField(limits, "context")
	model.OutputLimit = intPtrField(limits, "output")
}

func intPtrField(m map[string]any, key string) *int {
	if v, ok := m[key].(float64); ok {
		n := int(v)
		return &n
	}
	return nil
}

func fillModelCosts(m map[string]any, model *ModelsDevModel) {
	cost, ok := m["cost"].(map[string]any)
	if !ok {
		return
	}
	model.InputCost = floatPtrField(cost, "input")
	model.OutputCost = floatPtrField(cost, "output")
	model.CacheRead = floatPtrField(cost, "cache_read")
	model.CacheWrite = floatPtrField(cost, "cache_write")
}

func floatPtrField(m map[string]any, key string) *float64 {
	if v, ok := m[key].(float64); ok {
		return &v
	}
	return nil
}

func fillModelInputTypes(m map[string]any, model *ModelsDevModel) {
	modalities, ok := m["modalities"].(map[string]any)
	if !ok {
		return
	}
	input, ok := modalities["input"].([]any)
	if !ok {
		return
	}
	for _, v := range input {
		if s, ok := v.(string); ok {
			model.InputTypes = append(model.InputTypes, s)
		}
	}
}

func generateFile(path string, data *modelsDevData) error {
	var buf strings.Builder
	writeGeneratedSource(&buf, data)

	// Emit gofmt-clean output so regeneration never dirties the tree.
	formatted, err := format.Source([]byte(buf.String()))
	if err != nil {
		return fmt.Errorf("gofmt generated source: %w", err)
	}
	return os.WriteFile(path, formatted, 0o644)
}

func writeGeneratedSource(f *strings.Builder, data *modelsDevData) {
	fmt.Fprint(f, `// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Code generated by go generate; DO NOT EDIT.
// Source: models.dev/api.json
package models

import "github.com/pijalu/goa/internal/agentic/provider"

func init() {
	loadGeneratedModels()
}

func loadGeneratedModels() {
`)

	providerKeys := sortedProviderKeys(data.Providers)
	for _, pKey := range providerKeys {
		models := data.Providers[pKey]
		pm := findProviderMapping(pKey)
		if pm == nil {
			continue
		}
		ids := sortedModelIDs(models)
		for _, id := range ids {
			writeModelRegistration(f, id, models[id], *pm)
		}
	}

	fmt.Fprintf(f, "}\n")
}

func sortedProviderKeys(providers map[string]map[string]ModelsDevModel) []string {
	keys := make([]string, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedModelIDs(models map[string]ModelsDevModel) []string {
	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func writeModelRegistration(f *strings.Builder, id string, m ModelsDevModel, pm ProviderMapping) int {
	ctxWindow := intOrDefault(m.Context, 4096)
	maxTokens := intOrDefault(m.OutputLimit, 4096)
	// models.dev reports costs in USD per million tokens; Goa's ModelPricing
	// is per-token (see provider.costForUsage: cost = tokens × rate).
	inputCost := perToken(m.InputCost)
	outputCost := perToken(m.OutputCost)
	cacheRead := perToken(m.CacheRead)
	cacheWrite := perToken(m.CacheWrite)
	reasoning := boolLiteral(m.Reasoning)
	inputTypes := inputTypesLiteral(m.InputTypes)
	name := escapeModelName(modelDisplayName(id, m.Name))

	fmt.Fprintf(f, "\taddModel(provider.Model{\n")
	fmt.Fprintf(f, "\t\tID: %q, Name: %q, Api: provider.Api(%q), Provider: provider.Provider(%q),\n",
		id, name, pm.API, pm.Provider)
	fmt.Fprintf(f, "\t\tBaseURL: %q,\n", pm.BaseURL)
	fmt.Fprintf(f, "\t\tReasoning: %s, ContextWindow: %d, MaxTokens: %d, InputTypes: %s,\n",
		reasoning, ctxWindow, maxTokens, inputTypes)
	fmt.Fprintf(f, "\t\tCost: provider.ModelPricing{Input: %.10f, Output: %.10f, CacheRead: %.10f, CacheWrite: %.10f},\n",
		inputCost, outputCost, cacheRead, cacheWrite)
	fmt.Fprintf(f, "\t\tThinkingFormat: provider.ThinkingFormatNone,\n")
	fmt.Fprintf(f, "\t})\n")
	return 1
}

func intOrDefault(p *int, d int) int {
	if p != nil && *p > 0 {
		return *p
	}
	return d
}

func floatOrDefault(p *float64) float64 {
	if p != nil {
		return *p
	}
	return 0.0
}

// perToken converts a models.dev per-million-token price to Goa's per-token
// rate. Nil stays zero (unknown/free).
func perToken(p *float64) float64 {
	if p == nil {
		return 0.0
	}
	return *p / 1e6
}

func boolLiteral(p *bool) string {
	if p != nil && *p {
		return "true"
	}
	return "false"
}

func inputTypesLiteral(types []string) string {
	if containsImage(types) {
		return `[]string{"text", "image"}`
	}
	return `[]string{"text"}`
}

func modelDisplayName(id, name string) string {
	if name == "" {
		return id
	}
	return name
}

func escapeModelName(name string) string {
	name = strings.ReplaceAll(name, `\`, `\\`)
	name = strings.ReplaceAll(name, `"`, `\"`)
	return name
}

func findProviderMapping(key string) *ProviderMapping {
	for i := range supportedProviders {
		if supportedProviders[i].Key == key {
			return &supportedProviders[i]
		}
	}
	return nil
}

func containsImage(types []string) bool {
	for _, t := range types {
		if t == "image" {
			return true
		}
	}
	return false
}
