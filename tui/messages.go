// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"github.com/pijalu/goa/internal"
)

// ConsoleItemType categorizes entries in the chat history.
type ConsoleItemType int

const (
	ConsoleUserMessage ConsoleItemType = iota
	ConsoleSystemMessage
	ConsoleThinkingBlock
	ConsoleAssistantMessage
	ConsoleToolCall
	ConsoleToolResult
	ConsoleFinishLine
	ConsoleAgentMessage           // agent-to-agent messages in multi-agent workflows
	ConsoleCompanionMessage       // companion output with purple gutter
	ConsoleCompanionThinkingBlock // companion thinking with gray gutter
	ConsoleInfoMessage            // plain informational text (no box/background)
	ConsoleSteeringPending        // pending steering input shown until consumed by the model
)

// ModeChangeMsg is emitted when the agent's mode changes.
type ModeChangeMsg struct {
	OldMode internal.ModeState
	NewMode internal.ModeState
	Source  string // "user", "skill", "pipeline", etc.
}

// SkillActivateMsg is emitted when a skill is added to the stack.
type SkillActivateMsg struct {
	Skill string
	Mode  internal.ModeState
}

// SkillDeactivateMsg is emitted when a skill is removed from the stack.
type SkillDeactivateMsg struct {
	Skill string
	Mode  internal.ModeState
}

// AutonomyChangeMsg is emitted when the autonomy level changes.
type AutonomyChangeMsg struct {
	OldAutonomy internal.AutonomyLevel
	NewAutonomy internal.AutonomyLevel
	Source      string
}

// PipelineProgressMsg is emitted when a pipeline stage progresses.
type PipelineProgressMsg struct {
	PipelineID string
	StageID    string
	Status     string // "running", "paused", "completed", "failed", "cancelled"
}

// InterAgentMsg is emitted when agents communicate in multi-agent mode.
type InterAgentMsg struct {
	From    string
	To      string
	Content string
}

// ShowOutputModalMsg requests a modal to display command output.
type ShowOutputModalMsg struct {
	Title   string
	Content string
}

// InsertInputMsg inserts text into the input pane.
type InsertInputMsg struct {
	Text string
}

// StopRequestMsg requests the TUI to stop and exit.
type StopRequestMsg struct{}

// AgentTurnMsg is emitted when a sub-agent has output during multi-agent workflows.
type AgentTurnMsg struct {
	AgentName string // "planner", "coder", "reviewer", "orchestrator"
	Role      string // same as AgentName or more specific
	Content   string
	Status    string // "running", "complete", "error"
}

// WorkflowStatusMsg is emitted when the multi-agent workflow progresses.
type WorkflowStatusMsg struct {
	Mode         string // "review", "pair", "orchestrate"
	Step         int
	TotalSteps   int
	CurrentAgent string
}

// MinorModeChangeMsg is emitted when the orchestrator's minor mode changes.
type MinorModeChangeMsg struct {
	Mode string // "review", "pair", "" (empty when inactive)
}

// ClearChatMsg requests the chat viewport to be cleared.
type ClearChatMsg struct{}

// FlashMsg requests a transient flash message displayed in the status area.
type FlashMsg struct {
	Text string
}

// GateApprovalMsg is emitted when a workflow stage reaches an approval gate.
type GateApprovalMsg struct {
	StageID   string
	StageName string
	Prompt    string
}

// SteeringInputMsg routes user input as a steering command during active workflows.
type SteeringInputMsg struct {
	Text string
}

// TaskStatusChangeMsg is emitted when a task's status changes in task orchestration.
type TaskStatusChangeMsg struct {
	TaskID      string
	Description string
	Status      string
	Index       int
	Total       int
}

// WorkflowProgressMsg is emitted when a workflow stage progresses.
type WorkflowProgressMsg struct {
	StageIndex  int
	TotalStages int
	StageName   string
	StageID     string
	Status      string // "running", "gate", "complete", "failed"
}

// ThinkingLevelChangeMsg is emitted when the thinking level changes.
type ThinkingLevelChangeMsg struct {
	Level string // "off", "minimal", "low", "medium", "high", "xhigh"
}

// FooterRefreshMsg is emitted when configuration changes and the status bar
// should rebuild from the current config.
type FooterRefreshMsg struct{}

// CompanionCycleMsg is emitted when the framework-driven companion
// back-and-forth counter changes.
type CompanionCycleMsg struct {
	Current int
	Max     int
}

// CompanionCycleEndMsg is emitted when a companion cycle finishes, carrying
// the end message (the review forwarded to the main LLM).
type CompanionCycleEndMsg struct {
	EndMessage string
}
