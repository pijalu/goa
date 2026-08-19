// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal"
)

// setupTestConfig creates a temporary home and project directory structure
// for testing the config cascade.

func TestFirstRunDetection(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	os.RemoveAll(filepath.Join(homeDir, ".goa"))

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !cfg.FirstRun {
		t.Error("FirstRun should be true with no home config")
	}

	// Now create the config file and verify FirstRun is false
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `active_provider: test`)
	cfg2, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg2.FirstRun {
		t.Error("FirstRun should be false when home config exists")
	}
}

// TestSave verifies ConfigSaver.Save writes valid YAML.
func TestSave(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)

	// Load defaults first
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Modify and save
	cfg.ActiveProvider = "saved-provider"
	cfg.ActiveModel = "saved-model"
	if err := loader.Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists and contains our values
	savedPath := filepath.Join(homeDir, ".goa", "config.yaml")
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("Read saved config: %v", err)
	}

	content := string(data)
	if !containsStr(content, "saved-provider") {
		t.Errorf("Saved config missing active_provider: %s", content)
	}
	if !containsStr(content, "saved-model") {
		t.Errorf("Saved config missing active_model: %s", content)
	}
}

// TestSaveHomeField updates a single nested field without overwriting others.
func TestSaveHomeField(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)

	// Pre-populate home config with an unrelated setting.
	savedPath := filepath.Join(homeDir, ".goa", "config.yaml")
	writeConfig(t, savedPath, `
active_provider: existing-provider
`)

	if err := loader.SaveHomeField([]string{"tui", "transparency", "thinking_collapsed"}, true); err != nil {
		t.Fatalf("SaveHomeField failed: %v", err)
	}

	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("Read saved config: %v", err)
	}
	content := string(data)
	if !containsStr(content, "existing-provider") {
		t.Errorf("existing active_provider should be preserved, got: %s", content)
	}
	if !containsStr(content, "thinking_collapsed") {
		t.Errorf("saved config should contain thinking_collapsed, got: %s", content)
	}
	if !containsStr(content, "true") {
		t.Errorf("saved thinking_collapsed should be true, got: %s", content)
	}
}

// TestSaveHomeProvidersAndModels updates provider/model fields without
// overwriting unrelated home settings.
func TestSaveHomeProvidersAndModels(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)

	savedPath := filepath.Join(homeDir, ".goa", "config.yaml")
	writeConfig(t, savedPath, `
active_provider: existing-provider
execution:
  mode: confirm
providers:
  - id: existing
    endpoint: https://existing.example.com
    api_key: old-key
`)

	cfg := &Config{
		ActiveProvider: "new-provider",
		ActiveModel:    "new-model",
		Providers: []ProviderConfig{
			{ID: "new-provider", Endpoint: "https://new.example.com", APIKey: "new-key"},
		},
		Models: []ModelConfig{
			{ID: "new-model", ProviderID: "new-provider", Model: "new-model"},
		},
	}
	if err := loader.SaveHomeProvidersAndModels(cfg); err != nil {
		t.Fatalf("SaveHomeProvidersAndModels failed: %v", err)
	}

	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("Read saved config: %v", err)
	}
	assertSavedHomeProvidersAndModels(t, string(data))

	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	assertReloadedProvidersAndModels(t, reloaded)
}

func assertSavedHomeProvidersAndModels(t *testing.T, content string) {
	t.Helper()
	wantContains := []string{"new-provider", "new-model", "new-key", "execution:", "confirm"}
	for _, want := range wantContains {
		if !containsStr(content, want) {
			t.Errorf("saved config should contain %q, got: %s", want, content)
		}
	}
	if containsStr(content, "old-key") {
		t.Errorf("existing provider should have been replaced, got: %s", content)
	}
}

func assertReloadedProvidersAndModels(t *testing.T, cfg *Config) {
	t.Helper()
	if cfg.ActiveProvider != "new-provider" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "new-provider")
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].APIKey != "new-key" {
		t.Errorf("Providers not updated correctly: %+v", cfg.Providers)
	}
}

// TestEnvInterpolation verifies ${VAR} and ${VAR:-default} resolution.
func TestEnvInterpolation(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)
	t.Setenv("MY_API_KEY", "sk-real-key")
	t.Setenv("MY_ENDPOINT", "")

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_provider: interpolated
providers:
  - id: interpolated
    api_key: ${MY_API_KEY}
    endpoint: ${MY_ENDPOINT:-https://default.example.com}
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Fatalf("Providers = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].APIKey != "sk-real-key" {
		t.Errorf("APIKey = %q, want %q", cfg.Providers[0].APIKey, "sk-real-key")
	}
	// Should use default since MY_ENDPOINT is empty
	if cfg.Providers[0].Endpoint != "https://default.example.com" {
		t.Errorf("Endpoint = %q, want %q", cfg.Providers[0].Endpoint, "https://default.example.com")
	}
}

// containsStr checks if a string contains a substring.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && containsStrHelper(s, substr)
}

func containsStrHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestLegacyExecutionModeMigration verifies old execution.mode is migrated on load.
func TestLegacyExecutionModeMigration(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// Write a config with old-style execution.mode and active_profile, no mode section
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_profile: planner
execution:
  mode: confirm
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify legacy migration: active_profile → mode.default.major
	if cfg.Mode.Default.Major != internal.MajorPlanner {
		t.Errorf("Mode.Default.Major = %q, want %q", cfg.Mode.Default.Major, internal.MajorPlanner)
	}

	// Verify legacy migration: execution.mode: confirm → mode.defaults.planner: confirm
	if cfg.Mode.Defaults == nil {
		t.Fatal("Mode.Defaults should be populated after migration")
	}
	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomyConfirm {
		t.Errorf("Mode.Defaults[planner] = %q, want %q", cfg.Mode.Defaults[internal.MajorPlanner], internal.AutonomyConfirm)
	}

	// DefaultModeState should pick up the migrated autonomy
	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorPlanner {
		t.Errorf("DefaultModeState().Major = %q, want %q", ms.Major, internal.MajorPlanner)
	}
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("DefaultModeState().Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyConfirm)
	}
}

func TestLegacyExecutionModeMigration_AlreadyMigrated(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// Config already has mode.defaults — migration should be a no-op
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_profile: coder
mode:
  defaults:
    coder: yolo
    planner: review
execution:
  mode: review
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Mode.Defaults should keep existing values, not be overwritten by migration
	if cfg.Mode.Defaults == nil {
		t.Fatal("Mode.Defaults should be present")
	}
	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomyReview {
		t.Errorf("Mode.Defaults[planner] = %q, want %q", cfg.Mode.Defaults[internal.MajorPlanner], internal.AutonomyReview)
	}
	// Coder default should still be yolo
	if cfg.Mode.Defaults[internal.MajorCoder] != internal.AutonomyYolo {
		t.Errorf("Mode.Defaults[coder] = %q, want %q", cfg.Mode.Defaults[internal.MajorCoder], internal.AutonomyYolo)
	}
}

func TestLegacyExecutionModeMigration_FallbackToCoder(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// No active_profile in config — migration should default to coder
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
execution:
  mode: confirm
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Mode.Defaults == nil {
		t.Fatal("Mode.Defaults should be populated after migration")
	}
	if cfg.Mode.Defaults[internal.MajorCoder] != internal.AutonomyConfirm {
		t.Errorf("Mode.Defaults[coder] = %q, want %q", cfg.Mode.Defaults[internal.MajorCoder], internal.AutonomyConfirm)
	}

	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorCoder {
		t.Errorf("DefaultModeState().Major = %q, want %q", ms.Major, internal.MajorCoder)
	}
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("DefaultModeState().Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyConfirm)
	}
}

func TestLegacyExecutionModeMigration_ExplicitYoloMigrated(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// With the new default being solo, an explicit execution.mode: yolo must be
	// migrated to mode.defaults so the user's explicit choice is preserved.
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_profile: coder
execution:
  mode: yolo
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Mode.Defaults[internal.MajorCoder] != internal.AutonomyYolo {
		t.Errorf("Mode.Defaults[coder] = %q, want %q", cfg.Mode.Defaults[internal.MajorCoder], internal.AutonomyYolo)
	}

	ms := cfg.DefaultModeState()
	if ms.Autonomy != internal.AutonomyYolo {
		t.Errorf("DefaultModeState().Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyYolo)
	}
}

func TestLegacyExecutionModeMigration_DefaultSoloNotMigrated(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// Config with the new default execution.mode: solo should NOT trigger migration
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_profile: coder
execution:
  mode: solo
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Mode.Defaults should be empty (matches the new built-in default)
	if len(cfg.Mode.Defaults) != 0 {
		t.Errorf("Mode.Defaults = %v, want empty (migration should not trigger for solo)", cfg.Mode.Defaults)
	}

	ms := cfg.DefaultModeState()
	if ms.Autonomy != internal.AutonomySolo {
		t.Errorf("DefaultModeState().Autonomy = %q, want %q", ms.Autonomy, internal.AutonomySolo)
	}
}

// TestLoadToolsDefaults verifies the embedded defaults populate the tools
// display config (view=summary, preview_lines=10).
func TestLoadToolsDefaults(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.TUI.Tools.View != ToolViewSummary {
		t.Errorf("default Tools.View = %q, want %q", cfg.TUI.Tools.View, ToolViewSummary)
	}
	if cfg.TUI.Tools.PreviewLines != 10 {
		t.Errorf("default Tools.PreviewLines = %d, want 10", cfg.TUI.Tools.PreviewLines)
	}
	if cfg.TUI.Tools.ShowRead {
		t.Errorf("default Tools.ShowRead = true, want false")
	}
}

// TestLoadToolsOverride verifies a project config overrides the tools defaults.
func TestLoadToolsOverride(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `
tui:
  tools:
    view: full
    preview_lines: 7
    show_read: true
`)
	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.TUI.Tools.View != ToolViewFull {
		t.Errorf("Tools.View = %q, want %q", cfg.TUI.Tools.View, ToolViewFull)
	}
	if cfg.TUI.Tools.PreviewLines != 7 {
		t.Errorf("Tools.PreviewLines = %d, want 7", cfg.TUI.Tools.PreviewLines)
	}
	if !cfg.TUI.Tools.ShowRead {
		t.Errorf("Tools.ShowRead = false, want true")
	}
}

// TestLoad_SanitizesSelectorSentinels is the regression for the /provider
// '+' bug that persisted a provider with ID "__add__" into the home config
// (observed in a real user export). Load must drop sentinel providers/models
// and clear sentinel active_provider/active_model so the picker state cannot
// resurrect them.
func TestLoad_SanitizesSelectorSentinels(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_provider: __add__
active_model: some-model
providers:
  - id: openai
    name: OpenAI
    endpoint: https://api.openai.com/v1
  - id: __add__
    name: ""
    endpoint: ""
models:
  - id: some-model
    provider: openai
    model: some-model
  - id: leaked-model
    provider: __add__
    model: leaked-model
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.GetProviderByID("__add__") != nil {
		t.Error("sentinel provider __add__ survived load sanitization")
	}
	if cfg.GetProviderByID("openai") == nil {
		t.Error("legitimate provider openai was dropped by sanitization")
	}
	if cfg.ActiveProvider == "__add__" {
		t.Error("ActiveProvider still holds the __add__ sentinel")
	}
	for _, m := range cfg.Models {
		if m.ProviderID == "__add__" || m.ID == "__add__" {
			t.Errorf("sentinel-linked model survived sanitization: %+v", m)
		}
	}
}

// TestLoad_SanitizesDeletePrefixedSentinels is the regression for the
// "Model delete" bug: a '-' hotkey sentinel ("__delete__<id>") leaked and was
// persisted verbatim as a model/provider ID. Load must drop those entries so
// the polluted row cannot resurface in the picker (where the old "__" guard
// also made it undeletable).
func TestLoad_SanitizesDeletePrefixedSentinels(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_provider: openai
active_model: deepseek-v4-flash
providers:
  - id: openai
    name: OpenAI
    endpoint: https://api.openai.com/v1
models:
  - id: deepseek-v4-flash
    provider: openai
    model: deepseek-v4-flash
  - id: __delete__deepseek-v4-flash
    provider: openai
    model: deepseek-v4-flash
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	for _, m := range cfg.Models {
		if m.ID == "__delete__deepseek-v4-flash" {
			t.Errorf("delete-prefixed sentinel model survived sanitization: %+v", m)
		}
	}
	// The legitimate model the sentinel pointed at must be preserved.
	found := false
	for _, m := range cfg.Models {
		if m.ID == "deepseek-v4-flash" {
			found = true
		}
	}
	if !found {
		t.Error("legitimate model deepseek-v4-flash was dropped by sanitization")
	}
}
