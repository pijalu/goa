// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	goaltui "github.com/pijalu/goa/tui/goal"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func (a *App) showPipelineProgress(p *event.PipelineProgress) {
	if a.subs.chat == nil || p == nil || p.Status == "" {
		return
	}
	a.subs.chat.AddSystemMessage(fmt.Sprintf("[pipeline %s] stage %s: %s", p.PipelineID, p.StageID, p.Status))
}

func (a *App) handleModeChangeEvent(e *event.ModeChange) {
	subs := a.subs
	profileName := string(e.NewMode.Major)
	if profileName == "" {
		profileName = string(subs.effectiveModeState().Major)
	}
	subs.statusMsg.Clear()
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Mode:                   string(e.NewMode.Autonomy),
		Profile:                profileName,
		Model:                  activeModelDisplay(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

func (a *App) handleInterAgentEvent(e *event.InterAgent) {
	if a.subs.chat == nil {
		return
	}
	if e.From != "" && e.From != "system" && e.From != "user" {
		a.subs.chat.AddAgentMessage(e.From, e.Content)
	} else {
		a.subs.chat.AddSystemMessage(e.Content)
	}
}

func (a *App) handleThinkingLevelChange(e *event.ThinkingLevel) {
	if e == nil {
		return
	}
	a.applyThinkingLevelToUI(e.Level)
	if a.subs.footer == nil {
		return
	}
	data := a.subs.footer.Data()
	data.ThinkingLevel = e.Level
	a.subs.footer.SetData(data)
	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleCompanionCycleChange(e *event.CompanionCycle) {
	if a.subs.footer == nil {
		return
	}
	data := a.subs.footer.Data()
	data.CompanionCycleCount = e.Current
	data.CompanionCycleMax = e.Max
	a.subs.footer.SetData(data)

	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleWorkflowStatusEvent(e *event.WorkflowStatus) {
	if a.subs.footer == nil {
		return
	}
	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Mode:                   subs.footer.Data().Mode,
		Profile:                string(subs.effectiveModeState().Major),
		Model:                  activeModelDisplay(subs),
		MinorMode:              subs.footer.Data().MinorMode,
		WorkflowActive:         e.Step < e.TotalSteps,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

func (a *App) handleWorkflowProgressEvent(e *event.WorkflowProgress) {
	if a.subs.footer == nil {
		return
	}
	activity := ""
	if e.Status == "running" && e.StageName != "" {
		activity = fmt.Sprintf("stage %d/%d: %s", e.StageIndex+1, e.TotalStages, e.StageName)
	} else if e.Status == "gate" {
		activity = fmt.Sprintf("gate: %s", e.StageName)
	}
	data := a.subs.footer.Data()
	data.WorkflowActive = e.Status == "running" || e.Status == "gate"
	if data.WorkflowActive {
		data.SteeringHint = "type to steer"
	} else {
		data.SteeringHint = ""
	}
	if activity != "" {
		data.Activity = activity
	}
	a.subs.footer.SetData(data)
}

func (a *App) handleGoalUpdate(update *event.GoalUpdate) {
	if update == nil || a.subs.chat == nil {
		return
	}

	a.updateGoalFooter(update)

	if update.Change != nil {
		switch update.Change.Kind {
		case goal.GoalChangeLifecycle:
			marker := goaltui.NewMarker((*goal.GoalChange)(update.Change))
			a.subs.chat.AddComponent(marker)
		case goal.GoalChangeCompletion:
			if update.Snapshot != nil {
				a.subs.chat.AddComponent(goaltui.NewCompletion(update.Snapshot))
				// Stash the completion evidence so the next auto-promoted
				// queued goal inherits it as its Handoff note.
				a.goalCompletionHandoff = update.Snapshot.TerminalReason
				// Stash the /goal:pause:next one-shot: the completion clear
				// (next event) then promotes the successor PAUSED instead of
				// auto-starting it.
				a.goalPauseOnComplete = update.Snapshot.PauseAfterComplete
			}
		}
	}

	if update.Snapshot == nil {
		// Consume the pause-on-complete stash with the clear event it belongs
		// to: markCompleteLocked emits the completion and its clear together,
		// so a cancel or runtime clear never observes a stale flag.
		pauseOnComplete := a.goalPauseOnComplete
		a.goalPauseOnComplete = false
		if a.subs.goalManager != nil {
			switch {
			case cancelClearPausesPromotion(update.Change):
				a.promoteQueuedGoalPaused(promotePauseCancel)
			case pauseOnComplete:
				a.promoteQueuedGoalPaused(promotePauseComplete)
			default:
				a.promoteNextQueuedGoal()
			}
		}
	}
}

// seedGoalUI restores the goal bubble and footer from the goal manager's
// current state at startup. The durable goal store is replayed before the
// TUI exists, so no GoalUpdate bus event covers an already-persisted goal —
// without this seed the bubble stays hidden until the next live goal event
// (Issue 1).
func (a *App) seedGoalUI() {
	if a.subs.goalManager == nil {
		return
	}
	a.updateGoalFooter(&event.GoalUpdate{Snapshot: a.subs.goalManager.Mode.GetGoal().Goal})
}

func (a *App) updateGoalFooter(update *event.GoalUpdate) {
	if a.subs.goalBubble != nil {
		if update.Snapshot != nil {
			a.subs.goalBubble.SetSnapshot(update.Snapshot)
		} else {
			a.subs.goalBubble.SetSnapshot(nil)
		}
	}
	if a.subs.footer == nil {
		return
	}
	// SetGoalStatus is the explicit writer of the footer's goal fields
	// (routine SetData rebuilds preserve them — a stats tick must never clear
	// the ◈ marker, Issues 3-4). The footer carries no other goal
	// detail: objective/status/todo titles are the goal bubble's job.
	// The goal count is 1 (the current goal) + queued goals, so the ◈ sign
	// follows the todo-marker shape: 1 → "◈", 3 → "◈◈◈", 25 → "25◈".
	if update.Snapshot == nil {
		a.subs.footer.SetGoalStatus("", 0, 0)
	} else {
		a.subs.footer.SetGoalStatus(
			string(update.Snapshot.Status),
			1+countQueuedGoals(a.subs.goalManager),
			countPendingGoalTodos(update.Snapshot.Todos))
	}
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

// countQueuedGoals returns the number of goals waiting in the queue (the
// current goal is counted separately by the caller). A nil manager or a
// failed queue read yields 0 — the footer must keep rendering even when the
// queue store is unavailable.
func countQueuedGoals(mgr *core.GoalManager) int {
	if mgr == nil {
		return 0
	}
	queued, err := mgr.Queue.Read()
	if err != nil {
		return 0
	}
	return len(queued)
}

// countPendingGoalTodos returns the number of todos not yet done on a goal
// snapshot — the count behind the footer's ⬩ markers next to the mode.
func countPendingGoalTodos(todos []goal.GoalTodoItem) int {
	pending := 0
	for _, t := range todos {
		if t.Status != goal.TodoDone {
			pending++
		}
	}
	return pending
}

// cancelClearPausesPromotion reports whether the clear event comes from an
// explicit user/model cancel: such clears must NOT auto-start the queued
// successor — it is promoted PAUSED instead. A completion clear carries no
// change (start the next goal, the queue drains autonomously), and runtime
// framework clears (postpone, unblock flow, orchestrator cleanup) are
// scheduling machinery that likewise keeps driving.
func cancelClearPausesPromotion(change *goal.GoalChange) bool {
	if change == nil || change.Kind != goal.GoalChangeClear || change.Actor == nil {
		return false
	}
	return *change.Actor == goal.GoalActorUser || *change.Actor == goal.GoalActorModel
}

// promoteNextQueuedGoal removes the head of the goal queue and activates it.
// Fired by the goal-cleared event (completion or cancel), so the queue drains
// across completions without any model round-trip. Runs on the
// event-forwarder goroutine; the queue store serializes concurrent access.
func (a *App) promoteNextQueuedGoal() {
	a.promoteQueuedGoal(nil)
}

// promotePauseCause identifies why a promoted queued goal is parked PAUSED:
// an explicit user/model cancel, or a completion with the /goal:pause:next
// one-shot armed. The pause reason recorded on the goal and the system chat
// message differ per cause.
