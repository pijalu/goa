// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
)

// danglingModelRefs returns every dangling reference to id left in cfg:
// team member models, orchestrator role models, per-model pool caps,
// per-model compression overrides, and active_model. Used to assert the
// delete paths leave zero dangling references (B-CfgStaleModel).
func danglingModelRefs(cfg *config.Config, id string) []string {
	var out []string
	out = append(out, teamModelRefs(cfg, id)...)
	out = append(out, orchestratorModelRefs(cfg, id)...)
	if _, ok := cfg.ContextCompression.PerModel[id]; ok {
		out = append(out, "context_compression.per_model."+id)
	}
	if cfg.ActiveModel == id {
		out = append(out, "active_model")
	}
	return out
}

// teamModelRefs returns the team definition paths whose member model is id.
func teamModelRefs(cfg *config.Config, id string) []string {
	var out []string
	for teamName, def := range cfg.Teams.Definitions {
		if def.Main != nil && def.Main.Model == id {
			out = append(out, "teams.definitions."+teamName+".main.model")
		}
		if def.Companion != nil && def.Companion.Model == id {
			out = append(out, "teams.definitions."+teamName+".companion.model")
		}
		for name, m := range def.Members {
			if m.Model == id {
				out = append(out, "teams.definitions."+teamName+".members."+name+".model")
			}
		}
	}
	return out
}

// orchestratorModelRefs returns the orchestrator paths whose model is id.
func orchestratorModelRefs(cfg *config.Config, id string) []string {
	var out []string
	for name, role := range cfg.Orchestrator.Roles {
		if role.Model == id {
			out = append(out, "orchestrator.roles."+name+".model")
		}
	}
	if _, ok := cfg.Orchestrator.Pool.MaxAgentsPerModel[id]; ok {
		out = append(out, "orchestrator.pool.max_agents_per_model."+id)
	}
	return out
}

// modelRefConfig builds a config with one model (ox-alpha) referenced from
// every model-bearing field, so a deletion can be checked for full cleanup.
func modelRefConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := twoProviderConfig(t)
	cfg.ActiveModel = "ox-alpha"
	cfg.ContextCompression.PerModel = map[string]config.ModelCompressionOverride{
		"ox-alpha": {Strategy: "summarize"},
	}
	cfg.Teams.Definitions = map[string]config.TeamDefinition{
		"Loco": {
			Main:      &config.TeamMember{Model: "ox-alpha"},
			Companion: &config.TeamMember{Model: "ox-alpha"},
			Members:   map[string]config.TeamMember{"worker": {Model: "ox-alpha", Role: "worker"}},
			Review:    "off",
		},
	}
	cfg.Orchestrator.Roles = map[string]config.OrchestratorRole{
		"worker": {Model: "ox-alpha"},
	}
	cfg.Orchestrator.Pool.MaxAgentsPerModel = map[string]int{"ox-alpha": 2}
	return cfg
}

// TestRemoveModelFromConfig_ClearsAllModelRefs pins the delete-time cleanup
// for the /model removal path: every config reference to the removed model
// (team member models, orchestrator role models, pool caps, compression
// overrides, active_model) must be cleared, leaving zero dangling references
// (B-CfgStaleModel).
func TestRemoveModelFromConfig_ClearsAllModelRefs(t *testing.T) {
	cfg := modelRefConfig(t)
	ctx, _ := newPickerTestContext(t, cfg)

	removeModelFromConfig(cfg, "ox-alpha", ctx.ConfigSaver, *ctx)

	if refs := danglingModelRefs(cfg, "ox-alpha"); len(refs) > 0 {
		t.Errorf("dangling references after removeModelFromConfig: %v", refs)
	}
	if _, ok := cfg.Teams.Definitions["Loco"]; !ok {
		t.Errorf("team definition must survive model removal: %v", cfg.Teams.Definitions)
	}
	if _, ok := cfg.Orchestrator.Roles["worker"]; !ok {
		t.Errorf("orchestrator role must survive model removal: %v", cfg.Orchestrator.Roles)
	}
}

// TestDoRemoveModel_ClearsAllModelRefs pins the same cleanup for the /config
// model-manager removal path (doRemoveModel).
func TestDoRemoveModel_ClearsAllModelRefs(t *testing.T) {
	cfg := modelRefConfig(t)
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	m := &configMenu{ctx: *ctx}

	m.doRemoveModel("ox-alpha")

	if refs := danglingModelRefs(cfg, "ox-alpha"); len(refs) > 0 {
		t.Errorf("dangling references after doRemoveModel: %v", refs)
	}
}
