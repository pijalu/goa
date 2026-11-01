// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// driveWizardKeyEntry walks the exact production key-entry transition for a
// preset provider that requires an API key: ProviderType → Enter → Key screen
// → typed key → Enter (advanceFromKey).
func driveWizardKeyEntry(t *testing.T, key string) *wizardComponent {
	t.Helper()
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	w.state = stateProviderType
	w.HandleInput(tui.KeyEnter) // select focused preset → key screen
	if w.state != stateProviderKey {
		t.Fatalf("state = %d, want stateProviderKey (preset 0 requires a key)", w.state)
	}
	for _, r := range key {
		w.HandleInput(string(r))
	}
	w.HandleInput(tui.KeyEnter)
	return w
}

// B42 RED: typing an API key on the key screen and pressing Enter must keep
// the key. The transition used to commit into the slot and then re-assign the
// field from the already-cleared editor, saving an empty key.
func TestWizardQuickStart_APIKeySurvivesKeyEntry(t *testing.T) {
	const key = "sk-test-1234567890"
	w := driveWizardKeyEntry(t, key)
	if w.main.apiKey != key {
		t.Errorf("apiKey = %q, want %q — the typed key was lost on Enter", w.main.apiKey, key)
	}
	if w.state != stateProviderTest {
		t.Errorf("state = %d, want stateProviderTest", w.state)
	}
}

// B42 RED: a custom endpoint typed on the endpoint screen must survive the
// transition, along with the derived provider identity.
func TestWizardQuickStart_CustomEndpointSurvivesEndpointEntry(t *testing.T) {
	const endpoint = "https://api.example.com/v1"
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, nil, "/tmp", done)
	// Focus the Custom row so the endpoint screen is the next step.
	w.focusProvider(-1)
	w.state = stateProviderType
	w.HandleInput(tui.KeyEnter)
	if w.state != stateProviderEndpoint {
		t.Fatalf("state = %d, want stateProviderEndpoint", w.state)
	}
	for _, r := range endpoint {
		w.HandleInput(string(r))
	}
	w.HandleInput(tui.KeyEnter)

	if w.main.endpoint != endpoint {
		t.Errorf("endpoint = %q, want %q — the typed endpoint was lost on Enter", w.main.endpoint, endpoint)
	}
	if w.main.providerID != "example" {
		t.Errorf("providerID = %q, want example (derived from the endpoint host)", w.main.providerID)
	}
	if w.state != stateProviderKey {
		t.Errorf("state = %d, want stateProviderKey", w.state)
	}
}

// quickStartLoader builds a CascadeLoader rooted at temp dirs so Save writes
// into the sandbox instead of the developer's home.
func quickStartLoader(t *testing.T) (*CascadeLoader, string) {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOA_HOME", home)
	return NewCascadeLoader(project, "", nil), home
}

// driveWizardQuickStart walks the whole quick-start flow with Enter presses,
// typing only the API key (preset 0 requires one), and records every state
// the flow visited.
func driveWizardQuickStart(t *testing.T, loader *CascadeLoader) (*wizardComponent, []wizardState) {
	t.Helper()
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(&Config{}, loader, t.TempDir(), done)
	var visited []wizardState
	record := func() { visited = append(visited, w.state) }
	record()
	for i := 0; i < 32; i++ {
		if w.state == stateDone {
			break
		}
		if w.inputMode == "apikey" && w.editor.Text() == "" {
			for _, r := range "sk-flow-key-987654321" {
				w.HandleInput(string(r))
			}
		}
		w.HandleInput(tui.KeyEnter)
		record()
	}
	if w.state != stateDone {
		t.Fatalf("flow never reached stateDone; last state %d (visited %v)", w.state, visited)
	}
	return w, visited
}

// B42: the quick-start flow must ask only provider + model questions. The
// allowlist is every state the pruned flow may legally visit; anything else
// (companion, execution mode, skill mode, webfetch, dream, advanced options,
// prompt/workflow copy) is a regression to the long-form wizard.
func TestWizardQuickStart_FlowAsksOnlyProviderAndModel(t *testing.T) {
	loader, _ := quickStartLoader(t)
	_, visited := driveWizardQuickStart(t, loader)

	allowed := map[wizardState]bool{
		stateWelcome:          true,
		stateProviderType:     true,
		stateProviderEndpoint: true,
		stateProviderKey:      true,
		stateProviderTest:     true,
		stateModelSelect:      true,
		stateModelSetup:       true,
		stateModelAdvanced:    true,
		stateDone:             true,
	}
	for _, st := range visited {
		if !allowed[st] {
			t.Errorf("quick-start flow visited state %d — outside the provider+model allowlist (long-form question resurfaced)", st)
		}
	}
	if len(visited) < 2 || visited[0] != stateWelcome || visited[len(visited)-1] != stateDone {
		t.Errorf("visited = %v, want it to start at welcome and end at done", visited)
	}
}

// B42: finishing the wizard applies the quick-start defaults — coding-posture
// major, yolo autonomy (canonical mode state + legacy execution mode), the
// typed API key persisted verbatim, thinking level high, and NO companion
// enabled.
func TestWizardQuickStart_SavedConfigDefaults(t *testing.T) {
	const key = "sk-flow-key-987654321"
	loader, home := quickStartLoader(t)
	w, _ := driveWizardQuickStart(t, loader)

	cfg := w.config
	if got := cfg.DefaultModeState(); got.Major != internal.MajorMode("coding-posture") || got.Autonomy != internal.AutonomyYolo {
		t.Errorf("DefaultModeState = %s/%s, want coding-posture/yolo", got.Major, got.Autonomy)
	}
	if cfg.Execution.Mode != internal.ExecutionYolo {
		t.Errorf("Execution.Mode = %q, want yolo (legacy fallback surface)", cfg.Execution.Mode)
	}
	if cfg.MultiAgent.CompanionModel != "" || cfg.MultiAgent.CompanionProvider != "" {
		t.Errorf("companion = %q@%q, want unset — the wizard must not enable the companion", cfg.MultiAgent.CompanionModel, cfg.MultiAgent.CompanionProvider)
	}
	if len(cfg.Providers) == 0 || cfg.Providers[0].APIKey != key {
		t.Errorf("Providers[0].APIKey = %q, want the typed key %q persisted", providerKey(cfg), key)
	}
	if len(cfg.Models) == 0 || cfg.Models[0].ThinkingLevel != "high" {
		t.Errorf("Models[0].ThinkingLevel = %v, want high by default", modelThinking(cfg))
	}
	assertSavedQuickStartFile(t, home, key)
}

// The saved home config file must carry the quick-start values (a real Save
// round-trip, not just the in-memory copy).
func assertSavedQuickStartFile(t *testing.T, home, key string) {
	t.Helper()
	data := readFile(t, filepath.Join(home, ".goa", "config.yaml"))
	for _, want := range []string{"coding-posture", "yolo", key} {
		if !strings.Contains(data, want) {
			t.Errorf("saved config missing %q:\n%s", want, data)
		}
	}
}

// The skills/tools configuration stays untouched by the quick-start defaults:
// the wizard keeps the current (embedded) setup instead of writing its own.
func TestWizardQuickStart_KeepsToolsAndSkillsUntouched(t *testing.T) {
	loader, _ := quickStartLoader(t)
	cfg := &Config{}
	cfg.Tools.WebFetch.Summary.Enabled = true // a non-default embedded value
	cfg.Skills.ExecutionMode = AgenticSkillModeInline
	done := make(chan *WizardResult, 1)
	w := newWizardComponent(cfg, loader, t.TempDir(), done)
	w.main.providerID = "openai"
	w.main.providerName = "OpenAI"
	w.main.modelID = "gpt-4o"
	w.main.modelName = "gpt-4o"
	w.state = stateModelAdvanced
	w.advance() // → done + save
	w.saveConfig()

	if !cfg.Tools.WebFetch.Summary.Enabled {
		t.Errorf("Tools.WebFetch.Summary.Enabled wiped by the wizard — quick start must keep the current tool setup")
	}
	if cfg.Skills.ExecutionMode != AgenticSkillModeInline {
		t.Errorf("Skills.ExecutionMode = %q, want untouched", cfg.Skills.ExecutionMode)
	}
	if cfg.Memory.Dream.Enabled {
		t.Errorf("Memory.Dream.Enabled written by the wizard — quick start must not touch memory defaults")
	}
}

func providerKey(cfg *Config) string {
	if len(cfg.Providers) == 0 {
		return ""
	}
	return cfg.Providers[0].APIKey
}

func modelThinking(cfg *Config) interface{} {
	if len(cfg.Models) == 0 {
		return nil
	}
	return cfg.Models[0].ThinkingLevel
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
