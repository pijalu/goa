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
	c.Skills.Enabled = append(c.Skills.Enabled, other.Skills.Enabled...)
	c.Skills.Enabled = uniqueStrings(c.Skills.Enabled)
	c.Skills.Disabled = append(c.Skills.Disabled, other.Skills.Disabled...)
	c.Skills.Disabled = uniqueStrings(c.Skills.Disabled)
	c.Skills.EmbeddedEnabled = append(c.Skills.EmbeddedEnabled, other.Skills.EmbeddedEnabled...)
	c.Skills.EmbeddedEnabled = uniqueStrings(c.Skills.EmbeddedEnabled)
	c.Skills.Sticky = append(c.Skills.Sticky, other.Skills.Sticky...)
	c.Skills.Sticky = uniqueStrings(c.Skills.Sticky)
	c.Skills.StickyOff = append(c.Skills.StickyOff, other.Skills.StickyOff...)
	c.Skills.StickyOff = uniqueStrings(c.Skills.StickyOff)
}

// mergeMCP merges MCP server definitions. Servers are keyed by name; a server
// present in other replaces the same-named server wholesale (last-write-wins),
// matching the per-name semantics users expect from cascade overrides.
func (c *Config) mergeMCP(other *Config) {
	if len(other.MCP) == 0 {
		return
	}
	if c.MCP == nil {
		c.MCP = make(map[string]MCPServerConfig, len(other.MCP))
	}
	for name, srv := range other.MCP {
		c.MCP[name] = srv
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

func mergeBashConfig(dst, src *BashConfig) {
	if src.BlockedCommands != nil {
		dst.BlockedCommands = src.BlockedCommands
	}
	if src.AllowedCommands != nil {
		dst.AllowedCommands = src.AllowedCommands
	}
	if src.EnvMaskPatterns != nil {
		dst.EnvMaskPatterns = src.EnvMaskPatterns
	}
	if src.MaxOutputBytes != 0 {
		dst.MaxOutputBytes = src.MaxOutputBytes
	}
	if src.MaxCaptureBytes != 0 {
		dst.MaxCaptureBytes = src.MaxCaptureBytes
	}
	if src.MaxComplexityScore != 0 {
		dst.MaxComplexityScore = src.MaxComplexityScore
	}
	if src.EnableComplexityAnalysis {
		dst.EnableComplexityAnalysis = true
	}
	if src.CompressOutput != nil {
		dst.CompressOutput = src.CompressOutput
	}
	if src.WarnFileEdits != nil {
		dst.WarnFileEdits = src.WarnFileEdits
	}
}

func mergeSearchConfig(dst, src *SearchConfig) {
	if src.Threads != 0 {
		dst.Threads = src.Threads
	}
	if src.MaxResults != 0 {
		dst.MaxResults = src.MaxResults
	}
	if src.Exclude != nil {
		dst.Exclude = src.Exclude
	}
}

func mergeToolsBashAndSearch(dst, src *Config) {
	mergeBashConfig(&dst.Tools.Bash, &src.Tools.Bash)
	if src.Tools.SSH.Hosts != nil {
		dst.Tools.SSH.Hosts = src.Tools.SSH.Hosts
	}
	mergeSearchConfig(&dst.Tools.Search, &src.Tools.Search)
}

// mergeTools merges the tools config section.
func (c *Config) mergeTools(other *Config) {
	mergeToolsBashAndSearch(c, other)
	mergeTerminal(&c.Tools.Terminal, &other.Tools.Terminal)
	mergeSmartSearch(&c.Tools.SmartSearch, &other.Tools.SmartSearch)
	mergeWebFetch(&c.Tools.WebFetch, &other.Tools.WebFetch)
	mergeReadFile(&c.Tools.ReadFile, &other.Tools.ReadFile)
	mergeEditFile(&c.Tools.Edit, &other.Tools.Edit)
	mergeWriteFile(&c.Tools.Write, &other.Tools.Write)
	mergePython(&c.Tools.Python, &other.Tools.Python)
	mergeRunCode(&c.Tools.RunCode, &other.Tools.RunCode)
	other.Tools.Enabled.ApplyTo(&c.Tools.Enabled)
}

// mergeReadFile merges the read_file tool config, preserving the default-on
// fuzzy_match and dedup values when the source config does not set them.
func mergeReadFile(dst, src *tools.FileToolConfig) {
	if src.FuzzyMatch != nil {
		dst.FuzzyMatch = src.FuzzyMatch
	}
	if src.Dedup != nil {
		dst.Dedup = src.Dedup
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

// mergeRunCode merges the run_code tool config, preserving defaults for unset
// scalar fields so embedded defaults are not zeroed by a config layer that
// only touches other fields. The *bool Jail pointer is copied only when
// explicitly set (nil = keep the default), so the default jailed worker can
// be opted out with jail: false and survives unrelated merges. Disabling the
// tool entirely is handled through tools.enabled.run_code.
func mergeRunCode(dst, src *RunCodeConfig) {
	mergeNonZeroScalars(reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem())
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
	if other.TUI.SpinnerLocation != "" {
		c.TUI.SpinnerLocation = other.TUI.SpinnerLocation
	}
	mergeToolDisplay(&c.TUI.Tools, &other.TUI.Tools)
	mergeHistoryConfig(&c.TUI.History, &other.TUI.History)
	mergeFontStyles(&c.TUI.FontStyles, &other.TUI.FontStyles)
}

// mergeFontStyles deep-merges the per-style toggles: only styles explicitly
// set in src override dst, so a config layer that omits font_styles does not
// clobber a layer that set them (each toggle is a *bool, nil = unset).
func mergeFontStyles(dst, src *FontStylesConfig) {
	if src.Bold != nil {
		dst.Bold = src.Bold
	}
	if src.Italic != nil {
		dst.Italic = src.Italic
	}
	if src.Underline != nil {
		dst.Underline = src.Underline
	}
	if src.Strikethrough != nil {
		dst.Strikethrough = src.Strikethrough
	}
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
	if other.Logging.CaptureStream != "" {
		c.Logging.CaptureStream = other.Logging.CaptureStream
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
	if contextCompressionLayerEmpty(cc) {
		return
	}
	// Enabled is tri-state: an explicit true or false in a higher layer wins;
	// a layer that leaves it unset preserves the lower layer's value.
	if cc.Enabled != nil {
		c.ContextCompression.Enabled = cc.Enabled
	}
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
	// the historical replace semantics: any layer touching compression
	// carries the effective value.
	c.ContextCompression.OnContextError = cc.OnContextError
	if cc.Strategy != "" {
		c.ContextCompression.Strategy = cc.Strategy
	}
	if cc.OnErrorStrategy != "" {
		c.ContextCompression.OnErrorStrategy = cc.OnErrorStrategy
	}
	if cc.CacheGate != "" {
		c.ContextCompression.CacheGate = cc.CacheGate
	}
	mergeCompressionThresholds(&c.ContextCompression.Thresholds, cc.Thresholds)
	mergeCompressionStrategies(&c.ContextCompression.Strategies, cc.Strategies)
	mergeCompressionPerModel(&c.ContextCompression.PerModel, cc.PerModel)
	mergeMicroCompaction(&c.ContextCompression.MicroCompaction, cc.MicroCompaction)
	mergeToolResultPruning(&c.ContextCompression.ToolResultPruning, cc.ToolResultPruning)
	mergeFreshWindow(&c.ContextCompression.FreshWindow, cc.FreshWindow)
}

// contextCompressionLayerEmpty reports whether a cascade layer carries no
// context_compression settings at all, so merging it would be a no-op (and
// must not flip OnContextError or wipe the block with zero values).
func contextCompressionLayerEmpty(cc ContextCompressionConfig) bool {
	return cc.Enabled == nil &&
		cc.MaxTokens == 0 &&
		cc.ThresholdPercent == 0 &&
		cc.PreserveRecentTurns == 0 &&
		cc.Strategy == "" &&
		cc.OnErrorStrategy == "" &&
		cc.CacheGate == "" &&
		contextCompressionSubBlocksEmpty(cc)
}

// contextCompressionSubBlocksEmpty reports whether every nested
// context_compression sub-block (thresholds, strategies, per-model overrides
// and the strategy-specific settings) is at its zero value.
func contextCompressionSubBlocksEmpty(cc ContextCompressionConfig) bool {
	return cc.Thresholds == (CompressionThresholdsConfig{}) &&
		cc.Strategies == (CompressionLayerStrategiesConfig{}) &&
		len(cc.PerModel) == 0 &&
		cc.MicroCompaction == (MicroCompactionSettings{}) &&
		cc.ToolResultPruning == (ToolResultPruningSettings{}) &&
		cc.FreshWindow == (FreshWindowSettings{})
}

// mergeCompressionStrategies overlays non-empty per-layer strategy fields.
func mergeCompressionStrategies(dst *CompressionLayerStrategiesConfig, src CompressionLayerStrategiesConfig) {
	if src.Soft != "" {
		dst.Soft = src.Soft
	}
	if src.Trigger != "" {
		dst.Trigger = src.Trigger
	}
	if src.Hard != "" {
		dst.Hard = src.Hard
	}
}

// mergeMicroCompaction overlays micro-compaction settings field-wise so a
// higher layer setting one key does not reset the others to zero.
func mergeMicroCompaction(dst *MicroCompactionSettings, src MicroCompactionSettings) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.KeepRecentMessages != 0 {
		dst.KeepRecentMessages = src.KeepRecentMessages
	}
	if src.MinContentTokens != 0 {
		dst.MinContentTokens = src.MinContentTokens
	}
	if src.CacheMissThreshold != "" {
		dst.CacheMissThreshold = src.CacheMissThreshold
	}
	if src.TruncatedMarker != "" {
		dst.TruncatedMarker = src.TruncatedMarker
	}
	if src.MinContextRatio != 0 {
		dst.MinContextRatio = src.MinContextRatio
	}
}

// mergeToolResultPruning overlays tool-result pruner settings field-wise so a
// higher layer setting one key does not reset the others to zero. Enabled is
// tri-state: only an explicitly set pointer overrides the lower layer.
func mergeToolResultPruning(dst *ToolResultPruningSettings, src ToolResultPruningSettings) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.ThresholdChars != 0 {
		dst.ThresholdChars = src.ThresholdChars
	}
	if src.HeadChars != 0 {
		dst.HeadChars = src.HeadChars
	}
	if src.TailChars != 0 {
		dst.TailChars = src.TailChars
	}
}

// mergeFreshWindow overlays fresh-window strategy settings field-wise so a
// higher layer setting one key does not reset the others to zero. Enabled is
// tri-state: only an explicitly set pointer overrides the lower layer.
func mergeFreshWindow(dst *FreshWindowSettings, src FreshWindowSettings) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.PreserveRecentTurns != 0 {
		dst.PreserveRecentTurns = src.PreserveRecentTurns
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
