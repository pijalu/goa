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
//
// Quick-start persistence (bugs.md B42): the wizard writes ONLY what it
// asked — provider, key, endpoint, model, thinking level — plus the fixed
// quick-start posture (coding-posture major, yolo autonomy). Skills, tools,
// webfetch, memory/dream, compression and companion settings are left to the
// embedded defaults / existing configuration; re-running the wizard must not
// rewrite them.

func (w *wizardComponent) saveConfig() {
	w.applyProviderConfig()

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

	// Apply model advanced options. Thinking level defaults to high (the
	// quick-start posture); "off" means the user explicitly disabled it.
	w.config.Models[0].Reasoning = &w.main.modelReasoning
	if w.main.modelThinkingLevel != "" && w.main.modelThinkingLevel != "off" {
		w.config.Models[0].ThinkingLevel = w.main.modelThinkingLevel
	}

	// Fixed quick-start posture: coding-posture persona, yolo autonomy. Both
	// the canonical mode state and the legacy execution-mode fallback carry
	// the same choice so every surface agrees.
	w.config.SetActiveMajor(wizardDefaultMajor)
	w.config.Mode.Default.Autonomy = wizardDefaultAutonomy
	w.config.Execution.Mode = internal.ExecutionYolo
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

func (w *wizardComponent) ensureProjectDirs() {
	projectGoa := filepath.Join(w.projectDir, ".goa")
	for _, dir := range []string{
		projectGoa,
		filepath.Join(projectGoa, "memory"),
		filepath.Join(projectGoa, "skills"),
		filepath.Join(projectGoa, "prompts"),
		filepath.Join(projectGoa, "workflows"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return
		}
	}
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
