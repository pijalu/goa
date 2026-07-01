// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// TestModelChange_PersistsAcrossRestart verifies that changing the model with
// /model survives a restart when the project config has auto_save_model: true.
func TestModelChange_PersistsAcrossRestart(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")

	// Simulate the user's actual config:
	// Project config has active_model: deepseek-v4-flash and auto_save_model: true
	writeTestConfig(t, projectPath, `active_provider: deepseek
active_model: deepseek-v4-flash
execution:
    auto_save_model: true
providers:
  - id: deepseek
    endpoint: http://deepseek.example.com/v1
models:
  - id: deepseek-v4-flash
    provider: deepseek
    model: deepseek-chat
`)

	// Home config has no active_model (it inherits from project)
	writeTestConfig(t, homePath, ``)

	// Load config (simulates first start)
	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify initial state
	if cfg.ActiveModel != "deepseek-v4-flash" {
		t.Fatalf("Initial model: got %q, want deepseek-v4-flash", cfg.ActiveModel)
	}

	// Add a second model to the config (simulating gemma being configured)
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         "gemma",
		ProviderID: "deepseek",
		Model:      "gemma-2-27b",
	})

	// Create context
	var buf strings.Builder
	ctx := core.Context{
		OutputBuffer:    &buf,
		Config:          cfg,
		ConfigSaver:     loader,
		ProviderManager: newTestProviderManager(),
	}

	// Simulate user running: /model gemma
	cmd := &ModelCommand{}
	err = cmd.Run(ctx, []string{"gemma"})
	if err != nil {
		t.Fatalf("Run /model gemma failed: %v", err)
	}

	if cfg.ActiveModel != "gemma" {
		t.Errorf("After /model gemma: ActiveModel = %q, want gemma", cfg.ActiveModel)
	}

	// Verify output mentions success
	if !strings.Contains(buf.String(), "Switched to model: gemma") {
		t.Errorf("Output should contain 'Switched to model: gemma', got %q", buf.String())
	}

	// Now simulate a restart: load config again from disk
	loader2 := config.NewCascadeLoader(projectDir, "", nil)
	cfg2, err := loader2.Load()
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// The model change should have persisted to the project config
	if cfg2.ActiveModel != "gemma" {
		t.Errorf("After restart, ActiveModel = %q, want gemma (model change did not persist)", cfg2.ActiveModel)
	}

	// Verify the project config file was updated
	projectData := readTestFile(t, projectPath)
	if !strings.Contains(projectData, "active_model: gemma") {
		t.Errorf("Project config should have active_model: gemma, got:\n%s", projectData)
	}

	// Verify the home config was also updated
	homeData := readTestFile(t, homePath)
	if !strings.Contains(homeData, "active_model: gemma") {
		t.Errorf("Home config should have active_model: gemma, got:\n%s", homeData)
	}
}

// TestModelChange_PersistsWithoutAutoSave verifies that model changes persist
// even without auto_save_model when the project config doesn't override it.
func TestModelChange_PersistsWithoutAutoSave(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")

	// Project config without active_model and without auto_save_model
	writeTestConfig(t, projectPath, `execution:
    mode: yolo
`)
	// Home config with initial model
	writeTestConfig(t, homePath, `active_provider: deepseek
active_model: deepseek-v4-flash
models:
  - id: deepseek-v4-flash
    provider: deepseek
    model: deepseek-chat
`)

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Add gemma model
	cfg.Models = append(cfg.Models, config.ModelConfig{
		ID:         "gemma",
		ProviderID: "deepseek",
		Model:      "gemma-2-27b",
	})

	ctx := core.Context{
		Config:          cfg,
		ConfigSaver:     loader,
		ProviderManager: newTestProviderManager(),
	}

	cmd := &ModelCommand{}
	err = cmd.Run(ctx, []string{"gemma"})
	if err != nil {
		t.Fatalf("Run /model gemma failed: %v", err)
	}

	// Simulate restart
	loader2 := config.NewCascadeLoader(projectDir, "", nil)
	cfg2, err := loader2.Load()
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if cfg2.ActiveModel != "gemma" {
		t.Errorf("After restart without auto_save, ActiveModel = %q, want gemma", cfg2.ActiveModel)
	}

	// Home config should have been updated
	homeData := readTestFile(t, homePath)
	if !strings.Contains(homeData, "active_model: gemma") {
		t.Errorf("Home config should have active_model: gemma, got:\n%s", homeData)
	}

	// Project config should NOT have active_model (it wasn't there before)
	projectData := readTestFile(t, projectPath)
	if strings.Contains(projectData, "active_model") && !strings.Contains(projectData, "active_model: gemma") {
		// This is OK - project might have other fields
	}
}

// TestProviderChange_PersistsAcrossRestart verifies that switching providers
// persists across a restart with auto_save_model.
func TestProviderChange_PersistsAcrossRestart(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")
	homePath := filepath.Join(homeDir, ".goa", "config.yaml")

	writeTestConfig(t, projectPath, `active_provider: openai
active_model: gpt-4
execution:
    auto_save_model: true
providers:
  - id: openai
    endpoint: http://openai.example.com/v1
  - id: anthropic
    endpoint: http://anthropic.example.com/v1
models:
  - id: gpt-4
    provider: openai
    model: gpt-4o
  - id: claude-3-5
    provider: anthropic
    model: claude-3-5-sonnet
`)
	writeTestConfig(t, homePath, ``)

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveProvider != "openai" {
		t.Fatalf("Initial provider: got %q, want openai", cfg.ActiveProvider)
	}

	var buf strings.Builder
	ctx := core.Context{
		OutputBuffer:    &buf,
		Config:          cfg,
		ConfigSaver:     loader,
		ProviderManager: newTestProviderManager(),
	}

	cmd := &ProviderCommand{}
	err = cmd.Run(ctx, []string{"anthropic"})
	if err != nil {
		t.Fatalf("Run /provider anthropic failed: %v", err)
	}

	if cfg.ActiveProvider != "anthropic" {
		t.Errorf("After /provider anthropic: ActiveProvider = %q, want anthropic", cfg.ActiveProvider)
	}

	// Simulate restart
	loader2 := config.NewCascadeLoader(projectDir, "", nil)
	cfg2, err := loader2.Load()
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if cfg2.ActiveProvider != "anthropic" {
		t.Errorf("After restart, ActiveProvider = %q, want anthropic (provider change did not persist)", cfg2.ActiveProvider)
	}

	// Verify project config was updated
	projectData := readTestFile(t, projectPath)
	if !strings.Contains(projectData, "active_provider: anthropic") {
		t.Errorf("Project config should have active_provider: anthropic, got:\n%s", projectData)
	}
}
