// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"reflect"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tools"
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
	c.mergeTelegram(other)
	c.mergePermissions(other)
	c.mergeOrchestrator(other)
	c.mergePlan(other)
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
	mergeIntIfSet(&dst.MaxToolRepeatTotal, src.MaxToolRepeatTotal)
	mergeIntIfSet(&dst.MaxToolRepeatConsecutive, src.MaxToolRepeatConsecutive)
	mergeIntIfSet(&dst.MaxToolCalls, src.MaxToolCalls)
	mergeIntIfSet(&dst.ToolCallLimitResetWindow, src.ToolCallLimitResetWindow)
	mergeIntIfSet(&dst.ThinkingStallWarnSeconds, src.ThinkingStallWarnSeconds)
	mergeIntIfSet(&dst.ThinkingStallStopSeconds, src.ThinkingStallStopSeconds)
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

func mergeTerminal(dst, src *TerminalConfig) {
	if src.Sandbox.BlockedCommands != nil {
		dst.Sandbox.BlockedCommands = src.Sandbox.BlockedCommands
	}
	if src.Sandbox.AllowedCommands != nil {
		dst.Sandbox.AllowedCommands = src.Sandbox.AllowedCommands
	}
	if src.Sandbox.TimeoutSeconds != 0 {
		dst.Sandbox.TimeoutSeconds = src.Sandbox.TimeoutSeconds
	}
	if src.Sandbox.MaxOutputChars != 0 {
		dst.Sandbox.MaxOutputChars = src.Sandbox.MaxOutputChars
	}
	dst.Sandbox.Enabled = src.Sandbox.Enabled
	dst.Sandbox.BypassAllowed = src.Sandbox.BypassAllowed
}

func mergeDream(dst, src *DreamConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Auto {
		dst.Auto = true
	}
	if src.Interval != "" {
		dst.Interval = src.Interval
	}
	if src.MinSessions != 0 {
		dst.MinSessions = src.MinSessions
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.MaxTokens != 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.Temperature != 0 {
		dst.Temperature = src.Temperature
	}
	if src.OutputDir != "" {
		dst.OutputDir = src.OutputDir
	}
	if src.ConsolidatedDir != "" {
		dst.ConsolidatedDir = src.ConsolidatedDir
	}
	if src.ApplyAfterReview {
		dst.ApplyAfterReview = true
	}
}

// mergeSkills merges the skills config section.
func (c *Config) mergeSkills(other *Config) {
	c.Skills.Dirs = append(c.Skills.Dirs, other.Skills.Dirs...)
	c.Skills.Dirs = uniqueStrings(c.Skills.Dirs)
	c.Skills.Embedded = other.Skills.Embedded
	if other.Skills.ExecutionMode != "" {
		c.Skills.ExecutionMode = other.Skills.ExecutionMode
	}
}

// uniqueStrings returns a deduplicated copy of the input slice, preserving
// the first occurrence of each string.
func uniqueStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	return result
}

// mergeTools merges the tools config section.
func (c *Config) mergeTools(other *Config) {
	if other.Tools.Bash.BlockedCommands != nil {
		c.Tools.Bash.BlockedCommands = other.Tools.Bash.BlockedCommands
	}
	if other.Tools.Bash.AllowedCommands != nil {
		c.Tools.Bash.AllowedCommands = other.Tools.Bash.AllowedCommands
	}
	if other.Tools.Bash.EnvMaskPatterns != nil {
		c.Tools.Bash.EnvMaskPatterns = other.Tools.Bash.EnvMaskPatterns
	}
	if other.Tools.Bash.MaxOutputBytes != 0 {
		c.Tools.Bash.MaxOutputBytes = other.Tools.Bash.MaxOutputBytes
	}
	if other.Tools.Bash.MaxComplexityScore != 0 {
		c.Tools.Bash.MaxComplexityScore = other.Tools.Bash.MaxComplexityScore
	}
	if other.Tools.Bash.EnableComplexityAnalysis {
		c.Tools.Bash.EnableComplexityAnalysis = other.Tools.Bash.EnableComplexityAnalysis
	}
	if other.Tools.Bash.CompressOutput != nil {
		c.Tools.Bash.CompressOutput = other.Tools.Bash.CompressOutput
	}
	mergeTerminal(&c.Tools.Terminal, &other.Tools.Terminal)
	if other.Tools.SSH.Hosts != nil {
		c.Tools.SSH.Hosts = other.Tools.SSH.Hosts
	}
	if other.Tools.Search.Threads != 0 {
		c.Tools.Search.Threads = other.Tools.Search.Threads
	}
	if other.Tools.Search.MaxResults != 0 {
		c.Tools.Search.MaxResults = other.Tools.Search.MaxResults
	}
	if other.Tools.Search.Exclude != nil {
		c.Tools.Search.Exclude = other.Tools.Search.Exclude
	}
	mergeSmartSearch(&c.Tools.SmartSearch, &other.Tools.SmartSearch)
	mergeWebFetch(&c.Tools.WebFetch, &other.Tools.WebFetch)
	mergeReadFile(&c.Tools.ReadFile, &other.Tools.ReadFile)
	mergeEditFile(&c.Tools.Edit, &other.Tools.Edit)
	mergeWriteFile(&c.Tools.Write, &other.Tools.Write)
	mergePython(&c.Tools.Python, &other.Tools.Python)
	other.Tools.Enabled.ApplyTo(&c.Tools.Enabled)
}

// mergeReadFile merges the read_file tool config, preserving the default-on
// fuzzy_match value when the source config does not set it.
func mergeReadFile(dst, src *tools.FileToolConfig) {
	if src.FuzzyMatch != nil {
		dst.FuzzyMatch = src.FuzzyMatch
	}
}

// mergeEditFile merges the edit tool config, preserving the default-on
// fuzzy_match value when the source config does not set it.
func mergeEditFile(dst, src *EditConfig) {
	if src.FuzzyMatch != nil {
		dst.FuzzyMatch = src.FuzzyMatch
	}
	if src.AllowFuzzOnEdits {
		dst.AllowFuzzOnEdits = true
	}
}

// mergePython merges the python tool config.
func mergePython(dst, src *PythonConfig) {
	if src.TimeoutSeconds != 0 {
		dst.TimeoutSeconds = src.TimeoutSeconds
	}
}

// mergeWriteFile merges the write tool config. Write does not support fuzzy
// filename matching (writing to the wrong path is irreversible data loss),
// so this is a no-op placeholder for future write-specific options.
func mergeWriteFile(dst, src *WriteConfig) {}

// mergeSmartSearch merges the smartsearch config fields.
func mergeSmartSearch(dst, src *SmartSearchConfig) {
	if src.MaxResults != 0 {
		dst.MaxResults = src.MaxResults
	}
	if src.MinScore != 0 {
		dst.MinScore = src.MinScore
	}
	if src.ExcludeDirs != nil {
		dst.ExcludeDirs = src.ExcludeDirs
	}
	if src.K1 != 0 {
		dst.K1 = src.K1
	}
	if src.B != 0 {
		dst.B = src.B
	}
	dst.Enabled = src.Enabled
}

// mergeWebFetch merges the webfetch tool config, preserving defaults for
// unset scalar fields so embedded defaults are not zeroed by a project
// config that only touches other tools. Boolean flags are left at their
// default unless explicitly set to true; disabling is handled through
// tools.enabled.webfetch.
func mergeWebFetch(dst, src *tools.WebFetchConfig) {
	mergeNonZeroScalars(reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem())
}

// mergeNonZeroScalars copies non-zero exported scalar, slice and string fields
// from src into dst. It recurses into nested structs so callers can keep
// per-section merge functions small.
func mergeNonZeroScalars(dst, src reflect.Value) {
	t := dst.Type()
	for i := 0; i < dst.NumField(); i++ {
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		df := dst.Field(i)
		sf := src.Field(i)
		if ft.Type.Kind() == reflect.Struct {
			mergeNonZeroScalars(df, sf)
			continue
		}
		if !sf.IsZero() {
			df.Set(sf)
		}
	}
}

// mergeTUI merges the TUI config section.
func (c *Config) mergeTUI(other *Config) {
	if other.TUI.Theme != "" {
		c.TUI.Theme = other.TUI.Theme
	}
	if other.TUI.Layout != "" {
		c.TUI.Layout = other.TUI.Layout
	}
	c.TUI.ShowTimestamps = other.TUI.ShowTimestamps
	mergeTransparency(&c.TUI.Transparency, &other.TUI.Transparency)
	if other.TUI.ModeLine.Left != nil {
		c.TUI.ModeLine.Left = other.TUI.ModeLine.Left
	}
	if other.TUI.ModeLine.Right != nil {
		c.TUI.ModeLine.Right = other.TUI.ModeLine.Right
	}
	if other.TUI.Spinner != "" {
		c.TUI.Spinner = other.TUI.Spinner
	}
	mergeToolDisplay(&c.TUI.Tools, &other.TUI.Tools)
	mergeHistoryConfig(&c.TUI.History, &other.TUI.History)
}

// mergeToolDisplay merges the tools display config. Non-zero PreviewLines and
// non-empty View win, so a more specific layer (project/local) overrides the
// embedded defaults.
func mergeToolDisplay(dst, src *ToolDisplayConfig) {
	if src.View != "" {
		dst.View = src.View
	}
	if src.PreviewLines != 0 {
		dst.PreviewLines = src.PreviewLines
	}
	if src.ShowRead {
		dst.ShowRead = true
	}
}

// mergeTransparency merges transparency config fields.
// mergeHistoryConfig merges the history config. Only overrides if src.MaxLoaded
// is non-nil, so a more specific layer (project/local) can explicitly disable
// history loading by setting max_loaded: 0.
func mergeHistoryConfig(dst, src *HistoryConfig) {
	if src.MaxLoaded != nil {
		dst.MaxLoaded = src.MaxLoaded
	}
}

// mergeTransparency merges transparency config fields.
func mergeTransparency(dst, src *TransparencyConfig) {
	if src.ShowThinking {
		dst.ShowThinking = true
	}
	if src.ShowStreaming {
		dst.ShowStreaming = true
	}
	if src.ShowToolCalls {
		dst.ShowToolCalls = true
	}
	if src.ShowTokenStats {
		dst.ShowTokenStats = true
	}
	if src.ShowLogs {
		dst.ShowLogs = true
	}
	if src.ThinkingPanePosition != "" {
		dst.ThinkingPanePosition = src.ThinkingPanePosition
	}
	dst.HighlightToolInput = src.HighlightToolInput
	dst.ThinkingCollapsed = src.ThinkingCollapsed
}

// mergePlugins merges the plugins config section.
func (c *Config) mergePlugins(other *Config) {
	if other.Plugins.Dirs != nil {
		c.Plugins.Dirs = other.Plugins.Dirs
	}
	if other.Plugins.Enabled != nil {
		c.Plugins.Enabled = other.Plugins.Enabled
	}
}

// mergeLogging merges the logging config section.
func (c *Config) mergeLogging(other *Config) {
	if other.Logging.Level != "" {
		c.Logging.Level = other.Logging.Level
	}
	if other.Logging.File != "" {
		c.Logging.File = other.Logging.File
	}
	if other.Logging.TerminalLog != "" {
		c.Logging.TerminalLog = other.Logging.TerminalLog
	}
	if other.Logging.RenderTrace != "" {
		c.Logging.RenderTrace = other.Logging.RenderTrace
	}
	c.Logging.TraceKeys = c.Logging.TraceKeys || other.Logging.TraceKeys
}

// mergeThinkingLevels merges the thinking levels config section.
func (c *Config) mergeThinkingLevels(other *Config) {
	if other.ThinkingLevels.Default != "" {
		c.ThinkingLevels.Default = other.ThinkingLevels.Default
	}
	if other.ThinkingLevels.MainAgent != "" {
		c.ThinkingLevels.MainAgent = other.ThinkingLevels.MainAgent
	}
	if other.ThinkingLevels.Companion != "" {
		c.ThinkingLevels.Companion = other.ThinkingLevels.Companion
	}
	if other.ThinkingLevels.Planner != "" {
		c.ThinkingLevels.Planner = other.ThinkingLevels.Planner
	}
	if other.ThinkingLevels.Coder != "" {
		c.ThinkingLevels.Coder = other.ThinkingLevels.Coder
	}
}

func (c *Config) mergeContextCompression(other *Config) {
	cc := other.ContextCompression
	if !cc.Enabled {
		return
	}
	c.ContextCompression.Enabled = true
	// Numeric scalars merge field-wise (0 = inherit from the lower layer),
	// matching the thresholds/per-model merge below. This fixes the previous
	// replace-all behavior where a higher layer that enabled compression
	// without restating every scalar silently reset them to zero.
	if cc.MaxTokens != 0 {
		c.ContextCompression.MaxTokens = cc.MaxTokens
	}
	if cc.ThresholdPercent != 0 {
		c.ContextCompression.ThresholdPercent = cc.ThresholdPercent
	}
	if cc.PreserveRecentTurns != 0 {
		c.ContextCompression.PreserveRecentTurns = cc.PreserveRecentTurns
	}
	// OnContextError is a bool (explicit false is meaningful), so it keeps
	// the historical replace semantics: any layer with compression enabled
	// carries the effective value.
	c.ContextCompression.OnContextError = cc.OnContextError
	if cc.Strategy != "" {
		c.ContextCompression.Strategy = cc.Strategy
	}
	mergeCompressionThresholds(&c.ContextCompression.Thresholds, cc.Thresholds)
	mergeCompressionPerModel(&c.ContextCompression.PerModel, cc.PerModel)
	if cc.MicroCompaction != (MicroCompactionSettings{}) {
		c.ContextCompression.MicroCompaction = cc.MicroCompaction
	}
}

// mergeCompressionThresholds overlays non-zero threshold fields.
func mergeCompressionThresholds(dst *CompressionThresholdsConfig, src CompressionThresholdsConfig) {
	if src.SoftPercent != 0 {
		dst.SoftPercent = src.SoftPercent
	}
	if src.TriggerPercent != 0 {
		dst.TriggerPercent = src.TriggerPercent
	}
	if src.HardPercent != 0 {
		dst.HardPercent = src.HardPercent
	}
}

// mergeCompressionPerModel overlays per-model override entries, merging
// field-wise so a higher cascade layer can adjust a single field without
// restating the whole entry.
func mergeCompressionPerModel(dst *map[string]ModelCompressionOverride, src map[string]ModelCompressionOverride) {
	for id, o := range src {
		if *dst == nil {
			*dst = map[string]ModelCompressionOverride{}
		}
		m := (*dst)[id]
		if o.MaxTokens != 0 {
			m.MaxTokens = o.MaxTokens
		}
		if o.ThresholdPercent != 0 {
			m.ThresholdPercent = o.ThresholdPercent
		}
		mergeCompressionThresholds(&m.Thresholds, o.Thresholds)
		if o.Strategy != "" {
			m.Strategy = o.Strategy
		}
		if o.PreserveRecentTurns != 0 {
			m.PreserveRecentTurns = o.PreserveRecentTurns
		}
		(*dst)[id] = m
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