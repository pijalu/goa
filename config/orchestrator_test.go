// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOrchestratorConfigDefaultsMerge(t *testing.T) {
	base, err := DefaultConfigYAML()
	if err != nil {
		t.Fatalf("DefaultConfigYAML: %v", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(base), cfg); err != nil {
		t.Fatalf("parse default: %v", err)
	}
	if cfg.Orchestrator.Pool.MaxTotalAgents != 8 {
		t.Errorf("default max_total_agents = %d, want 8", cfg.Orchestrator.Pool.MaxTotalAgents)
	}
	if cfg.Orchestrator.Defaults.Topology != OrchestratorTopologyHub {
		t.Errorf("default topology = %q, want hub", cfg.Orchestrator.Defaults.Topology)
	}
	if cfg.Orchestrator.Roles == nil {
		t.Errorf("default roles map should be non-nil after merge")
	}
}

func TestOrchestratorConfigMergePreservesRoles(t *testing.T) {
	base := &Config{
		Orchestrator: OrchestratorConfig{
			Roles: map[string]OrchestratorRole{
				"orchestrator": {Model: "m1"},
				"coder":        {Model: "m2"},
			},
			Pool: OrchestratorPoolConfig{MaxTotalAgents: 4},
		},
	}
	override := &Config{
		Orchestrator: OrchestratorConfig{
			Roles: map[string]OrchestratorRole{
				"coder": {Model: "m3", Provider: "p3"},
			},
			Pool:     OrchestratorPoolConfig{MaxTotalAgents: 6, MaxAgentsPerModel: map[string]int{"m3": 2}},
			Defaults: OrchestratorDefaultsConfig{Topology: OrchestratorTopologyFanout},
		},
	}
	base.DeepMerge(override)

	if got := base.Orchestrator.Roles["orchestrator"].Model; got != "m1" {
		t.Errorf("base role 'orchestrator' overwritten: got %q", got)
	}
	if got := base.Orchestrator.Roles["coder"].Model; got != "m3" {
		t.Errorf("override role 'coder' not applied: got %q", got)
	}
	if got := base.Orchestrator.Roles["coder"].Provider; got != "p3" {
		t.Errorf("override role 'coder' provider not applied: got %q", got)
	}
	if base.Orchestrator.Pool.MaxTotalAgents != 6 {
		t.Errorf("max_total_agents = %d, want 6", base.Orchestrator.Pool.MaxTotalAgents)
	}
	if got := base.Orchestrator.Pool.MaxAgentsPerModel["m3"]; got != 2 {
		t.Errorf("max_agents_per_model.m3 = %d, want 2", got)
	}
	if base.Orchestrator.Defaults.Topology != OrchestratorTopologyFanout {
		t.Errorf("topology = %q, want fanout", base.Orchestrator.Defaults.Topology)
	}
}

func TestOrchestratorConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     OrchestratorConfig
		models  []ModelConfig
		wantSub string // empty => expect no error mentioning this fragment
	}{
		{
			name:   "empty is valid",
			cfg:    OrchestratorConfig{},
			models: nil,
		},
		{
			name: "unknown model rejected when models configured",
			cfg: OrchestratorConfig{
				Roles: map[string]OrchestratorRole{"coder": {Model: "ghost"}},
			},
			models:  []ModelConfig{{ID: "real"}},
			wantSub: "orchestrator.roles.coder.model",
		},
		{
			name: "empty role model rejected",
			cfg: OrchestratorConfig{
				Roles: map[string]OrchestratorRole{"coder": {Model: ""}},
			},
			wantSub: "orchestrator.roles.coder.model: must be set",
		},
		{
			name: "bad topology",
			cfg: OrchestratorConfig{
				Defaults: OrchestratorDefaultsConfig{Topology: "star"},
			},
			wantSub: "orchestrator.defaults.topology",
		},
		{
			name: "per-model cap < 1 rejected",
			cfg: OrchestratorConfig{
				Pool: OrchestratorPoolConfig{MaxAgentsPerModel: map[string]int{"m1": 0}},
			},
			wantSub: "max_agents_per_model.m1",
		},
		{
			name: "known model accepted",
			cfg: OrchestratorConfig{
				Roles: map[string]OrchestratorRole{"coder": {Model: "real"}},
			},
			models: []ModelConfig{{ID: "real"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Orchestrator: tc.cfg, Models: tc.models}
			err := c.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", errOrEmpty(err), tc.wantSub)
			}
		})
	}
}

func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
