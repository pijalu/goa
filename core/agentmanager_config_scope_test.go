// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/event"
	"gopkg.in/yaml.v3"
)

// TestAgentManager_SetThinkingLevel_KeepsGlobalConfigOutOfProject is the B38
// regression: changing a model's thinking level persists the global model
// catalog, but must not snapshot that catalog (or any other merged/global
// section) into the project layer. Existing intentional project keys survive.
func TestAgentManager_SetThinkingLevel_KeepsGlobalConfigOutOfProject(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)

	homePath := filepath.Join(home, ".goa", "config.yaml")
	writeConfigScopeTestFile(t, homePath, `
active_provider: global-provider
active_model: global-model
providers:
  - id: global-provider
    endpoint: https://global.example.invalid/v1
    api_key: global-secret
    headers:
      X-Global-Secret: header-secret
models:
  - id: global-model
    provider: global-provider
    model: remote-global-model
context_compression:
  max_tokens: 12345
`)
	projectPath := filepath.Join(project, ".goa", "config.yaml")
	writeConfigScopeTestFile(t, projectPath, `
tools:
  enabled:
    goal: false
`)

	loader := config.NewCascadeLoader(project, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertGlobalConfigScopeSelection(t, cfg)
	autoSave := true
	cfg.Execution.AutoSaveModel = &autoSave

	am := NewAgentManager(cfg, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetConfigSaver(loader)
	if err := am.SetThinkingLevel("high"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	assertProjectConfigScopeSections(t, projectPath)

	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if reloaded.Tools.Enabled.Goal {
		t.Error("explicit project tools.enabled.goal:false override was lost")
	}
	if reloaded.ContextCompression.MaxTokens != 12345 {
		t.Errorf("global compression setting = %d, want inherited 12345", reloaded.ContextCompression.MaxTokens)
	}
	assertFreshProjectGlobalState(t)
}

func assertGlobalConfigScopeSelection(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg.ActiveProvider != "global-provider" || cfg.ActiveModel != "global-model" {
		t.Errorf("selection = (%q, %q), want global default", cfg.ActiveProvider, cfg.ActiveModel)
	}
}

func assertProjectConfigScopeSections(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	var projectLayer map[string]any
	if err := yaml.Unmarshal(raw, &projectLayer); err != nil {
		t.Fatalf("unmarshal project config: %v\n%s", err, raw)
	}
	allowed := map[string]bool{"active_provider": true, "active_model": true, "tools": true}
	var unexpected []string
	for key := range projectLayer {
		if !allowed[key] {
			unexpected = append(unexpected, key)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Fatalf("project config acquired global/general sections %v", unexpected)
	}
}

// assertFreshProjectGlobalState also proves a read-only initialization does
// not create a project config: global selection and model settings are enough.
func assertFreshProjectGlobalState(t *testing.T) {
	t.Helper()
	project := t.TempDir()
	fresh, err := config.NewCascadeLoader(project, "", nil).Load()
	if err != nil {
		t.Fatalf("load fresh project: %v", err)
	}
	assertGlobalConfigScopeSelection(t, fresh)
	if model := fresh.GetModelByID("global-model"); model == nil || model.ThinkingLevel != "high" {
		t.Errorf("global model thinking level was not persisted: %+v", model)
	}
	if _, err := os.Stat(filepath.Join(project, ".goa", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("fresh project load created config.yaml: %v", err)
	}
}

func writeConfigScopeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
