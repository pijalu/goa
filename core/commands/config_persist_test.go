// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// TestProviderSwitch_PersistsToHomeConfig verifies that switching providers
// writes provider/model state to ~/.goa/config.yaml without touching the
// project .goa/config.yaml.
func TestProviderSwitch_PersistsToHomeConfig(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")

	writeTestConfig(t, homePath, `active_provider: home-provider
providers:
  - id: home-provider
    endpoint: http://home.example.com/v1
  - id: openai
    endpoint: http://openai.example.com/v1
`)
	writeTestConfig(t, projectPath, `active_provider: project-provider
providers:
  - id: project-provider
    endpoint: http://project.example.com/v1
`)

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cmd := &ProviderCommand{}
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf, Config: cfg, ConfigSaver: loader}
	if err := cmd.Run(ctx, []string{"openai"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	homeData := readTestFile(t, homePath)
	if !strings.Contains(homeData, "active_provider: openai") {
		t.Errorf("home config should have active_provider: openai, got:\n%s", homeData)
	}

	projectData := readTestFile(t, projectPath)
	if strings.Contains(projectData, "active_provider: openai") {
		t.Errorf("project config should not have been updated, got:\n%s", projectData)
	}
	if !strings.Contains(projectData, "active_provider: project-provider") {
		t.Errorf("project config should keep its original active_provider, got:\n%s", projectData)
	}
}

// TestModelSwitch_PersistsToHomeConfig verifies that switching models writes
// provider/model state to ~/.goa/config.yaml without touching the project
// .goa/config.yaml.
func TestModelSwitch_PersistsToHomeConfig(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")

	writeTestConfig(t, homePath, `active_provider: openai
active_model: gpt-4o
providers:
  - id: openai
    endpoint: http://openai.example.com/v1
  - id: anthropic
    endpoint: http://anthropic.example.com/v1
models:
  - id: gpt-4o
    provider: openai
    model: gpt-4o
  - id: claude-3-5
    provider: anthropic
    model: claude-3-5-sonnet
`)
	writeTestConfig(t, projectPath, `active_model: project-model
models:
  - id: project-model
    provider: openai
    model: project-model
`)

	loader := config.NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	cmd := &ModelCommand{}
	var buf strings.Builder
	ctx := core.Context{
		OutputBuffer:    &buf,
		Config:          cfg,
		ConfigSaver:     loader,
		ProviderManager: newTestProviderManager(),
	}
	if err := cmd.Run(ctx, []string{"claude-3-5"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	homeData := readTestFile(t, homePath)
	if !strings.Contains(homeData, "active_provider: anthropic") {
		t.Errorf("home config should have active_provider: anthropic, got:\n%s", homeData)
	}
	if !strings.Contains(homeData, "active_model: claude-3-5") {
		t.Errorf("home config should have active_model: claude-3-5, got:\n%s", homeData)
	}

	projectData := readTestFile(t, projectPath)
	if strings.Contains(projectData, "active_provider: anthropic") {
		t.Errorf("project config should not have been updated, got:\n%s", projectData)
	}
}

func writeTestConfig(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create dir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
