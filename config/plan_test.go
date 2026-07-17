// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPlanConfigDefaults(t *testing.T) {
	base, err := DefaultConfigYAML()
	if err != nil {
		t.Fatalf("DefaultConfigYAML: %v", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(base), cfg); err != nil {
		t.Fatalf("parse default: %v", err)
	}
	if cfg.Plan.Retention.Enabled != true {
		t.Errorf("default plan.retention.enabled = %v, want true", cfg.Plan.Retention.Enabled)
	}
	if cfg.Plan.Retention.Days != 7 {
		t.Errorf("default plan.retention.days = %d, want 7", cfg.Plan.Retention.Days)
	}
}

func TestPlanConfigParse(t *testing.T) {
	y := `
plan:
  retention:
    enabled: false
    days: 0
`
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(y), cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Plan.Retention.Enabled != false {
		t.Errorf("enabled = %v, want false", cfg.Plan.Retention.Enabled)
	}
	if cfg.Plan.Retention.Days != 0 {
		t.Errorf("days = %d, want 0", cfg.Plan.Retention.Days)
	}
}

func TestPlanConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     PlanConfig
		wantSub string // empty = no error expected
	}{
		{
			name: "empty is valid",
			cfg:  PlanConfig{},
		},
		{
			name: "zero retention days accepted",
			cfg: PlanConfig{
				Retention: PlanRetentionConfig{Enabled: true, Days: 0},
			},
		},
		{
			name: "negative retention days rejected",
			cfg: PlanConfig{
				Retention: PlanRetentionConfig{Enabled: true, Days: -1},
			},
			wantSub: "plan.retention.days",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Plan: tc.cfg}
			err := c.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestPlanConfigMerge(t *testing.T) {
	base := &Config{
		Plan: PlanConfig{
			Retention: PlanRetentionConfig{Enabled: true, Days: 14},
		},
	}
	override := &Config{
		Plan: PlanConfig{
			Retention: PlanRetentionConfig{Enabled: true, Days: 30},
		},
	}
	base.DeepMerge(override)
	if base.Plan.Retention.Days != 30 {
		t.Errorf("after merge days = %d, want 30", base.Plan.Retention.Days)
	}
	if base.Plan.Retention.Enabled != true {
		t.Errorf("after merge enabled = %v, want true", base.Plan.Retention.Enabled)
	}
}

func TestPlanConfigMergePreservesBase(t *testing.T) {
	base := &Config{
		Plan: PlanConfig{
			Retention: PlanRetentionConfig{Enabled: true, Days: 7},
		},
	}
	override := &Config{
		Plan: PlanConfig{}, // empty — should not override
	}
	base.DeepMerge(override)
	if base.Plan.Retention.Enabled != true {
		t.Errorf("enabled should remain true")
	}
	if base.Plan.Retention.Days != 7 {
		t.Errorf("days should remain 7")
	}
}

func TestOrchestratorRoleContextWindow(t *testing.T) {
	y := `
orchestrator:
  roles:
    coder:
      model: "gpt-4"
      context_window: 16384
      max_tokens: 8192
`
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(y), cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	role := cfg.Orchestrator.Roles["coder"]
	if role.ContextWindow != 16384 {
		t.Errorf("context_window = %d, want 16384", role.ContextWindow)
	}
	if role.MaxTokens != 8192 {
		t.Errorf("max_tokens = %d, want 8192", role.MaxTokens)
	}
}

func TestOrchestratorRoleValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     OrchestratorRole
		wantSub string // empty = no error expected
	}{
		{
			name: "valid zero values",
			cfg:  OrchestratorRole{Model: "gpt-4"},
		},
		{
			name: "negative context_window rejected",
			cfg:  OrchestratorRole{Model: "gpt-4", ContextWindow: -1},
			wantSub: "context_window",
		},
		{
			name: "negative max_tokens rejected",
			cfg:  OrchestratorRole{Model: "gpt-4", MaxTokens: -1},
			wantSub: "max_tokens",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				Models: []ModelConfig{{ID: "gpt-4"}},
				Orchestrator: OrchestratorConfig{
					Roles: map[string]OrchestratorRole{"coder": tc.cfg},
				},
			}
			err := c.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}
