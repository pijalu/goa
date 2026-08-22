// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"
)

// TestDefaultConfigYAML verifies embedded defaults can be loaded.
func TestDefaultConfigYAML(t *testing.T) {
	yaml, err := DefaultConfigYAML()
	if err != nil {
		t.Fatalf("DefaultConfigYAML returned error: %v", err)
	}
	if yaml == "" {
		t.Fatal("DefaultConfigYAML returned empty")
	}
	if !containsStr(yaml, "yolo") {
		t.Error("Default config should contain yolo mode")
	}
	if !containsStr(yaml, "dark") {
		t.Error("Default config should contain dark theme")
	}
}

// TestDefaultConfig_AutoHealToolCalls verifies the embedded default keeps
// auto-heal OFF (adopted tuned default): auto-healing malformed XML tool
// calls is only wanted for local models that need it.
func TestDefaultConfig_AutoHealToolCalls(t *testing.T) {
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Execution.AutoHealToolCalls {
		t.Errorf("Execution.AutoHealToolCalls = true, want false")
	}
}

func TestDefaultConfig_ReadFileFuzzyMatch(t *testing.T) {
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Tools.ReadFile.FuzzyMatch == nil || !*cfg.Tools.ReadFile.FuzzyMatch {
		t.Errorf("Tools.ReadFile.FuzzyMatch = %v, want true", cfg.Tools.ReadFile.FuzzyMatch)
	}
}

func TestDefaultConfig_ContextCompressionMaxTokensAuto(t *testing.T) {
	// Isolate from the user's home config so only embedded defaults matter.
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.ContextCompression.EnabledValue() {
		t.Fatalf("ContextCompression.EnabledValue() = false, want true")
	}
	if cfg.ContextCompression.MaxTokens != 0 {
		t.Errorf("ContextCompression.MaxTokens = %d, want 0 (auto)", cfg.ContextCompression.MaxTokens)
	}
}

// TestDefaultThemeYAML verifies built-in themes exist.
func TestDefaultThemeYAML(t *testing.T) {
	dark := DefaultThemeYAML("dark")
	if dark == "" {
		t.Fatal("Dark theme YAML should not be empty")
	}
	if !containsStr(dark, "thinking_border") {
		t.Error("Dark theme should contain thinking_border token")
	}

	light := DefaultThemeYAML("light")
	if light == "" {
		t.Fatal("Light theme YAML should not be empty")
	}

	nonexistent := DefaultThemeYAML("nonexistent")
	if nonexistent != "" {
		t.Error("Non-existent theme should return empty")
	}
}

// TestDefaultSkillDirs verifies the default skill directories include
// ~/.agents/skills/ and $PWD/.agents/skills/.
func TestDefaultSkillDirs(t *testing.T) {
	dirs := DefaultSkillDirs("/tmp/test-project")
	if len(dirs) == 0 {
		t.Fatal("DefaultSkillDirs returned empty list")
	}
	// Check that .agents/skills is in the returned dirs
	foundAgents := false
	for _, d := range dirs {
		if strings.Contains(d, ".agents") && strings.Contains(d, "skills") {
			foundAgents = true
			break
		}
	}
	if !foundAgents {
		t.Errorf("Expected a directory containing '.agents/skills', got %v", dirs)
	}
	// Check project-scoped dir is present (cross-platform: normalize \ → / on Windows)
	foundProject := false
	for _, d := range dirs {
		norm := strings.ReplaceAll(d, "\\", "/")
		if strings.Contains(norm, "/tmp/test-project/.agents/skills") {
			foundProject = true
			break
		}
	}
	if !foundProject {
		t.Errorf("Expected project dir '/tmp/test-project/.agents/skills' in dirs, got %v", dirs)
	}
}

// TestDefaultConfig_VerifyToolEnabled verifies the verify tool is opt-OUT:
// enabled via the embedded config — but is opt-in per the tuned default
// adoption: users enable it via /tools when they want machine verification.
func TestDefaultConfig_VerifyToolEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Tools.Enabled.Verify {
		t.Errorf("Tools.Enabled.Verify = true, want false (opt-in per tuned default)")
	}
}

// TestDefaultConfig_AgentDrivenToolsEnabled verifies the agent-driven
// companion tools (request_review, delegate_to) are OFF in the embedded
// default (tuned adoption). Companion mode arms them at runtime via the
// companion-active override in registerAgentDrivenTools, so a user who turns
// companion on still gets the tools regardless of the persisted default.
func TestDefaultConfig_AgentDrivenToolsEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Tools.Enabled.RequestReview {
		t.Errorf("Tools.Enabled.RequestReview = true, want false (opt-in per tuned default)")
	}
	if cfg.Tools.Enabled.DelegateTo {
		t.Errorf("Tools.Enabled.DelegateTo = true, want false (opt-in per tuned default)")
	}
}

// TestDefaultConfig_PythonToolEnabled verifies the python tool is opt-OUT:
// enabled by default via the embedded config.
func TestDefaultConfig_PythonToolEnabled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.Tools.Enabled.PythonEnabled {
		t.Errorf("Tools.Enabled.PythonEnabled = false, want true (python is opt-out)")
	}
}
