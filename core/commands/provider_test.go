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
