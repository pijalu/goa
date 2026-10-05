// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// FooterData holds status bar information.
type FooterData struct {
	Workdir        string
	Mode           string // autonomy level: "yolo", "review", "plan"
	MinorMode      string // minor mode: "companion", "pair", "" (empty when inactive)
	Profile        string
	Model          string // main model display: "(provider) model"
	Provider       string // provider ID for formatting companion model
	CompanionModel string // companion model raw ID, empty when none
	Activity       string
	Tokens         string
	ThinkingLevel  string // "off", "low", "medium", "high"
	GitBranch      string // current git branch (empty if not a git repo)
	GitDirty       bool   // true if working tree has changes
	GitConflicts   bool   // true if merge conflicts exist
	Stats          string // conversation stats like "↑169k ↓209k  35.8%/1.0M (auto)"
	// StatsSegments is the same stat set as Stats, split into the width-
	// droppable fields of the B11 priority ladder (lowest Tier drops first).
	// Stats stays authoritative for consumers that need one string (headless
	// rendering, orchestration lines); when StatsSegments is empty the footer
	// treats Stats as a single never-dropped segment.
	StatsSegments          []FooterSegment
	CompanionThinkingLevel string // companion thinking level badge
	CompanionCycleCount    int    // current framework-driven companion cycle
	CompanionCycleMax      int    // maximum framework-driven companion cycles
	WorkflowActive         bool   // true when a multi-agent workflow is running
	SteeringHint           string // shown when workflow is active (e.g. "type to steer")
	SkillExecMode          string // "inline" or "sub-agent" for status line, empty when none
	Team                   string // active team name, empty when none (TEAMS.md §8.2)
	TeamDrifted            bool   // true when the active team has manual overrides (rendered with * marker)
	ModelBusy              bool   // true when the main model is actively generating
	CompanionBusy          bool   // true when the companion is reviewing
	MainActivity           string // current main agent activity: "sending", "thinking", "tool", "streaming"
	CompanionActivity      string // current companion activity, empty when idle

	// ActiveAgentProvider/ActiveAgentModel identify the sub-agent currently
	// streaming via delegate_to/companion (e.g. "coder" on provider X model Y).
	// When ActiveAgentModel is non-empty the status bar shows the delegated
	// agent's real provider/model instead of the main model, fixing the
	// "status bar shows wrong model info" team bug. Empty when idle.
	ActiveAgentProvider string
	ActiveAgentModel    string
	ActiveAgentRole     string // role label ("coder", "companion", …) for color-coding

	GoalStatus         string // "active", "paused", "blocked", or empty when no goal; drives only the ◈ marker (goal detail lives in the goal bubble)
	GoalCount          int    // total goals: 1 current + queued; drives the ◈ goal-count sign (1 → "◈", 3 → "◈◈◈", 25 → "25◈")
	GoalPendingTodos   int    // count of not-done todos on the active goal; drives the ⬩ markers next to the mode
	OrchestrationStats string // per-model orchestration stats rendered as an extra footer line
	// AgentTabStats is the ACTIVE multi-agent tab's stat line (T5), rendered
	// as one extra footer line below the chrome. Written ONLY through
	// Footer.SetAgentStats (the sole writer, mirroring SetGoalStatus) so
	// routine SetData rebuilds never flap it; preserved on empty for the
	// same reason.
	AgentTabStats string

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

// FooterSegment is one width-droppable piece of the line-2 status bar. Tier
// is its position on the B11 priority ladder: while the line does not fit, the
// LOWEST tier still present is dropped first, so a value that matters more must
// carry a HIGHER tier. Text is an already-styled fragment. Glue renders the
// segment directly against its predecessor with no separating space (used by
// fields that were one visual unit before the ladder split them, e.g.
// "CH:99.0%▸99.4%"), so dropping one leaves no stray separator behind.
type FooterSegment struct {
	Tier int
	Text string
	Glue bool
}

// The status-line priority ladder (bugs.md B11), lowest value → dropped first:
// the amount is the first casualty and the context/quota figure is never
// dropped. The model identity (tier 8) is preserved by ellipsizing the model
// name before anything at tier 8 is removed.
const (
	FooterTierAmount   = 1 // $0.6259
	FooterTierProvider = 2 // (opencode-go)
	FooterTierTokens   = 3 // ↑291.6K ↓130.4K
	FooterTierTools    = 4 // TC:190
	FooterTierSpeed    = 5 // 72.5 tok/s
	FooterTierLastCH   = 6 // ▸99.4%
	FooterTierAvgCH    = 7 // CH:99.0%
	// FooterTierIdentity covers the model name and its thinking badge; the
	// badge drops here, the name is ellipsized instead of removed.
	FooterTierIdentity = 8
	// FooterTierQuota is the context/quota figure — the last survivor.
	FooterTierQuota = 9
)

// joinFooterSegments renders the surviving segments in their original order,
// honoring each segment's glue flag.
func joinFooterSegments(segments []FooterSegment) string {
	var b strings.Builder
	for _, s := range segments {
		if strings.TrimSpace(ansi.Strip(s.Text)) == "" {
			continue
		}
		if b.Len() > 0 && !s.Glue {
			b.WriteByte(' ')
		}
		b.WriteString(s.Text)
	}
	return b.String()
}

// preserveFooterData merges new data with previously preserved fields so the
// footer stays stable across updates (git info, model names, minor mode, etc.).
func preserveFooterData(prev, data FooterData) FooterData {
	data = preserveFooterGitAndMode(prev, data)
	data = preserveFooterModels(prev, data)
	data = preserveFooterWorkflow(prev, data)
	data = preserveFooterPluginSegments(prev, data)
	data = preserveFooterGoal(prev, data)
	data = preserveFooterTeam(prev, data)
	data = preserveFooterAgentStats(prev, data)
	data = preserveFooterActiveAgent(prev, data)
	return data
}

// preserveFooterAgentStats keeps the active-tab stat line across routine
// footer rebuilds (token stats, activity) that construct a fresh FooterData
// without tab knowledge. SetAgentStats is the sole writer — an explicit
// clear goes through it (mirroring SetGoalStatus) — so the preserved value
// never goes stale and the line never flaps between writers.
func preserveFooterAgentStats(prev, data FooterData) FooterData {
	if data.AgentTabStats == "" {
		data.AgentTabStats = prev.AgentTabStats
	}
	return data
}

// preserveFooterActiveAgent keeps the actively-streaming sub-agent identity
// across routine footer rebuilds (token stats, activity) that construct a
// fresh FooterData without it. SetActiveAgent is the sole writer (mirroring
// SetTeam/SetGoalStatus): the forwarder sets it on stream start and clears it
// on stream end, so the preserved value never goes stale — but a SetData call
// with a partial struct (e.g. only CompanionActivity) must not wipe it
// mid-stream (this was the root cause of the active agent not appearing).
func preserveFooterActiveAgent(prev, data FooterData) FooterData {
	if data.ActiveAgentModel == "" {
		data.ActiveAgentProvider = prev.ActiveAgentProvider
		data.ActiveAgentModel = prev.ActiveAgentModel
		data.ActiveAgentRole = prev.ActiveAgentRole
	}
	return data
}

// preserveFooterTeam keeps the team badge across routine footer rebuilds
// (token stats, activity) that construct a fresh FooterData without team
// knowledge. SetTeam is the sole writer (mirroring SetGoalStatus): an
// explicit clear goes through SetTeam, so the preserved value never goes
// stale.
func preserveFooterTeam(prev, data FooterData) FooterData {
	if data.Team == "" {
		data.Team = prev.Team
		data.TeamDrifted = prev.TeamDrifted
	}
	return data
}

// preserveFooterGoal keeps the goal status across routine footer rebuilds
// (token stats, activity) that construct a fresh FooterData without goal
// knowledge. updateGoalFooter/SetGoalStatus is the sole writer of goal
// state, so the preserved value never goes stale: an explicit clear goes
// through SetGoalStatus, mirroring how SetMinorMode bypasses preservation
// for its own field (Issues 3-4: the ◈ marker must not flicker off
// on every stats tick).
func preserveFooterGoal(prev, data FooterData) FooterData {
	if data.GoalStatus == "" {
		data.GoalStatus = prev.GoalStatus
		data.GoalCount = prev.GoalCount
		data.GoalPendingTodos = prev.GoalPendingTodos
	}
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
