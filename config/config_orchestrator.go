// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

type MultiAgentConfig struct {
	Enabled                bool   `yaml:"enabled"`
	Pattern                string `yaml:"pattern"`
	MaxCompanionCycles     int    `yaml:"max_companion_cycles"`
	CompanionProvider      string `yaml:"companion_provider"`
	CompanionModel         string `yaml:"companion_model"`
	PlannerModel           string `yaml:"planner_model"`
	CoderModel             string `yaml:"coder_model"`
	MessageTimeout         string `yaml:"message_timeout"`
	ShowInterAgentMessages bool   `yaml:"show_inter_agent_messages"`
}

// OrchestratorConfig configures the orchestrator subsystem: per-run topology
// selection, a config-only role→model map, a bounded agent pool with
// total + per-model caps, and retention settings. See docs/ORCHESTRATION-DESIGN.md.
type OrchestratorConfig struct {
	Roles     map[string]OrchestratorRole `yaml:"roles,omitempty"`
	Pool      OrchestratorPoolConfig      `yaml:"pool,omitempty"`
	Defaults  OrchestratorDefaultsConfig  `yaml:"defaults,omitempty"`
	Retention OrchestratorRetentionConfig `yaml:"retention,omitempty"`
}

// OrchestratorRetentionConfig controls how long finished orchestration runs
// are kept on disk. Enabled=false or Days=0 means "keep forever".
type OrchestratorRetentionConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
}

// OrchestratorRole binds a role to a specific model/provider, tool allowlist,
// and optional context-window/max-tokens limits for worker agents.
type OrchestratorRole struct {
	Model         string   `yaml:"model"`
	Provider      string   `yaml:"provider,omitempty"`
	AllowedTools  []string `yaml:"allowed_tools,omitempty"`
	ContextWindow int      `yaml:"context_window,omitempty"` // tokens; 0 = model default
	MaxTokens     int      `yaml:"max_tokens,omitempty"`     // compression threshold; 0 = default
}

// OrchestratorPoolConfig bounds the live agent pool.
type OrchestratorPoolConfig struct {
	MaxTotalAgents    int            `yaml:"max_total_agents"`
	MaxAgentsPerModel map[string]int `yaml:"max_agents_per_model,omitempty"`
}

// OrchestratorDefaultsConfig holds default topology selection for new runs.
type OrchestratorDefaultsConfig struct {
	Topology        string `yaml:"topology"`
	RunTimeout      string `yaml:"run_timeout,omitempty"`      // per-run absolute wall-clock budget, e.g. "1h"; empty/invalid falls back to 1h
	ActivityTimeout string `yaml:"activity_timeout,omitempty"` // reset while events flow; empty/invalid falls back to 2m
}

// GoalsConfig controls the durable goal subsystem.
