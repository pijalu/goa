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
		"model":                 "cli-model",
		"profile":               "cli-profile",
		"provider":              "openai",
		"endpoint":              "http://localhost:1234/v1",
		"api_key":               "sk-test",
		"temperature":           "0.7",
		"max_tokens":            "2048",
		"max_tool_repeat_total": "5",
		"skill_mode":            "inline",
		"reasoning":             "true",
		"thinking_level":        "medium",
		"compression":           "true",
		"debug":                 "true",
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
	if m.Reasoning == nil || !*m.Reasoning {
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
	if !cfg.ContextCompression.EnabledValue() {
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
	if cfg.Execution.Mode != "yolo" {
		t.Errorf("Default mode = %q, want %q", cfg.Execution.Mode, "yolo")
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

// TestCascadeEnvNested_MaxConsecutiveToolRounds verifies the env var for
// the max_consecutive_tool_rounds key loads correctly.
func TestCascadeEnvNested_MaxConsecutiveToolRounds(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)
	t.Setenv("GOA_EXECUTION_MAX_CONSECUTIVE_TOOL_ROUNDS", "30")

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Execution.MaxConsecutiveToolRounds != 30 {
		t.Errorf("MaxConsecutiveToolRounds = %d, want 30", cfg.Execution.MaxConsecutiveToolRounds)
	}
}

// TestCascadeCLIOverride_MaxConsecutiveToolRounds verifies the CLI flag
// override for max_consecutive_tool_rounds.
func TestCascadeCLIOverride_MaxConsecutiveToolRounds(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	cliFlags := map[string]string{
		"max_consecutive_tool_rounds": "25",
	}

	loader := NewCascadeLoader(projectDir, "", cliFlags)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Execution.MaxConsecutiveToolRounds != 25 {
		t.Errorf("MaxConsecutiveToolRounds = %d, want 25", cfg.Execution.MaxConsecutiveToolRounds)
	}
}

// TestCascadeYAML_MaxConsecutiveToolRounds verifies the YAML config key
// loads correctly.
func TestCascadeYAML_MaxConsecutiveToolRounds(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `execution:
  max_consecutive_tool_rounds: 20
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Execution.MaxConsecutiveToolRounds != 20 {
		t.Errorf("MaxConsecutiveToolRounds = %d, want 20", cfg.Execution.MaxConsecutiveToolRounds)
	}
}

// TestFirstRunDetection verifies first-run detection.
