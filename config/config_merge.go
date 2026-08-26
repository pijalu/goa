// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"github.com/pijalu/goa/internal"
)

func (c *Config) DeepMerge(other *Config) {
	c.mergeTopLevelScalars(other)
	c.mergeMode(other)
	c.mergeProviders(other)
	c.mergeModels(other)
	c.mergeProfiles(other)
	c.mergeMultiAgent(other)
	c.mergeMemory(other)
	c.mergeSkills(other)
	c.mergeTools(other)
	c.mergeTUI(other)
	c.mergePlugins(other)
	c.mergeLogging(other)
	c.mergePrompts(other)
	c.mergeThinkingLevels(other)
	c.mergeContextCompression(other)
	c.mergeTimeContext(other)
	c.mergeTelegram(other)
	c.mergePermissions(other)
	c.mergeOrchestrator(other)
	c.mergeTeams(other)
	c.mergePlan(other)
	c.mergeGoals(other)
	c.mergeFeatures(other)
	c.mergeMCP(other)
}

// mergeGoals merges the goals config section field by field. Scalars copy
// when set (string != "", int != 0 — a negative explicitly disables a
// default); VerifyCommands is a tri-state pointer so an explicit false
// overrides the embedded default true.
func (c *Config) mergeGoals(other *Config) {
	if other.Goals.Retention.Days != 0 || other.Goals.Retention.Enabled {
		c.Goals.Retention = other.Goals.Retention
	}
	if other.Goals.DoneGate != "" {
		c.Goals.DoneGate = other.Goals.DoneGate
	}
	if other.Goals.VerifyCommands != nil {
		c.Goals.VerifyCommands = other.Goals.VerifyCommands
	}
	if other.Goals.MaxVerifyFailures != 0 {
		c.Goals.MaxVerifyFailures = other.Goals.MaxVerifyFailures
	}
	if other.Goals.StallTurns != 0 {
		c.Goals.StallTurns = other.Goals.StallTurns
	}
	if other.Goals.DefaultTurnBudget != 0 {
		c.Goals.DefaultTurnBudget = other.Goals.DefaultTurnBudget
	}
	if other.Goals.Judge != "" {
		c.Goals.Judge = other.Goals.Judge
	}
	// AutoUnblock is a tri-state pointer: an explicit false in a higher
	// cascade layer overrides the embedded default true.
	if other.Goals.AutoUnblock != nil {
		c.Goals.AutoUnblock = other.Goals.AutoUnblock
	}
	// FreshContext is the same tri-state pattern (explicit false wins).
	if other.Goals.FreshContext != nil {
		c.Goals.FreshContext = other.Goals.FreshContext
	}
}

// mergeFeatures merges opt-in feature gates. Each gate is a tri-state *bool:
// an explicit value in a higher cascade layer (true or false) overrides the
// lower layer, so a gate stays reversible; nil leaves the inherited value.
func (c *Config) mergeFeatures(other *Config) {
	if other.Features.RemoteCompaction != nil {
		c.Features.RemoteCompaction = other.Features.RemoteCompaction
	}
	if other.Features.MultiAgentScrollbackReplay != nil {
		c.Features.MultiAgentScrollbackReplay = other.Features.MultiAgentScrollbackReplay
	}
}

// mergeTopLevelScalars overwrites top-level scalar fields from other when set.
func (c *Config) mergeTopLevelScalars(other *Config) {
	if other.ActiveProvider != "" {
		c.ActiveProvider = other.ActiveProvider
	}
	if other.ActiveModel != "" {
		c.ActiveModel = other.ActiveModel
	}
	if other.ActiveProfile != "" {
		c.ActiveProfile = other.ActiveProfile
	}
	mergeExecution(&c.Execution, &other.Execution)
}

// DefaultModeState returns the default ModeState for the config.
// Resolution order:
//  1. Major: mode.default.major (fallback) → "coder"
//  2. Skills: mode.default.skills

func (c *Config) mergeMode(other *Config) {
	// Merge Mode.Default scalar fields (Major, Autonomy) only if set
	if other.Mode.Default.Major != "" {
		c.Mode.Default.Major = other.Mode.Default.Major
	}
	if other.Mode.Default.Autonomy != "" {
		c.Mode.Default.Autonomy = other.Mode.Default.Autonomy
	}
	if other.Mode.Default.Skills != nil {
		c.Mode.Default.Skills = other.Mode.Default.Skills
	}
	// Merge Mode.Defaults map — last-write-wins per key
	if other.Mode.Defaults != nil {
		if c.Mode.Defaults == nil {
			c.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
		}
		for k, v := range other.Mode.Defaults {
			c.Mode.Defaults[k] = v
		}
	}
}

func mergeExecutionLoopSettings(dst, src *ExecutionConfig) {
	if src.DisableThinkingLoopDetection != nil {
		dst.DisableThinkingLoopDetection = src.DisableThinkingLoopDetection
	}
	if src.DisableToolLoopDetection != nil {
		dst.DisableToolLoopDetection = src.DisableToolLoopDetection
	}
	if src.DisableStreamLoopDetection != nil {
		dst.DisableStreamLoopDetection = src.DisableStreamLoopDetection
	}
	if src.DisableThinkingStallDetection != nil {
		dst.DisableThinkingStallDetection = src.DisableThinkingStallDetection
	}
	if src.StreamLoopMaxRepeats != 0 {
		dst.StreamLoopMaxRepeats = src.StreamLoopMaxRepeats
	}
	if src.StreamLoopMinPeriod != 0 {
		dst.StreamLoopMinPeriod = src.StreamLoopMinPeriod
	}
	if src.StreamLoopMaxStrikes != 0 {
		dst.StreamLoopMaxStrikes = src.StreamLoopMaxStrikes
	}
	if src.StreamLoopResetAfter != 0 {
		dst.StreamLoopResetAfter = src.StreamLoopResetAfter
	}
	if src.RunawayLoopMaxRepeats != 0 {
		dst.RunawayLoopMaxRepeats = src.RunawayLoopMaxRepeats
	}
}

// mergeExecution merges fields from src into dst.
func mergeExecution(dst, src *ExecutionConfig) {
	if src.Mode != "" {
		dst.Mode = src.Mode
	}
	if src.Retries != 0 {
		dst.Retries = src.Retries
	}
	if src.TokenWarning != 0 {
		dst.TokenWarning = src.TokenWarning
	}
	if src.TokenCritical != 0 {
		dst.TokenCritical = src.TokenCritical
	}
	if src.LoopWarning != 0 {
		dst.LoopWarning = src.LoopWarning
	}
	if src.LoopInterrupt != 0 {
		dst.LoopInterrupt = src.LoopInterrupt
	}
	if src.ActivityTimeout != "" {
		dst.ActivityTimeout = src.ActivityTimeout
	}
	if src.ErrorThreshold != 0 {
		dst.ErrorThreshold = src.ErrorThreshold
	}
	if src.WorktreeMode != "" {
		dst.WorktreeMode = src.WorktreeMode
	}
	dst.AutoSaveModel = src.AutoSaveModel
	dst.DisableToolBudget = src.DisableToolBudget
	// AutoHealToolCalls is the /config "Tool call fixing" toggle. Copied
	// unconditionally like its sibling bools: it has no meaningful "unset"
	// state, and skipping it here silently dropped every persisted change on
	// the next load (bugs.md /config tool fixes not saved).
	dst.AutoHealToolCalls = src.AutoHealToolCalls
	mergeIntIfSet(&dst.MaxToolRepeatTotal, src.MaxToolRepeatTotal)
	mergeIntIfSet(&dst.MaxToolRepeatConsecutive, src.MaxToolRepeatConsecutive)
	mergeIntIfSet(&dst.MaxToolCalls, src.MaxToolCalls)
	mergeIntIfSet(&dst.MaxToolErrorStreak, src.MaxToolErrorStreak)
	mergeIntIfSet(&dst.ToolCallLimitResetWindow, src.ToolCallLimitResetWindow)
	mergeIntIfSet(&dst.MaxStreamRounds, src.MaxStreamRounds)
	mergeIntIfSet(&dst.MaxConsecutiveToolRounds, src.MaxConsecutiveToolRounds)
	mergeIntIfSet(&dst.ThinkingStallWarnSeconds, src.ThinkingStallWarnSeconds)
	mergeIntIfSet(&dst.ThinkingStallStopSeconds, src.ThinkingStallStopSeconds)
	mergeExecutionLoopSettings(dst, src)
}

// mergeIntIfSet copies src into dst when src is non-zero.
func mergeIntIfSet(dst *int, src int) {
	if src != 0 {
		*dst = src
	}
}

// mergeProviders merges provider lists by ID — later providers with the same
// ID overwrite earlier ones.
func (c *Config) mergeProviders(other *Config) {
	for _, op := range other.Providers {
		found := false
		for i, cp := range c.Providers {
			if cp.ID == op.ID {
				c.Providers[i] = op
				found = true
				break
			}
		}
		if !found {
			c.Providers = append(c.Providers, op)
		}
	}
}

// mergeProfiles is a no-op now that the profile system has been removed.
// It remains so that callers do not need to change.
func (c *Config) mergeProfiles(other *Config) {
	_ = other
}

// mergeMultiAgent merges the multi-agent config section.
func (c *Config) mergeMultiAgent(other *Config) {
	if other.MultiAgent.Enabled {
		c.MultiAgent.Enabled = true
	}
	if other.MultiAgent.Pattern != "" {
		c.MultiAgent.Pattern = other.MultiAgent.Pattern
	}
	if other.MultiAgent.MaxCompanionCycles != 0 {
		c.MultiAgent.MaxCompanionCycles = other.MultiAgent.MaxCompanionCycles
	}
	if other.MultiAgent.CompanionProvider != "" {
		c.MultiAgent.CompanionProvider = other.MultiAgent.CompanionProvider
	}
	if other.MultiAgent.CompanionModel != "" {
		c.MultiAgent.CompanionModel = other.MultiAgent.CompanionModel
	}
	if other.MultiAgent.PlannerModel != "" {
		c.MultiAgent.PlannerModel = other.MultiAgent.PlannerModel
	}
	if other.MultiAgent.CoderModel != "" {
		c.MultiAgent.CoderModel = other.MultiAgent.CoderModel
	}
	if other.MultiAgent.MessageTimeout != "" {
		c.MultiAgent.MessageTimeout = other.MultiAgent.MessageTimeout
	}
	c.MultiAgent.ShowInterAgentMessages = other.MultiAgent.ShowInterAgentMessages
}

// mergeModels merges the models array by ID.
func (c *Config) mergeModels(other *Config) {
	for _, om := range other.Models {
		found := false
		for i, cm := range c.Models {
			if cm.ID == om.ID {
				c.Models[i] = om
				found = true
				break
			}
		}
		if !found {
			c.Models = append(c.Models, om)
		}
	}
}

// mergePrompts merges the prompts config section.
func (c *Config) mergePrompts(other *Config) {
	if other.Prompts.Dir != "" {
		c.Prompts.Dir = other.Prompts.Dir
	}
}

// mergeMemory merges the memory config section.
func (c *Config) mergeMemory(other *Config) {
	if other.Memory.Enabled {
		c.Memory.Enabled = true
	}
	if other.Memory.Dir != "" {
		c.Memory.Dir = other.Memory.Dir
	}
	c.Memory.AutoSummarize = other.Memory.AutoSummarize
	mergeDream(&c.Memory.Dream, &other.Memory.Dream)
}

// mergeTimeContext merges the temporal-context injection section (CX6). The
// enable switch follows the enable-only cascade pattern (default is off, so
// higher layers can only turn it on); zone and interval propagate when set.
func (c *Config) mergeTimeContext(other *Config) {
	if other.TimeContext.Enabled {
		c.TimeContext.Enabled = true
	}
	if other.TimeContext.TimeZone != "" {
		c.TimeContext.TimeZone = other.TimeContext.TimeZone
	}
	if other.TimeContext.RefreshInterval != "" {
		c.TimeContext.RefreshInterval = other.TimeContext.RefreshInterval
	}
}

func (c *Config) mergeTelegram(other *Config) {
	if other.Telegram.Enabled {
		c.Telegram.Enabled = true
	}
}

func (c *Config) mergePermissions(other *Config) {
	if other.Permissions != nil {
		c.Permissions = other.Permissions
	}
}

// mergeOrchestrator merges the orchestrator config section. Roles and
// per-model caps are merged last-write-wins per key so partial overrides at
// the project/local layer do not wipe the base role map. Scalar caps adopt
// non-zero values; topology adopts any non-empty value.
func (c *Config) mergeOrchestrator(other *Config) {
	if other.Orchestrator.Roles != nil {
		if c.Orchestrator.Roles == nil {
			c.Orchestrator.Roles = make(map[string]OrchestratorRole)
		}
		for name, r := range other.Orchestrator.Roles {
			c.Orchestrator.Roles[name] = r
		}
	}
	if other.Orchestrator.Pool.MaxTotalAgents != 0 {
		c.Orchestrator.Pool.MaxTotalAgents = other.Orchestrator.Pool.MaxTotalAgents
	}
	if other.Orchestrator.Pool.MaxAgentsPerModel != nil {
		if c.Orchestrator.Pool.MaxAgentsPerModel == nil {
			c.Orchestrator.Pool.MaxAgentsPerModel = make(map[string]int)
		}
		for m, n := range other.Orchestrator.Pool.MaxAgentsPerModel {
			c.Orchestrator.Pool.MaxAgentsPerModel[m] = n
		}
	}
	if other.Orchestrator.Defaults.Topology != "" {
		c.Orchestrator.Defaults.Topology = other.Orchestrator.Defaults.Topology
	}
	if other.Orchestrator.Defaults.RunTimeout != "" {
		c.Orchestrator.Defaults.RunTimeout = other.Orchestrator.Defaults.RunTimeout
	}
	if other.Orchestrator.Defaults.ActivityTimeout != "" {
		c.Orchestrator.Defaults.ActivityTimeout = other.Orchestrator.Defaults.ActivityTimeout
	}
}

// mergePlan merges the plan config section.
func (c *Config) mergePlan(other *Config) {
	// Last-write-wins: if the override defines any plan config, use it wholesale.
	// This matches the pattern of orchestrator role merge (map key overwrite).
	if other.Plan.Retention.Days != 0 || other.Plan.Retention.Enabled {
		c.Plan.Retention = other.Plan.Retention
	}
}
