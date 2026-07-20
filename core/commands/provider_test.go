// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// TestProviderCommand_NoProviders_WritesMessage verifies that /provider
// (no args) writes a helpful message when no providers are configured.
func TestProviderCommand_NoProviders_WritesMessage(t *testing.T) {
	cmd := &ProviderCommand{}
	var buf strings.Builder
	ctx := core.Context{OutputBuffer: &buf, Config: &config.Config{}}

	if err := cmd.Run(ctx, nil); err != nil {
		t.Errorf("Run() returned error: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "Current provider") {
		t.Errorf("Run() output = %q, want substring 'Current provider'", out)
	}
}

// TestProviderCommand_StatusShowsCurrent verifies /provider? prints the live
// provider + model snapshot instead of the static help text.
func TestProviderCommand_StatusShowsCurrent(t *testing.T) {
	cmd := &ProviderCommand{}
	ctx := core.Context{Config: &config.Config{
		ActiveProvider: "openai",
		ActiveModel:    "gpt-4o-mini",
	}}
	got := cmd.Status(ctx)
	if !strings.Contains(got, "openai") || !strings.Contains(got, "gpt-4o-mini") {
		t.Errorf("Status() = %q, want substring openai and gpt-4o-mini", got)
	}
}

// TestProviderCommand_StatusEmptyWhenNoConfig guards against nil-config panics.
func TestProviderCommand_StatusEmptyWhenNoConfig(t *testing.T) {
	cmd := &ProviderCommand{}
	if got := cmd.Status(core.Context{}); got != "" {
		t.Errorf("Status() with nil config = %q, want empty", got)
	}
}

// TestProviderCommand_WithArgs_SwitchesProvider verifies /provider:id switches
// and persists provider/model config to the home directory.
func TestProviderCommand_WithArgs_SwitchesProvider(t *testing.T) {
	cmd := &ProviderCommand{}
	var buf strings.Builder
	cfg := &config.Config{ActiveProvider: "old", Providers: []config.ProviderConfig{
		{ID: "openai", Name: "OpenAI"},
	}}
	saver := &fakeConfigSaver{}
	ctx := core.Context{OutputBuffer: &buf, Config: cfg, ConfigSaver: saver}
	if err := cmd.Run(ctx, []string{"openai"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cfg.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want openai", cfg.ActiveProvider)
	}
	if !strings.Contains(buf.String(), "Switched to provider") {
		t.Errorf("output = %q, want 'Switched to provider'", buf.String())
	}
	if saver.savedCfg == nil {
		t.Error("expected provider switch to be persisted")
	}
	if saver.savedCfg.ActiveProvider != "openai" {
		t.Errorf("saved ActiveProvider = %q, want openai", saver.savedCfg.ActiveProvider)
	}
}

// TestProviderCommand_PickerAddRunsWizard is a regression test for the bug
// where pressing '+' in /provider's selector emitted "__add__" and the picker
// treated it as a provider ID — switching to provider "__add__" and persisting
// it. The '+' hotkey must open the add-provider wizard instead.
func TestProviderCommand_PickerAddRunsWizard(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "k3",
		Providers: []config.ProviderConfig{
			{ID: "kimi-code", Name: "Kimi Code", Endpoint: "https://api.kimi.com/coding/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "k3", ProviderID: "kimi-code", Model: "k3"},
		},
	}
	ctx, sr, ir, _ := newMenuTestContext(t, cfg)
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver

	cmd := &ProviderCommand{}
	if err := cmd.Run(*ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sr.title != "Select provider:" {
		t.Fatalf("expected provider picker, got %q", sr.title)
	}

	// User presses '+' — the selector emits "__add__".
	sr.onSel("__add__", true)

	// The wizard asks for the provider type; it must NOT switch to "__add__".
	if sr.title != "Select provider type:" {
		t.Fatalf("expected add-provider wizard, got selector %q", sr.title)
	}
	if cfg.ActiveProvider == "__add__" {
		t.Fatal("ActiveProvider was set to __add__ — the sentinel leaked into config")
	}
	if cfg.ActiveProvider != "kimi-code" {
		t.Errorf("ActiveProvider = %q, want unchanged kimi-code", cfg.ActiveProvider)
	}

	// Pick the zai preset; it needs an API key.
	sr.onSel("zai", true)
	if !strings.HasPrefix(ir.prompt, "API key for ") {
		t.Fatalf("expected API key prompt, got %q", ir.prompt)
	}
	ir.onSub("zai-key-123", true)

	if got := cfg.GetProviderByID("zai"); got == nil {
		t.Fatal("provider zai was not added to config")
	} else {
		if got.Endpoint != "https://api.z.ai/api/coding/paas/v4" {
			t.Errorf("zai endpoint = %q, want coding endpoint", got.Endpoint)
		}
		if got.APIKey != "zai-key-123" {
			t.Errorf("zai APIKey = %q, want zai-key-123", got.APIKey)
		}
	}
	if saver.savedCfg == nil {
		t.Error("expected the added provider to be persisted")
	}
	if cfg.ActiveProvider != "kimi-code" {
		t.Errorf("ActiveProvider = %q after add, want unchanged kimi-code", cfg.ActiveProvider)
	}
}

// TestProviderCommand_PickerAddCancelLeavesConfig verifies cancelling the
// wizard leaves provider state untouched.
func TestProviderCommand_PickerAddCancelLeavesConfig(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers:      []config.ProviderConfig{{ID: "openai", Name: "OpenAI"}},
	}
	ctx, sr, _, _ := newMenuTestContext(t, cfg)
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver

	cmd := &ProviderCommand{}
	if err := cmd.Run(*ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sr.onSel("__add__", true)
	// Cancel the preset selection.
	sr.onSel("", false)

	if cfg.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want openai", cfg.ActiveProvider)
	}
	if len(cfg.Providers) != 1 {
		t.Errorf("Providers = %d, want 1 (no provider added)", len(cfg.Providers))
	}
}

// TestRouterExecute_ProviderStatusSuffix verifies the router dispatches the
// "?" suffix through StatusProvider when implemented.
func TestRouterExecute_ProviderStatusSuffix(t *testing.T) {
	reg := core.NewCommandRegistry()
	reg.Register(&ProviderCommand{})
	docEng := core.NewDocEngine(reg)
	router := core.NewCommandRouter(reg, docEng)

	result := router.Parse("/provider?")
	if result == nil || result.Command == nil {
		t.Fatal("Parse('/provider?') should resolve")
	}
	if !result.IsHelp || result.DocLevel != core.DocSuffixShort {
		t.Fatalf("DocLevel = %v, want DocSuffixShort", result.DocLevel)
	}

	out, err := router.Execute(core.Context{Config: &config.Config{
		ActiveProvider: "anthropic",
		ActiveModel:    "claude-3-5",
	}}, result)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "anthropic") {
		t.Errorf("Execute('/provider?') = %q, want substring 'anthropic'", out)
	}
}

// TestRouterParseUnknownCommand ensures unknown commands don't panic.
func TestRouterParseUnknownCommand(t *testing.T) {
	reg := core.NewCommandRegistry()
	docEng := core.NewDocEngine(reg)
	router := core.NewCommandRouter(reg, docEng)

	result := router.Parse("/nonexistent")
	if result == nil {
		t.Fatal("Parse('/nonexistent') returned nil")
	}
	if result.Command != nil {
		t.Fatal("Command should be nil for unknown command")
	}
	if result.CmdName != "nonexistent" {
		t.Errorf("CmdName = %q, want %q", result.CmdName, "nonexistent")
	}

	output, err := router.Execute(core.Context{}, result)
	if err != nil {
		t.Errorf("Execute() returned error: %v", err)
	}
	expected := "Unknown command: /nonexistent"
	if !strings.Contains(output, expected) {
		t.Errorf("Execute() output = %q, want it to contain %q", output, expected)
	}
}

// TestRouterParse_RemovedCommands verifies the duplicate commands have been
// removed from the namespace (regression guard against accidental reintroduction).
func TestRouterParse_RemovedCommands(t *testing.T) {
	reg := core.NewCommandRegistry()
	if _, ok := reg.Resolve("providers"); ok {
		t.Error("/providers should not be registered (consolidated into /provider)")
	}
	if _, ok := reg.Resolve("prs"); ok {
		t.Error("/prs should not be registered (consolidated into /provider)")
	}
	if _, ok := reg.Resolve("models"); ok {
		t.Error("/models should not be registered (consolidated into /model)")
	}
	if _, ok := reg.Resolve("profiles"); ok {
		t.Error("/profiles should not be registered (consolidated into /profile)")
	}
}

// TestProviderCommand_SwitchClearsForeignModel is the regression for
// "Switched to: zai / k3": switching to a provider with no configured models
// must not leave the previous provider's model active. A preset provider
// falls back to its default model; a custom provider clears the model.
func TestProviderCommand_SwitchClearsForeignModel(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "k3",
		Providers: []config.ProviderConfig{
			{ID: "kimi-code", Name: "Kimi Code", Endpoint: "https://api.kimi.com/coding/v1"},
			{ID: "zai", Name: "Z.ai Coding", Endpoint: "https://api.z.ai/api/coding/paas/v4"},
			{ID: "my-custom", Name: "Custom", Endpoint: "http://example.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "k3", ProviderID: "kimi-code", Model: "k3"},
		},
	}
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver

	// Switch to zai (preset): model must become the preset default, not stay k3.
	applyProviderSelection(*ctx, cfg, saver, "zai")
	if cfg.ActiveModel == "k3" {
		t.Fatal("ActiveModel stayed k3 after switching to zai — foreign model leaked")
	}
	if cfg.ActiveModel != "glm-5.2" {
		t.Errorf("ActiveModel = %q, want preset default glm-5.2", cfg.ActiveModel)
	}

	// Switch to a custom provider with no preset: model must clear.
	cfg.ActiveModel = "glm-5.2"
	applyProviderSelection(*ctx, cfg, saver, "my-custom")
	if cfg.ActiveModel != "" {
		t.Errorf("ActiveModel = %q after switching to custom provider, want cleared", cfg.ActiveModel)
	}
}
