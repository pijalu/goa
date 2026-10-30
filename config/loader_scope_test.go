// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const scopeTestHomeConfig = `
active_provider: global-provider
active_model: global-model
providers:
  - id: global-provider
    endpoint: https://global.example.invalid/v1
    api_key: global-secret
    headers:
      X-Global-Secret: global-header-secret
models:
  - id: global-model
    provider: global-provider
    model: remote-global-model
context_compression:
  max_tokens: 12345
tools:
  enabled:
    goal: true
`

// TestCascadeScope_FreshProjectInheritsWithoutWriting verifies that merely
// loading a fresh project neither creates a project config nor duplicates the
// global selection, credentials, catalog, or general settings into one.
func TestCascadeScope_FreshProjectInheritsWithoutWriting(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)
	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), scopeTestHomeConfig)

	cfg, err := NewCascadeLoader(project, "", nil).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveProvider != "global-provider" || cfg.ActiveModel != "global-model" {
		t.Errorf("selection = (%q, %q), want global defaults", cfg.ActiveProvider, cfg.ActiveModel)
	}
	provider := cfg.GetProviderByID("global-provider")
	if provider == nil || provider.APIKey != "global-secret" {
		t.Errorf("global provider credentials not inherited: %+v", provider)
	}
	if cfg.ContextCompression.MaxTokens != 12345 {
		t.Errorf("global compression max_tokens = %d, want 12345", cfg.ContextCompression.MaxTokens)
	}
	if _, err := os.Stat(filepath.Join(project, ".goa", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("fresh project load created config.yaml: %v", err)
	}
}

// TestProjectProviderOverrides_SurviveSelectionPersistence verifies both sides
// of the compatibility contract: intentional project provider/model/general
// overrides still win, while the legacy project persistence entry point cannot
// add global credentials or unrelated global sections alongside them.
func TestProjectProviderOverrides_SurviveSelectionPersistence(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)
	writeConfig(t, filepath.Join(home, ".goa", "config.yaml"), scopeTestHomeConfig)
	projectPath := filepath.Join(project, ".goa", "config.yaml")
	writeConfig(t, projectPath, `
active_provider: project-provider
active_model: project-model
providers:
  - id: project-provider
    endpoint: https://project.example.invalid/v1
    api_key: project-secret
models:
  - id: project-model
    provider: project-provider
    model: remote-project-model
context_compression:
  max_tokens: 23456
tools:
  enabled:
    goal: false
`)

	loader := NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertProjectScopeOverrides(t, cfg)

	// Exercise the compatibility wrapper used by older callers. It may refresh
	// the active selection, but must leave all existing project-owned keys alone.
	if err := loader.SaveProjectProvidersAndModels(cfg); err != nil {
		t.Fatalf("SaveProjectProvidersAndModels: %v", err)
	}
	raw, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	content := string(raw)
	for _, globalValue := range []string{"global-secret", "global-header-secret", "https://global.example.invalid/v1", "remote-global-model"} {
		if strings.Contains(content, globalValue) {
			t.Errorf("project config copied global value %q", globalValue)
		}
	}
	for _, projectValue := range []string{"project-secret", "https://project.example.invalid/v1", "remote-project-model"} {
		if !strings.Contains(content, projectValue) {
			t.Errorf("explicit project value %q was not preserved", projectValue)
		}
	}
	var layer map[string]any
	if err := yaml.Unmarshal(raw, &layer); err != nil {
		t.Fatalf("unmarshal project layer: %v", err)
	}
	for key := range layer {
		switch key {
		case "active_provider", "active_model", "providers", "models", "context_compression", "tools":
		default:
			t.Errorf("selection persistence added unrelated project section %q", key)
		}
	}

	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	assertProjectScopeOverrides(t, reloaded)
}

func assertProjectScopeOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	if cfg.ActiveProvider != "project-provider" || cfg.ActiveModel != "project-model" {
		t.Errorf("selection = (%q, %q), want explicit project override", cfg.ActiveProvider, cfg.ActiveModel)
	}
	provider := cfg.GetProviderByID("project-provider")
	if provider == nil || provider.APIKey != "project-secret" {
		t.Errorf("project provider credentials = %+v, want project-secret", provider)
	}
	if cfg.ContextCompression.MaxTokens != 23456 {
		t.Errorf("compression max_tokens = %d, want project override 23456", cfg.ContextCompression.MaxTokens)
	}
	if cfg.Tools.Enabled.Goal {
		t.Error("explicit project tools.enabled.goal:false override was lost")
	}
}
