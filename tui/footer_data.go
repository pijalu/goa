// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// FooterData holds status bar information.
type FooterData struct {
	Workdir                string
	Mode                   string // autonomy level: "yolo", "review", "plan"
	MinorMode              string // minor mode: "companion", "pair", "" (empty when inactive)
	Profile                string
	Model                  string // main model display: "(provider) model"
	Provider               string // provider ID for formatting companion model
	CompanionModel         string // companion model raw ID, empty when none
	Activity               string
	Tokens                 string
	ThinkingLevel          string // "off", "low", "medium", "high"
	GitBranch              string // current git branch (empty if not a git repo)
	GitDirty               bool   // true if working tree has changes
	GitConflicts           bool   // true if merge conflicts exist
	Stats                  string // conversation stats like "↑169k ↓209k  35.8%/1.0M (auto)"
	CompanionThinkingLevel string // companion thinking level badge
	CompanionCycleCount    int    // current framework-driven companion cycle
	CompanionCycleMax      int    // maximum framework-driven companion cycles
	WorkflowActive         bool   // true when a multi-agent workflow is running
	SteeringHint           string // shown when workflow is active (e.g. "type to steer")
	SkillExecMode          string // "inline" or "sub-agent" for status line, empty when none
	ModelBusy              bool   // true when the main model is actively generating
	CompanionBusy          bool   // true when the companion is reviewing
	MainActivity           string // current main agent activity: "sending", "thinking", "tool", "streaming"
	CompanionActivity      string // current companion activity, empty when idle
	GoalStatus             string // "active", "paused", "blocked", or empty when no goal
	GoalObjective          string // truncated active/paused/blocked goal objective for the footer
	OrchestrationStats     string // per-model orchestration stats rendered as an extra footer line

	// PluginSegments holds pre-rendered status-bar segments contributed by JS
	// plugins (e.g. the quota carousel). They are appended to the right-hand
	// model side, ordered by priority. Strings are cached by the app layer so
	// footer rendering never calls back into JavaScript.
	PluginSegments []PluginSegment
}

// PluginSegment is one rendered plugin status-bar contribution. It mirrors
// plugins.UISegmentDef without importing the plugins package (tui must stay
// dependency-light); the app layer maps between them.
type PluginSegment struct {
	ID       string
	Priority int
	Text     string // already-rendered, ANSI-safe content
}

// preserveFooterData merges new data with previously preserved fields so the
// footer stays stable across updates (git info, model names, minor mode, etc.).
func preserveFooterData(prev, data FooterData) FooterData {
	data = preserveFooterGitAndMode(prev, data)
	data = preserveFooterModels(prev, data)
	data = preserveFooterWorkflow(prev, data)
	data = preserveFooterPluginSegments(prev, data)
	return data
}

// preserveFooterPluginSegments keeps plugin segments across routine footer
// updates (token stats, activity) that rebuild FooterData without touching
// plugins. The app layer pushes fresh segments explicitly; a nil update means
// "keep previous" so ordinary SetData calls never blank the quota carousel.
func preserveFooterPluginSegments(prev, data FooterData) FooterData {
	if data.PluginSegments == nil {
		data.PluginSegments = prev.PluginSegments
	}
	return data
}

func preserveFooterGitAndMode(prev, data FooterData) FooterData {
	if data.Workdir == "" {
		data.Workdir = prev.Workdir
	}
	if data.GitBranch == "" {
		data.GitBranch = prev.GitBranch
		data.GitDirty = prev.GitDirty
		data.GitConflicts = prev.GitConflicts
	}
	if data.MinorMode == "" {
		data.MinorMode = prev.MinorMode
	}
	if data.Stats == "" && prev.Stats != "" {
		data.Stats = prev.Stats
	}
	return data
}

func preserveFooterModels(prev, data FooterData) FooterData {
	if data.Model == "" && prev.Model != "" {
		data.Model = prev.Model
	}
	if data.Provider == "" && prev.Provider != "" {
		data.Provider = prev.Provider
	}
	if data.CompanionModel == "" && prev.CompanionModel != "" {
		data.CompanionModel = prev.CompanionModel
	}
	if data.CompanionThinkingLevel == "" && prev.CompanionThinkingLevel != "" {
		data.CompanionThinkingLevel = prev.CompanionThinkingLevel
	}
	return data
}

func preserveFooterWorkflow(prev, data FooterData) FooterData {
	if !data.WorkflowActive && prev.WorkflowActive {
		data.WorkflowActive = prev.WorkflowActive
	}
	if data.SteeringHint == "" && prev.SteeringHint != "" {
		data.SteeringHint = prev.SteeringHint
	}
	if data.CompanionCycleCount == 0 && prev.CompanionCycleCount > 0 {
		data.CompanionCycleCount = prev.CompanionCycleCount
		data.CompanionCycleMax = prev.CompanionCycleMax
	}
	return data
}
