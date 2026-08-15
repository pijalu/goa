// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net/url"
	"strings"
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
	c.validateTeams(&ve)
	c.validateGoals(&ve)
	c.validatePlan(&ve)
	c.validateMCP(&ve)
	c.validateTools(&ve)
	if ve.HasErrors() {
		return &ve
	}
	return nil
}

// validateTools rejects invalid tool-policy values at load: a negative
// max_inline_bytes would otherwise spill every result.
func (c *Config) validateTools(ve *internal.ValidationError) {
	if c.Tools.MaxInlineBytes < 0 {
		ve.Add(fmt.Sprintf("tools.max_inline_bytes: must be a non-negative integer (got %d)", c.Tools.MaxInlineBytes))
	}
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

// validateMCP validates every configured MCP server entry.
func (c *Config) validateMCP(ve *internal.ValidationError) {
	for name, srv := range c.MCP {
		validateMCPServer(ve, name, srv)
	}
}

// validateMCPServer validates one MCP server definition.
func validateMCPServer(ve *internal.ValidationError, name string, srv MCPServerConfig) {
	prefix := "mcp." + name
	switch srv.Type {
	case MCPTypeLocal:
		if len(srv.Command) == 0 || srv.Command[0] == "" {
			ve.Add(prefix + ": local server requires a non-empty command")
		}
	case MCPTypeRemote:
		validateMCPServerURL(ve, prefix, srv.URL)
	default:
		ve.Add(fmt.Sprintf("%s: type must be %q or %q (got %q)", prefix, MCPTypeLocal, MCPTypeRemote, srv.Type))
	}
	validateMCPServerTimeout(ve, prefix, srv.Timeout)
}

// validateMCPServerURL ensures a remote MCP server has a valid absolute URL.
func validateMCPServerURL(ve *internal.ValidationError, prefix, rawURL string) {
	if rawURL == "" {
		ve.Add(prefix + ": remote server requires a url")
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		ve.Add(fmt.Sprintf("%s: invalid url %q", prefix, rawURL))
	}
}

// validateMCPServerTimeout ensures an MCP server timeout parses as a duration.
func validateMCPServerTimeout(ve *internal.ValidationError, prefix, timeout string) {
	if timeout == "" {
		return
	}
	if _, err := time.ParseDuration(timeout); err != nil {
		ve.Add(fmt.Sprintf("%s: invalid timeout %q: %v", prefix, timeout, err))
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
	c.validateLoopWarningOrder(ve)
	c.validateToolRepeatThresholds(ve)
	c.validateStreamLoopThresholds(ve)
}

// validateLoopWarningOrder checks warning < interrupt when both are set.
func (c *Config) validateLoopWarningOrder(ve *internal.ValidationError) {
	if c.Execution.LoopWarning <= 0 || c.Execution.LoopInterrupt <= 0 {
		return
	}
	if c.Execution.LoopWarning >= c.Execution.LoopInterrupt {
		ve.Add(fmt.Sprintf("execution.loop_warning (%d) must be less than loop_interrupt (%d)",
			c.Execution.LoopWarning, c.Execution.LoopInterrupt))
	}
}

// validateToolRepeatThresholds checks consecutive must not exceed total.
func (c *Config) validateToolRepeatThresholds(ve *internal.ValidationError) {
	if c.Execution.MaxToolRepeatConsecutive > 0 && c.Execution.MaxToolRepeatTotal > 0 &&
		c.Execution.MaxToolRepeatConsecutive > c.Execution.MaxToolRepeatTotal {
		ve.Add(fmt.Sprintf("execution.max_tool_repeat_consecutive (%d) must not exceed execution.max_tool_repeat_total (%d)",
			c.Execution.MaxToolRepeatConsecutive, c.Execution.MaxToolRepeatTotal))
	}
}

// validateStreamLoopThresholds checks the stream-loop detector knobs.
func (c *Config) validateStreamLoopThresholds(ve *internal.ValidationError) {
	// Stream-loop repeat threshold must be a sane repeat count when set
	// (0 means "use the default"); a single occurrence can never be a loop.
	if c.Execution.StreamLoopMaxRepeats != 0 && c.Execution.StreamLoopMaxRepeats < 2 {
		ve.Add(fmt.Sprintf("execution.stream_loop_max_repeats (%d) must be 0 (default) or >= 2",
			c.Execution.StreamLoopMaxRepeats))
	}
	// Stream-loop minimum period must stay at or above the detector's
	// absolute scan floor (8 chars): below it periods are never scanned, so
	// a smaller configured floor would silently do nothing.
	if c.Execution.StreamLoopMinPeriod != 0 && c.Execution.StreamLoopMinPeriod < 8 {
		ve.Add(fmt.Sprintf("execution.stream_loop_min_period (%d) must be 0 (default 50) or >= 8",
			c.Execution.StreamLoopMinPeriod))
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
	if !cc.EnabledValue() {
		return
	}
	if !validCompressionStrategy(cc.Strategy) {
		ve.Add(fmt.Sprintf("context_compression.strategy: unknown strategy %q", cc.Strategy))
	}
	if !validCompressionStrategy(cc.OnErrorStrategy) {
		ve.Add(fmt.Sprintf("context_compression.on_error_strategy: unknown strategy %q", cc.OnErrorStrategy))
	}
	validateLayerStrategies(ve, "context_compression.strategies", cc.Strategies)
	validateCacheGate(ve, "context_compression.cache_gate", cc.CacheGate)
	if cc.ThresholdPercent < 0 || cc.ThresholdPercent > 100 {
		ve.Add(fmt.Sprintf("context_compression.threshold_percent: must be 0-100 (got %d)", cc.ThresholdPercent))
	}
	validateCompressionThresholds(ve, "context_compression.thresholds", cc.Thresholds)
	for id, o := range cc.PerModel {
		c.validateCompressionOverride(ve, id, o)
	}
}

// validateLayerStrategies checks the per-layer strategy names; any strategy
// is allowed on any layer (the soft layer defaults to micro).
func validateLayerStrategies(ve *internal.ValidationError, path string, s CompressionLayerStrategiesConfig) {
	if !validCompressionStrategy(s.Soft) {
		ve.Add(fmt.Sprintf("%s.soft: unknown strategy %q", path, s.Soft))
	}
	if !validCompressionStrategy(s.Trigger) {
		ve.Add(fmt.Sprintf("%s.trigger: unknown strategy %q", path, s.Trigger))
	}
	if !validCompressionStrategy(s.Hard) {
		ve.Add(fmt.Sprintf("%s.hard: unknown strategy %q", path, s.Hard))
	}
}

// validateCacheGate checks the cache-gate toggle ("on"|"off"|inherit).
func validateCacheGate(ve *internal.ValidationError, path, v string) {
	if v != "" && v != "on" && v != "off" {
		ve.Add(fmt.Sprintf("%s: must be \"on\" or \"off\" (got %q)", path, v))
	}
}

// validCompressionStrategy reports whether s is empty (inherit) or a known
// compression strategy.
func validCompressionStrategy(s string) bool {
	switch s {
	case "", AgenticCompressionToolElision, AgenticCompressionSelective, AgenticCompressionSummarize, AgenticCompressionHybrid, AgenticCompressionMicro:
		return true
	}
	return false
}

// validateCompressionOverride validates one per-model override entry.
func (c *Config) validateCompressionOverride(ve *internal.ValidationError, id string, o ModelCompressionOverride) {
	prefix := fmt.Sprintf("context_compression.per_model.%s", id)
	if c.GetModelByID(id) == nil {
		ve.Add(fmt.Sprintf("%s: no model with id %q is configured", prefix, id))
	}
	if o.ThresholdPercent < 0 || o.ThresholdPercent > 100 {
		ve.Add(fmt.Sprintf("%s.threshold_percent: must be 0-100 (got %d)", prefix, o.ThresholdPercent))
	}
	validateCompressionThresholds(ve, prefix+".thresholds", o.Thresholds)
	validateLayerStrategies(ve, prefix+".strategies", o.Strategies)
	validateCacheGate(ve, prefix+".cache_gate", o.CacheGate)
	if !validCompressionStrategy(o.Strategy) {
		ve.Add(fmt.Sprintf("%s.strategy: unknown strategy %q", prefix, o.Strategy))
	}
}

// validateCompressionThresholds checks levels (5-100 in 5% increments, 0 to
// disable the layer, -1 accepted as a legacy disable spelling) and ordering
// (soft ≤ trigger ≤ hard) for a thresholds block.
func validateCompressionThresholds(ve *internal.ValidationError, path string, t CompressionThresholdsConfig) {
	validateCompressionLevel(ve, path+".soft_percent", t.SoftPercent)
	validateCompressionLevel(ve, path+".trigger_percent", t.TriggerPercent)
	validateCompressionLevel(ve, path+".hard_percent", t.HardPercent)
	validateThresholdOrder(ve, path, "soft_percent", t.SoftPercent, "trigger_percent", t.TriggerPercent)
	validateThresholdOrder(ve, path, "trigger_percent", t.TriggerPercent, "hard_percent", t.HardPercent)
	validateThresholdOrder(ve, path, "soft_percent", t.SoftPercent, "hard_percent", t.HardPercent)
}

// validateCompressionLevel checks one compression level: 0 disables the
// layer, -1 is a legacy disable spelling, otherwise 5-100 in 5% increments.
func validateCompressionLevel(ve *internal.ValidationError, path string, v int) {
	if v == 0 || v == -1 {
		return
	}
	if v < 5 || v > 100 || v%5 != 0 {
		ve.Add(fmt.Sprintf("%s: must be 5-100 in 5%% increments, 0 to disable (got %d)", path, v))
	}
}

func validatePercentRange(ve *internal.ValidationError, path string, v int) {
	if v < 0 || v > 100 {
		ve.Add(fmt.Sprintf("%s: must be 0-100 (got %d)", path, v))
	}
}

func validateThresholdOrder(ve *internal.ValidationError, path, loName string, lo int, hiName string, hi int) {
	if lo > 0 && hi > 0 && lo > hi {
		ve.Add(fmt.Sprintf("%s: %s (%d) must be ≤ %s (%d)", path, loName, lo, hiName, hi))
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
		if role.ContextWindow < 0 {
			ve.Add(fmt.Sprintf("orchestrator.roles.%s.context_window: must be >= 0 (got %d)", name, role.ContextWindow))
		}
		if role.MaxTokens < 0 {
			ve.Add(fmt.Sprintf("orchestrator.roles.%s.max_tokens: must be >= 0 (got %d)", name, role.MaxTokens))
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
	if oc.Retention.Days < 0 {
		ve.Add(fmt.Sprintf("orchestrator.retention.days: must be >= 0 (got %d)", oc.Retention.Days))
	}
}

func (c *Config) validateGoals(ve *internal.ValidationError) {
	gr := c.Goals.Retention
	if gr.Days < 0 {
		ve.Add(fmt.Sprintf("goals.retention.days: must be >= 0 (got %d)", gr.Days))
	}
	switch strings.ToLower(strings.TrimSpace(c.Goals.DoneGate)) {
	case "", "verify", "evidence", "off":
	default:
		ve.Add(fmt.Sprintf("goals.done_gate: must be verify, evidence, or off (got %q)", c.Goals.DoneGate))
	}
	if n := c.Goals.MaxVerifyFailures; n < -1 {
		ve.Add(fmt.Sprintf("goals.max_verify_failures: must be >= -1 (got %d)", n))
	}
	if n := c.Goals.StallTurns; n < -1 {
		ve.Add(fmt.Sprintf("goals.stall_turns: must be >= -1 (got %d)", n))
	}
	if n := c.Goals.DefaultTurnBudget; n < -1 {
		ve.Add(fmt.Sprintf("goals.default_turn_budget: must be >= -1 (got %d)", n))
	}
	judge := strings.ToLower(strings.TrimSpace(c.Goals.Judge))
	if judge != "" && judge != "off" && judge != "same" && !strings.HasPrefix(judge, "model:") {
		ve.Add(fmt.Sprintf("goals.judge: must be off, same, or model:<id> (got %q)", c.Goals.Judge))
	}
}

func (c *Config) validatePlan(ve *internal.ValidationError) {
	pr := c.Plan.Retention
	if pr.Days < 0 {
		ve.Add(fmt.Sprintf("plan.retention.days: must be >= 0 (got %d)", pr.Days))
	}
}
