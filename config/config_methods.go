// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/role"
)

func (c *Config) GetThinkingLevel(r string) internal.ThinkingLevel {
	level := c.resolveThinkingLevel(r)
	if level == "" {
		level = c.ThinkingLevels.Default
	}
	if level == "" {
		level = "medium"
	}
	return internal.ThinkingLevel(level)
}

func (c *Config) resolveThinkingLevel(r string) string {
	switch r {
	case role.Main, "main_agent":
		return c.mainAgentThinkingLevel()
	case role.Companion:
		return c.companionThinkingLevel()
	case role.Planner:
		return c.ThinkingLevels.Planner
	case role.Coder:
		return c.ThinkingLevels.Coder
	}
	return ""
}

func (c *Config) mainAgentThinkingLevel() string {
	// The active model's own thinking_level wins over the global
	// thinking_levels.main_agent default: runtime thinking-level changes are
	// saved per model, and the more specific setting must shadow the global
	// one so switching models restores each model's saved level.
	if m, err := c.GetActiveModelConfig(); err == nil && m.ThinkingLevel != "" {
		return m.ThinkingLevel
	}
	if level := c.ThinkingLevels.MainAgent; level != "" {
		return level
	}
	return ""
}

func (c *Config) companionThinkingLevel() string {
	if level := c.ThinkingLevels.Companion; level != "" {
		return level
	}
	return c.modelThinkingLevel(c.MultiAgent.CompanionModel)
}

func (c *Config) modelThinkingLevel(modelID string) string {
	if modelID == "" {
		return ""
	}
	for i := range c.Models {
		if c.Models[i].ID == modelID {
			return c.Models[i].ThinkingLevel
		}
	}
	return ""
}

// GetReasoningEffort returns the agentic ReasoningEffort for the main agent,
// derived from thinking_levels configuration.
func (c *Config) GetReasoningEffort() string {
	return string(c.GetThinkingLevel("main_agent"))
}

// GetToolResultAsUser returns the explicit provider-level override, or nil to
// let agentic auto-detect based on provider/model.
func (c *Config) GetToolResultAsUser() *bool {
	p := c.GetActiveProviderConfig()
	if p != nil && p.ToolResultAsUser != nil {
		return p.ToolResultAsUser
	}
	return nil
}

// GetActiveProviderConfig returns the active provider config, falling back to
// the preferred provider.
func (c *Config) GetActiveProviderConfig() *ProviderConfig {
	if c.ActiveProvider != "" {
		if p := c.GetProviderByID(c.ActiveProvider); p != nil {
			return p
		}
	}
	return c.PreferredProvider()
}

// GetActiveModelConfig returns the active model config.
//
// If ActiveModel is set, that model is returned. Otherwise it falls back to
// the first model (in configuration order) whose ProviderID matches the
// active provider — when several models match, resolution is deterministic
// rather than an error, so a config without an explicit active_model stays
// usable. An error is returned only when no model can be resolved at all:
// no active provider, an explicit ActiveModel that does not exist, or no
// model bound to the active provider.
func (c *Config) GetActiveModelConfig() (ModelConfig, error) {
	if c.ActiveModel != "" {
		if m := c.GetModelByID(c.ActiveModel); m != nil {
			return *m, nil
		}
		return ModelConfig{}, fmt.Errorf("active_model %q not found", c.ActiveModel)
	}
	p := c.GetActiveProviderConfig()
	if p == nil {
		return ModelConfig{}, fmt.Errorf("no active provider configured")
	}

	for i := range c.Models {
		if c.Models[i].ProviderID == p.ID {
			return c.Models[i], nil
		}
	}
	return ModelConfig{}, fmt.Errorf("no model found for provider %q", p.ID)
}

// DefaultCompressForProvider reports whether tool output compression should
// be enabled by default for the given provider. Local providers (LM Studio,
// Ollama, and custom endpoints on localhost) default to enabled since they
// often run smaller models with tighter context windows and benefit from the
// token savings. Cloud providers default to disabled — the raw output is
// typically more useful and the LLM has ample context.
//
// The effective compression setting is resolved at tool execution time:
//
//  1. Model-level compress_output (if set) → wins
//  2. Provider auto-detect (this function) → local = on, remote = off
func DefaultCompressForProvider(p *ProviderConfig) bool {
	if p == nil {
		return false
	}
	switch p.Provider {
	case AgenticProviderLMStudio, AgenticProviderOllama:
		return true
	}
	// Custom providers using localhost or 127.0.0.1 endpoints are also local.
	if p.Endpoint != "" {
		if strings.Contains(p.Endpoint, "localhost") || strings.Contains(p.Endpoint, "127.0.0.1") {
			return true
		}
	}
	return false
}

// LoggingConfig controls log output.
