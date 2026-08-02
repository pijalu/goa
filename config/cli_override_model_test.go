// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: an empty-but-non-nil CLI override map (exactly what
// ParseCLIFlags returns on a flag-less launch) plus an ambiguous active
// model used to fabricate a persistent, provider-less "cli-override" model.
// With no model-scalar flags the model list must be left untouched.
func TestApplyCLIOverrides_NoModelFlags_Ambiguous(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{{ID: "p1"}},
		Models: []ModelConfig{
			{ID: "m1", ProviderID: "p1", Model: "m1"},
			{ID: "m2", ProviderID: "p1", Model: "m2"},
		},
	}
	cl := NewCascadeLoader(t.TempDir(), "", map[string]string{})
	cl.applyCLIOverrides(cfg)

	if len(cfg.Models) != 2 {
		t.Fatalf("Models count = %d, want 2 (untouched): %+v", len(cfg.Models), cfg.Models)
	}
}

// Regression: non-model flags (e.g. --debug) must not trigger scratch-model
// creation either, even when no model resolves.
func TestApplyCLIOverrides_NonModelFlagOnly(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{{ID: "p1"}},
	}
	cl := NewCascadeLoader(t.TempDir(), "", map[string]string{"debug": "true"})
	cl.applyCLIOverrides(cfg)

	if len(cfg.Models) != 0 {
		t.Fatalf("Models = %+v, want none created", cfg.Models)
	}
}

// Ambiguous active model + model-scalar flag: overrides land on the first
// model in config order; no extra entry is created.
func TestApplyCLIOverrides_AmbiguousAppliesToFirst(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{{ID: "p1"}},
		Models: []ModelConfig{
			{ID: "m1", ProviderID: "p1", Model: "m1"},
			{ID: "m2", ProviderID: "p1", Model: "m2"},
		},
	}
	cl := NewCascadeLoader(t.TempDir(), "", map[string]string{"temperature": "0.7"})
	cl.applyCLIOverrides(cfg)

	if len(cfg.Models) != 2 {
		t.Fatalf("Models count = %d, want 2: %+v", len(cfg.Models), cfg.Models)
	}
	if cfg.Models[0].Temperature != 0.7 {
		t.Errorf("first model temperature = %v, want 0.7", cfg.Models[0].Temperature)
	}
	if cfg.Models[1].Temperature != 0 {
		t.Errorf("second model temperature = %v, want untouched 0", cfg.Models[1].Temperature)
	}
}

// Model-scalar flags with no resolvable model: carry the overrides on a
// memory-only scratch model bound to the active provider. The scratch is
// ephemeral (never persisted) and resolvable for this session.
func TestApplyCLIOverrides_EphemeralScratch(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{{ID: "p1", DefaultModel: "local-llm"}},
	}
	// Prevent migrateProviderDefaultModels from synthesizing a real model —
	// the scratch path is only reachable when nothing resolves. (Calling
	// applyCLIOverrides directly, that migration is not in play here; the
	// provider simply has no model entries.)
	cl := NewCascadeLoader(t.TempDir(), "", map[string]string{"temperature": "0.5", "max_tokens": "512"})
	cl.applyCLIOverrides(cfg)

	if len(cfg.Models) != 1 {
		t.Fatalf("Models count = %d, want 1 scratch: %+v", len(cfg.Models), cfg.Models)
	}
	scratch := cfg.Models[0]
	if scratch.ID != cliOverrideModelID {
		t.Errorf("scratch ID = %q, want %q", scratch.ID, cliOverrideModelID)
	}
	if scratch.ProviderID != "p1" {
		t.Errorf("scratch ProviderID = %q, want p1", scratch.ProviderID)
	}
	if scratch.Model != "local-llm" {
		t.Errorf("scratch Model = %q, want provider default local-llm", scratch.Model)
	}
	if !scratch.Ephemeral {
		t.Error("scratch must be ephemeral (memory-only)")
	}
	if scratch.Temperature != 0.5 || scratch.MaxTokens != 512 {
		t.Errorf("scratch scalars = temp %v tokens %d, want 0.5/512", scratch.Temperature, scratch.MaxTokens)
	}

	// The scratch resolves as the active model for the session.
	m, err := cfg.GetActiveModelConfig()
	if err != nil {
		t.Fatalf("GetActiveModelConfig error: %v", err)
	}
	if m.ID != cliOverrideModelID {
		t.Errorf("resolved model = %q, want scratch %q", m.ID, cliOverrideModelID)
	}
}

// Model-scalar flags with no provider at all: nothing can apply, nothing
// is created.
func TestApplyCLIOverrides_NoProvider(t *testing.T) {
	cfg := &Config{}
	cl := NewCascadeLoader(t.TempDir(), "", map[string]string{"temperature": "0.5"})
	cl.applyCLIOverrides(cfg)

	if len(cfg.Models) != 0 {
		t.Fatalf("Models = %+v, want none created", cfg.Models)
	}
}

// Ephemeral models must never be written to disk: saving providers/models
// strips the scratch entry while keeping real ones.
func TestSaveHomeProvidersAndModels_StripsEphemeral(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg := &Config{
		ActiveProvider: "p1",
		Providers:      []ProviderConfig{{ID: "p1"}},
		Models: []ModelConfig{
			{ID: "real-model", ProviderID: "p1", Model: "gpt-4o"},
			{ID: cliOverrideModelID, ProviderID: "p1", Ephemeral: true, Temperature: 0.5},
		},
	}
	if err := loader.SaveHomeProvidersAndModels(cfg); err != nil {
		t.Fatalf("SaveHomeProvidersAndModels: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(homeDir, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	content := string(data)
	if strings.Contains(content, cliOverrideModelID) {
		t.Errorf("saved config contains ephemeral %q entry:\n%s", cliOverrideModelID, content)
	}
	if !strings.Contains(content, "real-model") {
		t.Errorf("saved config lost real model entry:\n%s", content)
	}
}

// The in-memory scratch must stay invisible to persistence across a full
// load→save round trip: even when it exists in memory, reloading the saved
// file yields no cli-override model.
func TestEphemeralScratch_NotPersistedAcrossReload(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
providers:
  - id: p1
    endpoint: http://localhost:1234/v1
`)

	// Load with a model-scalar flag: provider has no models → scratch.
	loader := NewCascadeLoader(projectDir, "", map[string]string{"temperature": "0.5"})
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Models) != 1 || !cfg.Models[0].Ephemeral {
		t.Fatalf("expected one ephemeral scratch, got %+v", cfg.Models)
	}

	// Save and reload: the scratch must be gone.
	if err := loader.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, m := range reloaded.Models {
		if m.ID == cliOverrideModelID {
			t.Fatalf("ephemeral scratch persisted across reload: %+v", m)
		}
	}
}
