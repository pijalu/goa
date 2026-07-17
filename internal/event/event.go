// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package event defines the typed event channels used by the application.
//
// The previous `chan interface{}` event bus has been split into three
// domain-specific channels so that producers and consumers are coupled
// only to the event categories they care about.
package event

import (
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// GoalUpdate carries a goal state change.
type GoalUpdate struct {
	Snapshot *goal.GoalSnapshot
	Change   *goal.GoalChange
}

// AgentEvent carries events produced by the agentic SDK and by the
// AgentManager's own lifecycle logic.
type AgentEvent struct {
	// Event is the agentic SDK output event.
	Event agentic.OutputEvent
	// GoalUpdate is non-nil when a goal state changes.
	GoalUpdate *GoalUpdate
}

// ControlEvent carries commands that affect application control flow or
// request user interaction.
type ControlEvent struct {
	// StopRequest asks the application to exit cleanly.
	StopRequest bool
	// NewSession requests a complete agent session restart (clear history +
	// stop old session + start fresh session with the same settings).
	NewSession bool
	// RunWizard asks the application to stop the TUI and run the setup wizard.
	RunWizard bool
	// GateApproval carries a workflow gate approval request.
	GateApproval *GateApproval
	// SteeringInput carries user steering text injected into an active workflow.
	SteeringInput *SteeringInput
}

// GateApproval is emitted when a workflow stage reaches an approval gate.
type GateApproval struct {
	StageID   string
	StageName string
	Prompt    string
}

// SteeringInput carries user steering text injected into an active workflow.
type SteeringInput struct {
	Text string
}

// RunWizard signals the main loop to stop the TUI, run the setup wizard,
// and restart with the new configuration.
type RunWizard struct{}

// ChatEvent carries events that affect the chat viewport or transient status.
type ChatEvent struct {
	// ClearChat requests the chat viewport to be cleared.
	ClearChat bool
	// Flash carries a transient flash message.
	Flash *Flash
	// InterAgent carries an agent-to-agent message.
	InterAgent *InterAgent
	// ShowOutputModal requests a modal with command output.
	ShowOutputModal *ShowOutputModal
	// ShowReviewPager opens the code-review diff pager.
	ShowReviewPager *ShowReviewPager
	// ShowPlanPager opens the plan-annotation pager.
	ShowPlanPager *ShowPlanPager
	// ShowPlanStatus opens the plan-status overlay.
	ShowPlanStatus *ShowPlanStatus
	// PipelineProgress carries a pipeline stage progress update.
	PipelineProgress *PipelineProgress
	// TaskUpdate carries a background task status update.
	TaskUpdate *TaskUpdate
	// SteeringInjected is emitted when buffered steering input is consumed and
	// injected into the conversation as a follow-up user message.
	SteeringInjected *SteeringInput
}

// Flash is a transient flash message displayed in the status area.
type Flash struct {
	Text string
}

// InterAgent is an agent-to-agent message in multi-agent mode.
type InterAgent struct {
	From    string
	To      string
	Content string
}

// ShowOutputModal requests a modal to display command output.
type ShowOutputModal struct {
	Title   string
	Content string
}

// ShowReviewPager requests the TUI to open the code-review diff pager.
type ShowReviewPager struct {
	Pager any // concrete type is *tui.ReviewPager to avoid an import cycle
}

// ShowPlanPager requests the TUI to open the plan-annotation pager.
type ShowPlanPager struct {
	Store any // concrete type is *plan.Store to avoid an import cycle
}

// ShowPlanStatus requests the TUI to open the plan-status overlay.
type ShowPlanStatus struct {
	Store any // concrete type is *plan.Store to avoid an import cycle
}

// PipelineProgress carries a pipeline stage progress update.
type PipelineProgress struct {
	PipelineID string
	StageID    string
	Status     string
}

// FooterEvent carries events that update the status bar.
type FooterEvent struct {
	// ModeChange carries a mode change notification.
	ModeChange *ModeChange
	// MinorMode carries the orchestrator's current minor mode.
	MinorMode *MinorMode
	// ThinkingLevel carries the main-agent thinking level.
	ThinkingLevel *ThinkingLevel
	// CompanionCycle carries the current/max companion cycle count.
	CompanionCycle *CompanionCycle
	// FooterRefresh requests a full footer rebuild from config.
	FooterRefresh bool
	// WorkflowStatus carries workflow status for the footer.
	WorkflowStatus *WorkflowStatus
	// WorkflowProgress carries workflow stage progress for the footer.
	WorkflowProgress *WorkflowProgress
	// TaskStatusChange carries a task status change notification.
	TaskStatusChange *TaskStatusChange
}

// ModeChange is emitted when the agent's mode changes.
type ModeChange struct {
	OldMode internal.ModeState
	NewMode internal.ModeState
	Source  string
}

// MinorMode is emitted when the orchestrator's minor mode changes.
type MinorMode struct {
	Mode string
}

// ThinkingLevel carries the main-agent thinking level.
type ThinkingLevel struct {
	Level string
}

// CompanionCycle carries the framework-driven companion back-and-forth count.
type CompanionCycle struct {
	Current int
	Max     int
}

// WorkflowStatus carries workflow status for the footer.
type WorkflowStatus struct {
	Mode         string
	Step         int
	TotalSteps   int
	CurrentAgent string
}

// WorkflowProgress carries workflow stage progress for the footer.
type WorkflowProgress struct {
	StageIndex  int
	TotalStages int
	StageName   string
	StageID     string
	Status      string
}

// TaskStatusChange is emitted when a task's status changes in task orchestration.
type TaskStatusChange struct {
	TaskID      string
	Description string
	Status      string
	Index       int
	Total       int
}

// TaskUpdate is emitted when a background task changes status.
type TaskUpdate struct {
	TaskID      string
	Status      string
	Description string
	Result      string
}

// Bus bundles the application's typed event channels.
// Producers send on one of the channels; consumers read from the channels
// they care about. The zero value is usable but nil channels block forever,
// so callers should always construct a Bus with MakeBus.
type Bus struct {
	Agent   chan AgentEvent
	Control chan ControlEvent
	Chat    chan ChatEvent
	Footer  chan FooterEvent
}

// MakeBus creates a new event bus with buffered channels.
func MakeBus(agentBuf, controlBuf, chatBuf, footerBuf int) *Bus {
	return &Bus{
		Agent:   make(chan AgentEvent, agentBuf),
		Control: make(chan ControlEvent, controlBuf),
		Chat:    make(chan ChatEvent, chatBuf),
		Footer:  make(chan FooterEvent, footerBuf),
	}
}
