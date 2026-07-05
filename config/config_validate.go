// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"time"

	"github.com/pijalu/goa/internal"
)

func (c *Config) Validate() error {
	var ve internal.ValidationError
	c.validateMode(&ve)
	c.validateWorktree(&ve)
	c.validateActiveProvider(&ve)
	c.validateTimeout(&ve)
	c.validateLoopThresholds(&ve)
	c.validateAgenticProviders(&ve)
	c.validateAgenticModels(&ve)
	c.validateContextCompression(&ve)
	c.validateSkillMode(&ve)
	c.validateOrchestrator(&ve)
	if ve.HasErrors() {
		return &ve
	}
	return nil
}

func (c *Config) validateMode(ve *internal.ValidationError) {
	switch c.Execution.Mode {
	case internal.ExecutionYolo, internal.ExecutionSolo, internal.ExecutionConfirm, internal.ExecutionReview, "":
		return
	default:
		ve.Add(fmt.Sprintf("execution.mode: must be one of 'yolo', 'solo', 'confirm', or 'review' (got %q)", c.Execution.Mode))
	}
}

func (c *Config) validateWorktree(ve *internal.ValidationError) {
	switch c.Execution.WorktreeMode {
	case internal.WorktreeAlways, internal.WorktreeMultiAgent, "":
		return
	default:
		ve.Add(fmt.Sprintf("execution.worktree_mode: must be 'always' or 'multi_agent' (got %q)", c.Execution.WorktreeMode))
	}
}

func (c *Config) validateActiveProvider(ve *internal.ValidationError) {
	if c.ActiveProvider == "" {
		return
	}
	// Skip provider validation if no providers are configured yet
	if len(c.Providers) == 0 {
		return
	}
	for _, p := range c.Providers {
		if p.ID == c.ActiveProvider {
			return
		}
	}
	ve.Add(fmt.Sprintf("active_provider: provider %q not found in providers list", c.ActiveProvider))
}

func (c *Config) validateTimeout(ve *internal.ValidationError) {
	if c.Execution.ActivityTimeout == "" {
		return
	}
	if _, err := time.ParseDuration(c.Execution.ActivityTimeout); err != nil {
		ve.Add(fmt.Sprintf("execution.activity_timeout: cannot parse %q as duration: %v", c.Execution.ActivityTimeout, err))
	}
}

func (c *Config) validateLoopThresholds(ve *internal.ValidationError) {
	if c.Execution.LoopWarning <= 0 || c.Execution.LoopInterrupt <= 0 {
		return
	}
	if c.Execution.LoopWarning >= c.Execution.LoopInterrupt {
		ve.Add(fmt.Sprintf("execution.loop_warning (%d) must be less than loop_interrupt (%d)",
			c.Execution.LoopWarning, c.Execution.LoopInterrupt))
	}
	// Validate tool repeat thresholds: consecutive must not exceed total.
	if c.Execution.MaxToolRepeatConsecutive > 0 && c.Execution.MaxToolRepeatTotal > 0 &&
		c.Execution.MaxToolRepeatConsecutive > c.Execution.MaxToolRepeatTotal {
		ve.Add(fmt.Sprintf("execution.max_tool_repeat_consecutive (%d) must not exceed execution.max_tool_repeat_total (%d)",
			c.Execution.MaxToolRepeatConsecutive, c.Execution.MaxToolRepeatTotal))
	}
}

func (c *Config) validateAgenticProviders(ve *internal.ValidationError) {
	for _, p := range c.Providers {
		c.validateProviderIdentity(ve, p)
		c.validateProviderTransport(ve, p)
		c.validateProviderCache(ve, p)
		c.validateProviderRetryDelay(ve, p)
	}
}

func (c *Config) validateProviderIdentity(ve *internal.ValidationError, p ProviderConfig) {
	if !IsValidAgenticProvider(p.Provider) {
		ve.Add(fmt.Sprintf("providers.%s.provider: unknown agentic provider %q", p.ID, p.Provider))
	}
	if !IsValidAgenticAPI(p.API) {
		ve.Add(fmt.Sprintf("providers.%s.api: unknown agentic API %q", p.ID, p.API))
	}
}

func (c *Config) validateProviderTransport(ve *internal.ValidationError, p ProviderConfig) {
	if p.Transport == "" || p.Transport == AgenticTransportSSE || p.Transport == AgenticTransportWebSocket {
		return
	}
	ve.Add(fmt.Sprintf("providers.%s.transport: must be %q or %q", p.ID, AgenticTransportSSE, AgenticTransportWebSocket))
}

func (c *Config) validateProviderCache(ve *internal.ValidationError, p ProviderConfig) {
	if p.CacheRetention == "" || p.CacheRetention == AgenticCacheRetentionNone ||
		p.CacheRetention == AgenticCacheRetentionShort || p.CacheRetention == AgenticCacheRetentionLong {
		return
	}
	ve.Add(fmt.Sprintf("providers.%s.cache_retention: must be one of none/short/long", p.ID))
}

func (c *Config) validateProviderRetryDelay(ve *internal.ValidationError, p ProviderConfig) {
	if p.MaxRetryDelay == "" {
		return
	}
	if _, err := time.ParseDuration(p.MaxRetryDelay); err != nil {
		ve.Add(fmt.Sprintf("providers.%s.max_retry_delay: cannot parse %q as duration: %v", p.ID, p.MaxRetryDelay, err))
	}
}

func (c *Config) validateAgenticModels(ve *internal.ValidationError) {
	for _, m := range c.Models {
		if !IsValidAgenticAPI(m.API) {
			ve.Add(fmt.Sprintf("models.%s.api: unknown agentic API %q", m.ID, m.API))
		}
		if !IsValidAgenticProvider(m.Provider) {
			ve.Add(fmt.Sprintf("models.%s.provider_name: unknown agentic provider %q", m.ID, m.Provider))
		}
		if m.ThinkingLevel != "" && m.ThinkingLevel != AgenticThinkingOff && m.ThinkingLevel != AgenticThinkingMinimal && m.ThinkingLevel != AgenticThinkingLow && m.ThinkingLevel != AgenticThinkingMedium && m.ThinkingLevel != AgenticThinkingHigh && m.ThinkingLevel != AgenticThinkingXHigh {
			ve.Add(fmt.Sprintf("models.%s.thinking_level: unknown thinking level %q", m.ID, m.ThinkingLevel))
		}
	}
}

func (c *Config) validateContextCompression(ve *internal.ValidationError) {
	cc := c.ContextCompression
	if !cc.Enabled {
		return
	}
	if cc.Strategy != "" && cc.Strategy != AgenticCompressionToolElision && cc.Strategy != AgenticCompressionSelective && cc.Strategy != AgenticCompressionSummarize && cc.Strategy != AgenticCompressionHybrid && cc.Strategy != AgenticCompressionMicro {
		ve.Add(fmt.Sprintf("context_compression.strategy: unknown strategy %q", cc.Strategy))
	}
	if cc.ThresholdPercent < 0 || cc.ThresholdPercent > 100 {
		ve.Add(fmt.Sprintf("context_compression.threshold_percent: must be 0-100 (got %d)", cc.ThresholdPercent))
	}
}

func (c *Config) validateSkillMode(ve *internal.ValidationError) {
	if c.Skills.ExecutionMode == "" {
		return
	}
	if c.Skills.ExecutionMode != AgenticSkillModeSubAgent && c.Skills.ExecutionMode != AgenticSkillModeInline {
		ve.Add(fmt.Sprintf("skills.execution_mode: must be %q or %q", AgenticSkillModeSubAgent, AgenticSkillModeInline))
	}
}

// Orchestrator topology values accepted by the per-run selector.
const (
	OrchestratorTopologyHub      = "hub"
	OrchestratorTopologyFanout   = "fanout"
	OrchestratorTopologyPipeline = "pipeline"
)

func (c *Config) validateOrchestrator(ve *internal.ValidationError) {
	oc := c.Orchestrator
	switch oc.Defaults.Topology {
	case "", OrchestratorTopologyHub, OrchestratorTopologyFanout, OrchestratorTopologyPipeline:
	default:
		ve.Add(fmt.Sprintf("orchestrator.defaults.topology: must be %q, %q, or %q (got %q)",
			OrchestratorTopologyHub, OrchestratorTopologyFanout, OrchestratorTopologyPipeline,
			oc.Defaults.Topology))
	}
	// When no models are configured at all (early bootstrap), skip model
	// existence checks so an empty embedded config validates cleanly.
	skipModelCheck := len(c.Models) == 0
	knownModels := make(map[string]struct{}, len(c.Models))
	for _, m := range c.Models {
		knownModels[m.ID] = struct{}{}
	}
	for name, role := range oc.Roles {
		if role.Model == "" {
			ve.Add(fmt.Sprintf("orchestrator.roles.%s.model: must be set", name))
			continue
		}
		if !skipModelCheck {
			if _, ok := knownModels[role.Model]; !ok {
				ve.Add(fmt.Sprintf("orchestrator.roles.%s.model: model %q not found in models list", name, role.Model))
			}
		}
	}
	if oc.Pool.MaxTotalAgents < 0 {
		ve.Add(fmt.Sprintf("orchestrator.pool.max_total_agents: must be >= 0 (got %d)", oc.Pool.MaxTotalAgents))
	}
	for m, n := range oc.Pool.MaxAgentsPerModel {
		if n < 1 {
			ve.Add(fmt.Sprintf("orchestrator.pool.max_agents_per_model.%s: must be >= 1 (got %d)", m, n))
		}
	}
}
