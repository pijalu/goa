// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// addModel adds a model to the built-in registry and the prefix-lookup slice.
// Calling addModel multiple times with the same ID does NOT overwrite the
// existing entry — the first registration wins. This ensures that hardcoded
// models (with detailed ThinkingFormat, Compat, and ThinkingLevelMap settings
// that models.dev does not provide) take priority over generated entries.
// Generated entries from models.dev fill in gaps for models not in the
// hardcoded registry.
func addModel(m provider.Model) {
	if _, exists := builtinModels[m.ID]; exists {
		return // existing entry (hardcoded with more detail) wins
	}
	modelDefs = append(modelDefs, m)
	builtinModels[m.ID] = m
}

// GetModel looks up a model by ID in the built-in registry.
// Returns nil if the model is not found.
func GetModel(id string) *provider.Model {
	if m, ok := builtinModels[id]; ok {
		return &m
	}
	return nil
}

// GetModels returns all models for a given provider.
func GetModels(providerName provider.Provider) []provider.Model {
	var result []provider.Model
	for _, m := range builtinModels {
		if m.Provider == providerName {
			result = append(result, m)
		}
	}
	return result
}

// AllModels returns all built-in models.
func AllModels() []provider.Model {
	result := make([]provider.Model, 0, len(builtinModels))
	for _, m := range builtinModels {
		result = append(result, m)
	}
	return result
}

// LookupByPrefix finds the first model whose canonical ID starts with
// the given prefix. Returns nil if no match is found.
//
// This is used as a fallback when GetModel(id) returns nil — unknown/local
// model names often share prefixes with known families, so we can still infer
// capabilities like context window, vision, and thinking support.
//
// Prefixes are matched case-insensitively. The longest matching prefix wins,
// so "claude-sonnet-4-" takes priority over the shorter "claude-".
// The returned model's ID is set to the queried modelName so downstream
// code uses the correct model identifier.
func LookupByPrefix(modelName string) *provider.Model {
	if modelName == "" {
		return nil
	}
	lower := strings.ToLower(modelName)
	var best *provider.Model
	bestLen := 0

	for _, m := range modelDefs {
		prefix := strings.ToLower(m.ID)
		if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
			cp := m
			cp.ID = modelName
			best = &cp
			bestLen = len(prefix)
		}
	}

	return best
}

// builtinModels is the curated registry of models.
var builtinModels = map[string]provider.Model{}

func init() {
	for _, m := range modelDefs {
		builtinModels[m.ID] = m
	}
}
