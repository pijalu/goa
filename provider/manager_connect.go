// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"strings"
	"time"

	"github.com/pijalu/goa/config"
)

func isCodexFamilyModel(id string) bool {
	if strings.Contains(id, "codex") {
		return true
	}
	// rest is the version+suffix after "gpt-5." — e.g. "4" for gpt-5.4,
	// "4-mini" for gpt-5.4-mini. Single-digit minor versions make the lexical
	// ">= 4" cut exact.
	rest, ok := strings.CutPrefix(id, "gpt-5.")
	if !ok {
		return false
	}
	version, suffix, hasSuffix := strings.Cut(rest, "-")
	if version < "4" {
		return false
	}
	if !hasSuffix {
		return true // bare gpt-5.N with N >= 4
	}
	// Only the Pi-listed codex-served suffixes qualify.
	switch suffix {
	case "mini", "luna", "sol", "terra":
		return true
	}
	return false
}

// TestConnection tests connectivity to a provider by listing models.
func (pm *ProviderManager) TestConnection(providerID string) (latency time.Duration, modelCount int, err error) {
	start := time.Now()
	models, err := pm.ListModels(providerID)
	latency = time.Since(start)
	if err != nil {
		return latency, 0, err
	}
	return latency, len(models), nil
}

// ResolveModelName resolves the actual model name to send to the API.
// This can take either a model config ID (from models[]) or a raw model name.
// Resolution order:
//  1. If input matches a ModelConfig.ID, return its Model field
//  2. Otherwise return the input verbatim (already a raw model name)
//  3. If input is empty, fall back to the provider's DefaultModel
//  4. If still empty, fall back to the first ModelConfig for the provider
func (pm *ProviderManager) ResolveModelName(cfg config.ProviderConfig, modelID string) string {
	cur := pm.cfg.Load()
	// 1. Look up by model config ID
	if modelID != "" {
		if mc := cur.GetModelByID(modelID); mc != nil && mc.Model != "" {
			return mc.Model
		}
		// Not a model config ID — return verbatim (raw model name)
		return modelID
	}

	// 2. Fall back to first ModelConfig for this provider
	for i := range cur.Models {
		if cur.Models[i].ProviderID == cfg.ID && cur.Models[i].Model != "" {
			return cur.Models[i].Model
		}
	}

	return ""
}

// resolveModelName is a convenience wrapper using ActiveModel from config.
func (pm *ProviderManager) resolveModelName(cfg config.ProviderConfig) string {
	return pm.ResolveModelName(cfg, pm.cfg.Load().ActiveModel)
}

// ResolveActiveModel resolves the active model through the agentic model registry.
// Returns the Model with populated capabilities (thinking levels, context window,
// pricing, compat flags) so callers can use provider.Stream() directly.
//
// Falls back to a minimal Model if the active model isn't in the built-in registry
// (e.g., custom/local models). In that case, Provider and Api are inferred from
// the active provider config's endpoint and preset mapping. Context window is taken
// from the model config if set, otherwise the built-in registry default is used.
// The real loaded context length for local providers (LM Studio, llama.cpp) is
// refreshed after the model has loaded and the first tokens have been received;
// see ProviderManager.RefreshLocalContextWindow.
