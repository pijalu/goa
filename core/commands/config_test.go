// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// fakeConfigSaver implements config.ConfigSaver for testing.
type fakeConfigSaver struct {
	savedCfg *config.Config
	saveErr  error
}

func (f *fakeConfigSaver) Save(cfg *config.Config) error {
	f.savedCfg = cfg
	return f.saveErr
}
func (f *fakeConfigSaver) SaveProjectConfig(cfg *config.Config) error             { return f.Save(cfg) }
func (f *fakeConfigSaver) SaveHomeProvidersAndModels(cfg *config.Config) error    { return f.Save(cfg) }
func (f *fakeConfigSaver) SaveProjectProvidersAndModels(cfg *config.Config) error { return f.Save(cfg) }
func (f *fakeConfigSaver) SaveHomeField(path []string, value any) error           { return nil }
func (f *fakeConfigSaver) SaveProjectField(path []string, value any) error        { return nil }
func (f *fakeConfigSaver) Reload() (*config.Config, error)                        { return f.savedCfg, nil }

func TestDoAddProvider_New(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{}}
	saver := &fakeConfigSaver{}
	w := newWriter()

	err := doAddProvider(cfg, saver, w, "openai", "https://api.openai.com/v1", "sk-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].ID != "openai" {
		t.Errorf("expected ID openai, got %s", cfg.Providers[0].ID)
	}
	if cfg.Providers[0].APIKey != "sk-abc" {
		t.Errorf("expected APIKey sk-abc, got %s", cfg.Providers[0].APIKey)
	}
	if saver.savedCfg == nil {
		t.Error("expected config to be saved")
	}
}

func TestDoAddProvider_Existing(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{
		{ID: "openai", Name: "OpenAI", Endpoint: "old", APIKey: "old-key"},
	}}
	saver := &fakeConfigSaver{}
	w := newWriter()

	err := doAddProvider(cfg, saver, w, "openai", "https://new", "new-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers[0].Endpoint != "https://new" {
		t.Errorf("expected new endpoint, got %s", cfg.Providers[0].Endpoint)
	}
	if cfg.Providers[0].APIKey != "new-key" {
		t.Errorf("expected new key, got %s", cfg.Providers[0].APIKey)
	}
}

func TestDoAddModel_New(t *testing.T) {
	cfg := &config.Config{Models: []config.ModelConfig{}}
	saver := &fakeConfigSaver{}
	w := newWriter()

	err := doAddModel(cfg, saver, w, "gpt4", "openai", "gpt-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(cfg.Models))
	}
	if cfg.Models[0].ID != "gpt4" {
		t.Errorf("expected ID gpt4, got %s", cfg.Models[0].ID)
	}
	if cfg.Models[0].Model != "gpt-4" {
		t.Errorf("expected model name gpt-4, got %s", cfg.Models[0].Model)
	}
}

func TestSaveAndReport_NoSaver(t *testing.T) {
	cfg := &config.Config{}
	w := newWriter()
	err := saveAndReport(w, nil, cfg, "provider", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Text() == "" {
		t.Error("expected output")
	}
}

func TestSaveAndReport_WithSaver(t *testing.T) {
	cfg := &config.Config{}
	saver := &fakeConfigSaver{}
	w := newWriter()
	err := saveAndReport(w, saver, cfg, "model", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saver.savedCfg == nil {
		t.Error("expected config to be saved")
	}
}

func TestSaveAndReport_SaveError(t *testing.T) {
	cfg := &config.Config{}
	saver := &fakeConfigSaver{saveErr: fmt.Errorf("disk full")}
	w := newWriter()
	err := saveAndReport(w, saver, cfg, "provider", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should report save failure but not return error
	text := w.Text()
	if text == "" {
		t.Error("expected save failure report in output")
	}
}

func TestValidateActiveModel_Valid(t *testing.T) {
	if err := validateActiveModel("llama3"); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
}

func TestValidateActiveModel_FooterDisplay(t *testing.T) {
	// Footer display strings contain " | " or bullet \u2022
	if err := validateActiveModel("llama3 | companion-model"); err == nil {
		t.Error("expected validation error for footer display string")
	}
	if err := validateActiveModel("model • high"); err == nil {
		t.Error("expected validation error for thinking level display")
	}
}

func TestSetExecutionMode_Valid(t *testing.T) {
	for _, mode := range []string{"yolo", "solo", "confirm", "review"} {
		cfg := &config.Config{}
		if err := setExecutionMode(cfg, mode); err != nil {
			t.Errorf("expected %s valid, got error: %v", mode, err)
		}
		if cfg.Execution.Mode != internal.ExecutionMode(mode) {
			t.Errorf("expected %s, got %s", mode, cfg.Execution.Mode)
		}
	}
}

func TestSetExecutionMode_Invalid(t *testing.T) {
	cfg := &config.Config{}
	if err := setExecutionMode(cfg, "auto"); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestSetThinkingLevel_Valid(t *testing.T) {
	cfg := &config.Config{}
	for _, lvl := range []string{"off", "minimal", "low", "medium", "high", "xhigh"} {
		if err := setThinkingLevel(cfg, lvl); err != nil {
			t.Errorf("expected valid for %s, got error: %v", lvl, err)
		}
	}
}

func TestSetThinkingLevel_Invalid(t *testing.T) {
	cfg := &config.Config{}
	if err := setThinkingLevel(cfg, "extreme"); err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestDeriveModelID(t *testing.T) {
	tests := []struct{ input, expected string }{
		{"gpt-4", "gpt-4"},
		{"openai/gpt-4", "gpt-4"},
		{"qwen/qwen3.5-9b", "qwen3-5-9b"},
		{"", "model"},
		{"!!!", "model"},
	}
	for _, tc := range tests {
		got := deriveModelID(tc.input)
		if got != tc.expected {
			t.Errorf("deriveModelID(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestSetConfigField_UnknownKey(t *testing.T) {
	cfg := &config.Config{}
	if err := setConfigField(cfg, []string{"nonexistent", "key"}, "value"); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestConfigKeyCompletions(t *testing.T) {
	comps := configKeyCompletions("")
	if len(comps) == 0 {
		t.Error("expected non-empty completion list")
	}
}

func TestApplyConfigSet_ActiveModelSwitchesProvider(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		{ID: "anthropic", Endpoint: "https://api.anthropic.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "gpt-4", ProviderID: "openai", Model: "gpt-4"},
		{ID: "claude-3-5", ProviderID: "anthropic", Model: "claude-3-5-sonnet"},
	}
	ctx.Config.ActiveProvider = "openai"
	ctx.Config.ActiveModel = "gpt-4"

	am := newTestAgentManager()
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "gpt-4"},
	}))
	ctx.AgentManager = am
	pm := &recordingProviderManager{}
	ctx.ProviderManager = pm
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "active_model", "claude-3-5"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}

	if ctx.Config.ActiveModel != "claude-3-5" {
		t.Errorf("ActiveModel = %q, want claude-3-5", ctx.Config.ActiveModel)
	}
	if ctx.Config.ActiveProvider != "anthropic" {
		t.Errorf("ActiveProvider = %q, want anthropic", ctx.Config.ActiveProvider)
	}
	if pm.setProvider != "anthropic" || pm.setModel != "claude-3-5" {
		t.Errorf("provider manager SetActive = (%q, %q), want (anthropic, claude-3-5)", pm.setProvider, pm.setModel)
	}
	if mdl := am.ActiveModel(); mdl.ID != "claude-3-5" {
		t.Errorf("agent active model = %q, want claude-3-5", mdl.ID)
	}
}

func TestApplyConfigSet_ActiveModelMissingProvider(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "gpt-4", ProviderID: "openai", Model: "gpt-4"},
		{ID: "claude-3-5", ProviderID: "anthropic", Model: "claude-3-5-sonnet"},
	}
	ctx.Config.ActiveProvider = "openai"
	ctx.Config.ActiveModel = "gpt-4"

	var buf strings.Builder
	ctx.OutputBuffer = &buf
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "active_model", "claude-3-5"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}

	if ctx.Config.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want openai (should not switch to missing provider)", ctx.Config.ActiveProvider)
	}
	if ctx.Config.ActiveModel != "gpt-4" {
		t.Errorf("ActiveModel = %q, want gpt-4 (should not change)", ctx.Config.ActiveModel)
	}
	if !strings.Contains(buf.String(), "not configured") {
		t.Errorf("expected output about missing provider, got %q", buf.String())
	}
}

func TestFilteredCompletions(t *testing.T) {
	comps := filteredCompletions([]string{"yolo", "confirm", "review"}, "y", "")
	if len(comps) != 1 || comps[0].Value != "yolo" {
		t.Errorf("expected [yolo], got %v", comps)
	}
}

func TestConfigSubcommandCompletions(t *testing.T) {
	comps := configSubcommandCompletions("se")
	if len(comps) != 1 || comps[0].Value != "set" {
		t.Errorf("expected [set], got %v", comps)
	}
}
