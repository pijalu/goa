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
	// projectActiveSaved + saveOrder support Bug6 assertions: the per-project
	// active-model pin must be written BEFORE the home fallback, and a pin
	// success must suppress the home write entirely (bugs.md model scope).
	projectActiveSaved *config.Config
	saveOrder          []string
	// projectActiveErr injects a failing project layer so tests can drive the
	// home fallback path of persistModelSwitch.
	projectActiveErr error
}

func (f *fakeConfigSaver) Save(cfg *config.Config) error {
	f.savedCfg = cfg
	f.saveOrder = append(f.saveOrder, "save")
	return f.saveErr
}
func (f *fakeConfigSaver) SaveProjectConfig(cfg *config.Config) error             { return f.Save(cfg) }
func (f *fakeConfigSaver) SaveHomeProvidersAndModels(cfg *config.Config) error    { return f.Save(cfg) }
func (f *fakeConfigSaver) SaveProjectProvidersAndModels(cfg *config.Config) error { return f.Save(cfg) }

// SaveProjectActiveModel records the per-project pin and its position in the
// save order so tests can assert project-first persistence (Bug6), and can
// simulate an unchangeable project layer via projectActiveErr. Deliberately
// does NOT delegate to Save: the home write is persistModelSwitch's fallback
// decision, not part of the pin itself.
func (f *fakeConfigSaver) SaveProjectActiveModel(cfg *config.Config) error {
	f.projectActiveSaved = cfg
	f.saveOrder = append(f.saveOrder, "project_active")
	return f.projectActiveErr
}
func (f *fakeConfigSaver) SaveHomeField(path []string, value any) error    { return nil }
func (f *fakeConfigSaver) SaveProjectField(path []string, value any) error { return nil }
func (f *fakeConfigSaver) SaveProjectFieldValue(path []string, value any) error {
	return nil
}
func (f *fakeConfigSaver) SaveHomeFieldValue(path []string, value any) error {
	return nil
}
func (f *fakeConfigSaver) SaveLocalFieldValue(path []string, value any) error {
	return nil
}
func (f *fakeConfigSaver) DeleteProjectField(path []string) error { return nil }
func (f *fakeConfigSaver) DeleteHomeField(path []string) error    { return nil }
func (f *fakeConfigSaver) Reload() (*config.Config, error)        { return f.savedCfg, nil }

func TestRetryConfigSetters(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "local"}}}
	if err := setConfigField(cfg, []string{"execution", "retries"}, "5"); err != nil {
		t.Fatalf("set execution.retries: %v", err)
	}
	if cfg.Execution.Retries != 5 {
		t.Fatalf("retries = %d, want 5", cfg.Execution.Retries)
	}
	if err := setConfigField(cfg, []string{"providers", "local", "max_retry_delay"}, "5m"); err != nil {
		t.Fatalf("set provider max_retry_delay: %v", err)
	}
	if cfg.Providers[0].MaxRetryDelay != "5m" {
		t.Fatalf("max retry delay = %q, want 5m", cfg.Providers[0].MaxRetryDelay)
	}
	if err := setConfigField(cfg, []string{"providers", "local", "retry_policy", "max_retries"}, "7"); err != nil {
		t.Fatalf("set provider max retries: %v", err)
	}
	if cfg.Providers[0].RetryPolicy == nil || cfg.Providers[0].RetryPolicy.MaxRetries != 7 {
		t.Fatalf("provider max retries not set: %#v", cfg.Providers[0].RetryPolicy)
	}
	if err := setConfigField(cfg, []string{"providers", "local", "retry_policy", "backoff", "max_ms"}, "300000"); err != nil {
		t.Fatalf("set provider max ms: %v", err)
	}
	if cfg.Providers[0].RetryPolicy.Backoff.MaxMS != 300000 {
		t.Fatalf("provider max ms = %d", cfg.Providers[0].RetryPolicy.Backoff.MaxMS)
	}
	if err := setConfigField(cfg, []string{"providers", "local", "max_retry_delay"}, "invalid"); err == nil {
		t.Fatal("invalid retry delay accepted")
	}
}

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

// TestSetConfigField_TimeContext verifies the time_context /config setters
// (CX6): the enable switch, display zone, and refresh interval round-trip,
// with the interval rejecting malformed durations.
func TestSetConfigField_TimeContext(t *testing.T) {
	cfg := &config.Config{}

	if err := setConfigField(cfg, []string{"time_context", "enabled"}, "true"); err != nil {
		t.Fatalf("set time_context.enabled: %v", err)
	}
	if !cfg.TimeContext.Enabled {
		t.Error("time_context.enabled must be set to true")
	}

	if err := setConfigField(cfg, []string{"time_context", "time_zone"}, "Asia/Shanghai"); err != nil {
		t.Fatalf("set time_context.time_zone: %v", err)
	}
	if cfg.TimeContext.TimeZone != "Asia/Shanghai" {
		t.Errorf("time_context.time_zone = %q, want Asia/Shanghai", cfg.TimeContext.TimeZone)
	}

	if err := setConfigField(cfg, []string{"time_context", "refresh_interval"}, "90s"); err != nil {
		t.Fatalf("set time_context.refresh_interval: %v", err)
	}
	if cfg.TimeContext.RefreshInterval != "90s" {
		t.Errorf("time_context.refresh_interval = %q, want 90s", cfg.TimeContext.RefreshInterval)
	}

	if err := setConfigField(cfg, []string{"time_context", "refresh_interval"}, "bogus"); err == nil {
		t.Error("set time_context.refresh_interval must reject a malformed duration")
	}

	// "0" clears to the empty string (inject every eligible step).
	if err := setConfigField(cfg, []string{"time_context", "refresh_interval"}, "0"); err != nil {
		t.Fatalf("set time_context.refresh_interval=0: %v", err)
	}
	if cfg.TimeContext.RefreshInterval != "" {
		t.Errorf("time_context.refresh_interval after 0 = %q, want empty", cfg.TimeContext.RefreshInterval)
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

// TestApplyConfigSet_AutoSaveModel verifies /config:set can toggle
// execution.auto_save_model (previously only editable by hand in YAML).
// The field is tri-state (*bool): the setter must MATERIALIZE an explicit
// value, never leave it nil.
func TestApplyConfigSet_AutoSaveModel(t *testing.T) {
	ctx := newModeTestContext()
	ctx.ConfigSaver = &fakeConfigSaver{}

	if err := applyConfigSet(ctx, "execution.auto_save_model", "on"); err != nil {
		t.Fatalf("applyConfigSet on: %v", err)
	}
	if ctx.Config.Execution.AutoSaveModel == nil || !*ctx.Config.Execution.AutoSaveModel {
		t.Errorf("AutoSaveModel = %v after 'on', want materialized true", ctx.Config.Execution.AutoSaveModel)
	}

	if err := applyConfigSet(ctx, "execution.auto_save_model", "off"); err != nil {
		t.Fatalf("applyConfigSet off: %v", err)
	}
	if ctx.Config.Execution.AutoSaveModel == nil || *ctx.Config.Execution.AutoSaveModel {
		t.Errorf("AutoSaveModel = %v after 'off', want materialized false", ctx.Config.Execution.AutoSaveModel)
	}
}

// TestApplyConfigSet_ActiveModelRejectsSentinel verifies a selector sentinel
// value ("__delete__X") cannot be persisted as the active model.
func TestApplyConfigSet_ActiveModelRejectsSentinel(t *testing.T) {
	ctx := newModeTestContext()
	ctx.Config.ActiveModel = "gpt-4"
	ctx.ConfigSaver = &fakeConfigSaver{}

	var buf strings.Builder
	ctx.OutputBuffer = &buf

	if err := applyConfigSet(ctx, "active_model", "__delete__gpt-4"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if ctx.Config.ActiveModel != "gpt-4" {
		t.Errorf("ActiveModel = %q, want gpt-4 (sentinel must be rejected)", ctx.Config.ActiveModel)
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
