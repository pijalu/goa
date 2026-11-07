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

// saveDeltaHome loads a fresh cascade, applies mutate, saves it and returns the
// home-config bytes.
func saveDeltaHome(t *testing.T, mutate func(*Config)) (string, *CascadeLoader, string) {
	t.Helper()
	homeDir, projectDir, cleanup := setupTestConfig(t)
	t.Cleanup(cleanup)
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if mutate != nil {
		mutate(cfg)
	}
	if err := loader.Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	path := filepath.Join(homeDir, ".goa", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved home config: %v", err)
	}
	return string(data), loader, path
}

// TestSave_PrunesShippedDefaults is the regression guard for the mcp bug: a Save
// must not persist values the user never changed, otherwise an older default
// stays pinned forever and a later shipped-default change never reaches the
// install (tools.enabled.mcp was frozen at false across the opt-out flip).
func TestSave_PrunesShippedDefaults(t *testing.T) {
	content, _, _ := saveDeltaHome(t, nil)

	for _, key := range []string{"mcp:", "todo_list:", "webfetch:", "retries:", "activity_timeout:"} {
		if strings.Contains(content, key) {
			t.Errorf("default-equal %q must not be pinned by Save; saved config:\n%s", key, content)
		}
	}
}

// TestSave_KeepsExplicitChoices: a value that deviates from the shipped default
// is a user choice and must survive the prune and the next load.
func TestSave_KeepsExplicitChoices(t *testing.T) {
	content, loader, _ := saveDeltaHome(t, func(cfg *Config) {
		cfg.Tools.Enabled.AgentMCP = false // shipped default: true
		cfg.Execution.Retries = 9          // shipped default: 5
	})

	for _, key := range []string{"mcp: false", "retries: 9"} {
		if !strings.Contains(content, key) {
			t.Errorf("explicit %q missing from saved config:\n%s", key, content)
		}
	}

	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Tools.Enabled.AgentMCP {
		t.Error("explicit tools.enabled.mcp=false was lost across save/reload")
	}
	if reloaded.Execution.Retries != 9 {
		t.Errorf("explicit retries = %d, want 9", reloaded.Execution.Retries)
	}
}

// TestSave_DeltaRoundTripKeepsEffectiveValues: pruning must be invisible in the
// merged config — the reload of a pruned save equals the config that was saved.
func TestSave_DeltaRoundTripKeepsEffectiveValues(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg.ActiveProvider = "delta"
	cfg.ActiveModel = "delta-model"
	cfg.Execution.ActivityTimeout = "90s"
	if err := loader.Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ActiveProvider != "delta" || reloaded.ActiveModel != "delta-model" {
		t.Errorf("actives = (%q, %q), want (delta, delta-model)", reloaded.ActiveProvider, reloaded.ActiveModel)
	}
	if reloaded.Execution.ActivityTimeout != "90s" {
		t.Errorf("activity_timeout = %q, want 90s", reloaded.Execution.ActivityTimeout)
	}
	if !reloaded.Tools.Enabled.AgentMCP {
		t.Error("pruned save must not disable a default-on tool on reload")
	}
	if reloaded.Execution.Retries != cfg.Execution.Retries {
		t.Errorf("retries = %d, want %d (default preserved)", reloaded.Execution.Retries, cfg.Execution.Retries)
	}
}

// TestSave_AllDefaultsWritesEmptyMapping: even an untouched config creates a
// loadable home file (its existence ends first-run detection) with no pins.
func TestSave_AllDefaultsWritesEmptyMapping(t *testing.T) {
	content, _, path := saveDeltaHome(t, nil)

	if strings.TrimSpace(content) != "{}" {
		t.Errorf("all-default save = %q, want an empty mapping", content)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("home config must exist after Save: %v", err)
	}
}

// TestSave_KeepsUserDataOutsideDefaults: user-owned data (mcp server
// definitions, aliases) is never pruned — it does not exist in the shipped
// defaults, so it cannot be mistaken for one.
//
// Reload is deliberately not asserted here: mergeMCP and the aliases map are
// not applied by Config.DeepMerge (pre-existing loader gap, tracked separately
// from the save-delta fix).
func TestSave_KeepsUserDataOutsideDefaults(t *testing.T) {
	content, _, _ := saveDeltaHome(t, func(cfg *Config) {
		cfg.Aliases = map[string]string{"zz": "config"}
		enabled := true
		cfg.MCP = map[string]MCPServerConfig{"local": {Command: []string{"mcp-local"}, Enabled: &enabled}}
	})

	if !strings.Contains(content, "zz: config") {
		t.Errorf("alias lost:\n%s", content)
	}
	if !strings.Contains(content, "mcp-local") {
		t.Errorf("mcp server definition lost:\n%s", content)
	}
}
