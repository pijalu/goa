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
func setupTestConfig(t *testing.T) (homeDir, projectDir string, cleanup func()) {
	t.Helper()
	homeDir, err := os.MkdirTemp("", "goa-test-home-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	projectDir, err = os.MkdirTemp("", "goa-test-project-*")
	if err != nil {
		os.RemoveAll(homeDir)
		t.Fatalf("create temp project: %v", err)
	}
	cleanup = func() {
		os.RemoveAll(homeDir)
		os.RemoveAll(projectDir)
	}
	return
}

// writeConfig writes a YAML config file for testing.
func writeConfig(t *testing.T, path string, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create dir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestCascadeHomeOverridesDefault verifies home config overrides embedded defaults.
func TestCascadeHomeOverridesDefault(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	// Override HOME to our temp dir
	t.Setenv("HOME", homeDir)
	os.Setenv("GOA_ACTIVE_PROVIDER", "") // ensure no env override

	// Write a home config with explicit override
	homeConfigDir := filepath.Join(homeDir, ".goa")
	os.MkdirAll(homeConfigDir, 0755)
	writeConfig(t, filepath.Join(homeConfigDir, "config.yaml"), `
active_provider: home-provider
active_model: test-model
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveProvider != "home-provider" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "home-provider")
	}
	if cfg.ActiveModel != "test-model" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "test-model")
	}
}

// TestCascadeProjectOverridesHome verifies project config overrides home.
func TestCascadeProjectOverridesHome(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// Home config
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `active_provider: home-provider`)

	// Project config
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `active_provider: project-provider`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveProvider != "project-provider" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "project-provider")
	}
}

// TestCascadeLocalOverridesProject verifies local config overrides project.
func TestCascadeLocalOverridesProject(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `active_provider: home`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `active_provider: project`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), `active_provider: local`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveProvider != "local" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "local")
	}
}

// TestCascadeExplicitConfigFile verifies --config flag overrides cascade.
func TestCascadeExplicitConfigFile(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// Create an explicit config file
	explicitPath := filepath.Join(homeDir, "custom.yaml")
	writeConfig(t, explicitPath, `active_provider: explicit`)

	// Also create project config that should be ignored
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `active_provider: project`)
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `active_provider: home`)

	loader := NewCascadeLoader(projectDir, explicitPath, nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveProvider != "explicit" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "explicit")
	}
}

// TestCascadeEnvOverride verifies GOA_* env vars override file config.
func TestCascadeEnvOverride(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)
	t.Setenv("GOA_ACTIVE_PROVIDER", "env-provider")
	t.Setenv("GOA_ACTIVE_MODEL", "env-model")

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `active_provider: file-provider`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveProvider != "env-provider" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "env-provider")
	}
	if cfg.ActiveModel != "env-model" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "env-model")
	}
}

// TestCascadeCLIOverride verifies CLI flags override everything.
func TestCascadeCLIOverride(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	cliFlags := cliOverrideFlags()
	loader := NewCascadeLoader(projectDir, "", cliFlags)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	assertCLISimpleOverrides(t, cfg)
	assertCLIProviderOverrides(t, cfg)
	assertCLIModelOverrides(t, cfg)
	assertCLIExecutionOverrides(t, cfg)
}

func cliOverrideFlags() map[string]string {
	return map[string]string{
		"model":           "cli-model",
		"profile":         "cli-profile",
		"provider":        "openai",
		"endpoint":              "http://localhost:1234/v1",
		"api_key":               "sk-test",
		"temperature":           "0.7",
		"max_tokens":            "2048",
		"max_tool_repeat_total": "5",
		"skill_mode":            "inline",
		"reasoning":       "true",
		"thinking_level":  "medium",
		"compression":     "true",
		"debug":           "true",
	}
}

func assertCLISimpleOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	if cfg.ActiveModel != "cli-model" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "cli-model")
	}
	if cfg.Mode.Default.Major != internal.MajorMode("cli-profile") {
		t.Errorf("Mode.Default.Major = %q, want %q", cfg.Mode.Default.Major, internal.MajorMode("cli-profile"))
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
}

func assertCLIProviderOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	p := cfg.GetActiveProviderConfig()
	if p == nil || p.Endpoint != "http://localhost:1234/v1" {
		t.Errorf("Provider endpoint not overridden, got %v", p)
	}
	if p == nil || p.APIKey != "sk-test" {
		t.Errorf("Provider API key not overridden, got %v", p)
	}
}

func assertCLIModelOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	m, err := cfg.GetActiveModelConfig()
	if err != nil {
		t.Fatalf("GetActiveModelConfig error: %v", err)
	}
	if m.Temperature != 0.7 {
		t.Errorf("Model temperature = %v, want 0.7", m.Temperature)
	}
	if m.MaxTokens != 2048 {
		t.Errorf("Model max_tokens = %d, want 2048", m.MaxTokens)
	}
	if !m.Reasoning {
		t.Error("Model reasoning should be enabled")
	}
	if m.ThinkingLevel != "medium" {
		t.Errorf("Model thinking_level = %q, want medium", m.ThinkingLevel)
	}
}

func assertCLIExecutionOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	if cfg.Execution.MaxToolRepeatTotal != 5 {
		t.Errorf("MaxToolRepeatTotal = %d, want 5", cfg.Execution.MaxToolRepeatTotal)
	}
	if cfg.Skills.ExecutionMode != "inline" {
		t.Errorf("Skills.ExecutionMode = %q, want inline", cfg.Skills.ExecutionMode)
	}
	if !cfg.ContextCompression.Enabled {
		t.Error("ContextCompression should be enabled")
	}
}

func TestCascadeCLIOverride_ExecutionMode(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", map[string]string{
		"execution_mode": "review",
	})
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Execution.Mode != "review" {
		t.Errorf("Execution.Mode = %q, want review", cfg.Execution.Mode)
	}
}

func TestCascadeCLIOverride_NewFlags(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	cliFlags := map[string]string{
		"max_tool_calls":               "12",
		"tool_call_limit_reset_window": "3",
		"theme":                        "light",
		"thinking_blocks":              "off",
		"show_thinking":                "true",
	}

	loader := NewCascadeLoader(projectDir, "", cliFlags)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Execution.MaxToolCalls != 12 {
		t.Errorf("MaxToolCalls = %d, want 12", cfg.Execution.MaxToolCalls)
	}
	if cfg.Execution.ToolCallLimitResetWindow != 3 {
		t.Errorf("ToolCallLimitResetWindow = %d, want 3", cfg.Execution.ToolCallLimitResetWindow)
	}
	if cfg.TUI.Theme != "light" {
		t.Errorf("TUI.Theme = %q, want light", cfg.TUI.Theme)
	}
	if !cfg.TUI.Transparency.ThinkingCollapsed {
		t.Error("ThinkingCollapsed should be true for thinking-blocks=off")
	}
	if !cfg.TUI.Transparency.ShowThinking {
		t.Error("ShowThinking should be true")
	}
}

func TestCascadeEnvNested_MaxToolCalls(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)
	t.Setenv("GOA_EXECUTION_MAX_TOOL_CALLS", "8")
	t.Setenv("GOA_EXECUTION_TOOL_CALL_LIMIT_RESET_WINDOW", "2")
	t.Setenv("GOA_TUI_TRANSPARENCY_THINKING_COLLAPSED", "true")

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Execution.MaxToolCalls != 8 {
		t.Errorf("MaxToolCalls = %d, want 8", cfg.Execution.MaxToolCalls)
	}
	if cfg.Execution.ToolCallLimitResetWindow != 2 {
		t.Errorf("ToolCallLimitResetWindow = %d, want 2", cfg.Execution.ToolCallLimitResetWindow)
	}
	if !cfg.TUI.Transparency.ThinkingCollapsed {
		t.Error("ThinkingCollapsed should be true")
	}
}

// TestCascadeNoFiles verifies missing all config files uses embedded defaults.
func TestCascadeNoFiles(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// Ensure no config files exist
	os.RemoveAll(filepath.Join(homeDir, ".goa"))
	os.RemoveAll(filepath.Join(projectDir, ".goa"))

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have embedded defaults
	if cfg.FirstRun != true {
		t.Error("FirstRun should be true when no home config exists")
	}
	if cfg.Execution.Mode != "solo" {
		t.Errorf("Default mode = %q, want %q", cfg.Execution.Mode, "solo")
	}
}

// TestCascadeEnvNested verifies GOA_ nested env vars.
func TestCascadeEnvNested(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)
	// Set a nested field via env var (case-sensitive mapping)
	// The loader uses yaml tag names, so we need the exact path
	t.Setenv("GOA_TUI_THEME", "light")
	t.Setenv("GOA_EXECUTION_MODE", "confirm")

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `active_provider: test`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.TUI.Theme != "light" {
		t.Errorf("TUI.Theme = %q, want %q", cfg.TUI.Theme, "light")
	}
	if cfg.Execution.Mode != "confirm" {
		t.Errorf("Execution.Mode = %q, want %q", cfg.Execution.Mode, "confirm")
	}
}

// TestFirstRunDetection verifies first-run detection.
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
