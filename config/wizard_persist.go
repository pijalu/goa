// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal"
)

// -- Persistence --------------------------------------------------

func (w *wizardComponent) saveConfig() {
	w.applyProviderConfig()
	w.config.SetActiveMajor("coder")
	w.applyExecutionMode()

	// Save companion model and provider
	if w.companionModelSelected {
		// Same provider/model as main
		w.config.MultiAgent.CompanionModel = w.main.modelID
		w.config.MultiAgent.CompanionProvider = w.main.providerID
	} else if w.companion.modelName != "" {
		// Companion has separate provider/model
		w.config.MultiAgent.CompanionModel = w.companion.modelID
		if w.companion.providerID != "" {
			w.config.MultiAgent.CompanionProvider = w.companion.providerID
			// Add companion provider if different from main
			if w.companion.providerID != w.main.providerID && w.companion.providerID != "" {
				w.config.Providers = append(w.config.Providers, ProviderConfig{
					ID:       w.companion.providerID,
					Name:     w.companion.providerName,
					Endpoint: w.companion.endpoint,
					APIKey:   w.companion.apiKey,
				})
			}
			w.config.Models = append(w.config.Models, ModelConfig{
				ID:            w.companion.modelID,
				Name:          w.companion.modelName,
				ProviderID:    w.companion.providerID,
				Model:         w.companion.modelName,
				Temperature:   parseTemp(w.companion.modelTemp),
				MaxTokens:     parseIntDefault(w.companion.modelMaxTokens, 0),
				Reasoning:     w.companion.modelReasoning,
				ThinkingLevel: w.companion.modelThinkingLevel,
			})
		}
	}
	if w.loader == nil {
		return
	}
	if err := w.loader.Save(w.config); err != nil {
		w.saveErr = err
		return
	}
	w.saved = true
	w.ensureProjectDirs()
}

func (w *wizardComponent) applyProviderConfig() {
	if w.main.providerID == "" {
		return
	}
	model := w.resolveDefaultModel()
	w.config.Providers = []ProviderConfig{
		{
			ID:        w.main.providerID,
			Name:      w.main.providerName,
			Endpoint:  w.main.endpoint,
			APIKey:    w.main.apiKey,
			Preferred: true,
		},
	}
	w.config.ActiveProvider = w.main.providerID
	w.config.ActiveModel = w.main.modelID
	w.config.Models = []ModelConfig{
		{
			ID:          w.main.modelID,
			Name:        w.main.modelName,
			ProviderID:  w.main.providerID,
			Model:       w.main.modelName,
			Temperature: parseTemp(w.main.modelTemp),
			MaxTokens:   parseIntDefault(w.main.modelMaxTokens, 0),
		},
	}
	_ = model
	if w.companionModelSelected {
		w.config.MultiAgent.CompanionModel = w.main.modelID
	}

	// Apply model advanced options.
	w.config.Models[0].Reasoning = w.main.modelReasoning
	if w.main.modelThinkingLevel != "" && w.main.modelThinkingLevel != "off" {
		w.config.Models[0].ThinkingLevel = w.main.modelThinkingLevel
	}

	// Apply skill mode.
	if w.skillMode == 1 {
		w.config.Skills.ExecutionMode = AgenticSkillModeInline
	} else {
		w.config.Skills.ExecutionMode = AgenticSkillModeSubAgent
	}

	// Apply webfetch summarization preference.
	w.config.Tools.WebFetch.Summary.Enabled = w.webfetchSummaryEnabled == 1

	// Apply dream mode preferences.
	w.config.Memory.Dream.Enabled = w.dreamEnabled == 1
	w.config.Memory.Dream.Auto = w.dreamAuto == 1
	w.config.Memory.Dream.ApplyAfterReview = w.dreamApplyAfterReview == 1
	// Default dream model falls back to the active main model.
	w.config.Memory.Dream.Model = w.main.modelID
	w.config.Memory.Dream.Provider = w.main.providerID
}

func parseTemp(s string) float64 {
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}

func parseIntDefault(s string, def int) int {
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return def
	}
	return v
}

func (w *wizardComponent) resolveDefaultModel() string {
	if w.main.selectedPresetIndex >= 0 {
		presets := PresetProviders()
		if w.main.selectedPresetIndex < len(presets) && presets[w.main.selectedPresetIndex].DefaultModel != "" {
			return presets[w.main.selectedPresetIndex].DefaultModel
		}
	}
	return "gpt-4o"
}

func (w *wizardComponent) applyExecutionMode() {
	modeValues := []internal.ExecutionMode{
		internal.ExecutionSolo,
		internal.ExecutionYolo,
		internal.ExecutionConfirm,
		internal.ExecutionReview,
	}
	if w.selectedMode >= 0 && w.selectedMode < len(modeValues) {
		w.config.Execution.Mode = modeValues[w.selectedMode]
	}
}

func (w *wizardComponent) ensureProjectDirs() {
	projectGoa := filepath.Join(w.projectDir, ".goa")
	os.MkdirAll(projectGoa, 0755)
	os.MkdirAll(filepath.Join(projectGoa, "memory"), 0755)
	os.MkdirAll(filepath.Join(projectGoa, "skills"), 0755)
	os.MkdirAll(filepath.Join(projectGoa, "prompts"), 0755)
	os.MkdirAll(filepath.Join(projectGoa, "workflows"), 0755)
	if w.copyPrompts {
		w.copyEmbeddedPrompts(filepath.Join(projectGoa, "prompts"))
	}
	if w.copyWorkflows {
		w.copyEmbeddedWorkflows(filepath.Join(projectGoa, "workflows"))
	}
}

func (w *wizardComponent) copyEmbeddedPrompts(destDir string) {
	_ = destDir
}

func (w *wizardComponent) copyEmbeddedWorkflows(destDir string) {
	_ = destDir
}

// -- Utilities ----------------------------------------------------

func maskKey(key string) string {
	if key == "" {
		return "(none)"
	}
	if len(key) < 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

func DeriveProviderID(endpoint string) string {
	s := endpoint
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	if len(parts) >= 2 && parts[len(parts)-2] != "localhost" {
		return parts[len(parts)-2]
	}
	return parts[0]
}

func deriveProviderName(endpoint string) string {
	id := DeriveProviderID(endpoint)
	if id == "" || id == "localhost" {
		return "Custom Provider"
	}
	return strings.ToUpper(id[:1]) + id[1:]
}
