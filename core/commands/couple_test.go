// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// failingProviderManager rejects every SetActive call, simulating a manager
// whose config copy does not know the requested provider (e.g. after a hot
// reload diverged from the boot config object).
type failingProviderManager struct {
	testProviderManager
	err error
}

func (p *failingProviderManager) SetActive(providerID, model string) error {
	return p.err
}

// coupleTestContext wires the shared fixture: two providers with one model
// each, an agent manager with a live agent, and a recording provider manager.
func coupleTestContext(t *testing.T) (core.Context, *recordingProviderManager, *core.AgentManager, *config.Config) {
	t.Helper()
	ctx := newModeTestContext()
	ctx.Config.ActiveProvider = "local"
	ctx.Config.ActiveModel = "llama3"
	ctx.Config.Providers = []config.ProviderConfig{
		{ID: "local", Endpoint: "http://localhost:1234/v1"},
		{ID: "openai", Endpoint: "https://api.openai.com/v1"},
	}
	ctx.Config.Models = []config.ModelConfig{
		{ID: "llama3", ProviderID: "local", Model: "llama3"},
		{ID: "gpt-4", ProviderID: "openai", Model: "gpt-4"},
	}
	pm := &recordingProviderManager{}
	pm.model = "llama3"
	ctx.ProviderManager = pm
	am := newTestAgentManager()
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{
		Model: agenticprovider.Model{ID: "llama3"},
	}))
	ctx.AgentManager = am
	saver := &fakeConfigSaver{}
	ctx.ConfigSaver = saver
	var buf strings.Builder
	ctx.OutputBuffer = &buf
	return ctx, pm, am, ctx.Config
}

// TestSwitchProvider_PropagatesCoupleToAgent is the regression test for the
// "provider not always updated" bug: /provider <id> used to mutate only the
// config, leaving the live agent session on the previous provider's model and
// stream options while the status bar already showed the new provider.
func TestSwitchProvider_PropagatesCoupleToAgent(t *testing.T) {
	ctx, pm, am, cfg := coupleTestContext(t)

	if err := switchProvider(ctx, cfg, "openai"); err != nil {
		t.Fatalf("switchProvider: %v", err)
	}

	if pm.setProvider != "openai" || pm.setModel != "gpt-4" {
		t.Errorf("manager SetActive = (%q, %q), want (openai, gpt-4)", pm.setProvider, pm.setModel)
	}
	if mdl := am.ActiveModel(); mdl.ID != "gpt-4" {
		t.Errorf("live agent model = %q, want gpt-4 (agent session must follow the switch)", mdl.ID)
	}
	if cfg.ActiveProvider != "openai" || cfg.ActiveModel != "gpt-4" {
		t.Errorf("cfg couple = (%s, %s), want (openai, gpt-4)", cfg.ActiveProvider, cfg.ActiveModel)
	}
}

// TestApplyProviderSelection_PropagatesCoupleToAgent locks the picker path to
// the same contract as /provider <id>.
func TestApplyProviderSelection_PropagatesCoupleToAgent(t *testing.T) {
	ctx, pm, am, cfg := coupleTestContext(t)

	applyProviderSelection(ctx, cfg, ctx.ConfigSaver, "openai")

	if pm.setProvider != "openai" || pm.setModel != "gpt-4" {
		t.Errorf("manager SetActive = (%q, %q), want (openai, gpt-4)", pm.setProvider, pm.setModel)
	}
	if mdl := am.ActiveModel(); mdl.ID != "gpt-4" {
		t.Errorf("live agent model = %q, want gpt-4", mdl.ID)
	}
	if cfg.ActiveProvider != "openai" || cfg.ActiveModel != "gpt-4" {
		t.Errorf("cfg couple = (%s, %s), want (openai, gpt-4)", cfg.ActiveProvider, cfg.ActiveModel)
	}
}

// TestModelCommand_ManagerRejectKeepsCouple pins the atomicity rule: when the
// provider manager rejects the couple (SetActive error), NOTHING may be
// mutated — config keeps its old values so no surface can render a mixed pair
// like "(openai-codex) <other-provider-model>".
func TestModelCommand_ManagerRejectKeepsCouple(t *testing.T) {
	ctx, _, _, cfg := coupleTestContext(t)
	failing := &failingProviderManager{
		err: &providerNotFoundError{id: "openai"},
	}
	ctx.ProviderManager = failing

	cmd := &ModelCommand{}
	err := cmd.Run(ctx, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("Run must report the failure inline, got error: %v", err)
	}
	out := ctx.OutputBuffer.String()
	if !strings.Contains(out, "Cannot switch") {
		t.Errorf("output must explain the rejected switch, got %q", out)
	}
	if cfg.ActiveProvider != "local" || cfg.ActiveModel != "llama3" {
		t.Errorf("rejected switch must leave the couple untouched, got (%s, %s)",
			cfg.ActiveProvider, cfg.ActiveModel)
	}
}

// providerNotFoundError is the SetActive failure shape from the real manager.
type providerNotFoundError struct{ id string }

func (e *providerNotFoundError) Error() string {
	return "provider " + e.id + " not found"
}

// TestApplyCoupledSwitch_KeepCurrentProvider covers the "" provider contract:
// a custom/remote model (no configured provider binding) switches the model
// while keeping the current provider — all surfaces together.
func TestApplyCoupledSwitch_KeepCurrentProvider(t *testing.T) {
	ctx, pm, am, cfg := coupleTestContext(t)

	if err := applyCoupledSwitch(ctx, cfg, ctx.ConfigSaver, "", "custom-model"); err != nil {
		t.Fatalf("applyCoupledSwitch: %v", err)
	}
	if pm.setProvider != "local" || pm.setModel != "custom-model" {
		t.Errorf("manager SetActive = (%q, %q), want (local, custom-model)", pm.setProvider, pm.setModel)
	}
	if mdl := am.ActiveModel(); mdl.ID != "custom-model" {
		t.Errorf("live agent model = %q, want custom-model", mdl.ID)
	}
	if cfg.ActiveProvider != "local" || cfg.ActiveModel != "custom-model" {
		t.Errorf("cfg couple = (%s, %s), want (local, custom-model)", cfg.ActiveProvider, cfg.ActiveModel)
	}
}
